// HUAKAI · iKun
//go:build integration_pg

package voucherhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

func openVoucherHTTPIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 8})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type voucherHistoryFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	suffix   string
	tenantID int64
	userA    int64
	userB    int64
}

func newVoucherHistoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *voucherHistoryFixture {
	t.Helper()
	f := &voucherHistoryFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "vhistory-"+f.suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1,$2) RETURNING id`, f.tenantID, "user-a-"+f.suffix).Scan(&f.userA); err != nil {
		t.Fatalf("seed userA: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1,$2) RETURNING id`, f.tenantID, "user-b-"+f.suffix).Scan(&f.userB); err != nil {
		t.Fatalf("seed userB: %v", err)
	}
	t.Cleanup(f.cleanup)
	return f
}

func (f *voucherHistoryFixture) cleanup() {
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM billing_events WHERE tenant_id=$1`,
		`DELETE FROM voucher_redemption WHERE tenant_id=$1`,
		`DELETE FROM voucher WHERE tenant_id=$1`,
		`DELETE FROM users WHERE tenant_id=$1`,
		`DELETE FROM tenants WHERE id=$1`,
	} {
		_, _ = f.pool.Exec(ctx, q, f.tenantID)
	}
}

func (f *voucherHistoryFixture) seedRedemption(userID int64, amountCents int64, redeemedAt time.Time) int64 {
	f.t.Helper()
	codeHash := []byte(uuid.NewString())
	codeFingerprint := uuid.NewString()[:12]
	var voucherID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO voucher (
	tenant_id, code_hash, code_fingerprint, amount_cents, currency_code,
	valid_from, valid_until, max_redemptions, single_use_per_user, status
) VALUES ($1,$2,$3,$4,'USD',$5,$6,10,true,'active')
RETURNING id`,
		f.tenantID, codeHash, codeFingerprint, amountCents,
		redeemedAt.AddDate(0, 0, -1), redeemedAt.AddDate(0, 0, 30)).Scan(&voucherID); err != nil {
		f.t.Fatalf("seed voucher: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO voucher_redemption (
	tenant_id, voucher_id, user_id, amount_cents, currency_code, single_use_per_user, status, redeemed_at
) VALUES ($1,$2,$3,$4,'USD',false,'succeeded',$5)`,
		f.tenantID, voucherID, userID, amountCents, redeemedAt.UTC()); err != nil {
		f.t.Fatalf("seed redemption: %v", err)
	}
	return voucherID
}

// VCH-022: 用户兑换历史必须只读且 self-scope; userA 不能看到 userB 的兑换。
// MUTATION: 查询去掉 user_id WHERE → userA 会看到 3 条而不是 2 条 → 红。
func TestVoucherRedemptionHistorySelfScope(t *testing.T) {
	ctx := context.Background()
	pool := openVoucherHTTPIntegrationPool(t, ctx)
	f := newVoucherHistoryFixture(t, ctx, pool)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	userAVoucher1 := f.seedRedemption(f.userA, 100, now.Add(-2*time.Hour))
	userAVoucher2 := f.seedRedemption(f.userA, 300, now.Add(-1*time.Hour))
	userBVoucher := f.seedRedemption(f.userB, 900, now)

	router := chi.NewRouter()
	router.Get("/v1/me/voucher-redemptions", NewRedemptionHistoryHandler(Deps{
		Service: voucher.NewService(voucher.NewPostgresStore(pool)),
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/me/voucher-redemptions", nil)
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{
		TenantID: f.tenantID,
		UserID:   f.userA,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Redemptions []redemptionView `json:"redemptions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Redemptions) != 2 {
		t.Fatalf("redemptions len=%d want 2; body=%s", len(resp.Redemptions), rec.Body.String())
	}
	got := map[int64]bool{}
	for _, red := range resp.Redemptions {
		got[red.VoucherID] = true
		if red.Status != "succeeded" {
			t.Fatalf("status=%q want succeeded", red.Status)
		}
	}
	if !got[userAVoucher1] || !got[userAVoucher2] {
		t.Fatalf("missing userA vouchers in response: got=%v want %d/%d", got, userAVoucher1, userAVoucher2)
	}
	if got[userBVoucher] {
		t.Fatalf("response leaked userB voucher %d: %+v", userBVoucher, resp.Redemptions)
	}
}
