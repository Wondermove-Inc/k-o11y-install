-- ============================================================
-- ClickHouse DDL 최적화 적용 스크립트 v8 (Complete)
-- ============================================================
-- 목적: CPU 사용률 90% → 10-30% 감소 (배치 처리 방식)
-- 아키텍처: Watermark 기반 배치 INSERT (Network MV 제거)
-- 작성일: 2026-02-03
-- ============================================================
-- 핵심 변경:
--   - network_raw_mv, network_final_mv 제거 (CPU 부하 원인)
--   - CronJob 배치 INSERT로 network_map_connections 적재
--   - Dictionary 소스용 테이블/MV는 유지 (필수)
-- ============================================================
--
-- 적용 순서:
--   Part A: 기존 Network MV 삭제 (CPU 부하 제거)
--   Part B: 소스 테이블 생성 (Dictionary 데이터용)
--   Part C: 소스 MV 생성 (KSM 데이터 수집용)
--   Part D: Dictionary 생성
--   Part E: Network 테이블 생성
--   Part F: 워터마크 테이블 생성 (배치 처리용)
--
-- 배치 처리: network-batch-cronjob.yaml (Kubernetes CronJob)
-- ============================================================


-- ============================================================
-- Part A: 기존 Network MV 삭제 (CPU 부하 원인 제거)
-- ============================================================

DROP VIEW IF EXISTS signoz_traces.network_raw_mv;
DROP VIEW IF EXISTS signoz_traces.network_final_mv;
DROP VIEW IF EXISTS signoz_traces.network_optimized_mv;


-- ============================================================
-- Part B: 소스 테이블 생성 (Dictionary 데이터용)
-- ============================================================

-- B1. ReplicaSet → Deployment 매핑 테이블
CREATE TABLE IF NOT EXISTS signoz_traces.replicaset_deployment_map
(
    `cluster_name` String,
    `namespace` String,
    `replicaset_name` String,
    `deployment_name` String,
    `workload_type` String,
    `last_seen` DateTime64(9) DEFAULT now64(),
    `version` UInt64 DEFAULT toUnixTimestamp64Nano(now64())
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_name, namespace, replicaset_name)
TTL toDateTime(last_seen) + INTERVAL 1 HOUR
SETTINGS index_granularity = 8192;


-- B2. Service Endpoint 매핑 테이블
CREATE TABLE IF NOT EXISTS signoz_traces.svc_ep_addr
(
    service_name String,
    pod_ip String,
    cluster_name String,
    namespace String,
    port UInt32,
    last_seen DateTime64(9) DEFAULT now64(),
    version UInt64 DEFAULT toUnixTimestamp64Nano(now64())
) ENGINE = ReplacingMergeTree(version)
PARTITION BY toYYYYMM(last_seen)
ORDER BY (cluster_name, namespace, service_name)
TTL toDateTime(last_seen) + INTERVAL 1 HOUR
SETTINGS index_granularity = 8192;


-- B3. Cluster Nodes 테이블
CREATE TABLE IF NOT EXISTS signoz_traces.cluster_nodes (
    `cluster_name` LowCardinality(String),
    `node_name` String,
    `node_ip` String,
    `last_seen` DateTime DEFAULT now(),
    `is_active` UInt8 DEFAULT 1
) ENGINE = ReplacingMergeTree(last_seen)
ORDER BY (cluster_name, node_name, node_ip)
SETTINGS index_granularity = 8192;


-- ============================================================
-- Part C: 소스 MV 생성 (KSM 데이터 수집용 - 필수 유지)
-- ============================================================

-- C1. ReplicaSet → Deployment 매핑 MV
DROP VIEW IF EXISTS signoz_traces.replicaset_deployment_map_mv;

CREATE MATERIALIZED VIEW signoz_traces.replicaset_deployment_map_mv
TO signoz_traces.replicaset_deployment_map
AS
SELECT
    JSONExtractString(ts.labels, 'k8s.cluster.name') AS cluster_name,
    JSONExtractString(ts.labels, 'namespace') AS namespace,
    JSONExtractString(ts.labels, 'replicaset') AS replicaset_name,
    JSONExtractString(ts.labels, 'owner_name') AS deployment_name,
    JSONExtractString(ts.labels, 'owner_kind') AS workload_type,
    toDateTime64(s.unix_milli / 1000, 9) AS last_seen,
    toUnixTimestamp64Nano(now64()) AS version
FROM signoz_metrics.samples_v4 AS s
INNER JOIN signoz_metrics.time_series_v4 AS ts ON s.fingerprint = ts.fingerprint
WHERE s.metric_name = 'kube_replicaset_owner'
  AND s.value = 1
  AND JSONExtractString(ts.labels, 'replicaset') != ''
  AND JSONExtractString(ts.labels, 'owner_name') != ''
  AND JSONExtractString(ts.labels, 'namespace') != ''
  AND toDateTime64(s.unix_milli / 1000, 9) >= now() - INTERVAL 5 MINUTE
  AND toDateTime64(ts.unix_milli / 1000, 9) >= now() - INTERVAL 2 HOUR;


-- C2. Service Endpoint 매핑 MV
DROP VIEW IF EXISTS signoz_traces.svc_ep_addr_mv;

CREATE MATERIALIZED VIEW signoz_traces.svc_ep_addr_mv
TO signoz_traces.svc_ep_addr AS
SELECT
    JSONExtractString(ts.labels, 'endpoint') as service_name,
    JSONExtractString(ts.labels, 'ip') as pod_ip,
    JSONExtractString(ts.labels, 'k8s.cluster.name') as cluster_name,
    JSONExtractString(ts.labels, 'namespace') as namespace,
    toUInt32OrZero(JSONExtractString(ts.labels, 'port_number')) as port,
    toDateTime64(s.unix_milli / 1000, 9) as last_seen,
    toUnixTimestamp64Nano(toDateTime64(s.unix_milli / 1000, 9)) as version
FROM signoz_metrics.samples_v4 s
JOIN signoz_metrics.time_series_v4 ts ON s.fingerprint = ts.fingerprint
WHERE s.metric_name = 'kube_endpoint_address'
  AND s.value = 1
  AND toDateTime64(s.unix_milli / 1000, 9) >= now() - INTERVAL 5 MINUTE -- 20260113 cpu peak 장애 이슈
  AND toDateTime64(ts.unix_milli / 1000, 9) >= now() - INTERVAL 2 HOUR -- 20260113 time_series_v4는 매 시간 정각에 갱신됨(100% 커버리지를 위한 2HOUR 설정)
  AND JSONExtractString(ts.labels, 'endpoint') != ''
  AND JSONExtractString(ts.labels, 'ip') != ''
  AND JSONExtractString(ts.labels, 'ready') = 'true';


-- C3. Cluster Nodes MV (kube_node_info 기반)
-- ============================================================
-- 기존 MV 삭제
-- ============================================================
DROP VIEW IF EXISTS signoz_traces.cluster_nodes_mv;
truncate table signoz_traces.cluster_nodes;

-- ============================================================
-- cluster_nodes_mv (kube_node_info 기반)
-- ============================================================
-- kube_node_info 메트릭 라벨 구조:
--   - k8s.cluster.name: 클러스터 이름
--   - node: 노드 이름
--   - internal_ip: 노드 내부 IP
--   - kubelet_version, container_runtime_version 등 추가 정보
-- ============================================================

CREATE MATERIALIZED VIEW signoz_traces.cluster_nodes_mv
TO signoz_traces.cluster_nodes AS
SELECT
    JSONExtractString(ts.labels, 'k8s.cluster.name') AS cluster_name,
    JSONExtractString(ts.labels, 'node') AS node_name,
    JSONExtractString(ts.labels, 'internal_ip') AS node_ip,
    now() AS last_seen,
    1 AS is_active
FROM signoz_metrics.samples_v4 AS s
INNER JOIN signoz_metrics.time_series_v4 AS ts ON s.fingerprint = ts.fingerprint
WHERE s.metric_name = 'kube_node_info'
  AND s.value = 1
  AND JSONExtractString(ts.labels, 'k8s.cluster.name') != ''
  AND JSONExtractString(ts.labels, 'node') != ''
  AND JSONExtractString(ts.labels, 'internal_ip') != ''
  AND toDateTime64(s.unix_milli / 1000, 9) >= now() - INTERVAL 5 MINUTE
  AND toDateTime64(ts.unix_milli / 1000, 9) >= now() - INTERVAL 2 HOUR;


-- ============================================================
-- Part D: Dictionary 생성
-- ============================================================

-- D1. cluster_service_dict (Service ClusterIP 매핑)
DROP DICTIONARY IF EXISTS signoz_traces.cluster_service_dict;

CREATE DICTIONARY signoz_traces.cluster_service_dict (
    cluster_name String,
    service_ip String,
    service_name String,
    namespace String
)
PRIMARY KEY (cluster_name, service_ip)
SOURCE(CLICKHOUSE(
    HOST 'localhost'
    PORT 9000
    USER 'default'
    password '{{CH_PASSWORD}}'
    query 'SELECT
        JSONExtractString(labels, ''k8s.cluster.name'') as cluster_name,
        JSONExtractString(labels, ''cluster_ip'') as service_ip,
        JSONExtractString(labels, ''service'') as service_name,
        JSONExtractString(labels, ''namespace'') as namespace
    FROM signoz_metrics.time_series_v4
    WHERE metric_name = ''kube_service_info''
      AND length(JSONExtractString(labels, ''cluster_ip'')) > 0
      AND unix_milli >= toInt64((now64(3) - INTERVAL 2 HOUR)) * 1000'
))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 600);


-- D2. svc_ep_addr_dict (Service → Pod IP 매핑)
DROP DICTIONARY IF EXISTS signoz_traces.svc_ep_addr_dict;

CREATE DICTIONARY signoz_traces.svc_ep_addr_dict (
    cluster_name String,
    service_name String,
    namespace String,
    pod_ip String,
    port UInt16,
    ns String
)
PRIMARY KEY (cluster_name, service_name, namespace)
SOURCE(CLICKHOUSE(
    HOST 'localhost'
    PORT 9000
    USER 'default'
    password '{{CH_PASSWORD}}'
    query 'SELECT
        cluster_name,
        service_name,
        namespace,
        argMax(pod_ip, last_seen) as pod_ip,
        toUInt16(argMax(port, last_seen)) as port,
        namespace as ns
    FROM signoz_traces.svc_ep_addr
    WHERE last_seen >= now() - INTERVAL 10 MINUTE
    GROUP BY cluster_name, service_name, namespace'
))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 30 MAX 60);


-- D3. pod_workload_map_dict (Pod IP → Workload 매핑)
-- 최적화: INNER JOIN → IN 서브쿼리 방식으로 변경
-- 효과: time_series_v4 전체 스캔(987만 rows, 9GB) → fingerprint 기반 포인트 조회(70 rows, 183MB)
-- 검증: 양방향 EXCEPT 0건, DISTINCT 결과 100% 동일 (69행, 63 고유키)
DROP DICTIONARY IF EXISTS signoz_traces.pod_workload_map_dict;

CREATE DICTIONARY signoz_traces.pod_workload_map_dict (
    ip String,
    cluster_name String,
    workload_name String,
    workload_type String,
    namespace String,
    pod_name String
) PRIMARY KEY (ip, cluster_name)
SOURCE(CLICKHOUSE(
    password '{{CH_PASSWORD}}'
    HOST 'localhost'
    PORT 9000
    USER 'default'
    DB 'signoz_traces'
    QUERY 'SELECT
        pod_info.ip AS ip,
        pod_info.cluster_name AS cluster_name,
        if(
            pod_info.created_by_kind = ''ReplicaSet'' AND rd.deployment_name != '''',
            rd.deployment_name,
            pod_info.created_by_name
        ) AS workload_name,
        if(
            pod_info.created_by_kind = ''ReplicaSet'' AND rd.workload_type != '''',
            rd.workload_type,
            pod_info.created_by_kind
        ) AS workload_type,
        pod_info.namespace AS namespace,
        pod_info.pod_name AS pod_name
    FROM (
        SELECT DISTINCT
            JSONExtractString(ts.labels, ''pod_ip'') AS ip,
            JSONExtractString(ts.labels, ''k8s.cluster.name'') AS cluster_name,
            JSONExtractString(ts.labels, ''namespace'') AS namespace,
            JSONExtractString(ts.labels, ''pod'') AS pod_name,
            JSONExtractString(ts.labels, ''created_by_name'') AS created_by_name,
            JSONExtractString(ts.labels, ''created_by_kind'') AS created_by_kind
        FROM signoz_metrics.time_series_v4 AS ts
        WHERE ts.fingerprint IN (
            SELECT DISTINCT fingerprint
            FROM signoz_metrics.samples_v4
            WHERE metric_name = ''kube_pod_info''
              AND value = 1
              AND toDateTime64(unix_milli / 1000, 9) >= now() - INTERVAL 10 MINUTE
        )
        AND JSONExtractString(ts.labels, ''pod_ip'') != ''''
        AND JSONExtractString(ts.labels, ''created_by_name'') != ''''
    ) AS pod_info
    LEFT JOIN signoz_traces.replicaset_deployment_map AS rd FINAL
        ON rd.cluster_name = pod_info.cluster_name
        AND rd.namespace = pod_info.namespace
        AND rd.replicaset_name = pod_info.created_by_name'
))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 30 MAX 60);


-- D4. cluster_nodes_dict (Node 필터링용)
DROP DICTIONARY IF EXISTS signoz_traces.cluster_nodes_dict;

CREATE DICTIONARY signoz_traces.cluster_nodes_dict (
    cluster_name String,
    node_names Array(String),
    node_ips Array(String)
)
PRIMARY KEY cluster_name
SOURCE(CLICKHOUSE(
    HOST 'localhost'
    PORT 9000
    USER 'default'
    password '{{CH_PASSWORD}}'
    query 'SELECT
        cluster_name,
        groupArray(DISTINCT node_name) as node_names,
        groupArray(DISTINCT node_ip) as node_ips
    FROM signoz_traces.cluster_nodes
    WHERE is_active = 1
    GROUP BY cluster_name'
))
LAYOUT(HASHED())
LIFETIME(MIN 30 MAX 60);


-- ============================================================
-- Part E: Network 테이블 생성
-- ============================================================

-- E1. network_raw 테이블 (사용하지 않지만 호환성 유지)
-- CREATE TABLE IF NOT EXISTS signoz_traces.network_raw (
--     `src` LowCardinality(String) CODEC(ZSTD(1)),
--     `src_raw` String CODEC(ZSTD(1)),
--     `dest` String CODEC(ZSTD(1)),
--     `dest_raw` String CODEC(ZSTD(1)),
--     `dest_ip` String CODEC(ZSTD(1)),
--     `protocol` LowCardinality(String) CODEC(ZSTD(1)),
--     `method` LowCardinality(String) CODEC(ZSTD(1)),
--     `duration_sum` UInt64 CODEC(T64, ZSTD(1)),
--     `duration_count` UInt64 CODEC(T64, ZSTD(1)),
--     `duration_p50` Float64 CODEC(ZSTD(1)),
--     `duration_p95` Float64 CODEC(ZSTD(1)),
--     `duration_p99` Float64 CODEC(ZSTD(1)),
--     `error_count` UInt64 CODEC(T64, ZSTD(1)),
--     `total_count` UInt64 CODEC(T64, ZSTD(1)),
--     `timestamp` DateTime CODEC(DoubleDelta, LZ4),
--     `deployment_environment` LowCardinality(String) CODEC(ZSTD(1)),
--     `k8s_cluster_name` LowCardinality(String) CODEC(ZSTD(1)),
--     `src_namespace` LowCardinality(String) CODEC(ZSTD(1)),
--     `dest_namespace` LowCardinality(String) CODEC(ZSTD(1))
-- ) ENGINE = SummingMergeTree((duration_sum, duration_count, error_count, total_count))
-- PARTITION BY toDate(timestamp)
-- ORDER BY (timestamp, src, dest, protocol)
-- SETTINGS index_granularity = 8192;


-- E2. network_map_connections 테이블 (배치 INSERT 대상)
CREATE TABLE IF NOT EXISTS signoz_traces.network_map_connections (
    `src` LowCardinality(String),
    `src_raw` String,
    `dest` LowCardinality(String),
    `dest_raw` String,
    `protocol` LowCardinality(String),
    `method` LowCardinality(String),
    `is_external` UInt8,
    `duration_sum` UInt64,
    `duration_count` UInt64,
    `duration_p50` Float64,
    `duration_p95` Float64,
    `duration_p99` Float64,
    `error_count` UInt64,
    `total_count` UInt64,
    `timestamp` DateTime,
    `deployment_environment` LowCardinality(String),
    `k8s_cluster_name` LowCardinality(String),
    `src_namespace` LowCardinality(String),
    `dest_namespace` LowCardinality(String)
) ENGINE = SummingMergeTree((duration_sum, duration_count, error_count, total_count))
PARTITION BY toDate(timestamp)
PRIMARY KEY (timestamp, k8s_cluster_name, src_namespace)
ORDER BY (timestamp, k8s_cluster_name, src_namespace, dest_namespace, src, dest, protocol, method, deployment_environment, is_external)
SETTINGS index_granularity = 8192;


-- ============================================================
-- Part F: 워터마크 테이블 생성 (배치 처리용)
-- ============================================================

CREATE TABLE IF NOT EXISTS signoz_traces.network_batch_watermark (
    id UInt8 DEFAULT 1,
    last_processed_ts DateTime64(9),
    updated_at DateTime64(9) DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY id;

-- 초기 워터마크 값 삽입 (현재 시간 - 15분)
INSERT INTO signoz_traces.network_batch_watermark (id, last_processed_ts, updated_at)
VALUES (1, now64(9) - INTERVAL 15 MINUTE, now64(9));


-- ============================================================
-- 검증 쿼리
-- ============================================================

-- 1. Dictionary 상태 확인
-- SELECT name, status, element_count FROM system.dictionaries WHERE database = 'signoz_traces';

-- 2. 소스 테이블 데이터 확인
-- SELECT count() FROM signoz_traces.replicaset_deployment_map;
-- SELECT count() FROM signoz_traces.svc_ep_addr;
-- SELECT count() FROM signoz_traces.cluster_nodes;

-- 3. 워터마크 조회 (FINAL 필수)
-- SELECT * FROM signoz_traces.network_batch_watermark FINAL;

-- 4. MV 목록 확인
-- SELECT name FROM system.tables WHERE database = 'signoz_traces' AND engine = 'MaterializedView';


-- ============================================================
-- 배치 처리 (CronJob)
-- ============================================================
-- 배포: network-batch-cronjob.yaml
-- SQL: 05-network-batch-processor.sql
-- 주기: 매 분 실행, 20초 간격 3회 배치


-- ============================================================
-- 롤백 절차 (MV 방식으로 복원)
-- ============================================================
-- 1. DROP TABLE IF EXISTS signoz_traces.network_batch_watermark;
-- 2. clickhouse-ddl.sql 섹션 8, 9, 10 적용 (network MV 생성)
