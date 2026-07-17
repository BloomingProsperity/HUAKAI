-- sub2 真实数据 -> HUAKAI 测试种子映射
--
-- 目标库：仅允许 huakai_seed。脚本含数据库名硬保护；在其他数据库执行会立即报错，
-- 且不会进入清空或导入步骤。
--
-- 执行方式（由 Owner 执行，本文件不自动执行）：
--   PGPASSWORD=huakai psql -X -v ON_ERROR_STOP=1 \
--     -h 127.0.0.1 -U huakai -d huakai_seed \
--     -f scripts/sub2-seed/map.sql
--
-- 数据前提：
--   1. public 是 HUAKAI 已完成迁移的目标 schema；
--   2. sub2 是已载入真实数据的源 schema；
--   3. 本脚本仅用于测试和 UI 展示，不是生产迁移工具。
--
-- 测试级捷径与风险：
--   1. 用户密码：sub2.password_hash 的 bcrypt（常见前缀 $2a$10$）原样写入
--      public.users.password_hash。已核对 HUAKAI 当前口令校验器只接受 argon2id，没有 bcrypt
--      回退，因此这些用户不能直接使用原密码登录。此处只保留数据，不修改认证代码。
--   2. API key：HUAKAI 实现实际使用 bcrypt cost=10，而不是 SHA-256；key_prefix 为明文
--      bearer 的前 16 个字符。本脚本通过 pgcrypto 的 crypt/gen_salt 生成 bcrypt 哈希，
--      不在 public.api_keys 中保留明文。每次重跑盐值不同，但认证语义相同。若源 key 不符合
--      HUAKAI_API_KEY_PREFIX 配置出的 <base>_live_ / <base>_test_ 命名空间，仍会在 bcrypt
--      比对前被格式检查拒绝；本脚本按要求不改写源 key。
--   3. 上游凭证：不复制 sub2.accounts.credentials。public.provider_accounts.credentials 保持
--      空 JSON，public.account_credentials 也保持空；不伪造 bytea 密文、nonce 或 KEK key_id。
--      因此导入账户只供 UI 展示，不能用于真实上游请求。
--   4. provider/channel：插入 id=1 的测试占位 provider 和 channel，所有导入账户均挂到该
--      channel；这是“测试占位，二期换真注册表”，不能代表真实协议或路由归属。
--   5. account_type：只保留 HUAKAI CHECK 接受的类型；常见别名会归一化，未知类型降为
--      upstream_static。health_state 也按展示需要做保守归一化。若多个未删除源账户的 name
--      去空格后相同，会追加“[sub2:<id>]”以满足 HUAKAI 的租户内账户名唯一约束。
--   6. 订阅：为满足 HUAKAI plan 外键，从 sub2.groups 派生零价格、停售的测试 plan。
--      源订阅若结束时间不晚于开始时间，会把结束时间修正为开始时间后一秒；同用户同组若有
--      多条 active，除最新一条外均降为 cancelled，以满足目标唯一约束并保留全部展示行。
--   7. 幂等策略：在事务内 TRUNCATE public.tenants RESTART IDENTITY CASCADE。由于 HUAKAI
--      业务表均租户化，这会清空 huakai_seed 中所有 tenant 派生测试数据，而不只是下列七张表。
--      数据库名保护先于 TRUNCATE；绝不可把本脚本改成面向任何其他数据库。
--
-- 关键 parity：
--   users=195；user_balances=195；api_keys=259；pool_groups=23；
--   provider_accounts=51；user_subscriptions=22；默认 tenant/provider/channel 各 1；
--   public.user_balances.balance 合计必须精确等于 200000164.99321872，且等于 sub2.users.balance 合计。

\set ON_ERROR_STOP on

BEGIN;

DO $guard$
BEGIN
    IF current_database() <> 'huakai_seed' THEN
        RAISE EXCEPTION
            '拒绝执行：map.sql 只允许用于 huakai_seed，当前数据库为 %',
            current_database();
    END IF;
END
$guard$;

-- API key 的 bcrypt 生成依赖 pgcrypto；Owner 已授权在 seed 库创建此扩展。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

SET LOCAL search_path = public, pg_catalog;

-- 仅在数据库名保护通过后执行。CASCADE 用于清理所有指向租户数据的 FK 子表，保证可重跑。
TRUNCATE TABLE public.tenants RESTART IDENTITY CASCADE;

-- ---------------------------------------------------------------------------
-- 1. 默认租户
-- ---------------------------------------------------------------------------
INSERT INTO public.tenants (
    id, name, status, created_at, updated_at
)
VALUES (
    1, 'sub2-seed-default', 'active', now(), now()
);

SELECT setval(
    pg_get_serial_sequence('public.tenants', 'id'),
    (SELECT max(id) FROM public.tenants),
    true
);

-- ---------------------------------------------------------------------------
-- 2. 用户：保留源 id，便于后续所有用户外键直接复用。
-- ---------------------------------------------------------------------------
INSERT INTO public.users (
    id,
    tenant_id,
    email,
    display_name,
    role,
    status,
    password_hash,
    password_version,
    created_at,
    updated_at,
    deleted_at
)
SELECT
    u.id,
    1,
    u.email,
    COALESCE(
        NULLIF(btrim(u.username), ''),
        NULLIF(split_part(u.email, '@', 1), ''),
        '用户-' || u.id::text
    ),
    CASE lower(u.role)
        WHEN 'admin' THEN 'admin'
        ELSE 'user'
    END,
    CASE lower(u.status)
        WHEN 'pending_verification' THEN 'pending_verification'
        WHEN 'active' THEN 'active'
        WHEN 'disabled' THEN 'disabled'
        WHEN 'locked' THEN 'locked'
        WHEN 'reset_required' THEN 'reset_required'
        WHEN 'deleted' THEN 'deleted'
        ELSE 'active'
    END,
    u.password_hash,
    1,
    u.created_at,
    u.updated_at,
    u.deleted_at
FROM sub2.users AS u
ORDER BY u.id;

SELECT setval(
    pg_get_serial_sequence('public.users', 'id'),
    (SELECT max(id) FROM public.users),
    true
);

-- ---------------------------------------------------------------------------
-- 3. 用户余额：numeric 直传，不做浮点转换。
-- ---------------------------------------------------------------------------
INSERT INTO public.user_balances (
    tenant_id, user_id, balance, held, version, updated_at
)
SELECT
    1,
    u.id,
    u.balance,
    u.frozen_balance,
    0,
    u.updated_at
FROM sub2.users AS u
ORDER BY u.id;

-- balance 是本切片的硬 parity；不满足时回滚整个事务。
DO $balance_parity$
DECLARE
    source_total numeric;
    target_total numeric;
    expected_total constant numeric := 200000164.99321872;
BEGIN
    SELECT COALESCE(sum(balance), 0) INTO source_total FROM sub2.users;
    SELECT COALESCE(sum(balance), 0) INTO target_total FROM public.user_balances;

    IF source_total <> expected_total THEN
        RAISE EXCEPTION
            '源余额合计漂移：实际 %，预期 %', source_total, expected_total;
    END IF;
    IF target_total <> source_total THEN
        RAISE EXCEPTION
            '余额 parity 失败：sub2.users=%，public.user_balances=%',
            source_total, target_total;
    END IF;
END
$balance_parity$;

-- ---------------------------------------------------------------------------
-- 4. API keys：源明文只在本 SELECT 中送入 bcrypt，不写入目标表。
-- ---------------------------------------------------------------------------
INSERT INTO public.api_keys (
    id,
    tenant_id,
    user_id,
    name,
    key_hash,
    key_prefix,
    status,
    quota_policy_id,
    key_group_id,
    created_at,
    updated_at,
    expires_at,
    last_used_at,
    deleted_at
)
SELECT
    k.id,
    1,
    k.user_id,
    COALESCE(NULLIF(btrim(k.name), ''), '导入密钥-' || k.id::text),
    crypt(k.key, gen_salt('bf', 10)),
    left(k.key, 16),
    CASE lower(k.status)
        WHEN 'active' THEN 'active'
        WHEN 'disabled' THEN 'disabled'
        WHEN 'revoked' THEN 'revoked'
        WHEN 'expired' THEN 'expired'
        ELSE 'disabled'
    END,
    NULL,
    NULL,
    k.created_at,
    k.updated_at,
    k.expires_at,
    k.last_used_at,
    k.deleted_at
FROM sub2.api_keys AS k
ORDER BY k.id;

SELECT setval(
    pg_get_serial_sequence('public.api_keys', 'id'),
    (SELECT max(id) FROM public.api_keys),
    true
);

-- ---------------------------------------------------------------------------
-- 5. 账号池：保留 group id；HUAKAI 独有路由列全部采用目标默认值。
-- ---------------------------------------------------------------------------
INSERT INTO public.pool_groups (
    id, tenant_id, name, enabled, created_at, updated_at, deleted_at
)
SELECT
    g.id,
    1,
    g.name,
    lower(g.status) = 'active' AND g.deleted_at IS NULL,
    g.created_at,
    g.updated_at,
    g.deleted_at
FROM sub2.groups AS g
ORDER BY g.id;

-- 理论上真实数据有 23 个组；此兜底只为避免空源时占位 channel 无父池。
INSERT INTO public.pool_groups (
    id, tenant_id, name, enabled, created_at, updated_at
)
SELECT
    1, 1, 'sub2-seed-placeholder-pool', true, now(), now()
WHERE NOT EXISTS (SELECT 1 FROM public.pool_groups);

SELECT setval(
    pg_get_serial_sequence('public.pool_groups', 'id'),
    (SELECT max(id) FROM public.pool_groups),
    true
);

-- ---------------------------------------------------------------------------
-- 6a. 测试占位 provider/channel：二期应替换为真实 provider 注册表与真实池归属。
-- ---------------------------------------------------------------------------
INSERT INTO public.providers (
    id, tenant_id, code, display_name, upstream_protocol, enabled,
    created_at, updated_at
)
VALUES (
    1, 1, 'sub2-seed-placeholder', 'sub2 测试占位 Provider',
    'openai_chat', true, now(), now()
);

SELECT setval(
    pg_get_serial_sequence('public.providers', 'id'),
    (SELECT max(id) FROM public.providers),
    true
);

INSERT INTO public.channels (
    id, tenant_id, pool_group_id, name, enabled, created_at, updated_at
)
SELECT
    1,
    1,
    min(pg.id),
    'sub2-seed-placeholder-channel',
    true,
    now(),
    now()
FROM public.pool_groups AS pg;

SELECT setval(
    pg_get_serial_sequence('public.channels', 'id'),
    (SELECT max(id) FROM public.channels),
    true
);

-- ---------------------------------------------------------------------------
-- 6b. Provider accounts：只提供列表展示数据，不导入任何可用凭证。
-- ---------------------------------------------------------------------------
WITH normalized_accounts AS (
    SELECT
        a.*,
        NULLIF(btrim(a.name), '') AS normalized_name,
        count(*) FILTER (WHERE a.deleted_at IS NULL) OVER (
            PARTITION BY NULLIF(btrim(a.name), '')
        ) AS live_name_count
    FROM sub2.accounts AS a
)
INSERT INTO public.provider_accounts (
    id,
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    health_state,
    credentials,
    cap_concurrency,
    priority,
    last_dispatch_at,
    expires_at,
    created_at,
    updated_at,
    deleted_at
)
SELECT
    a.id,
    1,
    1,
    1,
    CASE
        WHEN a.normalized_name IS NULL THEN '导入账户-' || a.id::text
        WHEN a.deleted_at IS NULL AND a.live_name_count > 1
            THEN a.normalized_name || ' [sub2:' || a.id::text || ']'
        ELSE a.normalized_name
    END,
    CASE lower(a.type)
        WHEN 'oauth' THEN 'oauth'
        WHEN 'api_key' THEN 'api_key'
        WHEN 'apikey' THEN 'api_key'
        WHEN 'api-key' THEN 'api_key'
        WHEN 'service_account' THEN 'service_account'
        WHEN 'service-account' THEN 'service_account'
        WHEN 'session' THEN 'session'
        WHEN 'aws_sigv4' THEN 'aws_sigv4'
        WHEN 'upstream_static' THEN 'upstream_static'
        ELSE 'upstream_static'
    END,
    lower(a.status) = 'active'
        AND a.schedulable
        AND a.deleted_at IS NULL,
    CASE lower(a.status)
        WHEN 'active' THEN 'healthy'
        WHEN 'rate_limited' THEN 'throttled'
        WHEN 'throttled' THEN 'throttled'
        WHEN 'disabled' THEN 'revoked'
        WHEN 'revoked' THEN 'revoked'
        WHEN 'error' THEN 'revoked'
        ELSE 'cooldown'
    END,
    '{}'::jsonb,
    GREATEST(a.concurrency, 1),
    a.priority,
    a.last_used_at,
    a.expires_at,
    a.created_at,
    a.updated_at,
    a.deleted_at
FROM normalized_accounts AS a
ORDER BY a.id;

SELECT setval(
    pg_get_serial_sequence('public.provider_accounts', 'id'),
    (SELECT max(id) FROM public.provider_accounts),
    true
);

-- public.account_credentials 故意不插入：无法用测试脚本安全伪造 AES-GCM 密文与 KEK 元数据。

-- ---------------------------------------------------------------------------
-- 7a. 订阅计划测试派生：只为 user_subscriptions 外键和 UI 展示服务。
-- ---------------------------------------------------------------------------
INSERT INTO public.subscription_plans (
    id,
    tenant_id,
    name,
    description,
    price_cents,
    currency_code,
    validity_days,
    granted_group,
    daily_cap_usd,
    weekly_cap_usd,
    monthly_cap_usd,
    for_sale,
    enabled,
    sort_order,
    created_at,
    updated_at
)
SELECT
    g.id,
    1,
    g.name,
    COALESCE(g.description, '') || '（sub2 seed 测试派生计划）',
    0,
    'USD',
    GREATEST(g.default_validity_days, 1),
    g.name,
    g.daily_limit_usd,
    g.weekly_limit_usd,
    g.monthly_limit_usd,
    false,
    lower(g.status) = 'active' AND g.deleted_at IS NULL,
    g.sort_order,
    g.created_at,
    g.updated_at
FROM sub2.groups AS g
ORDER BY g.id;

SELECT setval(
    pg_get_serial_sequence('public.subscription_plans', 'id'),
    (SELECT max(id) FROM public.subscription_plans),
    true
);

-- ---------------------------------------------------------------------------
-- 7b. 用户订阅 best-effort 映射。源 usage 窗口计数不进入 HUAKAI 配额账本。
-- ---------------------------------------------------------------------------
WITH ranked AS (
    SELECT
        s.*,
        row_number() OVER (
            PARTITION BY s.user_id, s.group_id
            ORDER BY
                (s.deleted_at IS NULL AND lower(s.status) = 'active') DESC,
                s.created_at DESC,
                s.id DESC
        ) AS same_group_rank
    FROM sub2.user_subscriptions AS s
)
INSERT INTO public.user_subscriptions (
    id,
    tenant_id,
    user_id,
    plan_id,
    granted_group,
    daily_cap_usd,
    weekly_cap_usd,
    monthly_cap_usd,
    status,
    source,
    assigned_by_admin_id,
    assigned_by_actor,
    prev_user_group,
    starts_at,
    expires_at,
    cancelled_at,
    auto_renew,
    created_at,
    updated_at
)
SELECT
    s.id,
    1,
    s.user_id,
    s.group_id,
    g.name,
    g.daily_limit_usd,
    g.weekly_limit_usd,
    g.monthly_limit_usd,
    CASE
        WHEN s.deleted_at IS NOT NULL THEN 'cancelled'
        WHEN lower(s.status) = 'active' AND s.same_group_rank > 1 THEN 'cancelled'
        WHEN lower(s.status) = 'active' THEN 'active'
        WHEN lower(s.status) = 'expired' THEN 'expired'
        WHEN lower(s.status) = 'cancelled' THEN 'cancelled'
        WHEN lower(s.status) = 'revoked' THEN 'revoked'
        ELSE 'cancelled'
    END,
    'admin',
    NULL,
    CASE
        WHEN s.assigned_by IS NULL THEN NULL
        ELSE 'sub2-user:' || s.assigned_by::text
    END,
    'default',
    s.starts_at,
    GREATEST(s.expires_at, s.starts_at + interval '1 second'),
    CASE
        WHEN s.deleted_at IS NOT NULL THEN s.deleted_at
        WHEN lower(s.status) = 'cancelled' THEN s.updated_at
        WHEN lower(s.status) = 'active' AND s.same_group_rank > 1 THEN s.updated_at
        ELSE NULL
    END,
    false,
    s.created_at,
    s.updated_at
FROM ranked AS s
JOIN sub2.groups AS g ON g.id = s.group_id
JOIN public.users AS u ON u.tenant_id = 1 AND u.id = s.user_id
ORDER BY s.id;

SELECT setval(
    pg_get_serial_sequence('public.user_subscriptions', 'id'),
    (SELECT max(id) FROM public.user_subscriptions),
    true
);

-- ---------------------------------------------------------------------------
-- 8. 用量记录：保留源日志 id，并补齐分析查询所需的最小 claim 关系。
--
-- 金额口径：源 usage_logs 的成本列为 numeric(20,10)，HUAKAI 为 numeric(20,8)，
-- 这里显式 round 到 8 位小数。源表没有 currency_code；本测试种子假设其成本列
-- 与现有用量界面一致按 USD 计价。actual_cost 表示源侧实际扣除，映射到 HUAKAI
-- 最终结算金额；各成本分项按源值直传，可能因源侧倍率而不与 actual_cost 相加相等。
--
-- 状态口径：源 usage_logs 没有成功/失败列，不能可靠恢复错误分类。已持久化的用量行
-- 统一视为已完成请求；stream=true 映射为 stream_end_graceful，否则映射为
-- non_streaming。此假设只服务 seed UI，不作为生产审计或成功率基准。
--
-- claim 默认：源日志没有 HUAKAI 的请求指纹、计费策略版本、请求等级和 slot token，
-- 分别使用稳定 seed 标记、sub2-seed-import-v1、standard 与由日志 id 派生的 UUID；
-- endpoint_family 使用 chat 测试默认。源 group_id 找不到目标池时仅把 pooling_group_id
-- 置 NULL，不因此丢弃用量。这些值只用于满足目标约束和 UI 内连接，不是生产审计证据。
--
-- 幂等口径：usage_records 有 append-only DELETE/UPDATE 触发器，不能先删再写；因此
-- claim 和 usage 均复用源日志 id，并以 ON CONFLICT (id) DO NOTHING 支持本段重跑。
-- 整份脚本正常执行时，前面的 tenant TRUNCATE 已清空这些目标表。
-- ---------------------------------------------------------------------------
WITH eligible_usage_logs AS (
    SELECT
        l.*,
        pg.id AS target_pool_group_id,
        l.created_at
            - GREATEST(COALESCE(l.duration_ms, 0), 0) * interval '1 millisecond'
            AS derived_requested_at
    FROM sub2.usage_logs AS l
    JOIN public.users AS u
      ON u.tenant_id = 1
     AND u.id = l.user_id
    JOIN public.api_keys AS ak
      ON ak.tenant_id = 1
     AND ak.id = l.api_key_id
     AND ak.user_id = l.user_id
    JOIN public.provider_accounts AS pa
      ON pa.tenant_id = 1
     AND pa.id = l.account_id
    LEFT JOIN public.pool_groups AS pg
      ON pg.tenant_id = 1
     AND pg.id = l.group_id
)
INSERT INTO public.billing_ledger_claims (
    id,
    tenant_id,
    idempotency_key,
    request_fingerprint,
    api_key_id,
    user_id,
    logical_request_id,
    endpoint_family,
    requested_model,
    pooling_group_id,
    billing_policy_version,
    request_class,
    provider_account_id,
    acquisition_token,
    attempt_seq,
    predicted_cost,
    actual_cost,
    currency_code,
    status,
    reserved_at,
    settled_at,
    lease_expires_at
)
SELECT
    l.id,
    1,
    'sub2-seed-usage-log-' || l.id::text,
    'sub2-seed-usage-log-' || l.id::text,
    l.api_key_id,
    l.user_id,
    COALESCE(NULLIF(btrim(l.request_id), ''), 'sub2-seed-request-' || l.id::text),
    'chat',
    COALESCE(
        NULLIF(btrim(l.requested_model), ''),
        NULLIF(btrim(l.model), ''),
        'unknown'
    ),
    l.target_pool_group_id,
    'sub2-seed-import-v1',
    'standard',
    l.account_id,
    md5('sub2-seed-acquisition-' || l.id::text)::uuid,
    1,
    round(COALESCE(l.actual_cost, 0)::numeric, 8),
    round(COALESCE(l.actual_cost, 0)::numeric, 8),
    'USD',
    'committed',
    l.derived_requested_at,
    l.created_at,
    l.created_at
FROM eligible_usage_logs AS l
ORDER BY l.id
ON CONFLICT (id) DO NOTHING;

SELECT setval(
    pg_get_serial_sequence('public.billing_ledger_claims', 'id'),
    (SELECT max(id) FROM public.billing_ledger_claims),
    true
);

WITH eligible_usage_logs AS (
    SELECT
        l.*,
        l.created_at
            - GREATEST(COALESCE(l.duration_ms, 0), 0) * interval '1 millisecond'
            AS derived_requested_at
    FROM sub2.usage_logs AS l
    JOIN public.users AS u
      ON u.tenant_id = 1
     AND u.id = l.user_id
    JOIN public.api_keys AS ak
      ON ak.tenant_id = 1
     AND ak.id = l.api_key_id
     AND ak.user_id = l.user_id
    JOIN public.provider_accounts AS pa
      ON pa.tenant_id = 1
     AND pa.id = l.account_id
), prepared_usage AS (
    SELECT
        l.*,
        CASE
            WHEN l.first_token_ms IS NULL THEN NULL
            ELSE l.derived_requested_at
                + LEAST(
                    GREATEST(l.first_token_ms, 0),
                    GREATEST(COALESCE(l.duration_ms, l.first_token_ms), 0)
                  ) * interval '1 millisecond'
        END AS derived_first_byte_at
    FROM eligible_usage_logs AS l
)
INSERT INTO public.usage_records (
    id,
    tenant_id,
    claim_id,
    api_key_id,
    user_id,
    provider_account_id,
    acquisition_token,
    attempt_seq,
    tokens_input,
    tokens_output,
    cache_creation_tokens,
    cache_read_tokens,
    cache_creation_5m_tokens,
    cache_creation_1h_tokens,
    image_output_tokens,
    actual_cost,
    input_cost,
    output_cost,
    cache_creation_cost,
    cache_read_cost,
    image_output_cost,
    end_class,
    usage_source,
    pending_reconciliation,
    routing_reason,
    protocol_loss,
    requested_at,
    upstream_request_at,
    first_byte_at,
    first_event_at,
    last_event_at,
    settled_at,
    requested_model,
    upstream_model,
    stream,
    stream_state,
    delivered_token_count,
    settlement_source,
    cost_snapshot,
    image_count,
    image_size,
    image_size_breakdown,
    ip_address,
    user_agent
)
SELECT
    l.id,
    1,
    c.id,
    l.api_key_id,
    l.user_id,
    l.account_id,
    md5('sub2-seed-acquisition-' || l.id::text)::uuid,
    1,
    l.input_tokens,
    l.output_tokens,
    l.cache_creation_tokens,
    l.cache_read_tokens,
    l.cache_creation_5m_tokens,
    l.cache_creation_1h_tokens,
    l.image_output_tokens,
    round(COALESCE(l.actual_cost, 0)::numeric, 8),
    round(COALESCE(l.input_cost, 0)::numeric, 8),
    round(COALESCE(l.output_cost, 0)::numeric, 8),
    round(COALESCE(l.cache_creation_cost, 0)::numeric, 8),
    round(COALESCE(l.cache_read_cost, 0)::numeric, 8),
    round(COALESCE(l.image_output_cost, 0)::numeric, 8),
    CASE WHEN l.stream THEN 'stream_end_graceful' ELSE 'non_streaming' END,
    'reported',
    false,
    jsonb_build_object(
        'seed_source', 'sub2_usage_log',
        'source_usage_log_id', l.id
    ),
    '[]'::jsonb,
    l.derived_requested_at,
    l.derived_requested_at,
    l.derived_first_byte_at,
    CASE WHEN l.stream THEN l.derived_first_byte_at ELSE NULL END,
    CASE WHEN l.stream THEN l.created_at ELSE NULL END,
    l.created_at,
    COALESCE(
        NULLIF(btrim(l.requested_model), ''),
        NULLIF(btrim(l.model), ''),
        'unknown'
    ),
    COALESCE(NULLIF(btrim(l.upstream_model), ''), NULLIF(btrim(l.model), '')),
    l.stream,
    2,
    GREATEST(l.output_tokens, 0)::bigint,
    'provider_upstream',
    'sub2-seed:numeric20_10-to-numeric20_8',
    l.image_count,
    l.image_size,
    l.image_size_breakdown,
    l.ip_address,
    l.user_agent
FROM prepared_usage AS l
JOIN public.billing_ledger_claims AS c
  ON c.tenant_id = 1
 AND c.id = l.id
 AND c.idempotency_key = 'sub2-seed-usage-log-' || l.id::text
ORDER BY l.id
ON CONFLICT (id) DO NOTHING;

SELECT setval(
    pg_get_serial_sequence('public.usage_records', 'id'),
    (SELECT max(id) FROM public.usage_records),
    true
);

COMMIT;

-- ---------------------------------------------------------------------------
-- Parity 校验 SQL：默认随脚本执行，只读输出，不修改数据。
-- expected_rows 是本次已知源快照的期望值；source_rows/target_rows 应相等。
-- ---------------------------------------------------------------------------
WITH parity AS (
    SELECT 'users'::text AS item, 195::bigint AS expected_rows,
           (SELECT count(*) FROM sub2.users)::bigint AS source_rows,
           (SELECT count(*) FROM public.users WHERE tenant_id = 1)::bigint AS target_rows
    UNION ALL
    SELECT 'user_balances', 195,
           (SELECT count(*) FROM sub2.users),
           (SELECT count(*) FROM public.user_balances WHERE tenant_id = 1)
    UNION ALL
    SELECT 'api_keys', 259,
           (SELECT count(*) FROM sub2.api_keys),
           (SELECT count(*) FROM public.api_keys WHERE tenant_id = 1)
    UNION ALL
    SELECT 'pool_groups', 23,
           (SELECT count(*) FROM sub2.groups),
           (SELECT count(*) FROM public.pool_groups
             WHERE tenant_id = 1 AND name <> 'sub2-seed-placeholder-pool')
    UNION ALL
    SELECT 'provider_accounts', 51,
           (SELECT count(*) FROM sub2.accounts),
           (SELECT count(*) FROM public.provider_accounts WHERE tenant_id = 1)
    UNION ALL
    SELECT 'user_subscriptions', 22,
           (SELECT count(*) FROM sub2.user_subscriptions),
           (SELECT count(*) FROM public.user_subscriptions WHERE tenant_id = 1)
    UNION ALL
    SELECT 'usage_records', 8539,
           (SELECT count(*) FROM sub2.usage_logs),
           (SELECT count(*) FROM public.usage_records WHERE tenant_id = 1)
)
SELECT
    item,
    expected_rows,
    source_rows,
    target_rows,
    source_rows = expected_rows AS source_matches_snapshot,
    target_rows = source_rows AS parity_ok
FROM parity
ORDER BY item;

-- 用量映射跳过统计。skipped_rows 是去重后的总跳过行数；各原因列可能重叠。
-- API key 存在但不属于日志 user_id 时按主体错配跳过，避免 UI 错误归因。
WITH usage_reference_check AS (
    SELECT
        l.id,
        l.user_id AS source_user_id,
        u.id AS target_user_id,
        ak.id AS target_api_key_id,
        ak.user_id AS target_api_key_user_id,
        pa.id AS target_provider_account_id
    FROM sub2.usage_logs AS l
    LEFT JOIN public.users AS u
      ON u.tenant_id = 1
     AND u.id = l.user_id
    LEFT JOIN public.api_keys AS ak
      ON ak.tenant_id = 1
     AND ak.id = l.api_key_id
    LEFT JOIN public.provider_accounts AS pa
      ON pa.tenant_id = 1
     AND pa.id = l.account_id
)
SELECT
    count(*)::bigint AS source_usage_rows,
    count(*) FILTER (
        WHERE target_user_id IS NOT NULL
          AND target_api_key_id IS NOT NULL
          AND target_api_key_user_id = source_user_id
          AND target_provider_account_id IS NOT NULL
    )::bigint AS eligible_rows,
    count(*) FILTER (
        WHERE target_user_id IS NULL
           OR target_api_key_id IS NULL
           OR target_api_key_user_id IS DISTINCT FROM source_user_id
           OR target_provider_account_id IS NULL
    )::bigint AS skipped_rows,
    count(*) FILTER (WHERE target_user_id IS NULL)::bigint AS missing_user_rows,
    count(*) FILTER (WHERE target_api_key_id IS NULL)::bigint AS missing_api_key_rows,
    count(*) FILTER (
        WHERE target_api_key_id IS NOT NULL
          AND target_api_key_user_id IS DISTINCT FROM source_user_id
    )::bigint AS api_key_user_mismatch_rows,
    count(*) FILTER (WHERE target_provider_account_id IS NULL)::bigint
        AS missing_provider_account_rows
FROM usage_reference_check;

SELECT
    (SELECT COALESCE(sum(balance), 0) FROM sub2.users) AS source_balance_total,
    (SELECT COALESCE(sum(balance), 0)
       FROM public.user_balances
      WHERE tenant_id = 1) AS target_balance_total,
    200000164.99321872::numeric AS expected_balance_total,
    (SELECT COALESCE(sum(balance), 0)
       FROM public.user_balances
      WHERE tenant_id = 1) = 200000164.99321872::numeric AS balance_parity_ok;

SELECT
    (SELECT count(*) FROM public.tenants WHERE id = 1) AS tenant_rows,
    (SELECT count(*) FROM public.providers WHERE tenant_id = 1 AND id = 1) AS placeholder_provider_rows,
    (SELECT count(*) FROM public.channels WHERE tenant_id = 1 AND id = 1) AS placeholder_channel_rows,
    (SELECT count(*) FROM public.account_credentials WHERE tenant_id = 1) AS credential_rows_expected_zero;
