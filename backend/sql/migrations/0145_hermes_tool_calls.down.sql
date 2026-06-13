BEGIN;

-- Revert the hermes_audit_events action whitelist to its pre-0145 set (the 0059
-- state), dropping the hermes.tool.* actions.
ALTER TABLE hermes_audit_events
    DROP CONSTRAINT IF EXISTS hermes_audit_events_action_check,
    ADD CONSTRAINT hermes_audit_events_action_check
        CHECK (action IN (
            'hermes.enable',
            'hermes.disable',
            'hermes.profile.create',
            'hermes.profile.rotate',
            'hermes.chat.start',
            'hermes.message.send',
            'hermes.conversation.delete'
        ));

DROP TABLE IF EXISTS hermes_tool_calls;

COMMIT;
