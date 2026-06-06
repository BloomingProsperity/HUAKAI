//go:build integration_pg

package adminuserhttp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestAdminListUsers_TenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)

	a1 := f.seedUser("a-one", "active", "user", "10.00000000")
	a2 := f.seedUser("a-two", "disabled", "admin", "20.00000000")
	b1 := f.seedOtherTenantUser("b-one", "active", "user", "99.00000000")

	// MUTATION: remove u.tenant_id/sqlc tenant filter from AdminListUsersForTenant -> tenant B user leaks into tenant A list -> RED.
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

	// MUTATION: pass the unbounded requested limit through instead of capping at 100 -> this returns 120 rows -> RED.
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

	// MUTATION: drop tenant_id predicate from AdminGetUserForTenant -> globally unique tenant B id is readable by tenant A admin -> RED.
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

	// MUTATION: drop tenant/user source filters from AdminListUserBalanceHistoryForTenant -> other users' newer ledger rows leak or reorder result -> RED.
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

func (f *adminUsersFixture) seedOtherTenantUser(label, status, role, balance string) int64 {
	return f.seedUserInTenant(f.otherTenantID, label, status, role, balance)
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
}
