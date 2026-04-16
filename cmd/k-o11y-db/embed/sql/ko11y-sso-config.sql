-- ko11y-sso-config.sql
-- SSO 설정 및 Tenant Auto-Lock 테이블
--
-- 용도:
--   K-O11y Client SSO 로그인 시 허용된 tenant_id를 관리한다.
--   첫 SSO 로그인 시 tenant_id가 자동 INSERT되어 이후 해당 테넌트만 허용 (Auto-Lock).
--   DB에 수동으로 row를 추가하면 복수 테넌트도 허용 가능.
--
-- 동작 규칙 (SIGNOZ_K_O11Y_ALLOWED_TENANTS 환경변수 기준):
--   - (비움/미설정): 이 테이블 기반 Auto-Lock. 테이블 비어있으면 첫 로그인 시 자동 INSERT.
--   - '*': 모든 테넌트 허용 (테이블 무시, 내부 환경용)
--   - 'tenant1,tenant2': 명시 목록만 허용 (테이블 무시)

CREATE DATABASE IF NOT EXISTS ko11y;

CREATE TABLE IF NOT EXISTS ko11y.sso_config
(
    tenant_id   String,                     -- mgmt-portal JWT의 tenant_id claim
    locked_at   DateTime DEFAULT now(),     -- 등록 시각
    locked_by   String DEFAULT '',          -- 등록한 사용자 email (첫 로그인 사용자)
    version     UInt64                      -- ReplacingMergeTree 버전 컬럼
) ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id)
SETTINGS index_granularity = 256;
