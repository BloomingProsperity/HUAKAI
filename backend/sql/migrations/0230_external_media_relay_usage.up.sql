BEGIN;

ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_settlement_source_chk;

ALTER TABLE usage_records
    ADD CONSTRAINT usage_records_settlement_source_chk CHECK (
        (settlement_source = 'provider_upstream'
            AND provider_account_id IS NOT NULL
            AND acquisition_token IS NOT NULL)
        OR
        (settlement_source IN ('response_cache_l2', 'external_media_relay')
            AND provider_account_id IS NULL
            AND acquisition_token IS NULL)
    );

COMMENT ON COLUMN usage_records.settlement_source IS
    'provider_upstream=账号池上游；response_cache_l2=无上游缓存命中；external_media_relay=部署者配置的无账号异步媒体中继。';

COMMIT;
