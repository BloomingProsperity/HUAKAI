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
	setQuotaCalls int
	setGroupCalls int
	getGroupErr   error
}

func (s *stubService) SetKeyQuota(_ context.Context, req userkeycontrols.SetKeyQuotaRequest) (userkeycontrols.SetKeyQuotaResult, error) {
	s.setQuotaCalls++
	return userkeycontrols.SetKeyQuotaResult{
		APIKeyID:   req.APIKeyID,
		PolicyID:   777,
		LimitUSD:   req.LimitUSD,
		ScopeKind:  quota.ScopeAPIKey,
		ScopeID:    "123",
		Metric:     quota.MetricCostUSD,
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
