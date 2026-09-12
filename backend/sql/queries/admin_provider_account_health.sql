-- name: GetAdminProviderAccountHealth :one
SELECT
    pa.id,
    pa.tenant_id,
    COALESCE(p.code, '')::text AS provider_code,
    pa.account_type,
    COALESCE(current_ac.vendor, '')::text AS credential_vendor,
    COALESCE(current_ac.auth_mode, '')::text AS credential_auth_mode,
    current_ac.project_ref AS credential_project_ref,
    COALESCE(current_ac.serving_credential_candidates, 0)::integer AS serving_credential_candidates,
    subscription_state.vendor AS subscription_vendor,
    subscription_state.normalized_plan AS subscription_plan,
    subscription_state.raw_plan AS subscription_raw_plan,
    subscription_state.scope_kind AS subscription_scope,
    subscription_state.source_type AS subscription_source,
    subscription_state.trust_level AS subscription_trust,
    subscription_state.verification_status AS subscription_verification,
    subscription_state.state_status AS subscription_status,
    subscription_state.mapping_version AS subscription_mapping_version,
    subscription_state.error_class AS subscription_error_class,
    subscription_state.first_observed_at AS subscription_first_observed_at,
    subscription_state.observed_at AS subscription_observed_at,
    subscription_state.changed_at AS subscription_changed_at,
    pa.health_state,
    pa.health_state_until,
    pa.enabled,
    pa.disable_cooling,
    pa.credential_state,
    pa.model_rate_limits,
    pa.rate_limit_reset_at,
    pa.overload_until,
    pa.temp_unschedulable_until,
    EXISTS (
        SELECT 1
        FROM channels c
        WHERE c.id = pa.channel_id
          AND c.tenant_id = pa.tenant_id
          AND c.enabled = true
          AND c.deleted_at IS NULL
    ) AS channel_enabled,
    EXISTS (
        SELECT 1
        FROM providers p
        WHERE p.id = pa.provider_id
          AND p.tenant_id = pa.tenant_id
          AND p.enabled = true
          AND p.deleted_at IS NULL
    ) AS provider_available,
    pa.last_probe_latency_ms,
    pa.last_probe_at,
    pa.last_request_observed_at,
    pa.model_sync_last_check_at,
    pa.quota_snapshot_observed_at,
    pa.quota_snapshot_source,
    pa.quota_snapshot_outcome,
    pa.quota_snapshot_error_class,
    pa.session_window_5h_start,
    pa.session_window_5h_end,
    pa.session_window_5h_status,
    pa.session_window_5h_utilization,
    pa.session_window_7d_start,
    pa.session_window_7d_end,
    pa.session_window_7d_status,
    pa.session_window_7d_utilization,
    COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object(
                'metric_key', q.metric_key,
                'model_key', q.model_key,
                'state', q.state,
                'used_value', q.used_value,
                'limit_value', q.limit_value,
                'remaining_value', q.remaining_value,
                'unit', q.unit,
                'utilization_percent', q.utilization_percent,
                'remaining_percent', q.remaining_percent,
                'resets_at', q.resets_at,
                'observed_at', q.observed_at,
                'valid_until', q.valid_until,
                'source', q.source,
                'error_class', q.error_class
            ) ORDER BY q.metric_key, q.model_key
        )
        FROM provider_account_quota_facts q
        WHERE q.tenant_id = pa.tenant_id
          AND q.provider_account_id = pa.id
    ), '[]'::jsonb)::text AS quota_facts,
    COALESCE(refresh_ac.last_refresh_at, pa.last_refresh_at) AS last_refresh_at,
    COALESCE(refresh_ac.last_refresh_outcome, pa.last_refresh_outcome) AS last_refresh_outcome,
    refresh_ac.failure_class,
    COALESCE(refresh_ac.failure_count, 0)::integer AS failure_count,
    pa.updated_at
FROM provider_accounts pa
LEFT JOIN providers p
  ON p.id = pa.provider_id
 AND p.tenant_id = pa.tenant_id
LEFT JOIN provider_account_subscription_states subscription_state
  ON subscription_state.tenant_id = pa.tenant_id
 AND subscription_state.provider_account_id = pa.id
LEFT JOIN LATERAL (
    SELECT
        vendor,
        auth_mode,
        project_ref,
        count(*) OVER ()::integer AS serving_credential_candidates
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND pa.enabled
      AND (
          ac.state = 'active'
          OR (
              ac.state = 'refreshing_with_grace'
              AND (ac.grace_until IS NULL OR ac.grace_until > now())
          )
      )
    ORDER BY CASE ac.state WHEN 'active' THEN 0 ELSE 1 END, ac.updated_at DESC, ac.id DESC
    LIMIT 1
) current_ac ON true
LEFT JOIN LATERAL (
    SELECT
        last_refresh_at,
        last_refresh_outcome,
        failure_class,
        failure_count
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND ac.state NOT IN ('revoked')
    ORDER BY ac.last_refresh_at DESC NULLS LAST, ac.updated_at DESC, ac.credential_version DESC, ac.id DESC
    LIMIT 1
) refresh_ac ON true
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pa.id = sqlc.arg(id)::bigint
  AND pa.deleted_at IS NULL;

-- name: TouchProviderAccountRequestObservedAt :exec
-- 由异步请求完成事件调用,单调记录被动请求观测时间。
UPDATE provider_accounts
SET last_request_observed_at = sqlc.arg(observed_at)::timestamptz
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
  AND (
      last_request_observed_at IS NULL
      OR last_request_observed_at < sqlc.arg(observed_at)::timestamptz
  );

-- name: SummarizeProviderAccountHealth :many
-- 账号池健康聚合(B9 运维巡检):按 (health_state, enabled) 计数,跨整个租户池(非分页)。
-- 只读、不含钱字段;供管理端一眼看清问题账号分布。软删账号排除。
SELECT health_state, enabled, count(*)::bigint AS n
FROM provider_accounts
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
GROUP BY health_state, enabled
ORDER BY health_state, enabled;

-- name: GetPoolHealthTenant :one
-- 按池健康投影的租户存在性门:部署者显式指定的 tenant_id 指向不存在或已软删的租户时
-- 必须返回可辨识的 404,而不是把它当成空租户放行成空投影。租户 status 不影响只读投影,不取。
SELECT id
FROM tenants
WHERE id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: SummarizeProviderAccountHealthByPool :many
-- 按账号池聚合的账号调度健康投影(FE-GAP-002 第四切),服务端一次算完,不倒行给浏览器求和。
-- 分母 = 该池下经未软删渠道挂接的未软删账号(含运维停用账号)。每个账号恰属一个渠道、一个池,
-- 不会重复计数。分类互斥,优先级 unavailable > cooling_down > degraded > schedulable,
-- 谓词逐字对齐选号候选查询(ListEligibleAccountsByPoolGroup)与进程内健康门中**与请求无关**的账号级判定;
-- 按模型/协议/能力清单、模型限流、上游额度、并发与会话容量等按请求维度的门不在本投影内,
-- 因此 schedulable 表示"健康层放行",不表示某个具体请求一定会选中它:
--   unavailable  运维停用 / 渠道停用 / 上游 provider 停用或软删 / 账号已过期 /
--                revoked 未到期或无截止 / 无可服务凭据 / 最新 FSM disabled|manual_paused /
--                auth 降级车道硬禁(disable_cooling 不能豁免)
--   cooling_down throttled|cooldown 未到期或无截止 / 最新 FSM cooling_down 且未开 disable_cooling /
--                FSM ramping 但放量阶段为空(尚未放行任何流量)且未开 disable_cooling /
--                auth 降级车道软退避未过期且未开 disable_cooling
--   degraded     最新 FSM degraded / FSM ramping 按比例放量(阶段非空)且未开 disable_cooling
--   schedulable  其余(数据库层、FSM 门与 auth 车道都放行)
-- 最新 FSM 记录按 credential_version DESC, updated_at DESC 取,与健康门读取顺序一致;无记录视为放行。
-- auth 降级车道读跨副本真相表 provider_account_auth_cooldowns,与候选查询同源,不依赖任何副本的内存。
-- auth_cooldown_accounts 只统计车道真正改变了栏位或恢复时刻的账号(不含因其他原因已 unavailable 的账号,
-- 也不含数据库层已冷却且恢复未知或车道截止更早的账号)。
-- earliest_recovery_at 取冷却账号已知恢复时刻的最小值;任一生效冷却无截止的账号视为未知、不参与。
WITH latest_fsm AS (
    SELECT DISTINCT ON (chs.provider_account_id)
        chs.provider_account_id,
        chs.state,
        chs.cooldown_until,
        chs.ramp_stage_pct
    FROM channel_health_state chs
    WHERE chs.tenant_id = sqlc.arg(tenant_id)::bigint
      AND chs.provider_account_id IS NOT NULL
    ORDER BY chs.provider_account_id, chs.credential_version DESC, chs.updated_at DESC
),
base AS (
    SELECT
        pg.id AS pool_group_id,
        pa.id AS account_id,
        COALESCE(al.hard_disabled, false) AS lane_hard,
        (al.auth_until IS NOT NULL AND al.auth_until > NOW() AND NOT pa.disable_cooling) AS lane_soft,
        al.auth_until AS lane_until,
        CASE
            WHEN pa.id IS NULL THEN NULL
            WHEN NOT pa.enabled
                 OR NOT c.enabled
                 OR p.id IS NULL
                 OR (pa.expires_at IS NOT NULL AND pa.expires_at <= NOW())
                 OR (pa.health_state = 'revoked'
                     AND (pa.health_state_until IS NULL OR pa.health_state_until > NOW()))
                 OR NOT EXISTS (
                     SELECT 1 FROM account_credentials ac
                     WHERE ac.provider_account_id = pa.id
                       AND ac.tenant_id = pa.tenant_id
                       AND ac.deleted_at IS NULL
                       AND (
                           ac.state = 'active'
                           OR (ac.state = 'refreshing_with_grace'
                               AND (ac.grace_until IS NULL OR ac.grace_until > NOW()))
                       )
                 )
                 OR lf.state IN ('disabled', 'manual_paused')
                THEN 'unavailable'
            WHEN (pa.health_state IN ('throttled', 'cooldown')
                  AND (pa.health_state_until IS NULL OR pa.health_state_until > NOW()))
                 OR (lf.state = 'cooling_down' AND NOT pa.disable_cooling)
                 OR (lf.state = 'ramping' AND NOT pa.disable_cooling
                     AND COALESCE(lf.ramp_stage_pct, 0) <= 0)
                THEN 'cooling_down'
            WHEN lf.state = 'degraded'
                 OR (lf.state = 'ramping' AND NOT pa.disable_cooling)
                THEN 'degraded'
            ELSE 'schedulable'
        END AS base_class,
        -- 数据库健康态截止与 FSM 冷却截止取较晚者(两者都要过);任一生效中的冷却没有截止时间
        -- 就是未知(NULL),不用另一层的截止冒充。
        CASE
            WHEN pa.health_state IN ('throttled', 'cooldown') AND pa.health_state_until IS NULL
                THEN NULL
            WHEN lf.state = 'cooling_down' AND NOT pa.disable_cooling AND lf.cooldown_until IS NULL
                THEN NULL
            WHEN pa.health_state IN ('throttled', 'cooldown')
                 AND pa.health_state_until > NOW()
                 AND lf.state = 'cooling_down'
                 AND NOT pa.disable_cooling
                THEN GREATEST(pa.health_state_until, lf.cooldown_until)
            WHEN pa.health_state IN ('throttled', 'cooldown')
                 AND pa.health_state_until > NOW()
                THEN pa.health_state_until
            WHEN lf.state = 'cooling_down' AND NOT pa.disable_cooling
                THEN lf.cooldown_until
            ELSE NULL
        END AS base_recovery_at
    FROM pool_groups pg
    LEFT JOIN channels c
        ON c.pool_group_id = pg.id
       AND c.tenant_id = pg.tenant_id
       AND c.deleted_at IS NULL
    LEFT JOIN provider_accounts pa
        ON pa.channel_id = c.id
       AND pa.tenant_id = pg.tenant_id
       AND pa.deleted_at IS NULL
    LEFT JOIN providers p
        ON p.id = pa.provider_id
       AND p.tenant_id = pa.tenant_id
       AND p.enabled = true
       AND p.deleted_at IS NULL
    LEFT JOIN latest_fsm lf
        ON lf.provider_account_id = pa.id
    LEFT JOIN provider_account_auth_cooldowns al
        ON al.provider_account_id = pa.id
    WHERE pg.tenant_id = sqlc.arg(tenant_id)::bigint
      AND pg.deleted_at IS NULL
),
classified AS (
    SELECT
        b.pool_group_id,
        b.account_id,
        CASE
            WHEN b.base_class IS NULL THEN NULL
            WHEN b.base_class = 'unavailable' OR b.lane_hard THEN 'unavailable'
            WHEN b.base_class = 'cooling_down' OR b.lane_soft THEN 'cooling_down'
            ELSE b.base_class
        END AS account_class,
        -- 车道确实改变了结果才计数:硬禁把非 unavailable 账号抬升;软退避把非冷却账号拉进冷却,
        -- 或把已冷却账号的已知恢复时刻推后(数据库层恢复未知或车道截止更早时,车道没有改变任何结果)。
        (
            b.base_class IS NOT NULL
            AND b.base_class <> 'unavailable'
            AND (
                b.lane_hard
                OR (b.lane_soft AND b.base_class <> 'cooling_down')
                OR (b.lane_soft AND b.base_class = 'cooling_down'
                    AND b.base_recovery_at IS NOT NULL AND b.lane_until > b.base_recovery_at)
            )
        ) AS lane_affected,
        -- 车道软退避叠加:已冷却账号取两层较晚者(数据库层未知仍未知);仅车道冷却的账号取车道截止。
        CASE
            WHEN b.base_class = 'cooling_down' THEN
                CASE
                    WHEN b.base_recovery_at IS NULL THEN NULL
                    WHEN b.lane_soft THEN GREATEST(b.base_recovery_at, b.lane_until)
                    ELSE b.base_recovery_at
                END
            WHEN b.lane_soft THEN b.lane_until
            ELSE NULL
        END AS recovery_at
    FROM base b
)
SELECT
    pg.id AS pool_group_id,
    pg.name AS pool_name,
    pg.enabled AS pool_enabled,
    count(cl.account_id)::bigint AS total_accounts,
    count(*) FILTER (WHERE cl.account_class = 'schedulable')::bigint AS schedulable_accounts,
    count(*) FILTER (WHERE cl.account_class = 'degraded')::bigint AS degraded_accounts,
    count(*) FILTER (WHERE cl.account_class = 'cooling_down')::bigint AS cooling_down_accounts,
    count(*) FILTER (WHERE cl.account_class = 'unavailable')::bigint AS unavailable_accounts,
    count(*) FILTER (WHERE cl.lane_affected)::bigint AS auth_cooldown_accounts,
    (min(cl.recovery_at) FILTER (WHERE cl.account_class = 'cooling_down'))::timestamptz AS earliest_recovery_at
FROM pool_groups pg
LEFT JOIN classified cl ON cl.pool_group_id = pg.id
WHERE pg.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pg.deleted_at IS NULL
GROUP BY pg.id, pg.name, pg.enabled
ORDER BY pg.id;

-- name: CountUnpooledProviderAccounts :one
-- 未挂在任何有效池下的未删账号(渠道或池已软删):按池投影不含它们,单列出来让
-- Σ池.total_accounts + unpooled == SummarizeProviderAccountHealth 的总数可对账。
SELECT count(*)::bigint AS n
FROM provider_accounts pa
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pa.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM channels c
      INNER JOIN pool_groups pg
          ON pg.id = c.pool_group_id
         AND pg.tenant_id = c.tenant_id
         AND pg.deleted_at IS NULL
      WHERE c.id = pa.channel_id
        AND c.tenant_id = pa.tenant_id
        AND c.deleted_at IS NULL
  );
