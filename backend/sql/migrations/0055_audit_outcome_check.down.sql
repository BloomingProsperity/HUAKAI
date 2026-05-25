ALTER TABLE oauth_refresh_audit_events
    DROP CONSTRAINT IF EXISTS audit_outcome_check,
    DROP CONSTRAINT IF EXISTS oauth_refresh_audit_events_outcome_check,
    ADD CONSTRAINT oauth_refresh_audit_events_outcome_check CHECK (outcome IN (
        'cache_hit',
        'refresh_lock_held',
        'refresh_succeeded',
        'refresh_token_rotated',
        'db_version_conflict',
        'invalid_grant_race_recovered',
        'storm_budget_exhausted',
        'cas_lost',
        'token_malformed',
        'oauth_401_force_refresh',
        'permanent_disable',
        'mimicry_applied'
    ));
