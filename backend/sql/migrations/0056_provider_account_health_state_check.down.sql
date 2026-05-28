-- 同 up：先 DROP 当前（新）CHECK，再 UPDATE 回老值，最后 ADD 老 CHECK。
-- 否则在新 CHECK（healthy/throttled/revoked/cooldown）仍生效时 UPDATE 成
-- operational/degraded/failed/cooling_down 会立即违反约束。
ALTER TABLE provider_accounts
    DROP CONSTRAINT IF EXISTS provider_accounts_health_state_check;

UPDATE provider_accounts
SET health_state = CASE health_state
    WHEN 'healthy' THEN 'operational'
    WHEN 'throttled' THEN 'degraded'
    WHEN 'revoked' THEN 'failed'
    WHEN 'cooldown' THEN 'cooling_down'
    ELSE health_state
END
WHERE health_state IN ('healthy', 'throttled', 'revoked', 'cooldown');

ALTER TABLE provider_accounts
    ALTER COLUMN health_state SET DEFAULT 'operational';

ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_health_state_check CHECK (
        health_state IN ('operational', 'degraded', 'failed', 'cooling_down', 'error')
    );
