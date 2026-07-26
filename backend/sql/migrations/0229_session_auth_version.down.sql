BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM session_families
    ) THEN
        RAISE EXCEPTION
            '不能回滚 0229：仍存在绑定认证版本的会话，请先撤销并清理这些会话';
    END IF;
END
$$;

ALTER TABLE session_families
    DROP CONSTRAINT IF EXISTS session_families_auth_version_check,
    DROP COLUMN IF EXISTS auth_version;

COMMIT;
