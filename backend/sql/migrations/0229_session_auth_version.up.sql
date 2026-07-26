BEGIN;

ALTER TABLE session_families
    ADD COLUMN auth_version integer;

UPDATE session_families sf
SET auth_version = u.password_version
FROM users u
WHERE u.tenant_id = sf.tenant_id
  AND u.id = sf.user_id
  AND sf.auth_version IS NULL;

ALTER TABLE session_families
    ALTER COLUMN auth_version SET DEFAULT 1,
    ALTER COLUMN auth_version SET NOT NULL,
    ADD CONSTRAINT session_families_auth_version_check CHECK (auth_version >= 1);

COMMIT;
