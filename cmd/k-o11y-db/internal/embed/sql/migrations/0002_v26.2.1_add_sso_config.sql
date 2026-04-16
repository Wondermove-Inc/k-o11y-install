-- 0002_v26.2.1_add_sso_config.sql
-- SSO Tenant Auto-Lock 테이블 추가
-- 적용 대상: sso_config 테이블이 없는 환경에서 upgrade 시

CREATE DATABASE IF NOT EXISTS ko11y;

CREATE TABLE IF NOT EXISTS ko11y.sso_config
(
    tenant_id   String,
    locked_at   DateTime DEFAULT now(),
    locked_by   String DEFAULT '',
    version     UInt64
) ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id)
SETTINGS index_granularity = 256;
