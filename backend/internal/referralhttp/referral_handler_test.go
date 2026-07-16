package referralhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
)

func TestUserReferralsAuthRequired(t *testing.T) {
	stub := &referralServiceStub{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/referrals", nil)

	NewUserReferralsHandler(Deps{Service: stub}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusUnauthorized)
	if stub.userReferralCalls != 0 {
		t.Fatalf("unauthorized request touched service: calls=%d", stub.userReferralCalls)
	}
}

func TestUserReferralRewardsUsesSessionScopeAndFormatsAmount(t *testing.T) {
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	stub := &referralServiceStub{
		rewardPage: invitation.ReferralRewardPage{
			Items: []invitation.ReferralRewardLedgerEntry{{
				ReferralID: 55,
				RewardType: "credit",
				AmountUSD:  decimal.RequireFromString("1.234567"),
				CreatedAt:  now,
			}},
			Total:          1,
			TotalRewardUSD: decimal.RequireFromString("1.234567"),
			Limit:          100,
			Offset:         2,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/referrals/rewards?limit=500&offset=2", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()

	NewUserReferralRewardsHandler(Deps{Service: stub}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusOK)
	if stub.rewardIn.TenantID != 7 || stub.rewardIn.ReferrerUserID != 42 || stub.rewardIn.Limit != 100 || stub.rewardIn.Offset != 2 {
		t.Fatalf("reward input=%+v want tenant=7 referrer=42 limit cap=100 offset=2", stub.rewardIn)
	}
	var body struct {
		Object         string `json:"object"`
		Total          int64  `json:"total"`
		TotalRewardUSD string `json:"total_reward_usd"`
		Limit          int    `json:"limit"`
		Offset         int    `json:"offset"`
		Items          []struct {
			ReferralID int64  `json:"referral_id"`
			RewardType string `json:"reward_type"`
			AmountUSD  string `json:"amount_usd"`
			CreatedAt  string `json:"created_at"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.Object != "referral_reward_ledger" || body.Total != 1 || body.TotalRewardUSD != "1.234567" || body.Limit != 100 || body.Offset != 2 {
		t.Fatalf("reward response envelope=%+v", body)
	}
	if len(body.Items) != 1 || body.Items[0].ReferralID != 55 || body.Items[0].AmountUSD != "1.234567" || body.Items[0].CreatedAt == "" {
		t.Fatalf("reward item=%+v", body.Items)
	}
}

func TestAdminReferralsTenantScopeStatusAndPagination(t *testing.T) {
	stub := &referralServiceStub{
		adminPage: invitation.ReferralRecordPage{
			Items: []invitation.ReferralRecord{{
				ID: 70, ReferrerUserID: 42, RefereeUserID: 43, Status: "qualified",
				CreatedAt: time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
			}},
			Total:  1,
			Limit:  100,
			Offset: 3,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals?status=qualified&limit=500&offset=3", nil)
	rec := httptest.NewRecorder()

	NewAdminReferralsHandler(Deps{
		Service:   stub,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, 7)},
	}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusOK)
	if stub.adminIn.TenantID != 7 || stub.adminIn.Status == nil || *stub.adminIn.Status != "qualified" || stub.adminIn.Limit != 100 || stub.adminIn.Offset != 3 {
		t.Fatalf("admin list input=%+v want tenant=7 status=qualified limit cap=100 offset=3", stub.adminIn)
	}
	var body struct {
		Object string `json:"object"`
		Total  int64  `json:"total"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
		Items  []struct {
			ID             int64  `json:"id"`
			ReferrerUserID int64  `json:"referrer_user_id"`
			RefereeUserID  int64  `json:"referee_user_id"`
			Status         string `json:"status"`
			CreatedAt      string `json:"created_at"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.Object != "admin_referrals_list" || body.Total != 1 || body.Limit != 100 || body.Offset != 3 {
		t.Fatalf("admin envelope=%+v", body)
	}
	if len(body.Items) != 1 || body.Items[0].ID != 70 || body.Items[0].Status != "qualified" {
		t.Fatalf("admin items=%+v", body.Items)
	}
}

func TestAdminOverviewPlatformAdminRequiresTenantID(t *testing.T) {
	stub := &referralServiceStub{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals/overview", nil)
	rec := httptest.NewRecorder()

	NewAdminReferralOverviewHandler(Deps{
		Service:   stub,
		AdminAuth: referralAdminAuthStub{ident: admintest.Platform(1)},
	}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusBadRequest)
	if stub.overviewCalls != 0 {
		t.Fatalf("missing tenant_id touched overview service: calls=%d", stub.overviewCalls)
	}
}

func TestAdminOverviewFormatsCountsAndTotalReward(t *testing.T) {
	stub := &referralServiceStub{
		overview: invitation.ReferralOverview{
			CountsByStatus: map[string]int64{"pending": 1, "qualified": 2, "rewarded": 3, "rejected": 4},
			TotalRewardUSD: decimal.RequireFromString("2.5"),
			RewardCount:    3,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals/overview?tenant_id=9", nil)
	rec := httptest.NewRecorder()

	NewAdminReferralOverviewHandler(Deps{
		Service:   stub,
		AdminAuth: referralAdminAuthStub{ident: admintest.Platform(1)},
	}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusOK)
	if stub.overviewTenantID != 9 {
		t.Fatalf("overview tenant=%d want 9", stub.overviewTenantID)
	}
	var body struct {
		CountsByStatus map[string]int64 `json:"counts_by_status"`
		TotalRewardUSD string           `json:"total_reward_usd"`
		RewardCount    int64            `json:"reward_count"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.CountsByStatus["pending"] != 1 || body.CountsByStatus["qualified"] != 2 ||
		body.CountsByStatus["rewarded"] != 3 || body.CountsByStatus["rejected"] != 4 ||
		body.TotalRewardUSD != "2.5" || body.RewardCount != 3 {
		t.Fatalf("overview body=%+v", body)
	}
}

func TestAdminReferralsRejectsInvalidStatus(t *testing.T) {
	stub := &referralServiceStub{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals?status=paid", nil)
	rec := httptest.NewRecorder()

	NewAdminReferralsHandler(Deps{
		Service:   stub,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, 7)},
	}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("body=%s want invalid_request", rec.Body.String())
	}
	if stub.adminReferralCalls != 0 {
		t.Fatalf("invalid status touched service: calls=%d", stub.adminReferralCalls)
	}
}

type referralServiceStub struct {
	userReferralCalls  int
	userReferralIn     invitation.ListUserReferralsInput
	userReferralPage   invitation.ReferralRecordPage
	rewardCalls        int
	rewardIn           invitation.ListUserReferralRewardsInput
	rewardPage         invitation.ReferralRewardPage
	adminReferralCalls int
	adminIn            invitation.ListReferralsAdminInput
	adminPage          invitation.ReferralRecordPage
	adminRewardCalls   int
	adminRewardIn      invitation.ListReferralRewardsAdminInput
	adminRewardPage    invitation.AdminReferralRewardPage
	overviewCalls      int
	overviewTenantID   int64
	overview           invitation.ReferralOverview
	err                error
}

func (s *referralServiceStub) ListUserReferrals(_ context.Context, in invitation.ListUserReferralsInput) (invitation.ReferralRecordPage, error) {
	s.userReferralCalls++
	s.userReferralIn = in
	if s.err != nil {
		return invitation.ReferralRecordPage{}, s.err
	}
	return s.userReferralPage, nil
}

func (s *referralServiceStub) ListUserReferralRewards(_ context.Context, in invitation.ListUserReferralRewardsInput) (invitation.ReferralRewardPage, error) {
	s.rewardCalls++
	s.rewardIn = in
	if s.err != nil {
		return invitation.ReferralRewardPage{}, s.err
	}
	return s.rewardPage, nil
}

func (s *referralServiceStub) ListReferralsAdmin(_ context.Context, in invitation.ListReferralsAdminInput) (invitation.ReferralRecordPage, error) {
	s.adminReferralCalls++
	s.adminIn = in
	if s.err != nil {
		return invitation.ReferralRecordPage{}, s.err
	}
	return s.adminPage, nil
}

func (s *referralServiceStub) ReferralOverview(_ context.Context, tenantID int64) (invitation.ReferralOverview, error) {
	s.overviewCalls++
	s.overviewTenantID = tenantID
	if s.err != nil {
		return invitation.ReferralOverview{}, s.err
	}
	return s.overview, nil
}

type referralAdminAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s referralAdminAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

func assertReferralHTTPStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func decodeReferralHTTPBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
}

func (s *referralServiceStub) ListReferralRewardsAdmin(_ context.Context, in invitation.ListReferralRewardsAdminInput) (invitation.AdminReferralRewardPage, error) {
	s.adminRewardCalls++
	s.adminRewardIn = in
	if s.err != nil {
		return invitation.AdminReferralRewardPage{}, s.err
	}
	return s.adminRewardPage, nil
}

func TestAdminReferralRewardsTenantScopedAndFiltered(t *testing.T) {
	ref := int64(42)
	stub := &referralServiceStub{
		adminRewardPage: invitation.AdminReferralRewardPage{
			Items: []invitation.AdminReferralRewardEntry{{
				ID: 9, ReferralID: 70, ReferrerUserID: 42, RewardType: "credit",
				AmountUSD: decimal.RequireFromString("1.50"), CreatedAt: time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
			}},
			Total: 1, TotalRewardUSD: decimal.RequireFromString("1.50"), Limit: 100, Offset: 0,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/referrals/rewards?referrer_user_id=42&limit=500", nil)
	rec := httptest.NewRecorder()
	NewAdminReferralRewardsHandler(Deps{
		Service:   stub,
		AdminAuth: referralAdminAuthStub{ident: admintest.TenantOperator(1, 7)},
	}).ServeHTTP(rec, req)

	assertReferralHTTPStatus(t, rec, http.StatusOK)
	// 变异:handler 丢掉 tenant scope -> TenantID!=7;丢掉 referrer 解析 -> ReferrerUserID 为 nil。
	if stub.adminRewardIn.TenantID != 7 || stub.adminRewardIn.ReferrerUserID == nil || *stub.adminRewardIn.ReferrerUserID != ref || stub.adminRewardIn.Limit != 100 {
		t.Fatalf("admin reward input=%+v want tenant=7 referrer=42 limit-cap=100", stub.adminRewardIn)
	}
	var body struct {
		Object string `json:"object"`
		Total  int64  `json:"total"`
		Items  []struct {
			ReferrerUserID int64  `json:"referrer_user_id"`
			RewardType     string `json:"reward_type"`
		} `json:"items"`
	}
	decodeReferralHTTPBody(t, rec, &body)
	if body.Object != "admin_referral_rewards_list" || body.Total != 1 || len(body.Items) != 1 || body.Items[0].ReferrerUserID != 42 || body.Items[0].RewardType != "credit" {
		t.Fatalf("admin reward body=%+v", body)
	}
}
