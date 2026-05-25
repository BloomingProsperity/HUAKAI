BEGIN;

-- 还原 action CHECK (移除 hermes.message.send)
ALTER TABLE hermes_audit_events
    DROP CONSTRAINT IF EXISTS hermes_audit_events_action_check;

ALTER TABLE hermes_audit_events
    ADD CONSTRAINT hermes_audit_events_action_check
        CHECK (action IN (
            'hermes.enable',
            'hermes.disable',
            'hermes.profile.create',
            'hermes.profile.rotate',
            'hermes.chat.start'
        ));

DROP INDEX IF EXISTS hermes_jwt_keys_active;
DROP TABLE IF EXISTS hermes_jwt_keys;

DROP INDEX IF EXISTS hermes_messages_conv_created;
DROP TABLE IF EXISTS hermes_messages;

DROP INDEX IF EXISTS hermes_conversations_owner_active;
DROP TABLE IF EXISTS hermes_conversations;

COMMIT;
