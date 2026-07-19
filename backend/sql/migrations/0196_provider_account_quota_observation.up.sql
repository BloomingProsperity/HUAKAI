ALTER TABLE provider_accounts
    ADD COLUMN quota_snapshot_observed_at timestamptz,
    ADD COLUMN quota_snapshot_source text,
    ADD COLUMN quota_snapshot_outcome text,
    ADD COLUMN quota_snapshot_error_class text;

ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_quota_snapshot_source_check
        CHECK (quota_snapshot_source IS NULL OR quota_snapshot_source IN ('usage_endpoint', 'response_headers')),
    ADD CONSTRAINT provider_accounts_quota_snapshot_outcome_check
        CHECK (quota_snapshot_outcome IS NULL OR quota_snapshot_outcome IN ('success', 'partial', 'failed')),
    ADD CONSTRAINT provider_accounts_quota_snapshot_shape_check
        CHECK (
            (
                quota_snapshot_observed_at IS NULL
                AND quota_snapshot_source IS NULL
                AND quota_snapshot_outcome IS NULL
                AND quota_snapshot_error_class IS NULL
            )
            OR
            (
                quota_snapshot_observed_at IS NOT NULL
                AND quota_snapshot_source IS NOT NULL
                AND quota_snapshot_outcome IS NOT NULL
                AND (
                    (quota_snapshot_outcome = 'failed' AND quota_snapshot_error_class IS NOT NULL)
                    OR
                    (quota_snapshot_outcome IN ('success', 'partial') AND quota_snapshot_error_class IS NULL)
                )
            )
        );
