-- 0236 down:仅在没有覆盖行和资金日志时回退。已产生的绝对价变更是永久资金事实。

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM model_rate_overrides) THEN
        RAISE EXCEPTION 'refuse 0236 down: model_rate_overrides still has rows';
    END IF;
    IF EXISTS (SELECT 1 FROM model_rate_override_audit_log) THEN
        RAISE EXCEPTION 'refuse 0236 down: model_rate_override_audit_log still has rows';
    END IF;
END
$$;

DROP TABLE IF EXISTS model_rate_override_audit_log;
DROP TABLE IF EXISTS model_rate_overrides;

COMMIT;
