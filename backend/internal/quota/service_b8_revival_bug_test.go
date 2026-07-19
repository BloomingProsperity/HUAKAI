package quota

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestB8ReconciliationNeededRevivalNotDenied 判别测试 (bug B8 [S3]):
//
// 机理: 某 claim 的上一 attempt 释放失败, 其 reservation 卡在
// reconciliation_needed (units 仍被 enforce 计数器持有 —— release 未成功递减)。
// billing 复活该 claim (同 claim_id) 并为新 attempt 调 Reserve。因
// reservation 唯一 (tenant_id, claim_id), 复活命中同一行。
//
// 缺陷: existingReservationResult 对 reconciliation_needed 走 default 分支 ->
// inactiveReservationDecision -> DecisionDeny, 合法的复活请求被误判额度拒绝。
//
// 正确行为: reconciliation_needed 的 units 仍被持有 (settle 路径亦把
// reconciliation_needed 等同 reserved 处理), 因此复活 attempt 应复用既有持有
// (idempotency 命中 Allow), 既不误拒, 也不重新 IncrementWindowReserved 而重复计数。
//
// 当前有缺陷代码下本测试应 RED (deny != nil / !Allowed)。
func TestB8ReconciliationNeededRevivalNotDenied(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	req := ReserveRequest{
		TenantID:           7,
		ClaimID:            42,
		RequestFingerprint: "fp-b8",
		PredictedCost:      decimal.RequireFromString("1.25"),
		At:                 now,
	}
	// 卡在 reconciliation_needed 的同一 reservation 行; 身份 (fingerprint/
	// predicted_cost/scopes) 与复活请求一致。lease 已过期以贴近"卡住"的真实态。
	existing := Reservation{
		TenantID:           7,
		ID:                 99,
		ClaimID:            42,
		RequestFingerprint: "fp-b8",
		PredictedCost:      decimal.RequireFromString("1.25"),
		ReservedUnits:      decimal.NewFromInt(1),
		Status:             ReservationReconciliationNeeded,
		LeaseExpiresAt:     now.Add(-30 * time.Minute),
	}

	result, deny := existingReservationResult(req, existing)

	if deny != nil {
		t.Fatalf("revived reconciliation_needed attempt was denied: code=%s reason=%s; want Allow reuse",
			deny.Decision.Code, deny.Decision.Reason)
	}
	if !result.Allowed {
		t.Fatalf("result.Allowed=false for revived reconciliation_needed attempt; want true")
	}
	if !result.IdempotencyHit {
		t.Fatalf("result.IdempotencyHit=false; revival should reuse the held reservation")
	}
	if result.Reservation.ID != existing.ID {
		t.Fatalf("result.Reservation.ID=%d; want reuse of existing %d", result.Reservation.ID, existing.ID)
	}
}
