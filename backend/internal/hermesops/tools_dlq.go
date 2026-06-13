package hermesops

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// DLQInspectDeps is the read-only dependency the dlq_inspect tool wraps. List is
// the SELECT-only dead-letter-queue read; the mutating Replay is intentionally
// NOT referenced here — this wave is read-only, so inspection cannot trigger a
// replay.
type DLQInspectDeps struct {
	List func(ctx context.Context, filter dlq.ListFilter) ([]dlq.Record, error)
}

// DLQInspectSpec builds the read-only dlq_inspect tool. It lists dead-lettered
// events for the tenant (optionally filtered by event_kind / status) and returns
// the operational shape only: kind / lane / status / replay attempts / ids /
// timestamps. It DROPS the raw event Payload (which carries the original request
// body) — that never enters a tool result.
//
// Args: { "event_kind": <string, optional>, "status": <string, optional> }
func DLQInspectSpec(deps DLQInspectDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolDLQInspect,
		Category:     CategoryDiagnostic,
		Description:  "List dead-lettered events for the tenant (kind, lane, status, replay attempts) — raw payload dropped. READ ONLY (no replay).",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"event_kind": "filter by DLQ event kind (string, optional)",
			"status":     "filter by DLQ status (string, optional)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tenant := req.TenantID
			filter := dlq.ListFilter{TenantID: &tenant, Limit: obsReadLimit}
			if ek, ok := ArgString(req.Args, "event_kind"); ok {
				filter.EventKind = dlq.EventKind(ek)
			}
			if st, ok := ArgString(req.Args, "status"); ok {
				filter.Status = dlq.Status(st)
			}

			rows, err := deps.List(ctx, filter)
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			byStatus := map[string]int{}
			byKind := map[string]int{}
			for _, r := range rows {
				items = append(items, dlqDiagnosticShape(r))
				byStatus[string(r.Status)]++
				byKind[string(r.EventKind)]++
			}
			return ToolResult{Summary: map[string]any{
				"dlq_count": len(items),
				"by_status": intCountMap(byStatus),
				"by_kind":   intCountMap(byKind),
				"items":     items,
			}}, nil
		},
	}
}

// dlqDiagnosticShape projects a DLQ record into operational-diagnostic fields. It
// DROPS Payload (the raw event body — the highest-risk field) and surfaces
// kind / lane / status / replay attempts / replica status / ids / timestamps.
// failure_reason is the system-generated failure classification (no request
// body), retained for root-cause use.
func dlqDiagnosticShape(r dlq.Record) map[string]any {
	return map[string]any{
		"id":                    r.ID,
		"claim_id":              int64PtrAny(r.ClaimID),
		"event_kind":            string(r.EventKind),
		"lane":                  string(r.Lane),
		"status":                string(r.Status),
		"failure_reason":        r.FailureReason,
		"failure_at":            r.FailureAt.UTC(),
		"replay_attempts":       r.ReplayAttempts,
		"replay_failure_reason": deref(r.ReplayFailureReason),
		"replica_status":        r.ReplicaStatus,
		"source_table":          r.SourceTable,
		"source_id":             int64PtrAny(r.SourceID),
		"next_retry_at":         r.NextRetryAt.UTC(),
	}
}
