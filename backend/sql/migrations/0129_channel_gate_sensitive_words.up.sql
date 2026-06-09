-- 0129_channel_gate_sensitive_words.up.sql
--
-- Channel-level opt-in sensitive-word obfuscation cloak (RR-05).

BEGIN;

ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS sensitive_words text[] NOT NULL DEFAULT ARRAY[]::text[];

COMMENT ON COLUMN channels.sensitive_words IS
    'Opt-in keyword list for RR-05 sensitive-word obfuscation cloak. Empty array = disabled.';

COMMIT;
