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
    DROP CONSTRAINT IF EXISTS provider_accounts_health_state_check,
    ADD CONSTRAINT provider_accounts_health_state_check CHECK (
        health_state IN ('healthy', 'throttled', 'revoked', 'cooldown')
    );
