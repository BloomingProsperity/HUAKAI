-- 绑定级并发上限只依赖 acquisition 的活跃状态，不维护第二份可漂移计数器。
ALTER TABLE pool_slot_acquisitions
    ADD COLUMN binding_id bigint;

COMMENT ON COLUMN pool_slot_acquisitions.binding_id IS
    '命中 model_pool_bindings 的可空标识；存量行为 NULL，新获取写入实际 binding。';

CREATE INDEX idx_slot_acq_binding_acquired
    ON pool_slot_acquisitions (binding_id, status)
    WHERE status = 'acquired';
