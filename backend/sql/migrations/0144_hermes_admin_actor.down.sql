BEGIN;

ALTER TABLE hermes_conversations
    DROP COLUMN IF EXISTS admin_actor_token_id;

ALTER TABLE hermes_audit_events
    DROP COLUMN IF EXISTS admin_actor_token_id;

COMMIT;
