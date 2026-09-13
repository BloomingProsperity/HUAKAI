BEGIN;

ALTER TABLE proxies
    DROP CONSTRAINT IF EXISTS proxies_quality_grade_check,
    DROP CONSTRAINT IF EXISTS proxies_quality_source_check,
    DROP CONSTRAINT IF EXISTS proxies_quality_latency_check,
    DROP CONSTRAINT IF EXISTS proxies_quality_success_latency_check;

ALTER TABLE proxies
    DROP COLUMN IF EXISTS quality_probed_at,
    DROP COLUMN IF EXISTS quality_ok,
    DROP COLUMN IF EXISTS quality_latency_ms,
    DROP COLUMN IF EXISTS quality_error_class,
    DROP COLUMN IF EXISTS quality_grade,
    DROP COLUMN IF EXISTS quality_source,
    DROP COLUMN IF EXISTS quality_success_at,
    DROP COLUMN IF EXISTS quality_success_latency_ms;

COMMIT;
