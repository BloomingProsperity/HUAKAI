-- Hermes 会话、消息与保留期查询。

-- name: CreateConversation :one
INSERT INTO hermes_conversations (
    tenant_id, owner_user_id, actor_source, actor_id, actor_role, title
)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(owner_user_id)::bigint,
    sqlc.arg(actor_source)::text,
    sqlc.arg(actor_id)::bigint,
    sqlc.arg(actor_role)::text,
    sqlc.narg(title)::text
)
RETURNING id;

-- name: GetConversation :one
SELECT conversation.*
FROM hermes_conversations conversation
WHERE conversation.id = sqlc.arg(id)::bigint
  AND conversation.tenant_id = sqlc.arg(tenant_id)::bigint
  AND conversation.owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND conversation.actor_source = sqlc.arg(actor_source)::text
  AND conversation.actor_id = sqlc.arg(actor_id)::bigint;

-- name: ListConversationsByOwner :many
SELECT conversation.*
FROM hermes_conversations conversation
WHERE conversation.tenant_id = sqlc.arg(tenant_id)::bigint
  AND conversation.owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND conversation.actor_source = sqlc.arg(actor_source)::text
  AND conversation.actor_id = sqlc.arg(actor_id)::bigint
  AND conversation.deleted_at IS NULL
ORDER BY conversation.last_message_at DESC NULLS LAST, conversation.updated_at DESC, conversation.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: SoftDeleteConversation :execrows
UPDATE hermes_conversations
SET deleted_at = COALESCE(deleted_at, NOW()),
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND actor_source = sqlc.arg(actor_source)::text
  AND actor_id = sqlc.arg(actor_id)::bigint;

-- name: UpdateConversationLastMessageAt :execrows
UPDATE hermes_conversations
SET last_message_at = sqlc.arg(ts)::timestamptz,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND actor_source = sqlc.arg(actor_source)::text
  AND actor_id = sqlc.arg(actor_id)::bigint
  AND deleted_at IS NULL;

-- name: AppendMessage :one
INSERT INTO hermes_messages (tenant_id, conversation_id, role, content, content_ciphertext, token_count, completed_at)
SELECT
    c.tenant_id,
    c.id,
    sqlc.arg(role)::text,
    sqlc.arg(content)::jsonb,
    sqlc.narg(content_ciphertext)::bytea,
    sqlc.narg(token_count)::integer,
    sqlc.narg(completed_at)::timestamptz
FROM hermes_conversations c
WHERE c.id = sqlc.arg(conversation_id)::bigint
  AND c.tenant_id = sqlc.arg(tenant_id)::bigint
  AND c.owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND c.actor_source = sqlc.arg(actor_source)::text
  AND c.actor_id = sqlc.arg(actor_id)::bigint
  AND c.deleted_at IS NULL
RETURNING id;

-- name: ListMessagesByConversation :many
SELECT m.id, m.tenant_id, m.conversation_id, m.role, m.content, m.content_ciphertext,
       m.token_count, m.completed_at, m.created_at
FROM hermes_messages m
INNER JOIN hermes_conversations c
    ON c.tenant_id = m.tenant_id
    AND c.id = m.conversation_id
    AND c.deleted_at IS NULL
WHERE m.tenant_id = sqlc.arg(tenant_id)::bigint
  AND m.conversation_id = sqlc.arg(conversation_id)::bigint
  AND c.owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND c.actor_source = sqlc.arg(actor_source)::text
  AND c.actor_id = sqlc.arg(actor_id)::bigint
ORDER BY m.created_at ASC, m.id ASC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: PurgeMessagesBefore :execrows
WITH lease AS MATERIALIZED (
    SELECT pg_try_advisory_xact_lock(
        hashtextextended('huakai.hermes.message-retention', 0)
    ) AS acquired
), victims AS (
    SELECT message.id
    FROM hermes_messages message
    CROSS JOIN lease
    WHERE lease.acquired
      AND message.created_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY message.created_at ASC, message.id ASC
    LIMIT sqlc.arg(batch_limit)::integer
    FOR UPDATE OF message SKIP LOCKED
)
DELETE FROM hermes_messages
WHERE id IN (SELECT id FROM victims)
RETURNING id;

-- name: PurgeConversationsBefore :execrows
WITH lease AS MATERIALIZED (
    SELECT pg_try_advisory_xact_lock(
        hashtextextended('huakai.hermes.conversation-retention', 0)
    ) AS acquired
), victims AS (
    SELECT conversation.id
    FROM hermes_conversations conversation
    CROSS JOIN lease
    WHERE lease.acquired
      AND COALESCE(conversation.last_message_at, conversation.created_at) < sqlc.arg(cutoff)::timestamptz
      AND NOT EXISTS (
          SELECT 1
          FROM hermes_messages message
          WHERE message.tenant_id = conversation.tenant_id
            AND message.conversation_id = conversation.id
      )
    ORDER BY COALESCE(conversation.last_message_at, conversation.created_at) ASC,
             conversation.id ASC
    LIMIT sqlc.arg(batch_limit)::integer
    FOR UPDATE OF conversation SKIP LOCKED
)
DELETE FROM hermes_conversations
WHERE id IN (SELECT id FROM victims)
RETURNING id;

-- name: UpdateMessageCompleted :execrows
UPDATE hermes_messages
SET token_count = sqlc.narg(token_count)::integer,
    completed_at = sqlc.arg(completed_at)::timestamptz
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;
