-- 已产生绑定归属数据后禁止静默回滚，避免丢失并发审计锚点。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'pool_slot_acquisitions'
          AND column_name = 'binding_id'
    ) THEN
        IF EXISTS (
            SELECT 1
            FROM pool_slot_acquisitions
            WHERE binding_id IS NOT NULL
        ) THEN
            RAISE EXCEPTION '0183 rollback refused: pool_slot_acquisitions.binding_id contains data';
        END IF;
    END IF;
END
$$;

DROP INDEX IF EXISTS idx_slot_acq_binding_acquired;

ALTER TABLE pool_slot_acquisitions
    DROP COLUMN IF EXISTS binding_id;
