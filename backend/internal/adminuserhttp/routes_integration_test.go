//go:build integration_pg

package adminuserhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestAdminListUsers_TenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)

	a1 := f.seedUser("a-one", "active", "user", "10.00000000")
	a2 := f.seedUser("a-two", "disabled", "admin", "20.00000000")
	b1 := f.seedOtherTenantUser("b-one", "active", "user", "99.00000000")

	// 变异:从 AdminListUsersForTenant 移除 u.tenant_id/sqlc 租户过滤 → 租户 B 用户泄漏进租户 A 列表 → 红。
	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, "/admin/v1/users?limit=10", nil)

	assertStatus(t, rec, http.StatusOK)
	var body adminUsersListResponse
	decodeBody(t, rec, &body)
	got := map[int64]string{}
	for _, item := range body.Items {
		got[item.ID] = item.Email
		if item.ID == b1 {
			t.Fatalf("cross-tenant user leaked into list: %+v", item)
		}
	}
	if got[a1] == "" || got[a2] == "" {
		t.Fatalf("tenant A users missing from list: got=%v want ids %d/%d", got, a1, a2)
	}
}

func TestAdminListUsers_Pagination(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	for i := 0; i < 120; i++ {
		f.seedUser(fmt.Sprintf("page-%03d", i), "active", "user", "0.00000000")
	}

	// 变异:把无上限的请求 limit 直接透传而不封顶到 100 → 返回 120 行 → 红。
	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, "/admin/v1/users?limit=999&offset=0", nil)

	assertStatus(t, rec, http.StatusOK)
	var body adminUsersListResponse
	decodeBody(t, rec, &body)
	if body.Limit != 100 || body.Offset != 0 || len(body.Items) != 100 {
		t.Fatalf("pagination mismatch: limit=%d offset=%d len=%d want 100/0/100", body.Limit, body.Offset, len(body.Items))
	}
}

func TestAdminGetUser_TenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	_ = f.seedUser("tenant-a", "active", "user", "1.00000000")
	b1 := f.seedOtherTenantUser("tenant-b", "active", "user", "2.00000000")

	// 变异:从 AdminGetUserForTenant 去掉 tenant_id 谓词 → 全局唯一的租户 B id 可被租户 A admin 读到 → 红。
	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, fmt.Sprintf("/admin/v1/users/%d", b1), nil)

	assertStatus(t, rec, http.StatusNotFound)
}

func TestAdminBalanceHistory_ScopedNewestFirst(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	target := f.seedUser("history-target", "active", "user", "0.00000000")
	otherSameTenant := f.seedUser("history-other", "active", "user", "0.00000000")
	otherTenant := f.seedOtherTenantUser("history-tenant-b", "active", "user", "0.00000000")

	oldAt := time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)
	newAt := oldAt.Add(2 * time.Hour)
	f.seedPaymentCreditEvent(target, "target-old", "4.00000000", oldAt)
	f.seedPaymentCreditEvent(target, "target-new", "7.00000000", newAt)
	f.seedPaymentCreditEvent(otherSameTenant, "same-tenant-other", "13.00000000", newAt.Add(time.Hour))
	f.seedOtherTenantPaymentCreditEvent(otherTenant, "other-tenant", "99.00000000", newAt.Add(2*time.Hour))

	// 变异:从 AdminListUserBalanceHistoryForTenant 去掉 tenant/user 来源过滤 → 其他用户更新的账本行泄漏或扰乱结果顺序 → 红。
	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, fmt.Sprintf("/admin/v1/users/%d/balance-history?limit=10", target), nil)

	assertStatus(t, rec, http.StatusOK)
	var body adminBalanceHistoryResponse
	decodeBody(t, rec, &body)
	if len(body.Items) != 2 {
		t.Fatalf("history len=%d want 2 body=%+v", len(body.Items), body)
	}
	if body.Items[0].Fingerprint != "target-new" || body.Items[1].Fingerprint != "target-old" {
		t.Fatalf("history order/fingerprint mismatch: %+v", body.Items)
	}
	for _, item := range body.Items {
		if item.Fingerprint == "same-tenant-other" || item.Fingerprint == "other-tenant" {
			t.Fatalf("foreign balance history leaked: %+v", item)
		}
	}
}

// TestAdminBalanceHistory_IncludesSubscriptionAutoRenewal 守余额历史渲染订阅续费扣款:
// 续费事件行归属用户靠 subscription_auto_renewal_charges JOIN, 漏 JOIN 则该行被
// COALESCE(user)=NULL 整行滤掉——管理员对账看不见扣款, SUM(历史)≠余额变动。
// mutation: AdminListUserBalanceHistoryForTenant 去掉 sarc JOIN 或 COALESCE 里的 sarc.user_id
// → 续费行消失 (len=1) → 红。
func TestAdminBalanceHistory_IncludesSubscriptionAutoRenewal(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	target := f.seedUser("renewal-target", "active", "user", "0.00000000")

	at := time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)
	f.seedPaymentCreditEvent(target, "renewal-credit", "5.00000000", at)
	f.seedSubscriptionAutoRenewalEvent(target, "renewal-debit", "-5.00000000", at.Add(time.Hour))

	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, fmt.Sprintf("/admin/v1/users/%d/balance-history?limit=10", target), nil)

	assertStatus(t, rec, http.StatusOK)
	var body adminBalanceHistoryResponse
	decodeBody(t, rec, &body)
	if len(body.Items) != 2 {
		t.Fatalf("history len=%d want 2 (续费扣款行未渲染, 对账缺环) body=%+v", len(body.Items), body)
	}
	// 最新在前: 续费扣款行带正确类型与负金额。
	got := body.Items[0]
	if got.Fingerprint != "renewal-debit" || got.EventType != "subscription_auto_renewed" || got.SourceType != "subscription_auto_renewal" {
		t.Fatalf("续费行渲染错: %+v", got)
	}
	if got.Amount != "-5.00000000" {
		t.Fatalf("续费行金额 = %s, want -5.00000000 (钱包流出负号)", got.Amount)
	}
}

func TestTwoFAStats_TenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)

	a1 := f.seedUser("2fa-a-one", "active", "user", "0.00000000")
	a2 := f.seedUser("2fa-a-two", "active", "user", "0.00000000")
	_ = f.seedUser("2fa-a-three", "active", "user", "0.00000000")
	b1 := f.seedOtherTenantUser("2fa-b-one", "active", "user", "0.00000000")
	b2 := f.seedOtherTenantUser("2fa-b-two", "active", "user", "0.00000000")
	f.seedTwoFASetting(a1, true)
	f.seedTwoFASetting(a2, true)
	f.seedOtherTenantTwoFASetting(b1, true)
	f.seedOtherTenantTwoFASetting(b2, true)

	// 变异:去掉 enabled-count 的租户谓词,或在 handler 中忽略 ScopeTenantID → 租户 B 的启用行泄漏进租户 A 统计 → 红。
	rec := invokeAdminUsers(t, Deps{
		Auth:  usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store: admindb.New(pool),
	}, http.MethodGet, "/admin/v1/users/2fa-adoption-stats", nil)

	assertStatus(t, rec, http.StatusOK)
	var body adminTwoFAStatsResponse
	decodeBody(t, rec, &body)
	if body.EnabledUsers != 2 || body.TotalUsers != 3 || body.EnabledRate < 0.6666 || body.EnabledRate > 0.6667 {
		t.Fatalf("2fa stats mismatch: got=%+v want enabled=2 total=3 rate~=0.6667", body)
	}
}

func TestPGAdminUnlockUser(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	userID, email := f.seedPasswordUser("unlock", "correct-secret")

	authSvc := userauth.NewService(userauth.NewPostgresStore(pool))
	authSvc.LockoutThreshold = 2
	authSvc.RequireVerified = true
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}

	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "wrong-secret"}); !errors.Is(err, userauth.ErrInvalidCredentials) {
		t.Fatalf("first failed login err=%v want ErrInvalidCredentials", err)
	}
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "wrong-secret"}); !errors.Is(err, userauth.ErrInvalidCredentials) {
		t.Fatalf("second failed login err=%v want ErrInvalidCredentials", err)
	}
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "correct-secret"}); !errors.Is(err, userauth.ErrUserLocked) {
		t.Fatalf("locked login err=%v want ErrUserLocked", err)
	}

	rec := invokeAdminUsers(t, Deps{
		Auth:        usersAuthStub{ident: tenantOperator(f.tenantID)},
		Store:       admindb.New(pool),
		UnlockAudit: NewPostgresUnlockAuditStore(pool),
	}, http.MethodPost, fmt.Sprintf("/admin/v1/users/%d/unlock", userID), nil)

	assertStatus(t, rec, http.StatusOK)
	var body struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	decodeBody(t, rec, &body)
	if body.ID != userID || body.Status != "active" {
		t.Fatalf("unlock response mismatch: %+v", body)
	}

	var status string
	var failed int
	var lockedUntil *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, failed_login_count, locked_until FROM users WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, userID,
	).Scan(&status, &failed, &lockedUntil); err != nil {
		t.Fatalf("read unlocked user: %v", err)
	}
	if status != "active" || failed != 0 || lockedUntil != nil {
		t.Fatalf("db lockout state = status:%q failed:%d locked_until:%v, want active/0/nil", status, failed, lockedUntil)
	}
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "correct-secret"}); err != nil {
		t.Fatalf("login after admin unlock: %v", err)
	}
	var auditAction, auditTarget string
	if err := pool.QueryRow(ctx,
		`SELECT action, target_type FROM admin_audit_events
		 WHERE tenant_id=$1 AND actor_id=$2 AND target_id=$3
		 ORDER BY id DESC LIMIT 1`,
		// P2b-1 起审计 actor_id 统一为 AuditActor() 格式(token-admin = "admin_token:<TokenID>")。
		f.tenantID, "admin_token:12", userID,
	).Scan(&auditAction, &auditTarget); err != nil {
		t.Fatalf("read unlock audit: %v", err)
	}
	if auditAction != "unlock_user" || auditTarget != "user" {
		t.Fatalf("audit event = action:%q target:%q want unlock_user/user", auditAction, auditTarget)
	}
}

func openAdminUsersPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open PG: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type adminUsersFixture struct {
	t             *testing.T
	ctx           context.Context
	pool          *pgxpool.Pool
	suffix        string
	tenantID      int64
	otherTenantID int64
}

func newAdminUsersFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *adminUsersFixture {
	t.Helper()
	f := &adminUsersFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantID = f.seedTenant("a")
	f.otherTenantID = f.seedTenant("b")
	t.Cleanup(func() {
		c := context.Background()
		for _, tenantID := range []int64{f.tenantID, f.otherTenantID} {
			_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM two_factor_settings WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM payment_credits WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM payment_orders WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
		}
	})
	return f
}

func (f *adminUsersFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("admin-users-%s-%s", label, f.suffix),
	).Scan(&id); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	return id
}

func (f *adminUsersFixture) seedUser(label, status, role, balance string) int64 {
	return f.seedUserInTenant(f.tenantID, label, status, role, balance)
}

func (f *adminUsersFixture) seedPasswordUser(label, password string) (int64, string) {
	f.t.Helper()
	hash, err := userauth.HashPassword(password, userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16})
	if err != nil {
		f.t.Fatalf("hash password: %v", err)
	}
	email := fmt.Sprintf("%s-%s@example.test", label, f.suffix)
	var userID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, email, display_name, password_hash, status, role, email_verified)
		 VALUES ($1, $2, $3, $4, 'active', 'user', true)
		 RETURNING id`,
		f.tenantID, email, label, hash,
	).Scan(&userID); err != nil {
		f.t.Fatalf("seed password user %s: %v", label, err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version)
		 VALUES ($1, $2, 0, 0, 1)`,
		f.tenantID, userID,
	); err != nil {
		f.t.Fatalf("seed password user balance %s: %v", label, err)
	}
	return userID, email
}

func (f *adminUsersFixture) seedOtherTenantUser(label, status, role, balance string) int64 {
	return f.seedUserInTenant(f.otherTenantID, label, status, role, balance)
}

func (f *adminUsersFixture) seedTwoFASetting(userID int64, enabled bool) {
	f.seedTwoFASettingInTenant(f.tenantID, userID, enabled)
}

func (f *adminUsersFixture) seedOtherTenantTwoFASetting(userID int64, enabled bool) {
	f.seedTwoFASettingInTenant(f.otherTenantID, userID, enabled)
}

func (f *adminUsersFixture) seedTwoFASettingInTenant(tenantID, userID int64, enabled bool) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO two_factor_settings (tenant_id, user_id, secret_enc, is_enabled)
		 VALUES ($1, $2, $3, $4)`,
		tenantID, userID, []byte{1, 2, 3}, enabled,
	); err != nil {
		f.t.Fatalf("seed 2fa setting tenant=%d user=%d: %v", tenantID, userID, err)
	}
}

func (f *adminUsersFixture) seedUserInTenant(tenantID int64, label, status, role, balance string) int64 {
	f.t.Helper()
	email := fmt.Sprintf("%s-%s@example.test", label, f.suffix)
	var userID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, email, display_name, status, role, email_verified)
		 VALUES ($1, $2, $3, $4, $5, true)
		 RETURNING id`,
		tenantID, email, label, status, role,
	).Scan(&userID); err != nil {
		f.t.Fatalf("seed user %s: %v", label, err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version)
		 VALUES ($1, $2, $3::numeric(20,8), 0, 1)`,
		tenantID, userID, balance,
	); err != nil {
		f.t.Fatalf("seed user balance %s: %v", label, err)
	}
	return userID
}

func (f *adminUsersFixture) seedPaymentCreditEvent(userID int64, fingerprint, amount string, occurredAt time.Time) {
	f.seedPaymentCreditEventInTenant(f.tenantID, userID, fingerprint, amount, occurredAt)
}

func (f *adminUsersFixture) seedOtherTenantPaymentCreditEvent(userID int64, fingerprint, amount string, occurredAt time.Time) {
	f.seedPaymentCreditEventInTenant(f.otherTenantID, userID, fingerprint, amount, occurredAt)
}

func (f *adminUsersFixture) seedPaymentCreditEventInTenant(tenantID, userID int64, fingerprint, amount string, occurredAt time.Time) {
	f.t.Helper()
	var orderID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO payment_orders (tenant_id, user_id, out_trade_no, amount_cents, currency_code, status)
		 VALUES ($1, $2, $3, 100, 'USD', 'completed')
		 RETURNING id`,
		tenantID, userID, fingerprint+"-"+f.suffix,
	).Scan(&orderID); err != nil {
		f.t.Fatalf("seed payment order %s: %v", fingerprint, err)
	}
	var creditID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, created_at)
		 VALUES ($1, $2, $3, 100, 'USD', 'manual_confirmed', $4)
		 RETURNING id`,
		tenantID, orderID, userID, occurredAt,
	).Scan(&creditID); err != nil {
		f.t.Fatalf("seed payment credit %s: %v", fingerprint, err)
	}
	var eventID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO billing_events (
			tenant_id, event_type, actual_cost, actual_cost_signed,
			stream_state, delivered_token_count, fingerprint, payment_credit_id, occurred_at
		) VALUES (
			$1, 'payment_credited', $2::numeric(20,8), $2::numeric(20,8),
			2, 0, $3, $4, $5
		) RETURNING id`,
		tenantID, amount, fingerprint, creditID, occurredAt,
	).Scan(&eventID); err != nil {
		f.t.Fatalf("seed billing event %s: %v", fingerprint, err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE payment_credits SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		tenantID, creditID, eventID,
	); err != nil {
		f.t.Fatalf("link payment credit event %s: %v", fingerprint, err)
	}
}

// seedSubscriptionAutoRenewalEvent 为用户种一条订阅续费扣款账本链
// (plan → user_subscription → charge → billing_events + 回链), 供余额历史渲染测试。
func (f *adminUsersFixture) seedSubscriptionAutoRenewalEvent(userID int64, fingerprint, amount string, occurredAt time.Time) {
	f.t.Helper()
	var planID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO subscription_plans (tenant_id, name, price_cents, currency_code, validity_days, granted_group, for_sale, enabled)
		 VALUES ($1, $2, 500, 'USD', 30, 'premium', true, true)
		 RETURNING id`,
		f.tenantID, "plan-"+fingerprint+"-"+f.suffix,
	).Scan(&planID); err != nil {
		f.t.Fatalf("seed plan %s: %v", fingerprint, err)
	}
	var subID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO user_subscriptions (tenant_id, user_id, plan_id, granted_group, status, source, auto_renew, starts_at, expires_at)
		 VALUES ($1, $2, $3, 'premium', 'active', 'admin', true, $4::timestamptz, $4::timestamptz + interval '30 days')
		 RETURNING id`,
		f.tenantID, userID, planID, occurredAt,
	).Scan(&subID); err != nil {
		f.t.Fatalf("seed subscription %s: %v", fingerprint, err)
	}
	var chargeID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO subscription_auto_renewal_charges (tenant_id, user_id, user_subscription_id, period_key, plan_id, amount_cents, prev_expires_at, new_expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, 500, $6::timestamptz, $6::timestamptz + interval '30 days', $6::timestamptz)
		 RETURNING id`,
		f.tenantID, userID, subID, fingerprint+"-"+f.suffix, planID, occurredAt,
	).Scan(&chargeID); err != nil {
		f.t.Fatalf("seed renewal charge %s: %v", fingerprint, err)
	}
	var eventID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO billing_events (
			tenant_id, event_type, actual_cost, actual_cost_signed,
			stream_state, delivered_token_count, fingerprint, subscription_auto_renewal_charge_id, occurred_at
		) VALUES (
			$1, 'subscription_auto_renewed', 0, $2::numeric(20,8),
			2, 0, $3, $4, $5
		) RETURNING id`,
		f.tenantID, amount, fingerprint, chargeID, occurredAt,
	).Scan(&eventID); err != nil {
		f.t.Fatalf("seed renewal billing event %s: %v", fingerprint, err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE subscription_auto_renewal_charges SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, chargeID, eventID,
	); err != nil {
		f.t.Fatalf("link renewal billing event %s: %v", fingerprint, err)
	}
}

type adminUsersListResponse struct {
	Items  []adminUsersListItem `json:"items"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

type adminUsersListItem struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Balance string `json:"balance"`
}

type adminBalanceHistoryResponse struct {
	Items []adminBalanceHistoryItem `json:"items"`
}

type adminBalanceHistoryItem struct {
	Fingerprint string `json:"fingerprint"`
	EventType   string `json:"event_type"`
	SourceType  string `json:"source_type"`
	Amount      string `json:"amount"`
}

type adminTwoFAStatsResponse struct {
	EnabledUsers int64   `json:"enabled_users"`
	TotalUsers   int64   `json:"total_users"`
	EnabledRate  float64 `json:"enabled_rate"`
}

// seedAPIKeyForUser 为已存在用户种一个 active API key,返回明文 token。
// key_prefix = 明文前 16 字符(对齐 auth.APIKeyResolver 派生)。
func (f *adminUsersFixture) seedAPIKeyForUser(userID int64) string {
	f.t.Helper()
	plaintext := "hk_live_" + uuid.NewString() + "_tail"
	prefix := plaintext
	if len(prefix) > auth.APIKeyPrefixLen {
		prefix = prefix[:auth.APIKeyPrefixLen]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		f.t.Fatalf("bcrypt key: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, 'active', NULL)`,
		f.tenantID, userID, "k-"+f.suffix, string(hash), prefix,
	); err != nil {
		f.t.Fatalf("seed api_key: %v", err)
	}
	return plaintext
}

// TestPGAdminSetUserStatusBansBothAxes 承重守卫:封号(status=disabled)必须**同时**
// 切断登录(userauth)与已签发 API key(auth.api_key_resolver 联查 user_status),
// 解封恢复双轴。这是封禁端点「真有用」的核心——只挡登录、不挡 key 等于没封。
// MUTATION(端点侧):handler 不调 setter → 双轴仍通 → 红;
// MUTATION(resolver 侧,非本切片但本测试守):api_key_resolver 去掉 user_status 检查
// → 封号后 key 仍解析成功 → key 轴断言红。
func TestPGAdminSetUserStatusBansBothAxes(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	userID, email := f.seedPasswordUser("ban", "correct-secret")
	token := f.seedAPIKeyForUser(userID)

	authSvc := userauth.NewService(userauth.NewPostgresStore(pool))
	authSvc.RequireVerified = true
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	keyResolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	bearer := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}

	// 封号前:两轴都通。
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "correct-secret"}); err != nil {
		t.Fatalf("封号前登录应成功: %v", err)
	}
	if _, err := keyResolver.Resolve(ctx, bearer()); err != nil {
		t.Fatalf("封号前 API key 应解析成功: %v", err)
	}

	// 管理员封号(platform_admin 带 ?tenant_id,验证放行 + 封禁双效)。
	rec := invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            admindb.New(pool),
		UserStatusSetter: NewPostgresUserStatusStore(pool),
		Audit:            admindb.New(pool),
	}, http.MethodPut, fmt.Sprintf("/admin/v1/users/%d/status?tenant_id=%d", userID, f.tenantID), `{"status":"disabled","reason":"abuse"}`)
	assertStatus(t, rec, http.StatusOK)

	// 封号后:登录被拒 + API key 被拒(双轴切断)。
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "correct-secret"}); !errors.Is(err, userauth.ErrUserDisabled) {
		t.Fatalf("封号后登录 err=%v want ErrUserDisabled", err)
	}
	if _, err := keyResolver.Resolve(ctx, bearer()); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("封号后 API key err=%v want ErrUnauthorized(resolver 须联查 user_status)", err)
	}

	// DB 落地确认 + 审计。
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("db status=%q want disabled", status)
	}
	var action string
	if err := pool.QueryRow(ctx,
		`SELECT action FROM admin_audit_events WHERE tenant_id=$1 AND target_id=$2 AND action='set_user_status' ORDER BY id DESC LIMIT 1`,
		f.tenantID, userID).Scan(&action); err != nil {
		t.Fatalf("read set_user_status audit: %v", err)
	}

	// 解封:两轴恢复。
	rec = invokeAdminUsersBody(t, Deps{
		Auth:             usersAuthStub{ident: platformAdmin()},
		Store:            admindb.New(pool),
		UserStatusSetter: NewPostgresUserStatusStore(pool),
		Audit:            admindb.New(pool),
	}, http.MethodPut, fmt.Sprintf("/admin/v1/users/%d/status?tenant_id=%d", userID, f.tenantID), `{"status":"active"}`)
	assertStatus(t, rec, http.StatusOK)
	if _, err := authSvc.Authenticate(ctx, userauth.LoginInput{TenantID: f.tenantID, Email: email, Password: "correct-secret"}); err != nil {
		t.Fatalf("解封后登录应恢复: %v", err)
	}
	if _, err := keyResolver.Resolve(ctx, bearer()); err != nil {
		t.Fatalf("解封后 API key 应恢复: %v", err)
	}
}
