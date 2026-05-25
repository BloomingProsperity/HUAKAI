BEGIN;

ALTER TABLE invite_bindings DROP CONSTRAINT IF EXISTS invite_bindings_invite_code_fkey;
ALTER TABLE invite_bindings
    ADD CONSTRAINT invite_bindings_invite_code_fkey
    FOREIGN KEY (invite_code) REFERENCES invite_codes(code);

ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_family_id_fkey;
ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_family_id_fkey
    FOREIGN KEY (family_id) REFERENCES session_families(id) ON DELETE CASCADE;

ALTER TABLE session_tokens DROP CONSTRAINT IF EXISTS session_tokens_family_id_fkey;
ALTER TABLE session_tokens
    ADD CONSTRAINT session_tokens_family_id_fkey
    FOREIGN KEY (family_id) REFERENCES session_families(id) ON DELETE CASCADE;

ALTER TABLE invite_codes DROP CONSTRAINT IF EXISTS invite_codes_tenant_id_code_key;
ALTER TABLE session_families DROP CONSTRAINT IF EXISTS session_families_tenant_id_id_key;

COMMIT;
