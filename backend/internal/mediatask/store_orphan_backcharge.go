package mediatask

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// OrphanReconcileResult 是一次孤儿对账动作的结果,供调用方写审计 / 回客户端。
// CapturedCents 仅在真正发生追扣(BackCharge=true 且原 hold 仍 held)时为正;
// 默认仅标记终态不扣钱时为 0。BackCharged 表示本次是否调用了 settle 追扣路径。
type OrphanReconcileResult struct {
	OrphanID      int64
	TaskID        int64
	TenantID      int64
	UserID        int64
	Status        string
	BackCharged   bool
	CapturedCents int64
	// BackChargeOutcome 记追扣的实际结果(仅 backCharge=true 时有意义):
	// "captured"=真扣到钱;其余("hold_not_held"/"task_archived"/"no_estimate"/
	// "holdref_unparseable")=本笔追扣未发生(0 扣),此时孤儿保持 pending、不推进
	// reconciled 终态,供 admin 据因调查/重试或改用 back_charge=false 关闭。
	BackChargeOutcome string
}

// OrphanReconcileAuditHook 在对账事务内被回调,让调用方把 admin 审计行与状态推进 +
// 追扣写在同一事务里(原子)。返回非 nil 时整笔回滚(审计失败不留下半成品对账)。
type OrphanReconcileAuditHook func(ctx context.Context, tx pgx.Tx, result OrphanReconcileResult) error

// ReconcileOrphan 是孤儿对账闭环的唯一动钱入口:Manual-First,只应由 admin handler 同步
// 显式调用,绝无任何 worker / 定时器引用。它在单事务内做三件事:
//
//  1. 状态门推进(命门-1,防双扣):仅当孤儿仍处于 pending 时才把它推进到终态
//     (reconciled / cancelled / ignored)。已是终态的孤儿命中 RowsAffected()==0,
//     直接返回 ok=false 且【绝不进 Capture】——这是同一孤儿重复对账不双扣的第一道闸。
//  2. 追扣(仅当 backCharge=true 且 status=reconciled):复用既有 billing.Capture,把原始
//     media_tasks 行在创建时 Reserve 的那笔预扣 capture 成真实扣费,口径=预扣额
//     (estimated_cents),不新写任何 ledger / 扣费逻辑。billing.Capture 自带 hold.State
//     守卫(命门-2):若该 claim 已被抢到租约的 worker 结算(captured/released),Capture
//     退化为读余额快照的 no-op,余额一分不动——即便状态门被绕过也不会双扣。
//  3. 审计 hook:在同一事务内写 admin 审计行,与状态推进 + 追扣原子。
//
// 返回 ok=false(err=nil)表示该孤儿不存在或已是终态(幂等 no-op);ok=true 表示本次真推进了。
func (s *PostgresStore) ReconcileOrphan(
	ctx context.Context,
	orphanID int64,
	status string,
	backCharge bool,
	now time.Time,
	audit OrphanReconcileAuditHook,
) (OrphanReconcileResult, bool, error) {
	if s == nil || s.pool == nil {
		return OrphanReconcileResult{}, false, ErrStoreNotConfigured
	}
	switch status {
	case "reconciled", "cancelled", "ignored":
	default:
		return OrphanReconcileResult{}, false, ErrInvalidOrphanStatus
	}
	// 追扣只在显式标记 reconciled(已对上账)时才有意义;cancelled/ignored 是"判定不追",
	// 此时强行 backCharge 是矛盾输入,直接拒绝,避免运维误把"忽略"当成"追扣"。
	if backCharge && status != "reconciled" {
		return OrphanReconcileResult{}, false, ErrInvalidOrphanStatus
	}

	var (
		result   OrphanReconcileResult
		advanced bool
	)
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		// 锁定孤儿行 + 读出其 tenant/user/task,同时只对 pending 行加锁推进(状态门)。
		var (
			taskID, tenantID, userID int64
		)
		row := tx.QueryRow(ctx, `
			SELECT task_id, tenant_id, user_id
			FROM media_task_orphans
			WHERE id=$1 AND reconcile_status='pending'
			FOR UPDATE`, orphanID)
		if err := row.Scan(&taskID, &tenantID, &userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// 不存在或已终态:幂等 no-op,不进 Capture、不写审计。
				advanced = false
				return nil
			}
			return err
		}

		result = OrphanReconcileResult{
			OrphanID: orphanID, TaskID: taskID, TenantID: tenantID, UserID: userID, Status: status,
		}

		if backCharge {
			captured, outcome, err := captureOrphanHold(ctx, tx, taskID, tenantID)
			if err != nil {
				return err
			}
			result.BackCharged = true
			result.CapturedCents = captured
			result.BackChargeOutcome = outcome
			if captured == 0 {
				// 追扣请求但未真正扣到钱(hold 已 released / 行归档 / 金额非法 / holdref 不可解析):
				// 绝不把孤儿推进 reconciled 终态——那会把漏扣静默对平且永久不可重试。保持 pending,
				// 让 admin 据 outcome 调查/重试,或显式用 back_charge=false 关闭不可追回的孤儿。
				// 用专用哨兵回滚事务(本路径未动钱),外层把含 outcome 的 result 透出。
				advanced = false
				return errOrphanBackChargeNoOp
			}
		}

		// 推进终态:此处必带 reconcile_status='pending' 终态守卫(与 FOR UPDATE 选取一致),
		// 双保险,防并发下两个 admin 同时对账时第二个写入覆盖。
		tag, err := tx.Exec(ctx, markOrphanReconciledSQL, orphanID, status, now.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			// 理论上不会到这(已 FOR UPDATE 选中 pending),保险起见当 no-op 回滚追扣。
			advanced = false
			return errOrphanRaced
		}

		if audit != nil {
			if err := audit(ctx, tx, result); err != nil {
				return err
			}
		}
		advanced = true
		return nil
	})
	if errors.Is(err, errOrphanRaced) {
		return OrphanReconcileResult{}, false, nil
	}
	if errors.Is(err, errOrphanBackChargeNoOp) {
		// 追扣未发生:孤儿保持 pending(事务已回滚未动钱/未推进),把含 outcome 的 result
		// 透出(advanced=false),由 http 层据 outcome 给 admin 明确反馈而非静默成功。
		return result, false, nil
	}
	if err != nil {
		return OrphanReconcileResult{}, false, err
	}
	return result, advanced, nil
}

// errOrphanRaced 是内部哨兵:FOR UPDATE 选中 pending 后 UPDATE 却 0 行(并发竞态),
// 用它回滚事务并对外呈现为幂等 no-op(ok=false, err=nil)。
var errOrphanRaced = errors.New("mediatask: orphan reconcile raced")

// errOrphanBackChargeNoOp 是内部哨兵:backCharge=true 但 captureOrphanHold 未真正扣到钱
// (hold 已 released/captured、原任务行归档、估算非正、holdref 不可解析)。用它回滚事务
// (本路径未动钱、未推进终态),对外把孤儿保持 pending 并透出含 BackChargeOutcome 的 result。
var errOrphanBackChargeNoOp = errors.New("mediatask: orphan back-charge captured nothing")

// captureOrphanHold 把孤儿对应的原始 media_tasks 行那笔已 Reserve 的预扣,通过既有
// billing.Capture 结算成真实扣费。追扣额=预扣额 estimated_cents(与正常成功结算口径一致)。
// 若 media_tasks 行已不存在(归档等),无可追扣,返回 0。billing.Capture 的 hold.State
// 守卫保证:claim 已被结算(captured/released)时本调用为 no-op,余额不动(防双扣命门-2)。
func captureOrphanHold(ctx context.Context, tx pgx.Tx, taskID, tenantID int64) (int64, string, error) {
	var (
		holdRef        string
		estimatedCents int64
	)
	err := tx.QueryRow(ctx, `
		SELECT hold_ref, estimated_cents
		FROM media_tasks
		WHERE id=$1 AND tenant_id=$2
		FOR UPDATE`, taskID, tenantID).Scan(&holdRef, &estimatedCents)
	if errors.Is(err, pgx.ErrNoRows) {
		// 原任务行已不在(已归档/删除):无对应预扣可追,本笔追扣未发生。
		return 0, "task_archived", nil
	}
	if err != nil {
		return 0, "", err
	}
	if estimatedCents <= 0 {
		return 0, "no_estimate", nil
	}
	claimID, err := claimIDFromHoldRef(holdRef)
	if err != nil {
		// hold_ref 不可解析:不冒险动钱,本笔追扣未发生。
		return 0, "holdref_unparseable", nil
	}
	// 关键(修 S2):billing.Capture 在 hold 非 "held"(已 released/captured)时是 no-op
	// (只读余额、绝不扣钱)。若不先验而直接调 Capture 再无条件回报 estimated_cents,会把
	// "未真正发生的扣费"误报成已扣,且让孤儿被标 reconciled 永久掩盖漏扣。故先验 hold 仍
	// 可结算,只有真能扣到才调 Capture 并报 estimated_cents。
	capturable, err := billing.HoldCapturable(ctx, tx, claimID)
	if err != nil {
		return 0, "", err
	}
	if !capturable {
		// hold 已 released(预扣已退客户)或已 captured:本笔追扣无对象,绝不假报已扣。
		return 0, "hold_not_held", nil
	}
	// 复用既有 settle 路径:把预扣 capture 成真实扣费。
	if _, err := billing.Capture(ctx, tx, claimID, centsToUSD(estimatedCents)); err != nil {
		return 0, "", err
	}
	return estimatedCents, "captured", nil
}
