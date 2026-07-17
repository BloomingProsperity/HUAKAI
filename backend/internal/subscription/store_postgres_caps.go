// HUAKAI · iKun

package subscription

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

func installCapsTx(ctx context.Context, tx pgx.Tx, sub UserSubscription, now time.Time) error {
	for _, cap := range sub.Caps() {
		if err := insertCapPolicyTx(ctx, tx, sub, cap, now); err != nil {
			return err
		}
	}
	return nil
}

// insertCapPolicyTx 为某一窗口装一条新的 cost_usd quota 策略并建 link(铸新 policy_id,用量从 0)。
func insertCapPolicyTx(ctx context.Context, tx pgx.Tx, sub UserSubscription, cap CapSpec, now time.Time) error {
	scopeID := strconv.FormatInt(sub.UserID, 10)
	actor := fmt.Sprintf("subscription:%d", sub.ID)
	limit, err := encodeNumeric(cap.Limit)
	if err != nil {
		return ErrQuotaInstallFailed
	}
	var policyID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO quota_policies (
	tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
	limit_value, burst_value, mode, priority, enabled,
	valid_from, valid_until, created_by_actor, last_modified_by_actor, created_at, updated_at
) VALUES ($1, 'user', $2, 'cost_usd', $3, 0, $4, 0, 'enforce', $5, true, $6, $7, $8, $8, $9, $9)
RETURNING id`,
		sub.TenantID, scopeID, string(cap.Window), limit, subscriptionPolicyPriority,
		sub.StartsAt, sub.ExpiresAt, actor, now).Scan(&policyID); err != nil {
		return fmt.Errorf("subscription: install quota policy: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO subscription_policy_links (
	tenant_id, user_subscription_id, quota_policy_id, window_kind, status, created_at
) VALUES ($1, $2, $3, $4, 'active', $5)`,
		sub.TenantID, sub.ID, policyID, string(cap.Window), now); err != nil {
		return fmt.Errorf("subscription: link quota policy: %w", err)
	}
	return nil
}

// reconcileCapsTx 期中(未过期)续期时原地调和 caps,**绝不重置用量窗口**。对每个窗口
// (daily/weekly/monthly):
//   - 新套餐有该窗口 + 已有 active 策略 → 原地 UPDATE 其 limit_value(升档→上限增大)与
//     valid_until(顺延到新到期),保留同一 policy_id → quota_windows 按 policy_id 计的已用量完整保留;
//   - 新套餐有该窗口 + 无既存策略 → 装新窗口策略(insertCapPolicyTx);
//   - 新套餐移除该窗口(cap 变 nil)+ 有既存策略 → 关闭对应策略与 link(cap 解除)。
//
// 与「关旧装新(closeCapsTx+installCapsTx)铸新 policy_id」的本质区别:同一窗口 policy_id 不变、
// 用量不归零。这正是修复点——此前期中续期无条件关旧装新,使当月 cost_usd 计数归零,被自助复购
// 同档套餐绕过月度护栏、白吃约一倍上游成本。期中续期只能顺延，不能重置已用计数。
func reconcileCapsTx(ctx context.Context, tx pgx.Tx, sub UserSubscription, now time.Time) error {
	actor := fmt.Sprintf("subscription:%d", sub.ID)
	// 读现有 active 策略的 window_kind → policy_id。
	existing := map[string]int64{}
	rows, err := tx.Query(ctx, `
SELECT window_kind, quota_policy_id FROM subscription_policy_links
WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`, sub.TenantID, sub.ID)
	if err != nil {
		return fmt.Errorf("subscription: read active policy links: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var wk string
		var pid int64
		if err := rows.Scan(&wk, &pid); err != nil {
			return fmt.Errorf("subscription: scan policy link: %w", err)
		}
		existing[wk] = pid
	}
	if err := rows.Err(); err != nil {
		return err
	}

	want := map[string]bool{}
	for _, cap := range sub.Caps() {
		wk := string(cap.Window)
		want[wk] = true
		pid, ok := existing[wk]
		if !ok {
			// 新增窗口:装新策略。
			if err := insertCapPolicyTx(ctx, tx, sub, cap, now); err != nil {
				return err
			}
			continue
		}
		// 既有窗口:原地 UPDATE 上限+到期,保留 policy_id 与用量窗口,确保仍 enabled。
		limit, err := encodeNumeric(cap.Limit)
		if err != nil {
			return ErrQuotaInstallFailed
		}
		if _, err := tx.Exec(ctx, `
UPDATE quota_policies SET limit_value=$3, valid_until=$4, enabled=true, last_modified_by_actor=$5, updated_at=$6
WHERE tenant_id=$1 AND id=$2`, sub.TenantID, pid, limit, sub.ExpiresAt, actor, now); err != nil {
			return fmt.Errorf("subscription: reconcile quota policy: %w", err)
		}
	}

	// 新套餐不再包含的窗口:关闭其策略与 link(cap 解除)。
	for wk, pid := range existing {
		if want[wk] {
			continue
		}
		if _, err := tx.Exec(ctx, `
UPDATE quota_policies SET enabled=false, last_modified_by_actor=$3, updated_at=$4
WHERE tenant_id=$1 AND id=$2`, sub.TenantID, pid, actor, now); err != nil {
			return fmt.Errorf("subscription: disable removed-window policy: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE subscription_policy_links SET status='closed', closed_at=$4
WHERE tenant_id=$1 AND user_subscription_id=$2 AND quota_policy_id=$3 AND status='active'`,
			sub.TenantID, sub.ID, pid, now); err != nil {
			return fmt.Errorf("subscription: close removed-window link: %w", err)
		}
	}
	return nil
}

// closeCapsTx 关闭订阅的所有 active quota 策略 (disable, 保留行作审计) 并标记 link closed。
func closeCapsTx(ctx context.Context, tx pgx.Tx, tenantID, subscriptionID int64, now time.Time) error {
	if _, err := tx.Exec(ctx, `
UPDATE quota_policies SET enabled=false, last_modified_by_actor=$3, updated_at=$4
WHERE tenant_id=$1 AND id IN (
	SELECT quota_policy_id FROM subscription_policy_links
	WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'
)`, tenantID, subscriptionID, fmt.Sprintf("subscription:%d", subscriptionID), now); err != nil {
		return fmt.Errorf("subscription: disable quota policies: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE subscription_policy_links SET status='closed', closed_at=$3
WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`,
		tenantID, subscriptionID, now); err != nil {
		return fmt.Errorf("subscription: close policy links: %w", err)
	}
	return nil
}

// renewSubscriptionTx 把 active 订阅续期/换档: 覆盖 plan/group/caps 为新套餐, 延长 expires_at。
// 调用方负责 close 旧 caps 策略 + install 新策略 (基于返回的更新后订阅)。
