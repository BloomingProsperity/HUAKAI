package subscriptionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

func TestCreateSubscriptionOrderUsesSessionIdentityAndRejectsBodyTenantUser(t *testing.T) {
	svc := &subscriptionServiceStub{}
	mux := mountUserSubscriptionRoutes(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true, "hmacpay")
	rec := postJSON(mux, "/v1/users/me/subscription-orders", `{"tenant_id":999,"user_id":666,"plan_id":5,"provider":"hmacpay"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("spoof body status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(svc.createOrderCalls) != 0 {
		t.Fatalf("spoof body reached CreateOrder: %+v", svc.createOrderCalls)
	}

	svc.createOrderResult = subscription.Order{
		ID:              101,
		TenantID:        7,
		UserID:          42,
		PlanID:          5,
		RechargeOrderID: 88,
		TradeNo:         "rech_t7_sub_unit",
		Status:          subscription.OrderStatusPending,
		Price:           decimal.RequireFromString("20.00000000"),
		CurrencyCode:    "USD",
		Provider:        "hmacpay",
		PlanCode:        "pro",
		PlanName:        "Pro",
		CreatedAt:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
	}
	rec = postJSON(mux, "/v1/users/me/subscription-orders", `{"plan_id":5,"provider":"hmacpay"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order status=%d body=%s want 201", rec.Code, rec.Body.String())
	}
	if len(svc.createOrderCalls) != 1 {
		t.Fatalf("CreateOrder calls=%d want 1", len(svc.createOrderCalls))
	}
	call := svc.createOrderCalls[0]
	if call.TenantID != 7 || call.UserID != 42 || call.PlanID != 5 || call.Provider != "hmacpay" {
		t.Fatalf("CreateOrder call=%+v want session tenant/user and body plan/provider", call)
	}
}

func TestCreateSubscriptionOrderRejectsUnconfiguredProvider(t *testing.T) {
	svc := &subscriptionServiceStub{}
	mux := mountUserSubscriptionRoutes(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true, "hmacpay")
	rec := postJSON(mux, "/v1/users/me/subscription-orders", `{"plan_id":5,"provider":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfigured provider status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(svc.createOrderCalls) != 0 {
		t.Fatalf("unconfigured provider reached CreateOrder: %+v", svc.createOrderCalls)
	}
}

func TestCreateSubscriptionOrderMapsPaymentLimitErrors(t *testing.T) {
	svc := &subscriptionServiceStub{createOrderErr: payment.ErrPendingLimit}
	mux := mountUserSubscriptionRoutes(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true, "hmacpay")
	rec := postJSON(mux, "/v1/users/me/subscription-orders", `{"plan_id":5,"provider":"hmacpay"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("pending limit status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
}

func TestAdminPlanRoutesScopeCRUDAndSoftDelete(t *testing.T) {
	t.Run("tenant operator cross tenant rejected before service", func(t *testing.T) {
		svc := &subscriptionServiceStub{}
		rec := invokeAdminPlans(t, adminPlanAuthStub{ident: admin.AdminIdentity{TokenID: 3, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, svc,
			http.MethodPost, "/admin/v1/subscription-plans", `{"tenant_id":8,"code":"pro","name":"Pro","price":"20.00000000","currency":"USD","duration_unit":"month","duration_value":1,"enabled":true}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross tenant status=%d body=%s want 403", rec.Code, rec.Body.String())
		}
		if len(svc.createPlanCalls) != 0 {
			t.Fatalf("cross tenant touched CreatePlan: %+v", svc.createPlanCalls)
		}
	})

	t.Run("platform admin creates plan", func(t *testing.T) {
		svc := &subscriptionServiceStub{createPlanResult: subscription.Plan{
			ID:               9,
			TenantID:         7,
			Code:             "pro",
			Name:             "Pro",
			Enabled:          true,
			Price:            decimal.RequireFromString("20.00000000"),
			CurrencyCode:     "USD",
			DurationUnit:     subscription.DurationMonth,
			DurationValue:    1,
			QuotaLimit:       1000,
			QuotaResetPeriod: subscription.ResetMonthly,
			CreatedAt:        time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC),
			UpdatedAt:        time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC),
		}}
		rec := invokeAdminPlans(t, adminPlanAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}}, svc,
			http.MethodPost, "/admin/v1/subscription-plans", `{"tenant_id":7,"code":"pro","name":"Pro","price":"20.00000000","currency":"USD","duration_unit":"month","duration_value":1,"quota_limit":1000,"quota_reset_period":"monthly","enabled":true}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create plan status=%d body=%s want 201", rec.Code, rec.Body.String())
		}
		if len(svc.createPlanCalls) != 1 || svc.createPlanCalls[0].TenantID != 7 || svc.createPlanCalls[0].Code != "pro" {
			t.Fatalf("CreatePlan calls=%+v want tenant scoped create", svc.createPlanCalls)
		}
		var body planResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if body.ID != 9 || body.Price != "20.00000000" {
			t.Fatalf("create response=%+v want plan id and fixed money", body)
		}
	})

	t.Run("delete is soft archive", func(t *testing.T) {
		svc := &subscriptionServiceStub{}
		rec := invokeAdminPlans(t, adminPlanAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}}, svc,
			http.MethodDelete, "/admin/v1/subscription-plans/9?tenant_id=7", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete status=%d body=%s want 204", rec.Code, rec.Body.String())
		}
		if len(svc.archivePlanCalls) != 1 || svc.archivePlanCalls[0].tenantID != 7 || svc.archivePlanCalls[0].planID != 9 {
			t.Fatalf("ArchivePlan calls=%+v want soft archive tenant=7 plan=9", svc.archivePlanCalls)
		}
	})

	t.Run("patch name only does not clear numeric settings", func(t *testing.T) {
		svc := &subscriptionServiceStub{updatePlanResult: subscription.Plan{
			ID:                  9,
			TenantID:            7,
			Code:                "pro",
			Name:                "Pro renamed",
			Enabled:             true,
			Price:               decimal.RequireFromString("20.00000000"),
			CurrencyCode:        "USD",
			DurationUnit:        subscription.DurationMonth,
			DurationValue:       1,
			QuotaLimit:          1000,
			MaxPurchasesPerUser: 3,
			SortOrder:           8,
			CreatedAt:           time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC),
			UpdatedAt:           time.Date(2026, 6, 2, 14, 0, 0, 0, time.UTC),
		}}
		rec := invokeAdminPlans(t, adminPlanAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}}, svc,
			http.MethodPatch, "/admin/v1/subscription-plans/9?tenant_id=7", `{"name":"Pro renamed"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		if len(svc.updatePlanCalls) != 1 {
			t.Fatalf("UpdatePlan calls=%d want 1", len(svc.updatePlanCalls))
		}
		patch := svc.updatePlanCalls[0]
		if patch.Name == nil || *patch.Name != "Pro renamed" {
			t.Fatalf("patch name=%v want Pro renamed", patch.Name)
		}
		if patch.QuotaLimit != nil || patch.MaxPurchasesPerUser != nil || patch.SortOrder != nil {
			t.Fatalf("name-only patch cleared numeric fields: quota=%v max=%v sort=%v", patch.QuotaLimit, patch.MaxPurchasesPerUser, patch.SortOrder)
		}
	})
}

func mountUserSubscriptionRoutes(t *testing.T, service SubscriptionService, ident sessionauth.SessionIdentity, passIdent bool, providers ...string) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	bindings := map[string]paymenthttp.ProviderBinding{}
	for _, provider := range providers {
		bindings[provider] = paymenthttp.ProviderBinding{Provider: noopPaymentProvider{}}
	}
	MountUserRoutes(r, Deps{Service: service, Providers: bindings})
	if !passIdent {
		return r
	}
	withSession := chi.NewRouter()
	withSession.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	withSession.Mount("/", r)
	return withSession
}

func invokeAdminPlans(t *testing.T, auth adminPlanAuthStub, service SubscriptionService, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/subscription-plans", func(r chi.Router) {
		MountAdminPlanRoutes(r, Deps{AdminAuth: auth, Service: service})
	})
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body.(string))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func postJSON(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

type adminPlanAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminPlanAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type subscriptionServiceStub struct {
	createPlanCalls  []subscription.PlanInput
	createPlanResult subscription.Plan
	createPlanErr    error

	createOrderCalls  []subscription.CreateOrderInput
	createOrderResult subscription.Order
	createOrderErr    error

	updatePlanCalls  []subscription.PlanPatch
	updatePlanResult subscription.Plan
	updatePlanErr    error

	archivePlanCalls []struct {
		tenantID int64
		planID   int64
	}
	archivePlanErr error
}

func (s *subscriptionServiceStub) CreatePlan(_ context.Context, input subscription.PlanInput) (subscription.Plan, error) {
	s.createPlanCalls = append(s.createPlanCalls, input)
	if s.createPlanErr != nil {
		return subscription.Plan{}, s.createPlanErr
	}
	return s.createPlanResult, nil
}

func (s *subscriptionServiceStub) ListPlans(context.Context, int64, bool) ([]subscription.Plan, error) {
	return nil, nil
}

func (s *subscriptionServiceStub) GetPlan(context.Context, int64, int64) (subscription.Plan, error) {
	return subscription.Plan{}, nil
}

func (s *subscriptionServiceStub) UpdatePlan(_ context.Context, patch subscription.PlanPatch) (subscription.Plan, error) {
	s.updatePlanCalls = append(s.updatePlanCalls, patch)
	if s.updatePlanErr != nil {
		return subscription.Plan{}, s.updatePlanErr
	}
	return s.updatePlanResult, nil
}

func (s *subscriptionServiceStub) ArchivePlan(_ context.Context, tenantID, planID int64) error {
	s.archivePlanCalls = append(s.archivePlanCalls, struct {
		tenantID int64
		planID   int64
	}{tenantID: tenantID, planID: planID})
	return s.archivePlanErr
}

func (s *subscriptionServiceStub) CreateOrder(_ context.Context, input subscription.CreateOrderInput) (subscription.Order, error) {
	s.createOrderCalls = append(s.createOrderCalls, input)
	if s.createOrderErr != nil {
		return subscription.Order{}, s.createOrderErr
	}
	return s.createOrderResult, nil
}

func (s *subscriptionServiceStub) ListUserSubscriptions(context.Context, subscription.ListUserSubscriptionsInput) ([]subscription.UserSubscription, error) {
	return nil, nil
}

func (s *subscriptionServiceStub) ExpireDueSubscriptions(context.Context, subscription.ExpireDueInput) (int, error) {
	return 0, nil
}

func (s *subscriptionServiceStub) ResetDueSubscriptions(context.Context, subscription.ResetDueInput) (int, error) {
	return 0, nil
}

var _ = errors.New

type noopPaymentProvider struct{}

func (noopPaymentProvider) VerifyWebhook([]byte, http.Header, string) (payment.VerifiedCallback, error) {
	return payment.VerifiedCallback{}, nil
}
