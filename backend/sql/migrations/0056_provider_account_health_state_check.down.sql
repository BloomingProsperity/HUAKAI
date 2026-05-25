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
    DROP CONSTRAINT IF EXISTS provider_accounts_health_state_check,
    ADD CONSTRAINT provider_accounts_health_state_check CHECK (
        health_state IN ('operational', 'degraded', 'failed', 'cooling_down', 'error')
    );
