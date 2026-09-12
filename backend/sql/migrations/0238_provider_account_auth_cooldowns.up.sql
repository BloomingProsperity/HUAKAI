-- 0238:auth 降级车道持久化。请求时鉴权失败的退避与硬禁此前只存在于网关进程内存,
-- 多副本各学各的、重启后硬禁坏号复活、运营 resume 只清本副本。本表成为该车道唯一真相:
-- 选号候选查询直接排除阻断账号,进程镜像只承担本地即时性与写失败兜底。
-- 一账号一行;行不存在或 strike=0 且无截止且未硬禁 = 车道无状态。硬禁永不自动过期,只由运营 resume
-- (行保留为带 resumed_at 的墓碑)或显式凭据轮换(删除行)解除;成功请求只清除软退避。

BEGIN;

CREATE TABLE provider_account_auth_cooldowns (
    provider_account_id   bigint      NOT NULL PRIMARY KEY,
    tenant_id             bigint      NOT NULL,
    -- 连续鉴权失败计数;每个退避窗口至多 +1,凭据版本前进时重置。
    strike                integer     NOT NULL DEFAULT 0 CHECK (strike >= 0),
    -- 此刻之前账号被临时移出选号;NULL 表示没有生效中的软退避(例如仅由刷新永久失效直接硬禁)。
    auth_until            timestamptz,
    -- 硬禁:铁证类失败达到 strike 上限,或凭据刷新拿到永久失效。
    hard_disabled         boolean     NOT NULL DEFAULT false,
    -- 记录本行对应的凭据版本;更高版本的失败视作全新账号。
    credential_version    integer     NOT NULL DEFAULT 0 CHECK (credential_version >= 0),
    last_failure_class    text        NOT NULL DEFAULT 'ambiguous'
                              CHECK (last_failure_class IN ('ambiguous', 'iron_clad', 'refresh_permanent')),
    last_escalated_at     timestamptz,
    hard_disabled_at      timestamptz,
    -- 运营 resume 的时刻(墓碑):早于它的本地硬禁补写视为过时、不得写回;晚于它到达的新失败证据照常生效。
    resumed_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT provider_account_auth_cooldowns_account_fkey
        FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT provider_account_auth_cooldowns_hard_shape_check CHECK (
        (hard_disabled AND hard_disabled_at IS NOT NULL)
        OR (NOT hard_disabled AND hard_disabled_at IS NULL)
    )
);

COMMENT ON TABLE provider_account_auth_cooldowns IS
    'auth 降级车道的跨副本真相:账号级鉴权失败退避与硬禁;一账号一行,行不存在即无状态。';

COMMIT;
