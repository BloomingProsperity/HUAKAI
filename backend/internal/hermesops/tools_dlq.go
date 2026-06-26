package hermesops

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// DLQInspectDeps 是 dlq_inspect 工具包装的只读依赖。List 是对死信队列的 SELECT-only 读取;
// 有改动性的 Replay 在这里被有意不引用 —— 本批次是只读的,所以检视不能触发 replay。
type DLQInspectDeps struct {
	List func(ctx context.Context, filter dlq.ListFilter) ([]dlq.Record, error)
}

// DLQInspectSpec 构建只读 dlq_inspect 工具。它列出该租户的死信事件
// (可按 event_kind / status 过滤),仅返回运营结构:kind / lane / status /
// replay 次数 / ids / 时间戳。它丢弃原始事件 Payload(携带原始请求体)—— 它绝不会进入工具结果。
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

// dlqDiagnosticShape 把一条 DLQ 记录投影成运营诊断字段。它丢弃 Payload
// (原始事件体 —— 风险最高的字段),只露出 kind / lane / status / replay 次数 /
// 副本状态 / ids / 时间戳。failure_reason 是系统生成的失败分类(不含请求体),
// 保留以供根因分析。
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
