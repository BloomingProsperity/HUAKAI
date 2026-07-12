// HUAKAI · iKun
//go:build integration_pg

package voucher

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// money-via-login voucher 归属回归(真库,迁移 0168):登录 admin(session,AdminID=0)发券/撤券
// 不再被 AdminID<=0 硬守卫拒,且归属经 created_by_actor/revoked_by_actor text 列可追;token 双写。
// session-admin 发券:成功 + created_by_actor=admin_user:42、旧 created_by_admin_id NULL。
// 变异:守卫回退成 AdminID<=0 → session 发券 ErrInvalidInput → RED;或 store 不写 created_by_actor → RED。
func TestVoucherCreateSessionAdminActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openVoucherPool(t, ctx)
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vactor-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM voucher_batch WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	svc := NewService(NewPostgresStore(pool))
	now := time.Now().UTC()
	v, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, AdminID: 0, ActorRef: "admin_user:42", Code: "SESS-" + suffix,
		AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	})
	if err != nil {
		t.Fatalf("session-admin 发券应放行,得 %v", err)
	}
	var byAdmin sql.NullInt64
	var byActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT created_by_admin_id, created_by_actor FROM voucher WHERE tenant_id=$1 AND id=$2`,
		tenantID, v.Voucher.ID).Scan(&byAdmin, &byActor); err != nil {
		t.Fatalf("查询发券归属: %v", err)
	}
	if byAdmin.Valid {
		t.Fatalf("session-admin 的 created_by_admin_id 应 NULL,得 %+v", byAdmin)
	}
	if !byActor.Valid || byActor.String != "admin_user:42" {
		t.Fatalf("created_by_actor 应为 admin_user:42,得 %+v", byActor)
	}

	// 撤券:session-admin 亦放行 + revoked_by_actor 归属。
	rv, err := svc.Revoke(ctx, RevokeInput{TenantID: tenantID, ID: v.Voucher.ID, AdminID: 0, ActorRef: "admin_user:42", Reason: "sess revoke", Now: now})
	if err != nil {
		t.Fatalf("session-admin 撤券应放行,得 %v", err)
	}
	if rv.Status != StatusRevoked {
		t.Fatalf("状态应 revoked,得 %s", rv.Status)
	}
	var revBy sql.NullInt64
	var revActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT revoked_by_admin_id, revoked_by_actor FROM voucher WHERE tenant_id=$1 AND id=$2`,
		tenantID, v.Voucher.ID).Scan(&revBy, &revActor); err != nil {
		t.Fatalf("查询撤券归属: %v", err)
	}
	if revBy.Valid {
		t.Fatalf("session-admin 的 revoked_by_admin_id 应 NULL,得 %+v", revBy)
	}
	if !revActor.Valid || revActor.String != "admin_user:42" {
		t.Fatalf("revoked_by_actor 应为 admin_user:42,得 %+v", revActor)
	}
}

// token-admin 发券:双写 created_by_admin_id=7 + created_by_actor=admin_token:7。
func TestVoucherCreateTokenAdminDualWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openVoucherPool(t, ctx)
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vactor-tok-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM voucher WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	svc := NewService(NewPostgresStore(pool))
	now := time.Now().UTC()
	v, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, AdminID: 7, ActorRef: "admin_token:7", Code: "TOK-" + suffix,
		AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	})
	if err != nil {
		t.Fatalf("token-admin 发券: %v", err)
	}
	var byAdmin sql.NullInt64
	var byActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT created_by_admin_id, created_by_actor FROM voucher WHERE tenant_id=$1 AND id=$2`,
		tenantID, v.Voucher.ID).Scan(&byAdmin, &byActor); err != nil {
		t.Fatalf("查询归属: %v", err)
	}
	if !byAdmin.Valid || byAdmin.Int64 != 7 || !byActor.Valid || byActor.String != "admin_token:7" {
		t.Fatalf("token-admin 应双写 admin_id=7+actor=admin_token:7,得 %+v/%+v", byAdmin, byActor)
	}
}

// 双无身份(AdminID=0 且 ActorRef 空)仍必须被守卫拒(改判来源不是取消守卫)。
func TestVoucherCreateNoIdentityStillRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openVoucherPool(t, ctx)
	svc := NewService(NewPostgresStore(pool))
	now := time.Now().UTC()
	_, err := svc.Create(ctx, CreateInput{
		TenantID: 999999, AdminID: 0, ActorRef: "", Code: "NOID",
		AmountCents: 500, CurrencyCode: "USD", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
		MaxRedemptions: 1, SingleUsePerUser: true, Now: now,
	})
	if err != ErrInvalidInput {
		t.Fatalf("无任何 admin 身份发券应仍被拒 ErrInvalidInput,得 %v", err)
	}
}
