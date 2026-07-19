ALTER TABLE provider_accounts
    DROP CONSTRAINT IF EXISTS provider_accounts_quota_snapshot_shape_check,
    DROP CONSTRAINT IF EXISTS provider_accounts_quota_snapshot_outcome_check,
    DROP CONSTRAINT IF EXISTS provider_accounts_quota_snapshot_source_check,
    DROP COLUMN IF EXISTS quota_snapshot_error_class,
    DROP COLUMN IF EXISTS quota_snapshot_outcome,
    DROP COLUMN IF EXISTS quota_snapshot_source,
    DROP COLUMN IF EXISTS quota_snapshot_observed_at;
