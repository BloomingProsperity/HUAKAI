package hermesops

import (
	"context"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	// obsReadLimit 限定每一次诊断型可观测性读取的上限。小而固定:
	// 这些是根因排查的查询,而非批量导出。
	obsReadLimit = 100
)

// ObservabilityDeps 是可观测性工具包装的只读依赖。三者都是已有的 SELECT-only 读取
// (即 F-OBS-001 admin 读取 API):
//   - ListUsage 包装 Queries.ListUsageRecords。
//   - ListClaims 包装 Queries.ListBillingClaims。
//   - ListAudit 包装 Queries.ListAuditEvents。
type ObservabilityDeps struct {
	ListUsage  func(ctx context.Context, params dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
	ListClaims func(ctx context.Context, params dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error)
	ListAudit  func(ctx context.Context, params dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error)
}

// RequestDiagnoseSpec 构建只读 request_diagnose 工具。它按 request_id(以及可选的 claim_id)
// 把某租户的 usage records + billing claims 关联起来,只返回诊断结构:end/status 分类、
// token 计数、stream 状态、attempt seq、claim 状态 —— 不含费用金额、原始报文、prompt。
//
// 参数: { "request_id": <string>, "claim_id": <int, 可选> }
func RequestDiagnoseSpec(deps ObservabilityDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolRequestDiagnose,
		Category:     CategoryDiagnostic,
		Description:  "Correlate usage records + billing claims for a request_id/claim_id (classes, token counts, stream state).",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"request_id": "logical request id to correlate (string, required)",
			"claim_id":   "billing claim id (positive integer, optional narrowing filter)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.ListUsage == nil || deps.ListClaims == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			requestID, hasReq := ArgString(req.Args, "request_id")
			if !hasReq {
				return ToolResult{}, ErrInvalidArgs
			}
			tenant := req.TenantID

			usage, err := deps.ListUsage(ctx, dbbilling.ListUsageRecordsParams{
				TenantID: &tenant, PageLimit: obsReadLimit,
			})
			if err != nil {
				return ToolResult{}, err
			}
			claims, err := deps.ListClaims(ctx, dbbilling.ListBillingClaimsParams{
				TenantID: &tenant, PageLimit: obsReadLimit,
			})
			if err != nil {
				return ToolResult{}, err
			}

			// 可选的 claim_id 收窄过滤。
			claimFilter := int64(0)
			if cid, cerr := ArgInt(req.Args, "claim_id"); cerr == nil {
				claimFilter = cid
			}

			usageShapes := make([]map[string]any, 0)
			for _, u := range usage {
				if u.RequestID != requestID {
					continue
				}
				if claimFilter > 0 && u.ClaimID != claimFilter {
					continue
				}
				usageShapes = append(usageShapes, usageDiagnosticShape(u))
			}
			claimShapes := make([]map[string]any, 0)
			for _, c := range claims {
				if c.LogicalRequestID != requestID {
					continue
				}
				if claimFilter > 0 && c.ID != claimFilter {
					continue
				}
				claimShapes = append(claimShapes, claimDiagnosticShape(c))
			}

			return ToolResult{Summary: map[string]any{
				"request_id":     requestID,
				"usage_records":  usageShapes,
				"billing_claims": claimShapes,
				"usage_count":    len(usageShapes),
				"claim_count":    len(claimShapes),
			}}, nil
		},
	}
}

// usageDiagnosticShape 把一条 usage record 投影成仅诊断用的字段。它丢弃
// actual_cost(钱)和审计链 blob;保留分类 / 计数 / ids / stream 状态。
func usageDiagnosticShape(u dbbilling.ListUsageRecordsRow) map[string]any {
	return map[string]any{
		"id":                       u.ID,
		"claim_id":                 u.ClaimID,
		"requested_model":          u.RequestedModel,
		"upstream_model":           deref(u.UpstreamModel),
		"provider_account_id":      int64PtrAny(u.ProviderAccountID),
		"attempt_seq":              u.AttemptSeq,
		"tokens_input":             u.TokensInput,
		"tokens_output":            u.TokensOutput,
		"cache_read_tokens":        u.CacheReadTokens,
		"cache_creation_tokens":    u.CacheCreationTokens,
		"end_class":                u.EndClass,
		"usage_source":             u.UsageSource,
		"pending_reconciliation":   u.PendingReconciliation,
		"stream_state":             u.StreamState,
		"delivered_token_count":    u.DeliveredTokenCount,
		"stream_terminated_reason": deref(u.StreamTerminatedReason),
		"settlement_source":        u.SettlementSource,
		"created_at":               tsAny(u.CreatedAt),
	}
}

// claimDiagnosticShape 把一条 billing claim 投影成仅诊断用的字段。它丢弃
// predicted_cost / actual_cost / currency(钱)和 aborted_reason(自由文本);
// 保留 status / endpoint family / attempt seq / ids。
func claimDiagnosticShape(c dbbilling.ListBillingClaimsRow) map[string]any {
	return map[string]any{
		"id":                  c.ID,
		"endpoint_family":     c.EndpointFamily,
		"attempt_seq":         c.AttemptSeq,
		"status":              c.Status,
		"provider_account_id": int64PtrAny(c.ProviderAccountID),
		"pool_id":             int64PtrAny(c.PoolID),
		"created_at":          tsAny(c.CreatedAt),
		"settled_at":          tsAny(c.SettledAt),
	}
}

// AuditLookupSpec 构建只读 audit_lookup 工具。它读取某租户的可观测性审计事件,
// 只投影系统诊断字段 —— 它显式丢弃自由文本的 payload blob 和 reason 字符串
// (可能携带非诊断内容),只露出 event_class / event_type / severity / ids。
//
// 参数: { "event_class": <string, 可选>, "severity": <string, 可选> }
func AuditLookupSpec(deps ObservabilityDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolAuditLookup,
		Category:     CategoryDiagnostic,
		Description:  "Read recent observability audit events (classes, types, severity, ids) — payload/reason are dropped.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"event_class": "filter by event class (string, optional)",
			"severity":    "filter by severity (string, optional)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.ListAudit == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tenant := req.TenantID
			params := dbbilling.ListAuditEventsParams{TenantID: &tenant, PageLimit: obsReadLimit}
			if ec, ok := ArgString(req.Args, "event_class"); ok {
				params.EventClass = &ec
			}
			if sev, ok := ArgString(req.Args, "severity"); ok {
				params.Severity = &sev
			}

			rows, err := deps.ListAudit(ctx, params)
			if err != nil {
				return ToolResult{}, err
			}
			events := make([]map[string]any, 0, len(rows))
			for _, e := range rows {
				events = append(events, auditDiagnosticShape(e))
			}
			return ToolResult{Summary: map[string]any{
				"event_count": len(events),
				"events":      events,
			}}, nil
		},
	}
}

// auditDiagnosticShape 把一条审计事件投影成仅诊断用的字段。它丢弃 Payload
// (自由文本 blob)和 Reason(自由文本字符串)—— 这两个字段最可能携带非诊断内容,
// 在受隐私边界约束的运营工具结果里没有立足之地。
func auditDiagnosticShape(e dbbilling.ListAuditEventsRow) map[string]any {
	return map[string]any{
		"id":                  e.ID,
		"event_class":         e.EventClass,
		"event_type":          e.EventType,
		"severity":            e.Severity,
		"claim_id":            int64PtrAny(e.ClaimID),
		"provider_account_id": int64PtrAny(e.ProviderAccountID),
		"pool_group_id":       int64PtrAny(e.PoolGroupID),
		"request_id":          deref(e.RequestID),
		"actor_role":          deref(e.ActorRole),
		"created_at":          tsAny(e.CreatedAt),
	}
}

// LogAnalyzeSpec 构建只读 log_analyze 工具。它从 usage records 聚合 error_class /
// end_class 趋势(仅系统诊断枚举)。它绝不读取或暴露原始报文、prompt 或 completion ——
// 它只统计隐私边界已经允许的现有分类枚举。
//
// 参数: 无(分析该租户最近的用量窗口)。
func LogAnalyzeSpec(deps ObservabilityDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolLogAnalyze,
		Category:     CategoryDiagnostic,
		Description:  "Aggregate end_class / settlement / stream trends across the tenant's recent usage (enums + counts only).",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.ListUsage == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tenant := req.TenantID
			usage, err := deps.ListUsage(ctx, dbbilling.ListUsageRecordsParams{
				TenantID: &tenant, PageLimit: obsReadLimit,
			})
			if err != nil {
				return ToolResult{}, err
			}

			byEndClass := map[string]int{}
			bySettlement := map[string]int{}
			pendingReconcile := 0
			streamTerminations := 0
			for _, u := range usage {
				byEndClass[u.EndClass]++
				bySettlement[u.SettlementSource]++
				if u.PendingReconciliation {
					pendingReconcile++
				}
				if u.StreamTerminatedReason != nil && *u.StreamTerminatedReason != "" {
					streamTerminations++
				}
			}
			return ToolResult{Summary: map[string]any{
				"sample_size":          len(usage),
				"by_end_class":         intCountMap(byEndClass),
				"by_settlement_source": intCountMap(bySettlement),
				"pending_reconcile":    pendingReconcile,
				"stream_terminations":  streamTerminations,
			}}, nil
		},
	}
}

func int64PtrAny(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// intCountMap 把 int 计数 map 转成 any 值的 map,以便 JSON 编码。
func intCountMap(in map[string]int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
