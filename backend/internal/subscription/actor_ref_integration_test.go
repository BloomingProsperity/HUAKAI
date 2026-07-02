// HUAKAI · iKun
//go:build integration_pg

package subscription

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// money-via-login Stage 2 双身份归属回归(真库真路径,迁移 0166):
// 订阅 admin 操作的 ActorRef 必须落 user_subscriptions.assigned_by_actor 与
// subscription_audit_events.actor_ref / subscription_plan_audit_events.actor_ref;
// 旧 bigint 列(assigned_by_admin_id / actor_id)语义不变。

func querySubAssignedActor(t *testing.T, f *subFixture, subID int64) (actorText sql.NullString, adminID sql.NullInt64) {
	t.Helper()
	if err := f.pool.QueryRow(f.ctx,
		`SELECT assigned_by_actor, assigned_by_admin_id FROM user_subscriptions WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, subID).Scan(&actorText, &adminID); err != nil {
		t.Fatalf("查询订阅归属: %v", err)
	}
	return actorText, adminID
}

func querySubAuditActor(t *testing.T, f *subFixture, subID int64, eventType string) (kind string, ref sql.NullString, id sql.NullInt64) {
	t.Helper()
	if err := f.pool.QueryRow(f.ctx,
		`SELECT actor_kind, actor_ref, actor_id FROM subscription_audit_events
		 WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3
		 ORDER BY id DESC LIMIT 1`,
		f.tenantA, subID, eventType).Scan(&kind, &ref, &id); err != nil {
		t.Fatalf("查询订阅审计归属(%s): %v", eventType, err)
	}
	return kind, ref, id
}

// token-admin 指派:双写(assigned_by_admin_id=7 + assigned_by_actor=admin_token:7),审计同。
func TestSubscriptionAssignTokenAdminDualWritesActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium-ar-token")

	res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID,
		ActorAdminID: 7, ActorRef: "admin_token:7",
	})
	if err != nil {
		t.Fatalf("AssignSubscription(token-admin): %v", err)
	}
	actorText, adminID := querySubAssignedActor(t, f, res.Subscription.ID)
	if !actorText.Valid || actorText.String != "admin_token:7" {
		t.Fatalf("assigned_by_actor 应为 admin_token:7,得 %+v", actorText)
	}
	if !adminID.Valid || adminID.Int64 != 7 {
		t.Fatalf("旧 assigned_by_admin_id 应保持 7(双写),得 %+v", adminID)
	}
	kind, ref, id := querySubAuditActor(t, f, res.Subscription.ID, AuditSubscriptionCreated)
	if kind != ActorKindAdmin || !ref.Valid || ref.String != "admin_token:7" || !id.Valid || id.Int64 != 7 {
		t.Fatalf("assign 审计应 kind=admin/ref=admin_token:7/id=7,得 kind=%s ref=%+v id=%+v", kind, ref, id)
	}
}

// session-admin 指派:旧列 NULL(不误归 token 0)+ text 列 admin_user:42,审计 kind 仍是 admin。
func TestSubscriptionAssignSessionAdminAttributesViaActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium-ar-sess")

	res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID,
		ActorAdminID: 0, ActorRef: "admin_user:42",
	})
	if err != nil {
		t.Fatalf("AssignSubscription(session-admin): %v", err)
	}
	actorText, adminID := querySubAssignedActor(t, f, res.Subscription.ID)
	if !actorText.Valid || actorText.String != "admin_user:42" {
		t.Fatalf("assigned_by_actor 应为 admin_user:42,得 %+v", actorText)
	}
	if adminID.Valid {
		t.Fatalf("session-admin 的 assigned_by_admin_id 应为 NULL,得 %+v", adminID)
	}
	kind, ref, id := querySubAuditActor(t, f, res.Subscription.ID, AuditSubscriptionCreated)
	if kind != ActorKindAdmin || !ref.Valid || ref.String != "admin_user:42" || id.Valid {
		t.Fatalf("assign 审计应 kind=admin/ref=admin_user:42/id=NULL,得 kind=%s ref=%+v id=%+v", kind, ref, id)
	}
}

// changePlanActor 陷阱回归(§14 变异=把 admin 判定改回只看 ActorAdminID>0 → 本测 RED):
// session-admin(ActorAdminID=0 但 ActorRef 非空)做 ChangePlan,审计必须仍归 admin actor
// 且带 actor_ref,绝不能被误判成 user actor(那会把归属错记到目标用户头上)。
func TestSubscriptionChangePlanSessionAdminNotMisclassifiedAsUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	// 两套餐同一 granted_group(参数是组名非套餐名):走"同组换套餐"路径,避开组变更分支。
	planA := createPremiumPlan(t, ctx, svc, f.tenantA, "cp-ar-a")
	planB := createPremiumPlan(t, ctx, svc, f.tenantA, "cp-ar-a")

	res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: planA.ID,
		ActorAdminID: 0, ActorRef: "admin_user:42",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: res.Subscription.ID, NewPlanID: planB.ID,
		AllowDowngrade: true, ActorAdminID: 0, ActorRef: "admin_user:42",
	}); err != nil {
		t.Fatalf("ChangePlan(session-admin): %v", err)
	}
	kind, ref, id := querySubAuditActor(t, f, res.Subscription.ID, AuditSubscriptionRenewed)
	if kind != ActorKindAdmin {
		t.Fatalf("session-admin 换套餐审计 actor_kind 应为 admin(不得误判 user),得 %s", kind)
	}
	if !ref.Valid || ref.String != "admin_user:42" || id.Valid {
		t.Fatalf("换套餐审计应 ref=admin_user:42/id=NULL,得 ref=%+v id=%+v", ref, id)
	}
}

// 撤销(revoke):session-admin 归属经 actor_ref 落审计。
func TestSubscriptionRevokeSessionAdminActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "rv-ar")

	res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID,
		ActorAdminID: 0, ActorRef: "admin_user:42",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := svc.RevokeSubscription(ctx, RevokeSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: res.Subscription.ID,
		ActorAdminID: 0, ActorRef: "admin_user:42", Reason: "test revoke attribution",
	}); err != nil {
		t.Fatalf("RevokeSubscription(session-admin): %v", err)
	}
	kind, ref, id := querySubAuditActor(t, f, res.Subscription.ID, AuditSubscriptionRevoked)
	if kind != ActorKindAdmin || !ref.Valid || ref.String != "admin_user:42" || id.Valid {
		t.Fatalf("revoke 审计应 kind=admin/ref=admin_user:42/id=NULL,得 kind=%s ref=%+v id=%+v", kind, ref, id)
	}
}
