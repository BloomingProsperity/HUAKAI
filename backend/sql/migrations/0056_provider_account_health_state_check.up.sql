-- 必须先 DROP 旧 CHECK，再 UPDATE 老值，最后 ADD 新 CHECK。
-- 旧 CHECK（migration 0001）只允许 operational/degraded/failed/cooling_down/error；
-- 若在旧 CHECK 仍生效时 UPDATE 成 healthy/revoked/cooldown，行级 CHECK 会立即报
-- provider_accounts_health_state_check 违反。空表 fresh migrate UPDATE 命中 0 行不触发，
-- 但已有老数据的升级环境会失败，故顺序固定为 DROP→UPDATE→ADD。
ALTER TABLE provider_accounts
    DROP CONSTRAINT IF EXISTS provider_accounts_health_state_check;

UPDATE provider_accounts
SET health_state = CASE health_state
    WHEN 'operational' THEN 'healthy'
    WHEN 'degraded' THEN 'healthy'
    WHEN 'failed' THEN 'revoked'
    WHEN 'error' THEN 'revoked'
    WHEN 'cooling_down' THEN 'cooldown'
    ELSE health_state
END
WHERE health_state IN ('operational', 'degraded', 'failed', 'error', 'cooling_down');

ALTER TABLE provider_accounts
    ALTER COLUMN health_state SET DEFAULT 'healthy';

ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_health_state_check CHECK (
        health_state IN ('healthy', 'throttled', 'revoked', 'cooldown')
    );
