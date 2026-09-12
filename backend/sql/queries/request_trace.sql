-- 按网关请求标识拼装调用链路时间线的只读查询(FE-GAP-002 第五切)。
-- 权威关联键是 billing_ledger_claims.logical_request_id(网关签发);用量、账本事件、健康/路由/限流/刷新
-- 审计事件与信任链收据按该标识 join。每条查询都带租户谓词(tenant_id <= 0 表示部署者跨租户查询),
-- 任一表无对应行只让对应段为空,由投影层打降级标记,不让整个查询失败。

-- name: GetRequestTraceClaim :one
-- 请求头信息:一逻辑请求一行(失败切换复用同一行并递增 attempt_seq)。HUAKAI 有两把请求标识:
-- 账本 claim 的 logical_request_id(客户端幂等键或网关生成),以及响应头 / 收据 / 审计使用的网关
-- HTTP 请求标识(写在 billing_events.audit_request_id)。这里同时按两把键找到 claim,并回传该 claim
-- 关联的全部 HTTP 标识,供后续按审计与收据表 join。同一标识若存在多行(不同指纹的幂等冲突),取最新
-- reserved_at 的一行。user_id <= 0 表示不按用户收敛(管理面);> 0 表示最终用户只看自己的请求。
WITH candidates AS (
    SELECT blc.id
    FROM billing_ledger_claims blc
    WHERE blc.logical_request_id = sqlc.arg(request_id)::text
      AND (sqlc.arg(tenant_id)::bigint <= 0 OR blc.tenant_id = sqlc.arg(tenant_id)::bigint)
      AND (sqlc.arg(user_id)::bigint <= 0 OR blc.user_id = sqlc.arg(user_id)::bigint)
    UNION
    SELECT be.claim_id
    FROM billing_events be
    JOIN billing_ledger_claims blc ON blc.id = be.claim_id AND blc.tenant_id = be.tenant_id
    WHERE be.audit_request_id = sqlc.arg(request_id)::text
      AND (sqlc.arg(tenant_id)::bigint <= 0 OR be.tenant_id = sqlc.arg(tenant_id)::bigint)
      AND (sqlc.arg(user_id)::bigint <= 0 OR blc.user_id = sqlc.arg(user_id)::bigint)
)
SELECT
    blc.id,
    blc.tenant_id,
    blc.logical_request_id,
    blc.endpoint_family,
    blc.requested_model,
    blc.pooling_group_id,
    pg.name AS pool_name,
    blc.api_key_id,
    blc.user_id,
    blc.request_class,
    blc.billing_effect,
    blc.provider_account_id,
    blc.attempt_seq,
    blc.predicted_cost,
    blc.actual_cost,
    blc.currency_code,
    blc.status,
    blc.aborted_reason,
    blc.reserved_at,
    blc.settled_at,
    COALESCE(
        (SELECT array_agg(DISTINCT be.audit_request_id ORDER BY be.audit_request_id)
         FROM billing_events be
         WHERE be.claim_id = blc.id AND be.tenant_id = blc.tenant_id AND be.audit_request_id IS NOT NULL),
        ARRAY[]::text[]
    )::text[] AS http_request_ids,
    -- 同一标识命中的 claim 数;>1 时投影打 multiple_claims_for_id 标记而不是静默折叠。
    (SELECT count(*) FROM candidates)::bigint AS candidate_count
FROM billing_ledger_claims blc
JOIN candidates c ON c.id = blc.id
LEFT JOIN pool_groups pg
    ON pg.id = blc.pooling_group_id
   AND pg.tenant_id = blc.tenant_id
ORDER BY blc.reserved_at DESC, blc.id DESC
LIMIT 1;

-- name: ListRequestTraceUsageRecords :many
-- 已结算的上游尝试:每个 (claim, attempt_seq) 一行,含账号代号、渠道、厂商、结束分类、用量与费用分项、
-- 时间戳与选号解释。已拿到账号后中止的尝试也会落一行(结束分类 unknown_termination、选号解释为空);
-- 只有拿到账号之前就中止的尝试不在此表(由账本事件补位,账号未知)。
SELECT
    ur.id,
    ur.attempt_seq,
    ur.provider_account_id,
    pa.name AS provider_account_name,
    pa.channel_id,
    p.code AS provider_code,
    ur.requested_model,
    ur.upstream_model,
    ur.stream,
    ur.end_class,
    ur.usage_source,
    ur.settlement_source,
    ur.pending_reconciliation,
    ur.stream_state,
    ur.stream_terminated_reason,
    ur.delivered_token_count,
    ur.tokens_input,
    ur.tokens_output,
    ur.cache_creation_tokens,
    ur.cache_read_tokens,
    ur.image_output_tokens,
    ur.actual_cost,
    ur.input_cost,
    ur.output_cost,
    ur.cache_creation_cost,
    ur.cache_read_cost,
    ur.image_output_cost,
    ur.routing_reason,
    ur.protocol_loss,
    ur.requested_at,
    ur.upstream_request_at,
    ur.first_byte_at,
    ur.first_event_at,
    ur.last_event_at,
    ur.settled_at
FROM usage_records ur
LEFT JOIN provider_accounts pa
    ON pa.id = ur.provider_account_id
   AND pa.tenant_id = ur.tenant_id
LEFT JOIN providers p
    ON p.id = pa.provider_id
   AND p.tenant_id = ur.tenant_id
WHERE ur.claim_id = sqlc.arg(claim_id)::bigint
  AND ur.tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY ur.attempt_seq ASC, ur.settled_at ASC, ur.id ASC;

-- name: ListRequestTraceBillingEvents :many
-- 账本事件(永久保留):每次尝试的提交 / 中止与对账追加,构成用量明细过期后仍可用的时间线骨架。
SELECT
    be.id,
    be.event_type,
    be.actual_cost,
    be.actual_cost_signed,
    be.end_class,
    be.usage_source,
    be.occurred_at
FROM billing_events be
WHERE be.claim_id = sqlc.arg(claim_id)::bigint
  AND be.tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY be.occurred_at ASC, be.id ASC;

-- name: ListRequestTraceAuditEvents :many
-- 与该请求相关的账号级审计事件:健康状态转换、路由审计、限流/过载、凭据刷新。这些表写的是网关 HTTP
-- 请求标识,调用方传入该 claim 关联的全部标识(逻辑标识 + HTTP 标识)。统一成
-- (来源, 事件类型, 原因, 账号代号, 时刻) 的时间线子记录;payload 不回传(可能含上游原文)。
SELECT
    'channel_health'::text AS source,
    che.event_type::text AS event_type,
    che.reason_class::text AS reason,
    COALESCE(che.previous_state, '')::text AS previous_state,
    che.new_state::text AS new_state,
    che.provider_account_id,
    che.occurred_at
FROM channel_health_audit_events che
WHERE che.request_id = ANY(sqlc.arg(request_ids)::text[])
  AND che.tenant_id = sqlc.arg(tenant_id)::bigint
UNION ALL
SELECT
    'pool_routing'::text,
    pe.event_type::text,
    COALESCE(pe.reason, '')::text,
    ''::text,
    ''::text,
    pe.provider_account_id,
    pe.created_at
FROM pool_routing_audit_events pe
WHERE pe.request_id = ANY(sqlc.arg(request_ids)::text[])
  AND pe.tenant_id = sqlc.arg(tenant_id)::bigint
UNION ALL
SELECT
    'rate_limit'::text,
    re.event_type::text,
    COALESCE(re.rate_limit_reason, '')::text,
    ''::text,
    ''::text,
    re.provider_account_id,
    re.occurred_at
FROM rate_limit_audit_events re
WHERE re.upstream_request_id = ANY(sqlc.arg(request_ids)::text[])
  AND re.tenant_id = sqlc.arg(tenant_id)::bigint
UNION ALL
SELECT
    'oauth_refresh'::text,
    oe.outcome::text,
    COALESCE(oe.storm_scope, '')::text,
    ''::text,
    ''::text,
    oe.provider_account_id,
    oe.occurred_at
FROM oauth_refresh_audit_events oe
WHERE oe.request_id = ANY(sqlc.arg(request_ids)::text[])
  AND oe.tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY 7 ASC, 1 ASC;

-- name: GetRequestTraceReceipt :one
-- 信任链收据摘要:账本条目标识、签名公钥指纹、模型链;hop_chain 含上游跳点,由投影层按角色决定是否回传。
-- 信任链按网关 HTTP 请求标识落账。租户未知(tenant_id 为空)的历史条目只在 allow_null_tenant 为真
--(部署者档位,且调用方已用 claim 锁定租户)时匹配,租户管理员与最终用户只读明确归属本租户的条目。
SELECT
    ale.ledger_id,
    ale.occurred_at,
    ale.pubkey_fingerprint,
    ale.model_chain,
    ale.hop_chain
FROM audit_ledger_entries ale
WHERE ale.request_id = ANY(sqlc.arg(request_ids)::text[])
  AND (
      ale.tenant_id = sqlc.arg(tenant_id)::bigint
      OR (sqlc.arg(allow_null_tenant)::boolean AND ale.tenant_id IS NULL)
  )
ORDER BY ale.occurred_at DESC
LIMIT 1;

-- name: GetRequestTraceCostReceipt :one
-- 面向用户的签名费用收据(与 /v1/receipts 同源):按网关 HTTP 请求标识与租户取最新一份,只回传摘要。
SELECT
    r.request_id,
    r.receipt_sequence,
    r.model,
    r.input_tokens,
    r.output_tokens,
    r.cached_tokens,
    r.cost_usd_micros,
    r.created_at
FROM user_cost_receipts r
WHERE r.request_id = ANY(sqlc.arg(request_ids)::text[])
  AND r.tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY r.receipt_sequence DESC
LIMIT 1;
