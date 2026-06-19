package hermesops

import (
	"context"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	// obsReadLimit bounds every diagnostic observability read. Small + fixed:
	// these are root-cause lookups, not bulk export.
	obsReadLimit = 100
)

// ObservabilityDeps are the read-only dependencies the observability tools wrap.
// All three are EXISTING SELECT-only reads (the F-OBS-001 admin read APIs):
//   - ListUsage wraps Queries.ListUsageRecords.
//   - ListClaims wraps Queries.ListBillingClaims.
//   - ListAudit wraps Queries.ListAuditEvents.
type ObservabilityDeps struct {
	ListUsage  func(ctx context.Context, params dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
	ListClaims func(ctx context.Context, params dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error)
	ListAudit  func(ctx context.Context, params dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error)
}

// RequestDiagnoseSpec builds the read-only request_diagnose tool. It correlates
// usage records + billing claims for a tenant by request_id (and optionally
// claim_id), returning the diagnostic shape only: end/status classes, token
// counts, stream state, attempt seq, claim status — NO cost amounts, NO raw
// bodies, NO prompts.
//
// Args: { "request_id": <string>, "claim_id": <int, optional> }
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

			// Optional claim_id narrowing.
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

// usageDiagnosticShape projects a usage record into diagnostic-only fields. It
// DROPS actual_cost (money) and the audit-chain blobs; keeps classes / counts /
// ids / stream state.
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

// claimDiagnosticShape projects a billing claim into diagnostic-only fields. It
// DROPS predicted_cost / actual_cost / currency (money) and aborted_reason
// (free-form); keeps status / endpoint family / attempt seq / ids.
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

// AuditLookupSpec builds the read-only audit_lookup tool. It reads observability
// audit events for a tenant and projects ONLY system-diagnostic fields — it
// explicitly DROPS the free-form payload blob and reason string (which may carry
// non-diagnostic content), surfacing event_class / event_type / severity / ids.
//
// Args: { "event_class": <string, optional>, "severity": <string, optional> }
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

// auditDiagnosticShape projects an audit event into diagnostic-only fields. It
// DROPS Payload (free-form blob) and Reason (free-form string) — these are the
// fields most likely to carry non-diagnostic content and have no place in a
// privacy-bounded operator tool result.
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

// LogAnalyzeSpec builds the read-only log_analyze tool. It aggregates error_class
// / end_class trends from usage records (system-diagnostic enums only). It NEVER
// reads or surfaces raw bodies, prompts, or completions — it counts the existing
// classification enums that the privacy boundary already permits.
//
// Args: none (analyzes the tenant's recent usage window).
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

// intCountMap converts an int-count map to an any-valued map for JSON encoding.
func intCountMap(in map[string]int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
