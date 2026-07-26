package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type AdminProviderAccountRow struct {
	ID                          int64              `db:"id" json:"id"`
	TenantID                    int64              `db:"tenant_id" json:"tenant_id"`
	ProviderID                  int64              `db:"provider_id" json:"provider_id"`
	ChannelID                   int64              `db:"channel_id" json:"channel_id"`
	Name                        string             `db:"name" json:"name"`
	AccountType                 string             `db:"account_type" json:"account_type"`
	Enabled                     bool               `db:"enabled" json:"enabled"`
	ExpiresAt                   pgtype.Timestamptz `db:"expires_at" json:"expires_at"`
	RPMLimit                    int64              `db:"rpm_limit" json:"rpm_limit"`
	TPMLimit                    int64              `db:"tpm_limit" json:"tpm_limit"`
	WindowCostLimitCents        int64              `db:"window_cost_limit_cents" json:"window_cost_limit_cents"`
	MaxSessions                 int32              `db:"max_sessions" json:"max_sessions"`
	DisableCooling              bool               `db:"disable_cooling" json:"disable_cooling"`
	RefreshLeadSeconds          *int32             `db:"refresh_lead_seconds" json:"refresh_lead_seconds"`
	TLSFingerprintRotate        bool               `db:"tls_fingerprint_rotate" json:"tls_fingerprint_rotate"`
	HealthState                 string             `db:"health_state" json:"health_state"`
	CredentialState             string             `db:"credential_state" json:"credential_state"`
	CapConcurrency              int32              `db:"cap_concurrency" json:"cap_concurrency"`
	InFlightCount               int32              `db:"in_flight_count" json:"in_flight_count"`
	Priority                    int32              `db:"priority" json:"priority"`
	StaticWeight                int32              `db:"static_weight" json:"static_weight"`
	UpstreamCostRatio           *float64           `db:"upstream_cost_ratio" json:"upstream_cost_ratio"`
	ProbeModel                  *string            `db:"probe_model" json:"probe_model"`
	Tags                        []string           `db:"tags" json:"tags"`
	Extra                       []byte             `db:"extra" json:"extra"`
	LastDispatchAt              pgtype.Timestamptz `db:"last_dispatch_at" json:"last_dispatch_at"`
	LastProbeLatencyMS          *int32             `db:"last_probe_latency_ms" json:"last_probe_latency_ms"`
	LastProbeAt                 pgtype.Timestamptz `db:"last_probe_at" json:"last_probe_at"`
	LastRequestObservedAt       pgtype.Timestamptz `db:"last_request_observed_at" json:"last_request_observed_at"`
	TodayStatsWindowStart       pgtype.Timestamptz `db:"today_stats_window_start" json:"today_stats_window_start"`
	TodayStatsObservedAt        pgtype.Timestamptz `db:"today_stats_observed_at" json:"today_stats_observed_at"`
	TodayRequestCount           int64              `db:"today_request_count" json:"today_request_count"`
	TodaySuccessCount           int64              `db:"today_success_count" json:"today_success_count"`
	TodayFailureCount           int64              `db:"today_failure_count" json:"today_failure_count"`
	TodayTTFTP95MS              *float64           `db:"today_ttft_p95_ms" json:"today_ttft_p95_ms"`
	QuotaSnapshotObservedAt     pgtype.Timestamptz `db:"quota_snapshot_observed_at" json:"quota_snapshot_observed_at"`
	QuotaSnapshotSource         *string            `db:"quota_snapshot_source" json:"quota_snapshot_source"`
	QuotaSnapshotOutcome        *string            `db:"quota_snapshot_outcome" json:"quota_snapshot_outcome"`
	QuotaSnapshotErrorClass     *string            `db:"quota_snapshot_error_class" json:"quota_snapshot_error_class"`
	SessionWindow5hStart        pgtype.Timestamptz `db:"session_window_5h_start" json:"session_window_5h_start"`
	SessionWindow5hEnd          pgtype.Timestamptz `db:"session_window_5h_end" json:"session_window_5h_end"`
	SessionWindow5hStatus       *string            `db:"session_window_5h_status" json:"session_window_5h_status"`
	SessionWindow5hUtilization  pgtype.Numeric     `db:"session_window_5h_utilization" json:"session_window_5h_utilization"`
	SessionWindow7dStart        pgtype.Timestamptz `db:"session_window_7d_start" json:"session_window_7d_start"`
	SessionWindow7dEnd          pgtype.Timestamptz `db:"session_window_7d_end" json:"session_window_7d_end"`
	SessionWindow7dStatus       *string            `db:"session_window_7d_status" json:"session_window_7d_status"`
	SessionWindow7dUtilization  pgtype.Numeric     `db:"session_window_7d_utilization" json:"session_window_7d_utilization"`
	ModelAllowList              []string           `db:"model_allow_list" json:"model_allow_list"`
	CapabilityFlags             []string           `db:"capability_flags" json:"capability_flags"`
	RateLimitedAt               pgtype.Timestamptz `db:"rate_limited_at" json:"rate_limited_at"`
	RateLimitResetAt            pgtype.Timestamptz `db:"rate_limit_reset_at" json:"rate_limit_reset_at"`
	RateLimitReason             *string            `db:"rate_limit_reason" json:"rate_limit_reason"`
	OverloadUntil               pgtype.Timestamptz `db:"overload_until" json:"overload_until"`
	TempUnschedulableUntil      pgtype.Timestamptz `db:"temp_unschedulable_until" json:"temp_unschedulable_until"`
	TokenVersion                int32              `db:"token_version" json:"token_version"`
	LastRefreshAt               pgtype.Timestamptz `db:"last_refresh_at" json:"last_refresh_at"`
	LastRefreshOutcome          *string            `db:"last_refresh_outcome" json:"last_refresh_outcome"`
	OAuthEndpointHealth         string             `db:"oauth_endpoint_health" json:"oauth_endpoint_health"`
	CustomErrorCodesEnabled     bool               `db:"custom_error_codes_enabled" json:"custom_error_codes_enabled"`
	CustomErrorCodes            []int32            `db:"custom_error_codes" json:"custom_error_codes"`
	PoolMode                    bool               `db:"pool_mode" json:"pool_mode"`
	TempUnschedulableEnabled    bool               `db:"temp_unschedulable_enabled" json:"temp_unschedulable_enabled"`
	TempUnschedulableRules      []byte             `db:"temp_unschedulable_rules" json:"temp_unschedulable_rules"`
	ProxyID                     *int64             `db:"proxy_id" json:"proxy_id"`
	ProxyGroupID                *string            `db:"proxy_group_id" json:"proxy_group_id"`
	SubscriptionVendor          *string            `db:"subscription_vendor" json:"subscription_vendor"`
	SubscriptionPlan            *string            `db:"subscription_plan" json:"subscription_plan"`
	SubscriptionRawPlan         *string            `db:"subscription_raw_plan" json:"subscription_raw_plan"`
	SubscriptionScope           *string            `db:"subscription_scope" json:"subscription_scope"`
	SubscriptionSubjectRef      *string            `db:"subscription_subject_ref" json:"subscription_subject_ref"`
	SubscriptionWorkspaceRef    *string            `db:"subscription_workspace_ref" json:"subscription_workspace_ref"`
	SubscriptionSource          *string            `db:"subscription_source" json:"subscription_source"`
	SubscriptionTrust           *string            `db:"subscription_trust" json:"subscription_trust"`
	SubscriptionVerification    *string            `db:"subscription_verification" json:"subscription_verification"`
	SubscriptionStatus          *string            `db:"subscription_status" json:"subscription_status"`
	SubscriptionMappingVersion  *int32             `db:"subscription_mapping_version" json:"subscription_mapping_version"`
	SubscriptionErrorClass      *string            `db:"subscription_error_class" json:"subscription_error_class"`
	SubscriptionFirstObservedAt pgtype.Timestamptz `db:"subscription_first_observed_at" json:"subscription_first_observed_at"`
	SubscriptionObservedAt      pgtype.Timestamptz `db:"subscription_observed_at" json:"subscription_observed_at"`
	SubscriptionChangedAt       pgtype.Timestamptz `db:"subscription_changed_at" json:"subscription_changed_at"`
	QuotaFacts                  []byte             `db:"quota_facts" json:"quota_facts"`
	CreatedAt                   pgtype.Timestamptz `db:"created_at" json:"created_at"`
	UpdatedAt                   pgtype.Timestamptz `db:"updated_at" json:"updated_at"`
}

type ListAdminProviderAccountsParams struct {
	TenantID                 int64     `db:"tenant_id" json:"tenant_id"`
	AfterID                  int64     `db:"after_id" json:"after_id"`
	LimitCount               int32     `db:"limit_count" json:"limit_count"`
	PoolGroupID              int64     `db:"pool_group_id" json:"pool_group_id"`
	StateFilter              string    `db:"state_filter" json:"state_filter"`
	TagFilter                string    `db:"tag_filter" json:"tag_filter"`
	SubscriptionVendorFilter string    `db:"subscription_vendor_filter" json:"subscription_vendor_filter"`
	SubscriptionPlanFilter   string    `db:"subscription_plan_filter" json:"subscription_plan_filter"`
	SubscriptionScopeFilter  string    `db:"subscription_scope_filter" json:"subscription_scope_filter"`
	SubscriptionStatusFilter string    `db:"subscription_status_filter" json:"subscription_status_filter"`
	SubscriptionSourceFilter string    `db:"subscription_source_filter" json:"subscription_source_filter"`
	StatsSince               time.Time `db:"stats_since" json:"stats_since"`
	StatsUntil               time.Time `db:"stats_until" json:"stats_until"`
}

type GetAdminProviderAccountParams struct {
	ID       int64 `db:"id" json:"id"`
	TenantID int64 `db:"tenant_id" json:"tenant_id"`
}

type ListProviderAccountRiskPeersParams struct {
	TenantID  int64 `db:"tenant_id" json:"tenant_id"`
	ChannelID int64 `db:"channel_id" json:"channel_id"`
}

type ProviderAccountRiskPeerRow struct {
	ID                 int64  `db:"id" json:"id"`
	TenantID           int64  `db:"tenant_id" json:"tenant_id"`
	ProviderID         int64  `db:"provider_id" json:"provider_id"`
	ChannelID          int64  `db:"channel_id" json:"channel_id"`
	AccountType        string `db:"account_type" json:"account_type"`
	CredentialVendor   string `db:"credential_vendor" json:"credential_vendor"`
	CredentialAuthMode string `db:"credential_auth_mode" json:"credential_auth_mode"`
}

type ClearProviderAccountRateLimitParams struct {
	ID       int64   `db:"id" json:"id"`
	TenantID int64   `db:"tenant_id" json:"tenant_id"`
	ActorID  *string `db:"actor_id" json:"actor_id"`
}

type RecoverProviderAccountStateParams struct {
	ID       int64   `db:"id" json:"id"`
	TenantID int64   `db:"tenant_id" json:"tenant_id"`
	ActorID  *string `db:"actor_id" json:"actor_id"`
}

const adminProviderAccountColumns = `
    id,
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    rpm_limit,
    tpm_limit,
    window_cost_limit_cents,
    max_sessions,
    disable_cooling,
    refresh_lead_seconds,
    tls_fingerprint_rotate,
    health_state,
    credential_state,
    cap_concurrency,
    in_flight_count,
    priority,
    static_weight,
    upstream_cost_ratio,
    probe_model,
    tags,
    extra,
    last_dispatch_at,
    last_probe_latency_ms,
    last_probe_at,
    last_request_observed_at,
    quota_snapshot_observed_at,
    quota_snapshot_source,
    quota_snapshot_outcome,
    quota_snapshot_error_class,
    session_window_5h_start,
    session_window_5h_end,
    session_window_5h_status,
    session_window_5h_utilization,
    session_window_7d_start,
    session_window_7d_end,
    session_window_7d_status,
    session_window_7d_utilization,
    model_allow_list,
    capability_flags,
    rate_limited_at,
    rate_limit_reset_at,
    rate_limit_reason,
    overload_until,
    temp_unschedulable_until,
    token_version,
    last_refresh_at,
    last_refresh_outcome,
    oauth_endpoint_health,
    custom_error_codes_enabled,
    custom_error_codes,
    pool_mode,
    temp_unschedulable_enabled,
    temp_unschedulable_rules,
    proxy_id,
    proxy_group_id,
    created_at,
    updated_at`

const adminProviderAccountSubscriptionColumns = `,
    subscription.subscription_vendor,
    subscription.subscription_plan,
    subscription.subscription_raw_plan,
    subscription.subscription_scope,
    subscription.subscription_subject_ref,
    subscription.subscription_workspace_ref,
    subscription.subscription_source,
    subscription.subscription_trust,
    subscription.subscription_verification,
    subscription.subscription_status,
    subscription.subscription_mapping_version,
    subscription.subscription_error_class,
    subscription.subscription_first_observed_at,
    subscription.subscription_observed_at,
    subscription.subscription_changed_at`

const adminProviderAccountSubscriptionJoin = `
LEFT JOIN LATERAL (
    SELECT
        ps.vendor AS subscription_vendor,
        ps.normalized_plan AS subscription_plan,
        ps.raw_plan AS subscription_raw_plan,
        ps.scope_kind AS subscription_scope,
        ps.subject_ref AS subscription_subject_ref,
        ps.workspace_ref AS subscription_workspace_ref,
        ps.source_type AS subscription_source,
        ps.trust_level AS subscription_trust,
        ps.verification_status AS subscription_verification,
        ps.state_status AS subscription_status,
        ps.mapping_version AS subscription_mapping_version,
        ps.error_class AS subscription_error_class,
        ps.first_observed_at AS subscription_first_observed_at,
        ps.observed_at AS subscription_observed_at,
        ps.changed_at AS subscription_changed_at
    FROM provider_account_subscription_states ps
    WHERE ps.tenant_id = pa.tenant_id
      AND ps.provider_account_id = pa.id
) subscription ON true`

const adminProviderAccountQuotaColumns = `,
    COALESCE(quota.quota_facts, '[]'::jsonb)::text AS quota_facts`

const adminProviderAccountQuotaJoin = `
LEFT JOIN LATERAL (
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
    ) AS quota_facts
    FROM provider_account_quota_facts q
    WHERE q.tenant_id = pa.tenant_id
      AND q.provider_account_id = pa.id
) quota ON true`

const adminProviderAccountTodayStatsColumns = `,
    $12::timestamptz AS today_stats_window_start,
    $13::timestamptz AS today_stats_observed_at,
    today_stats.request_count AS today_request_count,
    today_stats.success_count AS today_success_count,
    today_stats.failure_count AS today_failure_count,
    today_stats.ttft_p95_ms AS today_ttft_p95_ms`

const adminProviderAccountTodayStatsJoin = `
LEFT JOIN LATERAL (
    SELECT
        count(*)::bigint AS request_count,
        count(*) FILTER (
            WHERE ur.end_class IN ('stream_end_graceful', 'non_streaming')
        )::bigint AS success_count,
        count(*) FILTER (
            WHERE ur.end_class NOT IN ('stream_end_graceful', 'non_streaming')
        )::bigint AS failure_count,
        percentile_cont(0.95) WITHIN GROUP (
            ORDER BY EXTRACT(EPOCH FROM (ur.first_byte_at - ur.requested_at)) * 1000
        ) FILTER (
            WHERE ur.first_byte_at IS NOT NULL
              AND ur.first_byte_at >= ur.requested_at
        )::double precision AS ttft_p95_ms
    FROM usage_records ur
    WHERE ur.tenant_id = pa.tenant_id
      AND ur.provider_account_id = pa.id
      AND ur.settled_at >= $12::timestamptz
      AND ur.settled_at < $13::timestamptz
) today_stats ON true`

const listAdminProviderAccounts = `
SELECT` + adminProviderAccountColumns + adminProviderAccountSubscriptionColumns + adminProviderAccountQuotaColumns + adminProviderAccountTodayStatsColumns + `
FROM provider_accounts pa
` + adminProviderAccountSubscriptionJoin + `
` + adminProviderAccountQuotaJoin + `
` + adminProviderAccountTodayStatsJoin + `
WHERE pa.tenant_id = $1
  AND pa.deleted_at IS NULL
  AND pa.id > $2
  AND ($4::bigint = 0 OR EXISTS (
      SELECT 1 FROM channels c
      WHERE c.id = pa.channel_id
        AND c.tenant_id = pa.tenant_id
        AND c.pool_group_id = $4::bigint
  ))
  AND (
      $5::text = ''
      OR ($5::text = 'active' AND pa.enabled = true
          AND pa.health_state = 'healthy'
          AND (pa.rate_limit_reset_at IS NULL OR pa.rate_limit_reset_at <= NOW())
          AND (pa.overload_until IS NULL OR pa.overload_until <= NOW())
          AND (pa.temp_unschedulable_until IS NULL OR pa.temp_unschedulable_until <= NOW()))
      OR ($5::text = 'disabled' AND pa.enabled = false)
      OR ($5::text = 'rate_limited' AND pa.rate_limit_reset_at IS NOT NULL AND pa.rate_limit_reset_at > NOW())
      OR ($5::text = 'overloaded' AND pa.overload_until IS NOT NULL AND pa.overload_until > NOW())
      OR ($5::text = 'temp_unschedulable' AND pa.temp_unschedulable_until IS NOT NULL AND pa.temp_unschedulable_until > NOW())
      OR ($5::text = 'error' AND (pa.health_state = 'revoked' OR pa.credential_state IN ('refresh_failed', 'revoked')))
  )
  AND ($6::text = '' OR pa.tags @> ARRAY[$6::text])
  AND ($7::text = '' OR subscription.subscription_vendor = $7::text)
  AND ($8::text = '' OR subscription.subscription_plan = $8::text)
  AND ($9::text = '' OR subscription.subscription_scope = $9::text)
  AND ($10::text = '' OR subscription.subscription_status = $10::text)
  AND ($11::text = '' OR subscription.subscription_source = $11::text)
ORDER BY pa.id ASC
LIMIT $3
`

func (q *Queries) ListAdminProviderAccounts(ctx context.Context, arg ListAdminProviderAccountsParams) ([]AdminProviderAccountRow, error) {
	rows, err := q.db.Query(ctx, listAdminProviderAccounts,
		arg.TenantID, arg.AfterID, arg.LimitCount, arg.PoolGroupID, arg.StateFilter, arg.TagFilter,
		arg.SubscriptionVendorFilter, arg.SubscriptionPlanFilter, arg.SubscriptionScopeFilter,
		arg.SubscriptionStatusFilter, arg.SubscriptionSourceFilter, arg.StatsSince, arg.StatsUntil,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []AdminProviderAccountRow
	for rows.Next() {
		var i AdminProviderAccountRow
		if err := scanAdminProviderAccountWithSubscriptionAndTodayStats(rows, &i); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getAdminProviderAccount = `
SELECT` + adminProviderAccountColumns + adminProviderAccountSubscriptionColumns + adminProviderAccountQuotaColumns + `
FROM provider_accounts pa
` + adminProviderAccountSubscriptionJoin + `
` + adminProviderAccountQuotaJoin + `
WHERE pa.id = $1
  AND pa.tenant_id = $2
  AND pa.deleted_at IS NULL
`

func (q *Queries) GetAdminProviderAccount(ctx context.Context, arg GetAdminProviderAccountParams) (AdminProviderAccountRow, error) {
	row := q.db.QueryRow(ctx, getAdminProviderAccount, arg.ID, arg.TenantID)
	var i AdminProviderAccountRow
	err := scanAdminProviderAccountWithSubscription(row, &i)
	return i, err
}

const listProviderAccountRiskPeers = `
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    pa.channel_id,
    pa.account_type,
    COALESCE(ac.vendor, '') AS credential_vendor,
    COALESCE(ac.auth_mode, '') AS credential_auth_mode
FROM provider_accounts pa
LEFT JOIN LATERAL (
    SELECT vendor, auth_mode
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND ac.state IN ('active', 'refreshing', 'refreshing_with_grace', 'temp_unschedulable', 'needs_rotation', 'operator_attention')
    ORDER BY ac.credential_version DESC, ac.id DESC
    LIMIT 1
) ac ON true
WHERE pa.tenant_id = $1
  AND pa.channel_id = $2
  AND pa.deleted_at IS NULL
ORDER BY pa.id ASC
`

func (q *Queries) ListProviderAccountRiskPeers(ctx context.Context, arg ListProviderAccountRiskPeersParams) ([]ProviderAccountRiskPeerRow, error) {
	rows, err := q.db.Query(ctx, listProviderAccountRiskPeers, arg.TenantID, arg.ChannelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProviderAccountRiskPeerRow{}
	for rows.Next() {
		var i ProviderAccountRiskPeerRow
		if err := rows.Scan(
			&i.ID,
			&i.TenantID,
			&i.ProviderID,
			&i.ChannelID,
			&i.AccountType,
			&i.CredentialVendor,
			&i.CredentialAuthMode,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// listProviderAccountsForProviderCompat 列某 provider 全部非删账号 × 其**每一条**活跃凭据的
// account_type 与 vendor/auth_mode,供协议变更时逐条校验与新协议兼容性。用全连接(非
// LIMIT 1)是防"较新兼容凭据遮蔽同账号另一条不兼容凭据"的绕过——一个账号可同时有多条
// 不同 (vendor,auth_mode) 的活跃凭据,任一不兼容都必须拦。无活跃凭据的账号出一行(空
// vendor/auth,仍受 account_type 校验)。列集合与 ProviderAccountRiskPeerRow 一致,复用其
// 行类型。UPDATE providers 的 FOR NO KEY UPDATE 与该事务同锁生命周期,防读后并发新增。
const listProviderAccountsForProviderCompat = `
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    pa.channel_id,
    pa.account_type,
    COALESCE(ac.vendor, '') AS credential_vendor,
    COALESCE(ac.auth_mode, '') AS credential_auth_mode
FROM provider_accounts pa
LEFT JOIN account_credentials ac
    ON ac.tenant_id = pa.tenant_id
   AND ac.provider_account_id = pa.id
   AND ac.deleted_at IS NULL
   AND ac.state IN ('active', 'refreshing', 'refreshing_with_grace', 'temp_unschedulable', 'needs_rotation', 'operator_attention')
WHERE pa.tenant_id = $1
  AND pa.provider_id = $2
  AND pa.deleted_at IS NULL
ORDER BY pa.id ASC, ac.id ASC
`

// ListProviderAccountsForProviderCompatParams 按 tenant+provider 列账号。
type ListProviderAccountsForProviderCompatParams struct {
	TenantID   int64 `db:"tenant_id" json:"tenant_id"`
	ProviderID int64 `db:"provider_id" json:"provider_id"`
}

// ListProviderAccountsForProviderCompat 返回该 provider 的账号(复用 RiskPeer 行类型)。
func (q *Queries) ListProviderAccountsForProviderCompat(ctx context.Context, arg ListProviderAccountsForProviderCompatParams) ([]ProviderAccountRiskPeerRow, error) {
	rows, err := q.db.Query(ctx, listProviderAccountsForProviderCompat, arg.TenantID, arg.ProviderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProviderAccountRiskPeerRow{}
	for rows.Next() {
		var i ProviderAccountRiskPeerRow
		if err := rows.Scan(
			&i.ID, &i.TenantID, &i.ProviderID, &i.ChannelID,
			&i.AccountType, &i.CredentialVendor, &i.CredentialAuthMode,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const clearProviderAccountRateLimit = `
UPDATE provider_accounts
SET
    rate_limited_at = NULL,
    rate_limit_reset_at = NULL,
    rate_limit_reason = NULL,
    overload_until = NULL,
    temp_unschedulable_until = NULL,
    temp_unschedulable_reason = NULL,
    temp_unschedulable_rule_index = NULL,
    model_rate_limits = '{}'::jsonb,
    openai_403_counter = 0,
    openai_403_window_start = NULL,
    updated_at = NOW(),
    last_modified_by_actor = $1::text
WHERE id = $2
  AND tenant_id = $3
  AND deleted_at IS NULL
RETURNING` + adminProviderAccountColumns + `
`

func (q *Queries) ClearProviderAccountRateLimit(ctx context.Context, arg ClearProviderAccountRateLimitParams) (AdminProviderAccountRow, error) {
	row := q.db.QueryRow(ctx, clearProviderAccountRateLimit, arg.ActorID, arg.ID, arg.TenantID)
	var i AdminProviderAccountRow
	err := scanAdminProviderAccount(row, &i)
	return i, err
}

// recoverProviderAccountState 是运维"完整恢复账号"原语:在 clear-rate-limit 清限流/过载/临时
// 停调/model 限流/403 五轴之外,额外把 health_state 复位为 healthy、health_state_until 清空。
// 缺此原语时终态 revoked(auth_expired/风控/账号禁用,health_state_until 恒 NULL)的账号无任何
// 恢复路径——clear-rate-limit 不碰 health_state,内存选号 gate 对 revoked 永久拒(修根因B①);
// 各恢复口(clear-rate-limit / force-active)各清一半亦由本原语一次清齐(修根因B④)。
const recoverProviderAccountState = `
UPDATE provider_accounts
SET
    health_state = 'healthy',
    health_state_until = NULL,
    rate_limited_at = NULL,
    rate_limit_reset_at = NULL,
    rate_limit_reason = NULL,
    overload_until = NULL,
    temp_unschedulable_until = NULL,
    temp_unschedulable_reason = NULL,
    temp_unschedulable_rule_index = NULL,
    model_rate_limits = '{}'::jsonb,
    openai_403_counter = 0,
    openai_403_window_start = NULL,
    updated_at = NOW(),
    last_modified_by_actor = $1::text
WHERE id = $2
  AND tenant_id = $3
  AND deleted_at IS NULL
RETURNING` + adminProviderAccountColumns + `
`

func (q *Queries) RecoverProviderAccountState(ctx context.Context, arg RecoverProviderAccountStateParams) (AdminProviderAccountRow, error) {
	row := q.db.QueryRow(ctx, recoverProviderAccountState, arg.ActorID, arg.ID, arg.TenantID)
	var i AdminProviderAccountRow
	err := scanAdminProviderAccount(row, &i)
	return i, err
}
