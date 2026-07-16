//go:build integration_pg

package referralhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestListUserReferrals_TenantUserScoped(t *testing.T) {
	// 变异:去掉 referrer_user_id 过滤 -> 用户 A 会看到同 tenant 下用户 B 的 referral。
	// 变异:去掉 tenant_id 过滤 -> 用户 A 会看到用其 referrer id 种入的跨 tenant 记录。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openReferralIntegrationPool(t, ctx)
	f := newReferralPGFixture(t, ctx, pool)
	service := invitation.NewService(invitation.NewPostgresStore(pool))
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)

	invA := f.seedInvitation(f.tenantA, f.userA, "ref-user-a")
	aPending := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "pending", CreatedAt: now.Add(-3 * time.Hour)})
	aRewarded := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "rewarded", CreatedAt: now.Add(-1 * time.Hour)})
	f.seedReward(aRewarded, "credit", 500000, now.Add(-30*time.Minute))

	invB := f.seedInvitation(f.tenantA, f.userB, "ref-user-b")
	f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invB, ReferrerUserID: f.userB, Status: "qualified", CreatedAt: now})
	invOtherTenant := f.seedInvitation(f.tenantB, f.userA, "ref-cross-tenant")
	f.seedReferral(referralSeedInput{TenantID: f.tenantB, InvitationID: invOtherTenant, ReferrerUserID: f.userA, Status: "qualified", CreatedAt: now.Add(time.Hour)})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/referrals?limit=10&offset=0", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: f.tenantA, UserID: f.userA}))
	NewUserReferralsHandler(Deps{Service: service}).ServeHTTP(rec, req)
	assertReferralHTTPStatus(t, rec, http.StatusOK)

	var body struct {
		Total int64 `json:"total"`
		Items []struct {
			ReferralID    int64   `json:"referral_id"`
			RefereeUserID int64   `json:"referee_user_id"`
			Status        string  `json:"status"`
			CreatedAt     string  `json:"created_at"`
			RewardedAt    *string `json:"rewarded_at"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.Total != 2 || len(body.Items) != 2 {
		t.Fatalf("user referrals total=%d len=%d body=%s want exactly user A's 2 tenant A rows", body.Total, len(body.Items), rec.Body.String())
	}
	gotIDs := []int64{body.Items[0].ReferralID, body.Items[1].ReferralID}
	sort.Slice(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] })
	wantIDs := []int64{aPending.ReferralID, aRewarded.ReferralID}
	sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] < wantIDs[j] })
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("referral IDs=%v want only %v", gotIDs, wantIDs)
	}
	for _, item := range body.Items {
		if item.CreatedAt == "" {
			t.Fatalf("missing created_at in item %+v", item)
		}
		if item.ReferralID == aRewarded.ReferralID && item.RewardedAt == nil {
			t.Fatalf("rewarded referral item missing rewarded_at: %+v", item)
		}
	}
}

func TestListUserReferralRewards_AmountConversion(t *testing.T) {
	// 变异:把 amount_usd 的除数从 1000000 改成 100 或 10000 -> 精确的 USD 字符串断言转红。
	// 变异:去掉 tenant_id/referrer_user_id 谓词 -> 同 tenant 下其他用户或跨 tenant 的 reward 记录会进入流水。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openReferralIntegrationPool(t, ctx)
	f := newReferralPGFixture(t, ctx, pool)
	service := invitation.NewService(invitation.NewPostgresStore(pool))
	now := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)

	invA := f.seedInvitation(f.tenantA, f.userA, "reward-user-a")
	aReferral := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "rewarded", CreatedAt: now})
	f.seedReward(aReferral, "credit", 1234567, now.Add(time.Minute))

	invB := f.seedInvitation(f.tenantA, f.userB, "reward-user-b")
	bReferral := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invB, ReferrerUserID: f.userB, Status: "rewarded", CreatedAt: now})
	f.seedReward(bReferral, "credit", 77000000, now.Add(2*time.Minute))

	invOtherTenant := f.seedInvitation(f.tenantB, f.userA, "reward-cross-tenant")
	crossReferral := f.seedReferral(referralSeedInput{TenantID: f.tenantB, InvitationID: invOtherTenant, ReferrerUserID: f.userA, Status: "rewarded", CreatedAt: now})
	f.seedReward(crossReferral, "credit", 88000000, now.Add(3*time.Minute))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/referrals/rewards", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: f.tenantA, UserID: f.userA}))
	NewUserReferralRewardsHandler(Deps{Service: service}).ServeHTTP(rec, req)
	assertReferralHTTPStatus(t, rec, http.StatusOK)

	var body struct {
		Total          int64  `json:"total"`
		TotalRewardUSD string `json:"total_reward_usd"`
		Items          []struct {
			ReferralID int64  `json:"referral_id"`
			RewardType string `json:"reward_type"`
			AmountUSD  string `json:"amount_usd"`
			CreatedAt  string `json:"created_at"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.Total != 1 || body.TotalRewardUSD != "1.234567" || len(body.Items) != 1 {
		t.Fatalf("ledger body=%s want one scoped reward with total 1.234567", rec.Body.String())
	}
	if body.Items[0].ReferralID != aReferral.ReferralID || body.Items[0].RewardType != "credit" || body.Items[0].AmountUSD != "1.234567" || body.Items[0].CreatedAt == "" {
		t.Fatalf("ledger item=%+v want referral=%d credit amount 1.234567", body.Items[0], aReferral.ReferralID)
	}
}

func TestReferralOverview_Counts(t *testing.T) {
	// 变异:把每条 referral 都计入同一个状态桶,或不分 referral 状态地汇总所有 reward 记录 -> counts/total_reward_usd 转红。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openReferralIntegrationPool(t, ctx)
	f := newReferralPGFixture(t, ctx, pool)
	service := invitation.NewService(invitation.NewPostgresStore(pool))
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	invA := f.seedInvitation(f.tenantA, f.userA, "overview")

	f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "pending", CreatedAt: now})
	qualifiedA := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "qualified", CreatedAt: now.Add(time.Minute)})
	f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userB, Status: "qualified", CreatedAt: now.Add(2 * time.Minute)})
	rewarded := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "rewarded", CreatedAt: now.Add(3 * time.Minute)})
	f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "rejected", CreatedAt: now.Add(4 * time.Minute)})
	f.seedReward(rewarded, "credit", 2500000, now.Add(5*time.Minute))
	f.seedReward(qualifiedA, "credit", 9000000, now.Add(6*time.Minute))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals/overview", nil)
	NewAdminReferralOverviewHandler(Deps{
		Service:   service,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, f.tenantA)},
	}).ServeHTTP(rec, req)
	assertReferralHTTPStatus(t, rec, http.StatusOK)

	var body struct {
		CountsByStatus map[string]int64 `json:"counts_by_status"`
		TotalRewardUSD string           `json:"total_reward_usd"`
		RewardCount    int64            `json:"reward_count"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.CountsByStatus["pending"] != 1 || body.CountsByStatus["qualified"] != 2 ||
		body.CountsByStatus["rewarded"] != 1 || body.CountsByStatus["rejected"] != 1 {
		t.Fatalf("overview counts=%+v want pending=1 qualified=2 rewarded=1 rejected=1", body.CountsByStatus)
	}
	if body.TotalRewardUSD != "2.5" || body.RewardCount != 1 {
		t.Fatalf("overview reward total=%s count=%d want 2.5/1", body.TotalRewardUSD, body.RewardCount)
	}
}

func TestListReferralsAdmin_StatusFilter(t *testing.T) {
	// 变异:忽略 status 过滤 -> pending 记录出现在 qualified 页;忽略 offset -> 返回第一条 qualified 记录而非第二条。
	// 变异:不把 limit 上限封到 100 -> pending 页返回超过 100 条记录。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openReferralIntegrationPool(t, ctx)
	f := newReferralPGFixture(t, ctx, pool)
	service := invitation.NewService(invitation.NewPostgresStore(pool))
	now := time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC)
	invA := f.seedInvitation(f.tenantA, f.userA, "admin-list")

	oldQualified := f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "qualified", CreatedAt: now})
	f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userA, Status: "qualified", CreatedAt: now.Add(time.Hour)})
	for i := 0; i < 102; i++ {
		f.seedReferral(referralSeedInput{TenantID: f.tenantA, InvitationID: invA, ReferrerUserID: f.userB, Status: "pending", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals?status=qualified&limit=1&offset=1", nil)
	NewAdminReferralsHandler(Deps{
		Service:   service,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, f.tenantA)},
	}).ServeHTTP(rec, req)
	assertReferralHTTPStatus(t, rec, http.StatusOK)
	var page struct {
		Total  int64 `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
		Items  []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &page)
	if page.Total != 2 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 ||
		page.Items[0].ID != oldQualified.ReferralID || page.Items[0].Status != "qualified" {
		t.Fatalf("qualified page=%+v want second qualified referral %d", page, oldQualified.ReferralID)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/referrals?status=pending&limit=500", nil)
	NewAdminReferralsHandler(Deps{
		Service:   service,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, f.tenantA)},
	}).ServeHTTP(rec, req)
	assertReferralHTTPStatus(t, rec, http.StatusOK)
	decodeReferralHTTPBody(t, rec, &page)
	if page.Total != 102 || page.Limit != 100 || len(page.Items) != 100 {
		t.Fatalf("pending capped page total=%d limit=%d len=%d want 102/100/100", page.Total, page.Limit, len(page.Items))
	}
	for _, item := range page.Items {
		if item.Status != "pending" {
			t.Fatalf("pending status filter returned item %+v", item)
		}
	}
}

func openReferralIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 10})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type referralPGFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenantA int64
	userA   int64
	userB   int64
	tenantB int64
	userC   int64
}

func newReferralPGFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *referralPGFixture {
	t.Helper()
	f := &referralPGFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA, f.userA = f.seedTenantUser("ref-a")
	f.userB = f.seedUser(f.tenantA, "ref-b")
	f.tenantB, f.userC = f.seedTenantUser("ref-c")
	t.Cleanup(f.cleanup)
	return f
}

func (f *referralPGFixture) seedTenantUser(label string) (int64, int64) {
	f.t.Helper()
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	return tenantID, f.seedUser(tenantID, label)
}

func (f *referralPGFixture) seedUser(tenantID int64, label string) int64 {
	f.t.Helper()
	var userID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix).Scan(&userID); err != nil {
		f.t.Fatalf("seed user %s: %v", label, err)
	}
	return userID
}

func (f *referralPGFixture) seedInvitation(tenantID, inviterUserID int64, label string) int64 {
	f.t.Helper()
	var id int64
	code := "code-" + label + "-" + f.suffix
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO invitations (tenant_id, code, inviter_user_id, created_at, max_usage)
VALUES ($1, $2, $3, $4, 500)
RETURNING id`, tenantID, code, inviterUserID, time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)).Scan(&id); err != nil {
		f.t.Fatalf("seed invitation %s: %v", label, err)
	}
	return id
}

type referralSeedInput struct {
	TenantID       int64
	InvitationID   int64
	ReferrerUserID int64
	Status         string
	CreatedAt      time.Time
}

type referralSeed struct {
	TenantID       int64
	ReferralID     int64
	ReferrerUserID int64
	RefereeUserID  int64
}

func (f *referralPGFixture) seedReferral(in referralSeedInput) referralSeed {
	f.t.Helper()
	refereeUserID := f.seedUser(in.TenantID, "referee-"+in.Status)
	var qualifiedAt any
	if in.Status == "qualified" || in.Status == "rewarded" {
		qualifiedAt = in.CreatedAt.Add(time.Minute)
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO referrals (tenant_id, referee_user_id, referrer_user_id, invitation_id, status, qualified_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id`, in.TenantID, refereeUserID, in.ReferrerUserID, in.InvitationID, in.Status, qualifiedAt, in.CreatedAt.UTC()).Scan(&id); err != nil {
		f.t.Fatalf("seed referral status=%s: %v", in.Status, err)
	}
	return referralSeed{TenantID: in.TenantID, ReferralID: id, ReferrerUserID: in.ReferrerUserID, RefereeUserID: refereeUserID}
}

func (f *referralPGFixture) seedReward(ref referralSeed, rewardType string, amountMicros int64, issuedAt time.Time) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO referral_rewards (tenant_id, referrer_user_id, referee_user_id, referral_id, reward_type, amount_usd_micros, issued_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ref.TenantID, ref.ReferrerUserID, ref.RefereeUserID, ref.ReferralID, rewardType, amountMicros, issuedAt.UTC()); err != nil {
		f.t.Fatalf("seed referral reward: %v", err)
	}
}

func (f *referralPGFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range []int64{f.tenantA, f.tenantB} {
		_, _ = f.pool.Exec(ctx, `DELETE FROM referral_reward_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM referral_rewards WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM referrals WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM invitations WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}
