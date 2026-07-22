BEGIN;

ALTER TABLE hermes_tool_calls
    ADD COLUMN actor_user_id BIGINT,
    ADD COLUMN admin_actor_token_id BIGINT REFERENCES admin_tokens(id) ON DELETE SET NULL;

UPDATE hermes_tool_calls call
SET actor_user_id = COALESCE(principal.user_id, call.actor_id),
    admin_actor_token_id = CASE WHEN call.actor_source = 'token' THEN call.actor_id ELSE NULL END
FROM hermes_service_principals principal
WHERE principal.tenant_id = call.tenant_id;

UPDATE hermes_tool_calls
SET actor_user_id = actor_id
WHERE actor_user_id IS NULL;

ALTER TABLE hermes_tool_calls
    ALTER COLUMN actor_user_id SET NOT NULL;

DROP INDEX hermes_tool_calls_retention_idx;
DROP INDEX hermes_tool_calls_actor_called_idx;

ALTER TABLE hermes_tool_calls
    DROP CONSTRAINT hermes_tool_calls_log_category_check,
    DROP CONSTRAINT hermes_tool_calls_actor_role_check,
    DROP CONSTRAINT hermes_tool_calls_actor_id_check,
    DROP CONSTRAINT hermes_tool_calls_actor_source_check,
    DROP COLUMN ingested_at,
    DROP COLUMN log_category,
    DROP COLUMN actor_role,
    DROP COLUMN actor_id,
    DROP COLUMN actor_source;

ALTER TABLE hermes_conversations
    ADD COLUMN admin_actor_token_id BIGINT REFERENCES admin_tokens(id) ON DELETE SET NULL;

UPDATE hermes_conversations
SET admin_actor_token_id = CASE WHEN actor_source = 'token' THEN actor_id ELSE NULL END;

ALTER TABLE hermes_conversations
    DROP CONSTRAINT hermes_conversations_actor_role_check,
    DROP CONSTRAINT hermes_conversations_actor_id_check,
    DROP CONSTRAINT hermes_conversations_actor_source_check,
    DROP COLUMN actor_role,
    DROP COLUMN actor_id,
    DROP COLUMN actor_source;

ALTER TABLE hermes_audit_events
    ADD COLUMN actor_user_id BIGINT,
    ADD COLUMN admin_actor_token_id BIGINT REFERENCES admin_tokens(id) ON DELETE SET NULL;

UPDATE hermes_audit_events event
SET actor_user_id = COALESCE(principal.user_id, event.actor_id),
    admin_actor_token_id = CASE WHEN event.actor_source = 'token' THEN event.actor_id ELSE NULL END
FROM hermes_service_principals principal
WHERE principal.tenant_id = event.tenant_id;

UPDATE hermes_audit_events
SET actor_user_id = actor_id
WHERE actor_user_id IS NULL;

ALTER TABLE hermes_audit_events
    ALTER COLUMN actor_user_id SET NOT NULL,
    ADD FOREIGN KEY (tenant_id, actor_user_id)
        REFERENCES users(tenant_id, id) ON DELETE RESTRICT;

DROP INDEX hermes_audit_events_actor_ts;

ALTER TABLE hermes_audit_events
    DROP CONSTRAINT hermes_audit_events_actor_role_check,
    DROP CONSTRAINT hermes_audit_events_actor_id_check,
    DROP CONSTRAINT hermes_audit_events_actor_source_check,
    DROP COLUMN actor_role,
    DROP COLUMN actor_id,
    DROP COLUMN actor_source;

COMMIT;
