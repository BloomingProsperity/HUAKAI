BEGIN;

DROP INDEX IF EXISTS hermes_messages_retention_created;

ALTER TABLE hermes_messages
    DROP COLUMN IF EXISTS content_ciphertext;

COMMIT;
