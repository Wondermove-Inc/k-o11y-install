-- 0001_v26.2.1_add_activate_requested.sql
-- Save/Activate 분리를 위한 activate_requested 플래그 추가
-- 적용 대상: 26.2.1 이전 버전에서 업그레이드 시

ALTER TABLE ko11y.s3_config ADD COLUMN IF NOT EXISTS activate_requested UInt8 DEFAULT 0 AFTER s3_enabled;
