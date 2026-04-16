-- ko11y-data-lifecycle-config.sql
-- 데이터 라이프사이클 설정을 중앙 관리하는 Single Source of Truth 테이블
-- Hot/Warm/Cold(Glacier) 전체 티어링 설정 + 백업 상태 추적
--
-- 사용처:
--   1. post-install-ch.sh: 설치 시 초기값 INSERT
--   2. SigNoz Backend (reader.go): setTTL 시 glacier_enabled 확인, getTTL 시 hot/warm 반환
--   3. o11y-core (lifecycle.go): GET/PUT /api/v1/settings/lifecycle
--   4. ch-glacier-cron.sh: glacier 설정 읽어서 백업 + DROP PARTITION
--   5. Frontend (Data Lifecycle UI): 설정 조회/변경
--
-- (cold_storage_config는 레거시, 삭제됨)

CREATE DATABASE IF NOT EXISTS ko11y;

CREATE TABLE IF NOT EXISTS ko11y.data_lifecycle_config
(
    signal_type              String,        -- 'global' (현재 단일 행, 향후 per-signal 확장 가능)

    -- Hot/Warm 티어 설정
    hot_days                 Int32,         -- Hot 스토리지 보존 일수 (TTL MOVE 시점)
    warm_days                Int32,         -- Warm(S3) 스토리지 보존 일수 (Hot 이후)
                                            -- Total Retention = hot_days + warm_days

    -- Cold (Glacier IR) 설정
    glacier_enabled          UInt8,         -- 0: 비활성 (TTL DELETE 사용), 1: 활성 (스크립트 DROP)
    glacier_retention_days   Int32,         -- Glacier 보관 기간 (일, 0=무제한)
    backup_frequency_hours   Int32,         -- 백업 주기 (시간, 기본 24)

    -- 백업 상태 추적
    last_backup_status       String DEFAULT 'never',  -- 'never' | 'success' | 'failed'
    last_backup_at           DateTime DEFAULT toDateTime(0),
    last_backup_error        String DEFAULT '',        -- 실패 시 에러 메시지

    -- 메타
    updated_by               String,        -- 'install-script' | 'signoz-ui' | 'o11y-core' | 'cron'
    updated_at               DateTime DEFAULT now(),
    version                  UInt64         -- ReplacingMergeTree 버전 컬럼
) ENGINE = ReplacingMergeTree(version)
ORDER BY (signal_type)
SETTINGS index_granularity = 256;
