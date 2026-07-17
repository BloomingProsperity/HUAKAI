package hermesops

import (
	"context"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// ProviderAccountListDeps 是 provider_account_list 工具的只读依赖:List 包装
// admindb.Queries.ListAdminProviderAccounts(按 tenant_id SELECT-only,SQL 含 deleted_at IS NULL
// 只返活跃账号)。nil 时工具按依赖检查 fail-closed。
type ProviderAccountListDeps struct {
	List func(ctx context.Context, params admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
}

// providerAccountListLimit 是单次列出账号上限(防超大读;账号多的租户可后续加分页参数)。
const providerAccountListLimit = 200

// ProviderAccountListSpec 构建只读 provider_account_list 工具:列出**整租户**的上游 provider account
// 清册及其状态/路由配置,补 account_health_diagnose(单账号)缺的"我有哪些账号、各自什么状态"。可选 state
// 过滤(active/disabled/rate_limited/overloaded/temp_unschedulable/error)。租户 scope 取自已鉴权
// req.TenantID(ListAdminProviderAccounts 按 tenant_id 过滤)。
//
// 隐私 safe-by-construction(providerAccountShape):AdminProviderAccountRow 含若干**非结构化/可能夹带
// 敏感信息**的字段——Extra([]byte 原始 JSON blob)、RateLimitReason(*string 可能含上游错误文本)、
// Tags([]string 运营自由标签)、ProxyGroupID(*string 自由标签)。这些**一律不投明文**:Extra/RateLimitReason/
// ProxyGroupID 不投,Tags 只露 count。其余只露结构化枚举/数值/布尔/时间戳。**本行根本不含凭证/token 明文**
// (原始凭证存 credentialstore;此处只有 credential_state 枚举与 token_version 计数)。
//
// Args: { "state": <string, optional 按账号 state 过滤> }
func ProviderAccountListSpec(deps ProviderAccountListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolProviderAccountList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's upstream provider accounts (inventory) with per-account enabled/health/credential/rate-limit state, routing priority/weight/concurrency, probe + refresh signals. Optionally filter by state (active/disabled/rate_limited/overloaded/temp_unschedulable/error). Tenant-wide complement to per-account account_health_diagnose. READ ONLY. Never returns credentials/tokens.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"state": "filter by account state (active/disabled/rate_limited/overloaded/temp_unschedulable/error, optional)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			// 未知/缺失 state → "":SQL 把 '' 当 no-filter;任意非法值只会匹配不到任何 OR 分支返回空,
			// 不构成注入(参数已绑定)。
			state, _ := ArgString(req.Args, "state")
			rows, err := deps.List(ctx, admindb.ListAdminProviderAccountsParams{
				TenantID:    req.TenantID,
				AfterID:     0,
				LimitCount:  providerAccountListLimit,
				PoolGroupID: 0,
				StateFilter: state,
				TagFilter:   "",
			})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			byHealthState := map[string]int{}
			enabledCount := 0
			for _, a := range rows {
				byHealthState[a.HealthState]++
				if a.Enabled {
					enabledCount++
				}
				items = append(items, providerAccountShape(a))
			}
			return ToolResult{Summary: map[string]any{
				"account_count":   len(rows),
				"enabled_count":   enabledCount,
				"by_health_state": intCountMap(byHealthState),
				"items":           items,
			}}, nil
		},
	}
}

// providerAccountShape 把一条 AdminProviderAccountRow 投影成账号清册诊断字段。**显式列举 +
// safe-by-construction**:只露结构化枚举/数值/布尔/时间戳 + 受控字符串数组(model_allow_list/
// capability_flags 是模型名/能力枚举);**有意不投** Extra(原始 blob)、RateLimitReason(自由文本)、
// Tags 值(→tag_count)、ProxyGroupID(自由文本)、TenantID(调用方已知)。LastRefreshOutcome 沿用
// account_health_diagnose 既有判定(受控结果枚举)投出。
func providerAccountShape(a admindb.AdminProviderAccountRow) map[string]any {
	return map[string]any{
		"id":                              a.ID,
		"provider_id":                     a.ProviderID,
		"channel_id":                      a.ChannelID,
		"name":                            a.Name,
		"account_type":                    a.AccountType,
		"enabled":                         a.Enabled,
		"health_state":                    a.HealthState,
		"credential_state":                a.CredentialState,
		"oauth_endpoint_health":           a.OAuthEndpointHealth,
		"priority":                        a.Priority,
		"static_weight":                   a.StaticWeight,
		"cap_concurrency":                 a.CapConcurrency,
		"in_flight_count":                 a.InFlightCount,
		"pool_mode":                       a.PoolMode,
		"probe_model":                     deref(a.ProbeModel),
		"model_allow_list":                a.ModelAllowList,
		"capability_flags":                a.CapabilityFlags,
		"custom_error_codes_enabled":      a.CustomErrorCodesEnabled,
		"custom_error_codes":              a.CustomErrorCodes,
		"token_version":                   a.TokenVersion,
		"tag_count":                       len(a.Tags),
		"has_proxy":                       a.ProxyID != nil,
		"last_probe_latency_ms":           int32PtrAny(a.LastProbeLatencyMS),
		"last_refresh_outcome":            deref(a.LastRefreshOutcome),
		"expires_at":                      tsAny(a.ExpiresAt),
		"last_dispatch_at":                tsAny(a.LastDispatchAt),
		"last_probe_at":                   tsAny(a.LastProbeAt),
		"last_request_observed_at":        tsAny(a.LastRequestObservedAt),
		"last_request_observation_source": "request_completion_event",
		"last_refresh_at":                 tsAny(a.LastRefreshAt),
		"rate_limited_at":                 tsAny(a.RateLimitedAt),
		"rate_limit_reset_at":             tsAny(a.RateLimitResetAt),
		"overload_until":                  tsAny(a.OverloadUntil),
		"temp_unschedulable_until":        tsAny(a.TempUnschedulableUntil),
		"temp_unschedulable_enabled":      a.TempUnschedulableEnabled,
		"created_at":                      tsAny(a.CreatedAt),
		"updated_at":                      tsAny(a.UpdatedAt),
	}
}
