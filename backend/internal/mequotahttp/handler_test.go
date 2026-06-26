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
	metrics   []quota.Metric
}

func (s *quotaStoreStub) ListCurrentWindowsForScopeMetrics(_ context.Context, tenantID int64, scopeKind quota.ScopeKind, scopeID string, at time.Time, metrics []quota.Metric) ([]quota.CurrentWindowRead, error) {
	s.calls = append(s.calls, quotaStoreCall{tenantID: tenantID, scopeKind: scopeKind, scopeID: scopeID, at: at, metrics: metrics})
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
	// 变异:remaining = Limit.Add(consumed) 会得到 15 而非 5。
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

// TestMeQuotaMultiMetricWindows 守护 F-OPS-001 多 metric 接口面:handler
// 必须请求三个窗口形态的 metric(requests/cost_usd/tokens_estimated),
// 绝不请求并发(基于槽位、已推迟),并把每个窗口的 metric + 维度值投影到
// 响应中。fixture 有区分度 —— 每个 metric 的 consumed 值各不相同,因此
// 丢弃 metric 投影或塌缩成只剩 cost 都会转红。
// 变异:从 windowView 去掉 `Metric` -> byMetric 的 key 变成 "" -> 查找失败
//       -> 转红;向 store 传入错误的 metrics -> metrics 断言 -> 转红。
func TestMeQuotaMultiMetricWindows(t *testing.T) {
	user := auth.Identity{TenantID: 7, UserID: 40}
	scopeID := strconv.FormatInt(user.UserID, 10)
	store := &quotaStoreStub{rowsByScope: map[string][]quota.CurrentWindowRead{
		scopeID: {
			meQuotaWindowMetric(user.TenantID, user.UserID, quota.MetricRequests, "1000", "0", "120", "0", 120),
			meQuotaWindowMetric(user.TenantID, user.UserID, quota.MetricCostUSD, "10", "0", "3.50", "0", 120),
			meQuotaWindowMetric(user.TenantID, user.UserID, quota.MetricTokensEstimated, "1000000", "0", "45000", "0", 120),
		},
	}}
	h := NewHandler(Deps{Auth: authStub{identity: user}, Store: store})

	rec := invokeMeQuota(h, "/v1/me/quota")
	assertMeQuotaStatus(t, rec, http.StatusOK)

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(body.Items) != 3 {
		t.Fatalf("items=%d want 3 (requests/cost/tokens); body=%s", len(body.Items), rec.Body.String())
	}
	byMetric := map[string]map[string]any{}
	for _, it := range body.Items {
		m, _ := it["metric"].(string)
		byMetric[m] = it
	}
	// 每个窗口都带自己的 metric 标记,每个维度有各自不同的 consumed/cap。
	assertStringField(t, byMetric["requests"], "consumed", "120")
	assertStringField(t, byMetric["requests"], "cap", "1000")
	assertStringField(t, byMetric["cost_usd"], "consumed", "3.5")
	assertStringField(t, byMetric["tokens_estimated"], "consumed", "45000")
	assertStringField(t, byMetric["tokens_estimated"], "cap", "1000000")

	// handler 恰好请求那三个窗口形态的 metric,绝不请求并发。
	if len(store.calls) != 1 {
		t.Fatalf("store calls=%d want 1", len(store.calls))
	}
	got := store.calls[0].metrics
	want := []quota.Metric{quota.MetricRequests, quota.MetricCostUSD, quota.MetricTokensEstimated}
	if len(got) != len(want) {
		t.Fatalf("requested metrics=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requested metrics[%d]=%s want %s", i, got[i], want[i])
		}
	}
	for _, m := range got {
		if m == quota.MetricConcurrency {
			t.Fatalf("concurrency must not be requested (slot-based, deferred)")
		}
	}
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

func meQuotaWindowMetric(tenantID, userID int64, metric quota.Metric, limit, reserved, settled, overage string, requests int64) quota.CurrentWindowRead {
	w := meQuotaWindow(tenantID, userID, limit, reserved, settled, overage, requests)
	w.Metric = metric
	return w
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
