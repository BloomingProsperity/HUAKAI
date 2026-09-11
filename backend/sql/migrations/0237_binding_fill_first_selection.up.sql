-- 0237:绑定选号增加 fill_first。不改默认 strict_priority，未配置绑定行为不变。

BEGIN;

ALTER TABLE model_pool_bindings
    DROP CONSTRAINT IF EXISTS model_pool_bindings_selection_mode_check;

ALTER TABLE model_pool_bindings
    ADD CONSTRAINT model_pool_bindings_selection_mode_check
    CHECK (selection_mode IN ('strict_priority', 'priority_weighted', 'fill_first'));

COMMIT;
