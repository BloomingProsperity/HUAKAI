BEGIN;

ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS cost_snapshot text;

COMMENT ON COLUMN usage_records.cost_snapshot IS
    'Pricing evaluator model used for this charge, e.g. flat or tiered:v<pricing-version>. Nullable for pre-0083 rows and non-priced settlement paths.';

COMMIT;
