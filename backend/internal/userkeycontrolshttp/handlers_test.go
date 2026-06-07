package userkeycontrolshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
)

func TestPutQuota_MissingSession_401(t *testing.T) {
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/quota", `{"limit_usd":"1.00"}`, false)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rr.Code, rr.Body.String())
	}
	if service.setQuotaCalls != 0 {
		t.Fatalf("service must not be called without session")
	}
}

func TestPutQuota_NegativeLimitUSD_400(t *testing.T) {
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/quota", `{"limit_usd":"-1"}`, true)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rr.Code, rr.Body.String())
	}
	if service.setQuotaCalls != 0 {
		t.Fatalf("negative limit must not reach service")
	}
}

func TestPutQuota_RequestCountMetricPropagates(t *testing.T) {
	// Mutation check: ignore the JSON metric field and the captured service
	// request stays cost_usd/empty instead of MetricRequests.
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/quota",
		`{"limit_usd":"2","metric":"request-count","window_kind":"fixed","window_seconds":60}`, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rr.Code, rr.Body.String())
	}
	if service.setQuotaCalls != 1 {
		t.Fatalf("SetKeyQuota calls=%d want 1", service.setQuotaCalls)
	}
	got := service.lastQuotaReq
	if got.Metric != quota.MetricRequests {
		t.Fatalf("Metric=%q want requests", got.Metric)
	}
	if got.WindowKind != quota.WindowFixed || got.WindowSeconds != 60 {
		t.Fatalf("window=%q/%d want fixed/60", got.WindowKind, got.WindowSeconds)
	}
}

func TestPutGroup_InvalidGroupID_400(t *testing.T) {
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/group", `{"group_id":-1}`, true)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rr.Code, rr.Body.String())
	}
	if service.setGroupCalls != 0 {
		t.Fatalf("invalid group_id must not reach service")
	}
}

func TestPutModelAllowlist_UsesSessionScopeAndBodyList(t *testing.T) {
	// Mutation check: route not mounted, scope sourced from body, or body list
	// ignored all make the status/captured request assertions go red.
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/model-allowlist",
		`{"allowed_models":["gpt-4o","claude-3"]}`, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rr.Code, rr.Body.String())
	}
	if service.setModelAllowlistCalls != 1 {
		t.Fatalf("SetKeyModelAllowlist calls=%d want 1", service.setModelAllowlistCalls)
	}
	got := service.lastModelAllowlistReq
	if got.TenantID != 11 || got.UserID != 22 || got.APIKeyID != 123 {
		t.Fatalf("scope=%+v want tenant=11 user=22 api_key=123", got)
	}
	if strings.Join(got.AllowedModels, ",") != "gpt-4o,claude-3" {
		t.Fatalf("AllowedModels=%+v want request body list", got.AllowedModels)
	}
	if !strings.Contains(rr.Body.String(), `"allowed_models"`) {
		t.Fatalf("body=%s must include allowed_models", rr.Body.String())
	}
}

func TestGetModelAllowlist_UsesSessionScope(t *testing.T) {
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodGet, "/123/model-allowlist", ``, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rr.Code, rr.Body.String())
	}
	if service.getModelAllowlistCalls != 1 {
		t.Fatalf("GetKeyModelAllowlist calls=%d want 1", service.getModelAllowlistCalls)
	}
	if !strings.Contains(rr.Body.String(), `"allowed_models"`) {
		t.Fatalf("body=%s must include allowed_models", rr.Body.String())
	}
}

func TestPutIPAllowlist_UsesSessionScopeAndBodyList(t *testing.T) {
	// Mutation check: take tenant/user/api_key from the body or skip the new
	// route and the captured request/status assertions go red.
	service := &stubService{}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/ip-allowlist",
		`{"ip_allowlist":["10.0.0.0/8","203.0.113.7"]}`, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rr.Code, rr.Body.String())
	}
	if service.setIPAllowlistCalls != 1 {
		t.Fatalf("SetKeyIPAllowlist calls=%d want 1", service.setIPAllowlistCalls)
	}
	got := service.lastIPAllowlistReq
	if got.TenantID != 11 || got.UserID != 22 || got.APIKeyID != 123 {
		t.Fatalf("scope=%+v want tenant=11 user=22 api_key=123", got)
	}
	if strings.Join(got.IPAllowlist, ",") != "10.0.0.0/8,203.0.113.7" {
		t.Fatalf("IPAllowlist=%+v want request body list", got.IPAllowlist)
	}
	if !strings.Contains(rr.Body.String(), `"ip_allowlist"`) {
		t.Fatalf("body=%s must include ip_allowlist", rr.Body.String())
	}
}

func TestPutIPAllowlist_InvalidEntry_400(t *testing.T) {
	service := &stubService{setIPAllowlistErr: userkeycontrols.ErrInvalidIPAllowlist}
	rr := serveControls(t, Deps{Service: service}, http.MethodPut, "/123/ip-allowlist",
		`{"ip_allowlist":["not-an-ip"]}`, true)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_ip_allowlist") {
		t.Fatalf("body=%s want invalid_ip_allowlist code", rr.Body.String())
	}
}

func TestGetQuota_ServiceNil_503(t *testing.T) {
	rr := serveControls(t, Deps{}, http.MethodGet, "/123/quota", ``, true)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetGroup_UnknownKey_404(t *testing.T) {
	service := &stubService{getGroupErr: userkeycontrols.ErrKeyNotFound}
	rr := serveControls(t, Deps{Service: service}, http.MethodGet, "/123/group", ``, true)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "api_key_not_found") {
		t.Fatalf("body must expose stable not-found code; got %s", rr.Body.String())
	}
}

func serveControls(t *testing.T, deps Deps, method, target, body string, withSession bool) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	MountRoutes(r, deps)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if withSession {
		ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 11, UserID: 22})
		req = req.WithContext(ctx)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

type stubService struct {
	setQuotaCalls          int
	setGroupCalls          int
	setIPAllowlistCalls    int
	setModelAllowlistCalls int
	getModelAllowlistCalls int
	lastQuotaReq           userkeycontrols.SetKeyQuotaRequest
	lastIPAllowlistReq     userkeycontrols.SetKeyIPAllowlistRequest
	lastModelAllowlistReq  userkeycontrols.SetKeyModelAllowlistRequest
	getGroupErr            error
	setIPAllowlistErr      error
	getIPAllowlistErr      error
	getModelAllowlistErr   error
}

func (s *stubService) SetKeyQuota(_ context.Context, req userkeycontrols.SetKeyQuotaRequest) (userkeycontrols.SetKeyQuotaResult, error) {
	s.setQuotaCalls++
	s.lastQuotaReq = req
	metric := req.Metric
	if metric == "" {
		metric = quota.MetricCostUSD
	}
	return userkeycontrols.SetKeyQuotaResult{
		APIKeyID:   req.APIKeyID,
		PolicyID:   777,
		LimitUSD:   req.LimitUSD,
		ScopeKind:  quota.ScopeAPIKey,
		ScopeID:    "123",
		Metric:     metric,
		WindowKind: quota.WindowCalendarDay,
		Mode:       quota.ModeEnforce,
	}, nil
}

func (s *stubService) GetKeyQuota(context.Context, int64, int64, int64) (userkeycontrols.KeyQuotaView, error) {
	return userkeycontrols.KeyQuotaView{
		APIKeyID:   123,
		PolicyID:   777,
		LimitUSD:   decimal.RequireFromString("1.00000000"),
		ScopeKind:  quota.ScopeAPIKey,
		ScopeID:    "123",
		Metric:     quota.MetricCostUSD,
		WindowKind: quota.WindowCalendarDay,
		Mode:       quota.ModeEnforce,
	}, nil
}

func (s *stubService) SetKeyGroup(_ context.Context, req userkeycontrols.SetKeyGroupRequest) (userkeycontrols.SetKeyGroupResult, error) {
	s.setGroupCalls++
	return userkeycontrols.SetKeyGroupResult{APIKeyID: req.APIKeyID, GroupID: req.GroupID}, nil
}

func (s *stubService) GetKeyGroup(context.Context, int64, int64, int64) (userkeycontrols.KeyGroupView, error) {
	if s.getGroupErr != nil {
		return userkeycontrols.KeyGroupView{}, s.getGroupErr
	}
	return userkeycontrols.KeyGroupView{APIKeyID: 123}, nil
}

func (s *stubService) SetKeyIPAllowlist(_ context.Context, req userkeycontrols.SetKeyIPAllowlistRequest) (userkeycontrols.SetKeyIPAllowlistResult, error) {
	s.setIPAllowlistCalls++
	s.lastIPAllowlistReq = req
	if s.setIPAllowlistErr != nil {
		return userkeycontrols.SetKeyIPAllowlistResult{}, s.setIPAllowlistErr
	}
	return userkeycontrols.SetKeyIPAllowlistResult{APIKeyID: req.APIKeyID, IPAllowlist: req.IPAllowlist}, nil
}

func (s *stubService) GetKeyIPAllowlist(context.Context, int64, int64, int64) (userkeycontrols.KeyIPAllowlistView, error) {
	if s.getIPAllowlistErr != nil {
		return userkeycontrols.KeyIPAllowlistView{}, s.getIPAllowlistErr
	}
	return userkeycontrols.KeyIPAllowlistView{APIKeyID: 123, IPAllowlist: []string{"10.0.0.0/8"}}, nil
}

func (s *stubService) SetKeyModelAllowlist(_ context.Context, req userkeycontrols.SetKeyModelAllowlistRequest) (userkeycontrols.SetKeyModelAllowlistResult, error) {
	s.setModelAllowlistCalls++
	s.lastModelAllowlistReq = req
	return userkeycontrols.SetKeyModelAllowlistResult{APIKeyID: req.APIKeyID, AllowedModels: req.AllowedModels}, nil
}

func (s *stubService) GetKeyModelAllowlist(context.Context, int64, int64, int64) (userkeycontrols.KeyModelAllowlistView, error) {
	s.getModelAllowlistCalls++
	if s.getModelAllowlistErr != nil {
		return userkeycontrols.KeyModelAllowlistView{}, s.getModelAllowlistErr
	}
	return userkeycontrols.KeyModelAllowlistView{APIKeyID: 123, AllowedModels: []string{"gpt-4o"}}, nil
}
