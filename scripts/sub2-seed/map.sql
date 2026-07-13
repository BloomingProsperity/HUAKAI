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
