package mequotahttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type authStub struct {
	identity auth.Identity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return s.identity, nil
}

type quotaStoreStub struct {
	rowsByScope map[string][]quota.CurrentWindowRead
	err         error
	calls       []quotaStoreCall
}

type quotaStoreCall struct {
	tenantID  int64
	scopeKind quota.ScopeKind
	scopeID   string
	at        time.Time
}

func (s *quotaStoreStub) ListCurrentWindowsForScope(_ context.Context, tenantID int64, scopeKind quota.ScopeKind, scopeID string, at time.Time) ([]quota.CurrentWindowRead, error) {
	s.calls = append(s.calls, quotaStoreCall{tenantID: tenantID, scopeKind: scopeKind, scopeID: scopeID, at: at})
	if s.err != nil {
		return nil, s.err
	}
	return append([]quota.CurrentWindowRead(nil), s.rowsByScope[scopeID]...), nil
}

func TestMeQuotaProjectionMath(t *testing.T) {
	userA := auth.Identity{TenantID: 7, UserID: 40}
	userB := auth.Identity{TenantID: 7, UserID: 41}
	store := &quotaStoreStub{rowsByScope: map[string][]quota.CurrentWindowRead{
		strconv.FormatInt(userA.UserID, 10): {
			meQuotaWindow(userA.TenantID, userA.UserID, "10", "3", "2", "0.25", 12),
		},
		strconv.FormatInt(userB.UserID, 10): {
			meQuotaWindow(userB.TenantID, userB.UserID, "99", "0", "0", "0", 99),
		},
		"": {
			meQuotaWindow(userA.TenantID, userA.UserID, "10", "3", "2", "0.25", 12),
			meQuotaWindow(userB.TenantID, userB.UserID, "99", "0", "0", "0", 99),
		},
	}}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeQuota(h, "/v1/me/quota?tenant_id=999&user_id=41&scope_id=41")

	assertMeQuotaStatus(t, rec, http.StatusOK)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode quota response: %v body=%s", err, rec.Body.String())
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len=%d want caller's one quota window; body=%s", len(body.Items), rec.Body.String())
	}
	item := body.Items[0]
	for _, field := range []string{"cap", "consumed", "remaining", "overage", "request_count", "window_kind", "window_start", "window_end"} {
		if _, ok := item[field]; !ok {
			t.Fatalf("quota item missing %q: %#v", field, item)
		}
	}
	// MUTATION: remaining = Limit.Add(consumed) produces 15 instead of 5.
	assertStringField(t, item, "cap", "10")
	assertStringField(t, item, "consumed", "5")
	assertStringField(t, item, "remaining", "5")
	assertStringField(t, item, "overage", "0.25")
	if got := item["request_count"]; got != float64(12) {
		t.Fatalf("request_count=%v want 12", got)
	}
	if strings.Contains(rec.Body.String(), `"99"`) {
		t.Fatalf("quota response leaked user B's window: %s", rec.Body.String())
	}
	if len(store.calls) != 1 {
		t.Fatalf("store calls=%d want 1", len(store.calls))
	}
	call := store.calls[0]
	if call.tenantID != userA.TenantID || call.scopeKind != quota.ScopeUser || call.scopeID != strconv.FormatInt(userA.UserID, 10) {
		t.Fatalf("store scope = tenant:%d kind:%s id:%q want tenant:%d kind:user id:%d",
			call.tenantID, call.scopeKind, call.scopeID, userA.TenantID, userA.UserID)
	}
	if !call.at.Equal(call.at.UTC()) {
		t.Fatalf("store timestamp must be UTC, got %s", call.at.Location())
	}

	unauthStore := &quotaStoreStub{rowsByScope: store.rowsByScope}
	unauth := NewHandler(Deps{Auth: authStub{err: auth.ErrUnauthorized}, Store: unauthStore})
	unauthRec := invokeMeQuota(unauth, "/v1/me/quota")
	assertMeQuotaStatus(t, unauthRec, http.StatusUnauthorized)
	if len(unauthStore.calls) != 0 {
		t.Fatalf("unauthenticated request reached store, calls=%d", len(unauthStore.calls))
	}
}

func TestMeQuotaStoreErrorIsServiceUnavailable(t *testing.T) {
	store := &quotaStoreStub{err: errors.New("quota read unavailable")}
	h := NewHandler(Deps{Auth: authStub{identity: auth.Identity{TenantID: 7, UserID: 40}}, Store: store})

	rec := invokeMeQuota(h, "/v1/me/quota")

	assertMeQuotaStatus(t, rec, http.StatusServiceUnavailable)
}

func meQuotaWindow(tenantID, userID int64, limit, reserved, settled, overage string, requests int64) quota.CurrentWindowRead {
	start := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	return quota.CurrentWindowRead{
		TenantID: tenantID,
		Scope:    quota.Scope{TenantID: tenantID, Kind: quota.ScopeUser, ID: strconv.FormatInt(userID, 10)},
		Metric:   quota.MetricCostUSD,
		Window: quota.Window{
			Kind:  quota.WindowCalendarDay,
			Start: start,
			End:   start.Add(24 * time.Hour),
		},
		LimitValue:    mustDecimal(limit),
		ReservedValue: mustDecimal(reserved),
		SettledValue:  mustDecimal(settled),
		OverageValue:  mustDecimal(overage),
		RequestCount:  requests,
	}
}

func mustDecimal(raw string) decimal.Decimal {
	value, err := decimal.NewFromString(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func invokeMeQuota(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func assertMeQuotaStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func assertStringField(t *testing.T, item map[string]any, key, want string) {
	t.Helper()
	got, ok := item[key].(string)
	if !ok || got != want {
		t.Fatalf("%s=%v want %q in %#v", key, item[key], want, item)
	}
}
