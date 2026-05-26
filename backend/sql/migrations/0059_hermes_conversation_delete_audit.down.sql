BEGIN;

ALTER TABLE hermes_audit_events
    DROP CONSTRAINT IF EXISTS hermes_audit_events_action_check;

ALTER TABLE hermes_audit_events
    ADD CONSTRAINT hermes_audit_events_action_check
        CHECK (action IN (
            'hermes.enable',
            'hermes.disable',
            'hermes.profile.create',
            'hermes.profile.rotate',
            'hermes.chat.start',
            'hermes.message.send'
        ));

COMMIT;
