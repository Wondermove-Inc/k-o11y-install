-- ko11y-agent-status.sql
-- CH VM 경량 에이전트의 heartbeat + 상태 추적 테이블
--
-- 사용처:
--   1. 에이전트 데몬: 매 폴링 주기마다 heartbeat INSERT
--   2. SigNoz Backend: agent_mode 판별 (last_heartbeat > now() - 90s)
--   3. CLI: agent status 서브커맨드

CREATE DATABASE IF NOT EXISTS ko11y;

CREATE TABLE IF NOT EXISTS ko11y.agent_status
(
    hostname         String,                                    -- VM hostname
    version          String,                                    -- 에이전트 바이너리 버전
    last_heartbeat   DateTime64(3, 'UTC') DEFAULT now64(3),     -- 마지막 heartbeat 시각
    status           String DEFAULT 'running',                  -- running | error | stopping
    last_action      String DEFAULT '',                         -- 마지막 수행 액션 (s3_activate 등)
    last_action_at   DateTime64(3, 'UTC') DEFAULT now64(3),     -- 마지막 액션 수행 시각
    last_error       String DEFAULT ''                          -- 마지막 에러 메시지
) ENGINE = ReplacingMergeTree(last_heartbeat)
ORDER BY (hostname)
SETTINGS index_granularity = 256;
