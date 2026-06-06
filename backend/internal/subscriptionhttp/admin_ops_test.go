// HUAKAI · iKun

package subscriptionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

type adminOpsServiceStub struct {
	fakeSubscriptionService
	gotUpdate subscription.UpdatePlanInput
	gotExtend subscription.ExtendSubscriptionInput
	gotReset  subscription.ResetQuotaInput
	gotBulk   subscription.BulkAssignInput
	gotRevoke subscription.RevokeSubscriptionInput
	bulk      subscription.BulkAssignResult
}

func (s *adminOpsServiceStub) UpdatePlan(_ context.Context, in subscription.UpdatePlanInput) (subscription.Plan, error) {
	s.gotUpdate = in
	return subscription.Plan{
		ID: in.PlanID, TenantID: in.TenantID, Name: in.Name, Description: in.Description,
		PriceCents: in.PriceCents, CurrencyCode: in.CurrencyCode, ValidityDays: in.ValidityDays,
		GrantedGroup: in.GrantedGroup, DailyCapUSD: in.DailyCapUSD, WeeklyCapUSD: in.WeeklyCapUSD,
		MonthlyCapUSD: in.MonthlyCapUSD, ForSale: in.ForSale, Enabled: true, SortOrder: in.SortOrder,
	}, nil
}

func (s *adminOpsServiceStub) ExtendSubscription(_ context.Context, in subscription.ExtendSubscriptionInput) (subscription.UserSubscription, error) {
	s.gotExtend = in
	sub := sampleSubscription()
	sub.ID = in.SubscriptionID
	sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, in.Days)
	return sub, nil
}

func (s *adminOpsServiceStub) ResetQuota(_ context.Context, in subscription.ResetQuotaInput) (subscription.UserSubscription, error) {
	s.gotReset = in
	sub := sampleSubscription()
	sub.ID = in.SubscriptionID
	return sub, nil
}

func (s *adminOpsServiceStub) BulkAssign(_ context.Context, in subscription.BulkAssignInput) (subscription.BulkAssignResult, error) {
	s.gotBulk = in
	return s.bulk, nil
}

func (s *adminOpsServiceStub) RevokeSubscription(_ context.Context, in subscription.RevokeSubscriptionInput) (subscription.UserSubscription, error) {
	s.gotRevoke = in
	sub := sampleSubscription()
	sub.ID = in.SubscriptionID
	sub.Status = subscription.StatusRevoked
	return sub, nil
}

func TestAdminUpdatePlanRoutePassesActorAndMutableFields(t *testing.T) {
	daily := "3"
	monthly := "25"
	forSale := false
	body, _ := json.Marshal(map[string]any{
		"tenant_id": 5, "name": "premium-plus", "description": "ops",
		"price_cents": 2999, "currency_code": "usd", "validity_days": 60,
		"granted_group": "premium", "daily_cap_usd": daily, "monthly_cap_usd": monthly,
		"for_sale": forSale, "sort_order": 11,
	})
	svc := &adminOpsServiceStub{}
	router := newSubAdminTestRouter(AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service: svc,
	})

	req := httptest.NewRequest(http.MethodPut, "/subs/plans/42", bytes.NewReader(body))
	req.Header.Set("X-Request-Id", "plan-update-route")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotUpdate.PlanID != 42 || svc.gotUpdate.TenantID != 5 || svc.gotUpdate.ActorAdminID != 77 ||
		svc.gotUpdate.RequestID != "plan-update-route" {
		t.Fatalf("update identity fields mismatch: %+v", svc.gotUpdate)
	}
	if svc.gotUpdate.Name != "premium-plus" || svc.gotUpdate.PriceCents != 2999 ||
		svc.gotUpdate.CurrencyCode != "usd" || svc.gotUpdate.ValidityDays != 60 || svc.gotUpdate.ForSale {
		t.Fatalf("update mutable fields mismatch: %+v", svc.gotUpdate)
	}
	if svc.gotUpdate.DailyCapUSD == nil || svc.gotUpdate.MonthlyCapUSD == nil {
		t.Fatalf("update caps not parsed: %+v", svc.gotUpdate)
	}
}

func TestAdminAssignmentLifecycleRoutesPassRequestIDAndReason(t *testing.T) {
	until := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	svc := &adminOpsServiceStub{}
	router := newSubAdminTestRouter(AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service: svc,
	})

	cases := []struct {
		name   string
		path   string
		body   string
		check  func(*testing.T)
		status int
	}{
		{
			name: "extend", path: "/subs/assignments/9/extend",
			body: `{"tenant_id":5,"days":30}`,
			check: func(t *testing.T) {
				if svc.gotExtend.SubscriptionID != 9 || svc.gotExtend.Days != 30 ||
					svc.gotExtend.ActorAdminID != 77 || svc.gotExtend.RequestID != "ops-route" {
					t.Fatalf("extend input mismatch: %+v", svc.gotExtend)
				}
			},
			status: http.StatusOK,
		},
		{
			name: "extend-until", path: "/subs/assignments/10/extend",
			body: `{"tenant_id":5,"until":"` + until.Format(time.RFC3339) + `"}`,
			check: func(t *testing.T) {
				if svc.gotExtend.SubscriptionID != 10 || svc.gotExtend.Until == nil || !svc.gotExtend.Until.Equal(until) {
					t.Fatalf("extend until input mismatch: %+v", svc.gotExtend)
				}
			},
			status: http.StatusOK,
		},
		{
			name: "reset", path: "/subs/assignments/9/reset-quota",
			body: `{"tenant_id":5}`,
			check: func(t *testing.T) {
				if svc.gotReset.SubscriptionID != 9 || svc.gotReset.ActorAdminID != 77 ||
					svc.gotReset.RequestID != "ops-route" {
					t.Fatalf("reset input mismatch: %+v", svc.gotReset)
				}
			},
			status: http.StatusOK,
		},
		{
			name: "revoke", path: "/subs/assignments/9/revoke",
			body: `{"tenant_id":5,"reason":"fraud"}`,
			check: func(t *testing.T) {
				if svc.gotRevoke.SubscriptionID != 9 || svc.gotRevoke.Reason != "fraud" ||
					svc.gotRevoke.ActorAdminID != 77 || svc.gotRevoke.RequestID != "ops-route" {
					t.Fatalf("revoke input mismatch: %+v", svc.gotRevoke)
				}
			},
			status: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Request-Id", "ops-route")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			tc.check(t)
		})
	}
}

func TestAdminBulkAssignRouteReturnsPerUserResults(t *testing.T) {
	svc := &adminOpsServiceStub{bulk: subscription.BulkAssignResult{Results: []subscription.BulkAssignUserResult{
		{UserID: 41, OK: true, Subscription: sampleSubscription()},
		{UserID: 999, OK: false, Error: "subscription: invalid input"},
	}}}
	svc.bulk.Results[0].Subscription.UserID = 41
	router := newSubAdminTestRouter(AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service: svc,
	})

	req := httptest.NewRequest(http.MethodPost, "/subs/assignments/bulk", strings.NewReader(`{"tenant_id":5,"user_ids":[41,999],"plan_id":3}`))
	req.Header.Set("X-Request-Id", "bulk-route")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotBulk.TenantID != 5 || svc.gotBulk.PlanID != 3 || svc.gotBulk.ActorAdminID != 77 ||
		svc.gotBulk.RequestID != "bulk-route" || len(svc.gotBulk.UserIDs) != 2 {
		t.Fatalf("bulk input mismatch: %+v", svc.gotBulk)
	}
	body := rec.Body.String()
	for _, want := range []string{`"user_id":41`, `"ok":true`, `"user_id":999`, `"ok":false`, `"error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("bulk response missing %s: %s", want, body)
		}
	}
}
