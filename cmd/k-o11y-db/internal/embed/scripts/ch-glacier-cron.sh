#!/bin/bash
###############################################################################
# ch-glacier-cron.sh
#
# Data Lifecycle cron 스크립트.
# ko11y.data_lifecycle_config에서 설정을 읽어:
#   1. Glacier 비활성화 시 스킵
#   2. hot_days + warm_days 기준으로 삭제 대상 파티션 계산
#   3. clickhouse-backup으로 해당 파티션 백업 + S3 Glacier IR 업로드
#   4. 업로드 검증 성공 시에만 DROP_TABLES에서 DROP PARTITION
#   5. 실패 시 DROP 건너뜀 + last_backup_status='failed' 업데이트 + 알림
#
# cron 등록:
#   0 18 * * * CH_PASSWORD="YOUR_PASSWORD" K_O11Y_ENCRYPTION_KEY="..." /opt/scripts/ch-glacier-cron.sh
#
# 전제:
#   - clickhouse-backup v2.6.5+ 설치 완료
#   - /opt/scripts/get-s3-creds 바이너리 배포 완료
#   - ko11y.data_lifecycle_config + ko11y.s3_config 테이블 존재
#   - K_O11Y_ENCRYPTION_KEY 환경변수 설정 (AES-256-GCM 복호화용)
#
# 테이블 분류:
#   - DROP_TABLES: 날짜 파티션 데이터 테이블 (백업 후 DROP 안전)
#   - BACKUP_ONLY_TABLES: 날짜 파티션이지만 메타데이터 성격 (백업만, DROP 금지)
#   - tuple() 테이블: --partitions로 매칭 안 됨 (schema만 백업, DROP 절대 금지)
###############################################################################
set -euo pipefail

CH_PASSWORD="${CH_PASSWORD:-}"
CH_HOST="${CH_HOST:-127.0.0.1}"
CH_CLIENT="clickhouse-client --host ${CH_HOST} --password ${CH_PASSWORD}"
GET_S3_CREDS="${GET_S3_CREDS:-/opt/scripts/get-s3-creds}"

LOG_FILE="/var/log/clickhouse-archive.log"
CACHE_FILE="/opt/scripts/.lifecycle_config_cache"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"; }

# --- DROP 대상 테이블 (날짜 파티션, 백업 후 DROP 안전) ---
# 모두 PARTITION BY toDate(...) 형식. DROP PARTITION 'YYYY-MM-DD'로 해당 날짜만 삭제.
DROP_TABLES=(
    # Logs (2)
    "signoz_logs.logs_v2"
    "signoz_logs.logs_v2_resource"
    # Metrics (4) — time_series_v4*는 메트릭 메타데이터이므로 DROP 금지
    "signoz_metrics.samples_v4"
    "signoz_metrics.samples_v4_agg_5m"
    "signoz_metrics.samples_v4_agg_30m"
    "signoz_metrics.exp_hist"
    # Traces (7)
    "signoz_traces.signoz_index_v3"
    "signoz_traces.traces_v3_resource"
    "signoz_traces.signoz_error_index_v2"
    "signoz_traces.usage_explorer"
    "signoz_traces.dependency_graph_minutes_v2"
    "signoz_traces.trace_summary"
    "signoz_traces.network_map_connections"
)

# --- 백업만 하고 DROP 하지 않는 테이블 (메타데이터 성격) ---
# 날짜 파티션이지만 SigNoz UI 필터/메트릭 조회에 필수. 삭제하면 UI 장애.
# tuple() 테이블(logs_attribute_keys, span_attributes_keys, top_level_operations 등)은
# --partitions 매칭이 안 되므로 schema만 백업됨 (별도 처리 불필요).
BACKUP_ONLY_TABLES=(
    "signoz_logs.tag_attributes_v2"
    "signoz_traces.tag_attributes_v2"
    "signoz_metadata.attributes_metadata"
    "signoz_metrics.metadata"
    # 메트릭 메타데이터 (메트릭 이름/레이블 목록 — DROP하면 UI에서 메트릭 조회 불가)
    "signoz_metrics.time_series_v4"
    "signoz_metrics.time_series_v4_6hrs"
    "signoz_metrics.time_series_v4_1day"
    "signoz_metrics.time_series_v4_1week"
)

# --- 1. data_lifecycle_config에서 설정 조회 ---
load_config() {
    local query="SELECT glacier_enabled, hot_days, warm_days, glacier_retention_days, backup_frequency_hours FROM ko11y.data_lifecycle_config FINAL WHERE signal_type = 'global' FORMAT JSONEachRow"
    local result

    if result=$($CH_CLIENT --query "$query" 2>/dev/null) && [ -n "$result" ]; then
        GLACIER_ENABLED=$(echo "$result" | grep -o '"glacier_enabled":[0-9]*' | cut -d: -f2)
        HOT_DAYS=$(echo "$result" | grep -o '"hot_days":[0-9]*' | cut -d: -f2)
        WARM_DAYS=$(echo "$result" | grep -o '"warm_days":[0-9]*' | cut -d: -f2)
        COLD_DAYS=$(echo "$result" | grep -o '"glacier_retention_days":[0-9]*' | cut -d: -f2)
        TOTAL_DAYS=$((HOT_DAYS + WARM_DAYS))
        echo "$result" > "$CACHE_FILE"
        log "Config loaded: glacier=${GLACIER_ENABLED}, hot=${HOT_DAYS}d, warm=${WARM_DAYS}d, total=${TOTAL_DAYS}d, cold=${COLD_DAYS}d"
    elif [ -f "$CACHE_FILE" ]; then
        local cached
        cached=$(cat "$CACHE_FILE")
        GLACIER_ENABLED=$(echo "$cached" | grep -o '"glacier_enabled":[0-9]*' | cut -d: -f2)
        HOT_DAYS=$(echo "$cached" | grep -o '"hot_days":[0-9]*' | cut -d: -f2)
        WARM_DAYS=$(echo "$cached" | grep -o '"warm_days":[0-9]*' | cut -d: -f2)
        TOTAL_DAYS=$((HOT_DAYS + WARM_DAYS))
        log "WARN: CH unavailable, using cached config: total=${TOTAL_DAYS}d"
    else
        log "ERROR: No config available (CH unavailable + no cache). Aborting."
        exit 1
    fi
}

# --- 2. 백업 상태 업데이트 ---
update_backup_status() {
    local status="$1"
    local error_msg="${2:-}"
    local version
    version=$(date +%s)

    $CH_CLIENT --query "INSERT INTO ko11y.data_lifecycle_config (signal_type, hot_days, warm_days, glacier_enabled, glacier_retention_days, backup_frequency_hours, last_backup_status, last_backup_at, last_backup_error, updated_by, version) VALUES ('global', ${HOT_DAYS}, ${WARM_DAYS}, ${GLACIER_ENABLED}, ${COLD_DAYS}, 24, '${status}', now(), '${error_msg}', 'cron', ${version})" 2>/dev/null || true
}

# --- 3. 알림 발송 (실패 시) ---
send_alert() {
    local message="$1"
    log "ALERT: ${message}"

    # Slack webhook (환경변수로 설정)
    if [ -n "${SLACK_WEBHOOK_URL:-}" ]; then
        curl -s -X POST "${SLACK_WEBHOOK_URL}" \
            -H 'Content-type: application/json' \
            -d "{\"text\":\"🚨 [ClickHouse Glacier] ${message}\"}" \
            2>/dev/null || true
    fi
}

# --- 메인 ---
log "=========================================="
log "ch-glacier-cron.sh started"
log "=========================================="

load_config

# Glacier 비활성화 시 스킵
if [ "${GLACIER_ENABLED:-0}" -eq 0 ]; then
    log "Glacier disabled in config. Skipping."
    exit 0
fi

# 삭제 대상 날짜 계산: TOTAL_DAYS일 전 파티션
ARCHIVE_DATE=$(date -d "${TOTAL_DAYS} days ago" +%Y-%m-%d)
ARCHIVE_DATE_COMPACT=$(date -d "${TOTAL_DAYS} days ago" +%Y%m%d)
BACKUP_NAME="archive-${ARCHIVE_DATE_COMPACT}"

log "Target partition: ${ARCHIVE_DATE} (${TOTAL_DAYS} days ago)"

# 해당 날짜에 데이터가 있는지 확인
ROW_COUNT=$($CH_CLIENT --query "SELECT count() FROM system.parts WHERE active AND database LIKE 'signoz_%' AND partition = '${ARCHIVE_DATE}' AND rows > 0" 2>/dev/null || echo "0")

if [ "${ROW_COUNT}" -eq 0 ]; then
    log "No data found for partition ${ARCHIVE_DATE}. Nothing to archive."
    exit 0
fi

log "Found ${ROW_COUNT} active parts for partition ${ARCHIVE_DATE}"

# --- 3.5. S3 credential DB 조회 ---
load_s3_creds() {
    if [ ! -x "${GET_S3_CREDS}" ]; then
        log "ERROR: get-s3-creds not found at ${GET_S3_CREDS}"
        return 1
    fi

    local creds_output
    if creds_output=$("${GET_S3_CREDS}" --type cold --ch-host "${CH_HOST}" --ch-password "${CH_PASSWORD}" 2>>"$LOG_FILE"); then
        eval "$creds_output"
        log "S3 credentials loaded from DB (bucket=${S3_BUCKET}, region=${S3_REGION})"
    else
        log "ERROR: Failed to load S3 credentials from DB"
        return 1
    fi
}

if ! load_s3_creds; then
    update_backup_status "failed" "S3 credential load failed"
    send_alert "S3 credential load failed. Check get-s3-creds and K_O11Y_ENCRYPTION_KEY."
    exit 1
fi

# clickhouse-backup은 config.yml에서 credential을 읽음
# 환경변수 오버라이드는 object_disk_path URL 충돌을 유발하므로 사용하지 않음
# get-s3-creds 결과는 검증 용도로만 사용 (credential 존재 확인)
unset S3_ACCESS_KEY S3_SECRET_KEY S3_BUCKET S3_REGION S3_ENDPOINT AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY

# --- 4. 백업 생성 + 업로드 ---
# signoz_*.* 전체를 대상으로 하되, --partitions로 해당 날짜 파티션만 백업
# - 날짜 파티션 테이블: 해당 날짜 data parts 포함
# - tuple() 테이블: schema만 백업 (DR 대비, 데이터는 CH에 유지)
BACKUP_SUCCESS=false

log "Step 1: Creating backup ${BACKUP_NAME}..."
if clickhouse-backup create --tables "signoz_*.*" --partitions "${ARCHIVE_DATE_COMPACT}" "${BACKUP_NAME}" 2>>"$LOG_FILE"; then
    log "Step 1: Local backup created"

    # 백업 데이터 검증: shadow에 실제 data 파일이 있는지 확인
    DATA_FILE_COUNT=$(find /var/lib/clickhouse/backup/${BACKUP_NAME}/shadow/ -type f 2>/dev/null | wc -l)
    if [ "${DATA_FILE_COUNT}" -eq 0 ]; then
        log "ERROR: Backup contains no data files (shadow empty). Partition format mismatch?"
        clickhouse-backup delete local "${BACKUP_NAME}" 2>>"$LOG_FILE" || true
        update_backup_status "failed" "Backup empty - no data files for partition ${ARCHIVE_DATE_COMPACT}"
        send_alert "Backup empty for partition ${ARCHIVE_DATE}. No data files found. Check --partitions format."
        exit 1
    fi
    log "Step 1: Backup verified (${DATA_FILE_COUNT} data files)"

    log "Step 2: Uploading to Glacier IR..."
    if clickhouse-backup upload "${BACKUP_NAME}" 2>>"$LOG_FILE"; then
        log "Step 2: Upload complete"

        # 업로드 검증
        log "Step 3: Verifying upload..."
        if clickhouse-backup list remote 2>/dev/null | grep -q "${BACKUP_NAME}"; then
            log "Step 3: Upload verified"
            BACKUP_SUCCESS=true
        else
            log "ERROR: Upload verification failed - backup not found in remote"
        fi
    else
        log "ERROR: Upload to Glacier IR failed"
    fi

    # 로컬 백업 정리
    clickhouse-backup delete local "${BACKUP_NAME}" 2>>"$LOG_FILE" || true
else
    log "ERROR: Backup creation failed"
fi

# --- 5. 백업 성공 시 DROP PARTITION (DROP_TABLES만 대상) ---
# BACKUP_ONLY_TABLES와 tuple() 테이블은 절대 DROP하지 않음
if [ "$BACKUP_SUCCESS" = true ]; then
    log "Step 4: Dropping partitions for ${ARCHIVE_DATE} (${#DROP_TABLES[@]} tables)..."
    DROP_SUCCESS=0
    DROP_SKIPPED=0

    for tbl in "${DROP_TABLES[@]}"; do
        # 해당 테이블에 해당 파티션이 있는지 확인
        local_parts=$($CH_CLIENT --query "SELECT count() FROM system.parts WHERE active AND database||'.'||table = '${tbl}' AND partition = '${ARCHIVE_DATE}' AND rows > 0" 2>/dev/null || echo "0")

        if [ "${local_parts}" -gt 0 ]; then
            if $CH_CLIENT --query "ALTER TABLE ${tbl} DROP PARTITION '${ARCHIVE_DATE}'" 2>>"$LOG_FILE"; then
                DROP_SUCCESS=$((DROP_SUCCESS + 1))
            else
                log "WARN: DROP PARTITION failed for ${tbl}"
            fi
        else
            DROP_SKIPPED=$((DROP_SKIPPED + 1))
        fi
    done

    log "Step 4: DROP complete (success=${DROP_SUCCESS}, skipped=${DROP_SKIPPED})"
    update_backup_status "success" ""
    log "Archive completed: ${BACKUP_NAME} → Glacier IR, ${DROP_SUCCESS} tables cleaned"
else
    # 백업 실패 → 파티션 보존 + 알림
    update_backup_status "failed" "Backup or upload failed for partition ${ARCHIVE_DATE}"
    send_alert "Backup failed for partition ${ARCHIVE_DATE}. Data preserved (NOT deleted). Check logs: ${LOG_FILE}"
    log "ERROR: Archive FAILED. Partition ${ARCHIVE_DATE} preserved (NOT deleted)."
    exit 1
fi

log "=========================================="
log "ch-glacier-cron.sh completed"
log "=========================================="
