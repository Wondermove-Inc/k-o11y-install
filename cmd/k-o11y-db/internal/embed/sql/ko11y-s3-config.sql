-- ko11y-s3-config.sql
-- S3 스토리지 설정을 중앙 관리하는 테이블
-- Warm(S3 Standard) 티어링 + Cold(Glacier IR) 백업에 사용되는 AWS credential 저장
--
-- 사용처:
--   1. SigNoz Backend: GET/PUT /api/v1/settings/s3 (credential 관리)
--   2. ch-glacier-cron.sh: 백업 시 credential 동적 조회
--   3. Frontend: S3 Settings UI
--
-- 보안:
--   access_key_id, secret_access_key는 AES-256-GCM으로 암호화 저장
--   암호화 키: K8s Secret (k-o11y-encryption-key) → 환경변수 K_O11Y_ENCRYPTION_KEY

CREATE DATABASE IF NOT EXISTS ko11y;

CREATE TABLE IF NOT EXISTS ko11y.s3_config
(
    config_id            String DEFAULT 'global',   -- 단일 행 ('global')
    auth_mode            String,                     -- 'static' | 'iam'
    bucket               String,                     -- S3 버킷 이름
    region               String,                     -- AWS 리전 (e.g., ap-northeast-2)
    endpoint             String,                     -- S3 endpoint URL (자동 생성 또는 커스텀)
    access_key_id        String,                     -- AES-256-GCM 암호화 저장
    secret_access_key    String,                     -- AES-256-GCM 암호화 저장
    s3_enabled           UInt8 DEFAULT 0,            -- 0: 비활성, 1: 활성
    activate_requested   UInt8 DEFAULT 0,            -- 0: 대기, 1: 활성화 요청 (에이전트가 처리 후 0으로 리셋)
    connection_tested    UInt8 DEFAULT 0,            -- 0: 미테스트/실패, 1: 성공
    connection_tested_at DateTime DEFAULT toDateTime(0),
    updated_by           String,                     -- 'install-script' | 'signoz-ui' | 'manual'
    updated_at           DateTime DEFAULT now(),
    version              UInt64                       -- ReplacingMergeTree 버전 컬럼
) ENGINE = ReplacingMergeTree(version)
ORDER BY (config_id)
SETTINGS index_granularity = 256;
