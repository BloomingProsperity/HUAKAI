package settlementrecovery

import "context"

// CommittedProof 是 worker handler 拿到 billing.ErrClaimNotReserving 后的兜底
// 判定 — 三证齐全才视"已成功提交"返回 nil(标 DLQ delivered),否则继续失败
// 重试 / quarantine。
//
// 三证:
//   1. billing_ledger_claims.status='committed' 同 tenant + claim_id 存在
//   2. usage_records 同 tenant + claim_id 存在
//   3. billing_events 同 tenant + claim_id + event_type='claim_committed' 存在
//
// 缺任一证视 false:
//   - 仅 status=committed:可能是其他 worker 半提交 / 数据腐蚀
//   - 缺 usage_records / billing_events:settle 同事务保证三者要么齐要么全无,
//     缺一就是事务 corrupt,不能视已成功
//
type CommittedProof interface {
	// IsCommitted 检查给定 (tenant, claim) 是否 fully committed。
	// 返回 (true, nil) 才允许 worker 把 DLQ 标 delivered;
	// (false, nil) = 未完全提交,worker 继续视失败(走重试 / quarantine);
	// (false, err) = DB 查询失败,worker 把 err 包到 replay_failure_reason。
	IsCommitted(ctx context.Context, tenantID, claimID int64) (bool, error)
}
