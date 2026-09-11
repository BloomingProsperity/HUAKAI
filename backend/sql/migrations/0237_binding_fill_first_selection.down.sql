-- 0237 down:仅在没有 fill_first 行时收回约束。

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM model_pool_bindings
        WHERE selection_mode = 'fill_first'
    ) THEN
        RAISE EXCEPTION '拒绝回退 0237：仍存在 fill_first 绑定';
    END IF;
END
$$;

ALTER TABLE model_pool_bindings
    DROP CONSTRAINT IF EXISTS model_pool_bindings_selection_mode_check;

ALTER TABLE model_pool_bindings
    ADD CONSTRAINT model_pool_bindings_selection_mode_check
    CHECK (selection_mode IN ('strict_priority', 'priority_weighted'));

COMMIT;
