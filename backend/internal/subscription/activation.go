// HUAKAI · iKun

package subscription

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ActivateInput 是事务内激活/续期入口的输入。
// 由 payment(订单 P3b-4) / voucher(兑换码 P3b-3) / admin 在各自已开的事务里调用,
// 本入口不 begin/commit, 共享调用方事务以保证"扣款/兑换 与 订阅激活"同事务原子。
type ActivateInput struct {
	TenantID int64
	UserID   int64
	PlanID   int64
	// SourceKind: EffectSourceOrder / EffectSourceVoucher / EffectSourceAdmin。
	SourceKind string
	ActorKind  string // ActorKindAdmin / ActorKindUser / ActorKindSystem
	ActorID    int64
	ActorRef   string // 双身份归属串(AuditActor() 形态,admin 通道才有),空则列落 NULL
	RequestID  string
	// EnforceUpgradeOnly: 自助购买 (订单/兑换码) 传 true → 同组叠买只能往高 (caps 逐窗口支配),
	// 往低返回 ErrDowngradeNotAllowed 零副作用; 管理员手动传 false → 可升可降 (override)。
	EnforceUpgradeOnly bool
	Now                time.Time
}

// ActivateResult 激活/续期结果。调用方据此 INSERT subscription_fulfillment_effects 效果行。
type ActivateResult struct {
	Subscription        UserSubscription
	ResultKind          string // ResultCreated / ResultRenewed
	PrevExpiresAt       *time.Time
	AppliedValidityDays int
	NewExpiresAt        time.Time
}

// ActivateOrRenewTx 在调用方事务内激活或续期一条订阅 (不 begin/commit)。
//   - 同 (tenant, user, plan.granted_group) 无 active → 新建 (装 caps 策略 + 升组 + 审计 created)。
//   - 已有 active(同组) → 续期: 到期日从 max(now, 现到期) 累加 plan.validity (封顶 MaxExpiresAt),
//     caps/plan 覆盖为新套餐, 关旧策略装新策略, 审计 renewed。EnforceUpgradeOnly 时往低 (caps 不支配)
//     直接返回 ErrDowngradeNotAllowed 不产生副作用 (调用方据此回滚整事务)。
//
// 同来源重放 (同订单/同券) 的幂等短路不在此函数: 由调用方先查 effect 唯一行命中即返回, 不进本函数;
// 本函数只处理"真新激活 / 真续期"。跨组 (plan.granted_group 与当前组不同) 沿用 P3a 升组逻辑 (新建分支)。
func ActivateOrRenewTx(ctx context.Context, tx pgx.Tx, in ActivateInput) (ActivateResult, error) {
	plan, err := getPlanTx(ctx, tx, in.TenantID, in.PlanID)
	if err != nil {
		return ActivateResult{}, err
	}
	if !plan.Enabled {
		return ActivateResult{}, ErrPlanDisabled
	}

	// 锁用户行串行化同用户并发激活。
	prevGroup, err := lockUserGroupTx(ctx, tx, in.TenantID, in.UserID)
	if err != nil {
		return ActivateResult{}, err
	}

	source := sourceFromKind(in.SourceKind)
	actorKind := actorKindOrDefault(in.ActorKind)

	existing, hasActive, err := getActiveByGroupForUpdateTx(ctx, tx, in.TenantID, in.UserID, plan.GrantedGroup)
	if err != nil {
		return ActivateResult{}, err
	}

	if !hasActive {
		return activateNewTx(ctx, tx, in, plan, prevGroup, source, actorKind)
	}

	// 同组已有 active → only-up 闸 + 续期。
	// 已过期(到期 worker 未及时清扫)的旧订阅不再是有效权益, 不参与 only-up 支配判定:
	// 用户此刻无更高权益可保护, 自助买任意档(含更低)都应放行, 走续期分支从 now 起算新窗口。
	if in.EnforceUpgradeOnly && !existing.IsExpiredAt(in.Now) &&
		!capsDominate(planCapsTriple(plan), subCapsTriple(existing)) {
		return ActivateResult{}, ErrDowngradeNotAllowed
	}

	base := in.Now
	if existing.ExpiresAt.After(base) {
		base = existing.ExpiresAt
	}
	newExpires := capExpiry(base.AddDate(0, 0, plan.ValidityDays))

	updated, err := renewSubscriptionTx(ctx, tx, existing, plan, newExpires, in.Now)
	if err != nil {
		return ActivateResult{}, err
	}
	// caps 处理按"旧订阅是否已过期"分叉:
	//   - 已过期续期 = 新周期,合法重置:关旧装新(铸新 policy_id,用量从 0 起),与 base=now 的新窗口一致。
	//   - 期中续期(未过期)= **绝不重置用量**:原地调和 caps(保留 policy_id 与 quota_windows 已用计数,
	//     只顺延 valid_until、按升档调 limit)。修复点:此前无条件关旧装新使当月 cost_usd 计数归零,被自助
	//     在自然月内复购同档套餐绕过月度护栏、白吃约一倍上游成本。对齐 sub2api 期中续期只顺延、不触碰已用计数。
	if existing.IsExpiredAt(in.Now) {
		if err := closeCapsTx(ctx, tx, in.TenantID, existing.ID, in.Now); err != nil {
			return ActivateResult{}, err
		}
		if err := installCapsTx(ctx, tx, updated, in.Now); err != nil {
			return ActivateResult{}, err
		}
	} else {
		if err := reconcileCapsTx(ctx, tx, updated, in.Now); err != nil {
			return ActivateResult{}, err
		}
	}
	prev := existing.ExpiresAt
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           in.TenantID,
		UserSubscriptionID: updated.ID,
		EventType:          AuditSubscriptionRenewed,
		ActorKind:          actorKind,
		ActorID:            in.ActorID,
		ActorRef:           in.ActorRef,
		RequestID:          in.RequestID,
		Payload: map[string]any{
			"plan_id":      plan.ID,
			"from_expires": prev.UTC(),
			"to_expires":   newExpires.UTC(),
			"source":       in.SourceKind,
		},
		Now: in.Now,
	}); err != nil {
		return ActivateResult{}, err
	}
	return ActivateResult{
		Subscription:        updated,
		ResultKind:          ResultRenewed,
		PrevExpiresAt:       &prev,
		AppliedValidityDays: plan.ValidityDays,
		NewExpiresAt:        newExpires,
	}, nil
}

// activateNewTx 新建一条 active 订阅 (镜像 assignOnce 的创建路径, 不 begin/commit)。
func activateNewTx(ctx context.Context, tx pgx.Tx, in ActivateInput, plan Plan, prevGroup string, source Source, actorKind string) (ActivateResult, error) {
	var assignedAdmin int64
	if source == SourceAdmin {
		assignedAdmin = in.ActorID
	}
	expiresAt := capExpiry(in.Now.AddDate(0, 0, plan.ValidityDays))
	sub := UserSubscription{
		TenantID:          in.TenantID,
		UserID:            in.UserID,
		PlanID:            plan.ID,
		GrantedGroup:      plan.GrantedGroup,
		DailyCapUSD:       plan.DailyCapUSD,
		WeeklyCapUSD:      plan.WeeklyCapUSD,
		MonthlyCapUSD:     plan.MonthlyCapUSD,
		Status:            StatusActive,
		Source:            source,
		AutoRenew:         true,
		AssignedByAdminID: assignedAdmin,
		PrevUserGroup:     prevGroup,
		StartsAt:          in.Now,
		ExpiresAt:         expiresAt,
	}
	sub, err := insertSubscriptionTx(ctx, tx, sub, in.ActorRef, in.Now)
	if err != nil {
		return ActivateResult{}, err
	}
	if err := installCapsTx(ctx, tx, sub, in.Now); err != nil {
		return ActivateResult{}, err
	}
	if plan.GrantedGroup != "" && plan.GrantedGroup != prevGroup {
		if _, err := tx.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`,
			in.TenantID, in.UserID, plan.GrantedGroup); err != nil {
			return ActivateResult{}, fmt.Errorf("subscription: upgrade user group: %w", err)
		}
		if err := insertSubAuditTx(ctx, tx, subAuditInsert{
			TenantID:           in.TenantID,
			UserSubscriptionID: sub.ID,
			EventType:          AuditGroupUpgraded,
			ActorKind:          actorKind,
			ActorID:            in.ActorID,
			ActorRef:           in.ActorRef,
			RequestID:          in.RequestID,
			Payload:            map[string]any{"from": prevGroup, "to": plan.GrantedGroup},
			Now:                in.Now,
		}); err != nil {
			return ActivateResult{}, err
		}
	}
	if err := insertSubAuditTx(ctx, tx, subAuditInsert{
		TenantID:           in.TenantID,
		UserSubscriptionID: sub.ID,
		EventType:          AuditSubscriptionCreated,
		ActorKind:          actorKind,
		ActorID:            in.ActorID,
		ActorRef:           in.ActorRef,
		RequestID:          in.RequestID,
		Payload:            assignAuditPayload(sub),
		Now:                in.Now,
	}); err != nil {
		return ActivateResult{}, err
	}
	return ActivateResult{
		Subscription:        sub,
		ResultKind:          ResultCreated,
		PrevExpiresAt:       nil,
		AppliedValidityDays: plan.ValidityDays,
		NewExpiresAt:        sub.ExpiresAt,
	}, nil
}

// capsTriple 是日/周/月三档上限 (nil = 该窗口无限/不设限)。
type capsTriple struct {
	Daily   *decimal.Decimal
	Weekly  *decimal.Decimal
	Monthly *decimal.Decimal
}

func planCapsTriple(p Plan) capsTriple {
	return capsTriple{Daily: p.DailyCapUSD, Weekly: p.WeeklyCapUSD, Monthly: p.MonthlyCapUSD}
}

func subCapsTriple(s UserSubscription) capsTriple {
	return capsTriple{Daily: s.DailyCapUSD, Weekly: s.WeeklyCapUSD, Monthly: s.MonthlyCapUSD}
}

// capsDominate 判断 newCaps 是否在每个窗口都 >= curCaps (即"只升不降")。
// nil 视为无限大 (最宽松)。处处支配 ⇒ 切换到 newCaps 等价零降额, 满足 only-up 信任链。
func capsDominate(newCaps, curCaps capsTriple) bool {
	return windowDominates(newCaps.Daily, curCaps.Daily) &&
		windowDominates(newCaps.Weekly, curCaps.Weekly) &&
		windowDominates(newCaps.Monthly, curCaps.Monthly)
}

// windowDominates 单窗口支配判定: new >= cur (nil=无限=最大)。
func windowDominates(newCap, curCap *decimal.Decimal) bool {
	if newCap == nil {
		return true // 新窗口无限, 处处 >= 任何值 (含 cur 无限)
	}
	if curCap == nil {
		return false // 新有限 < 当前无限, 不支配
	}
	return newCap.GreaterThanOrEqual(*curCap)
}

// capExpiry 把到期日封顶到 MaxExpiresAt, 防多次叠买累加溢出。
func capExpiry(t time.Time) time.Time {
	if t.After(MaxExpiresAt) {
		return MaxExpiresAt
	}
	return t
}

// sourceFromKind 把激活来源种类映射为 user_subscriptions.source。
func sourceFromKind(kind string) Source {
	switch kind {
	case EffectSourceVoucher:
		return SourceVoucher
	case EffectSourceAdmin:
		return SourceAdmin
	default:
		return SourceOrder
	}
}
