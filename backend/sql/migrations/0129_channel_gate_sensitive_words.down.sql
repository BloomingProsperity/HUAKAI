-- 0129_channel_gate_sensitive_words.down.sql
--
-- Undo channel-level sensitive-word obfuscation cloak (RR-05).

BEGIN;

ALTER TABLE channels
    DROP COLUMN IF EXISTS sensitive_words;

COMMIT;
