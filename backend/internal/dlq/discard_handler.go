package dlq

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// NewEphemeralSignalDiscardHandler 返回"确认并丢弃"handler,用于时效性信号类事件
// (account_health 的最近请求观察戳、metrics 的聚合触发):这类信号只在产生当下有意义,
// 进 DLQ 时已过时,迟到重放要么无法忠实重建(payload 不含重放所需全字段)、要么会把
// 过期状态写回去。不注册 handler 的替代后果是 ErrNoHandler 直接隔离,记录永久堆积且
// 运维每条都要人工分辨——显式确认丢弃 + 全上下文留痕,把"静默堆积"变成"可审计的决定"。
func NewEphemeralSignalDiscardHandler(kind EventKind) Handler {
	return func(ctx context.Context, rec Record) error {
		attrs := map[string]any{
			"event_class":     "dlq_ephemeral_signal_discarded",
			"event_kind":      string(kind),
			"record_id":       rec.ID,
			"tenant_id":       rec.TenantID,
			"source_table":    rec.SourceTable,
			"idempotency_key": rec.IdempotencyKey,
			"failure_reason":  rec.FailureReason,
			"replay_attempts": rec.ReplayAttempts,
		}
		if rec.SourceID != nil {
			attrs["source_id"] = *rec.SourceID
		}
		if rec.ClaimID != nil {
			attrs["claim_id"] = *rec.ClaimID
		}
		if !rec.FailureAt.IsZero() {
			attrs["age_ms"] = time.Since(rec.FailureAt).Milliseconds()
		}
		_ = privacy.LogSystem(ctx, privacy.SystemEvent{
			Severity:  privacy.SeverityWarn,
			Component: "dlq.ephemeral_discard",
			Attrs:     attrs,
		})
		return nil
	}
}
