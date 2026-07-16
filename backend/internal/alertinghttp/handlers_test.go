package alertinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
	"github.com/BloomingProsperity/HUAKAI/internal/alertmetrics"
)

func TestAlertRuleAdminCRUD(t *testing.T) {
	// 变异:create/update/delete/list 没有接到同一个按租户限定的服务上;禁用更新或删除后列表的断言失败。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := alerting.NewService(alerting.NewMemoryStore(), alerting.WithClock(func() time.Time { return now }))
	auth := fakeAdminAuth{identity: admintest.Platform(99)}

	create := `{"tenant_id":7,"name":"request spike","metric":"gateway.requests","metric_type":"cpu_usage_percent","comparator":"gte","threshold":100,"severity":"critical","window_seconds":60,"sustained_seconds":120,"cooldown_seconds":300,"notify_email":true,"filters":{"model":"x"}}`
	rec := serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-rules", []byte(create))
	assertStatus(t, rec, http.StatusCreated)
	var created ruleResponse
	decodeJSON(t, rec, &created)
	if created.ID <= 0 || created.TenantID != 7 || created.Name != "request spike" || !created.Enabled {
		t.Fatalf("created=%+v want persisted enabled rule", created)
	}
	if created.MetricType != "cpu_usage_percent" || created.SustainedSeconds != 120 ||
		created.CooldownSeconds != 300 || !created.NotifyEmail || created.Filters["model"] != "x" {
		t.Fatalf("created enrichment=%+v want metric_type/sustained/cooldown/notify_email/filters", created)
	}

	rec = serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-rules?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var list ruleListResponse
	decodeJSON(t, rec, &list)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list=%+v want created rule id %d", list.Items, created.ID)
	}

	disabled := `{"enabled":false,"threshold":250,"filters":{"model":"y"}}`
	rec = serveAlerting(t, svc, auth, http.MethodPut, "/v1/admin/alert-rules/"+strconv.FormatInt(created.ID, 10)+"?tenant_id=7", []byte(disabled))
	assertStatus(t, rec, http.StatusOK)
	var updated ruleResponse
	decodeJSON(t, rec, &updated)
	if updated.Enabled || updated.Threshold != 250 || updated.Name != "request spike" || updated.Filters["model"] != "y" {
		t.Fatalf("updated=%+v want disabled threshold 250 with name preserved and filters updated", updated)
	}

	rec = serveAlerting(t, svc, auth, http.MethodDelete, "/v1/admin/alert-rules/"+strconv.FormatInt(created.ID, 10)+"?tenant_id=8", nil)
	assertStatus(t, rec, http.StatusNotFound)
	rec = serveAlerting(t, svc, auth, http.MethodDelete, "/v1/admin/alert-rules/"+strconv.FormatInt(created.ID, 10)+"?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusNoContent)

	rec = serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-rules?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	decodeJSON(t, rec, &list)
	if len(list.Items) != 0 {
		t.Fatalf("list after delete=%+v want empty", list.Items)
	}
}

func TestAlertRuleAdminValidation(t *testing.T) {
	// 变异:绕过 HTTP/service 校验;非法的 comparator 或 severity 返回 201 而非 400。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := alerting.NewService(alerting.NewMemoryStore(), alerting.WithClock(func() time.Time { return now }))
	auth := fakeAdminAuth{identity: admintest.Platform(99)}

	tests := []struct {
		name string
		body string
	}{
		{name: "bad comparator", body: `{"tenant_id":7,"name":"bad","metric":"gateway.requests","comparator":"eq","threshold":100,"severity":"critical","window_seconds":60}`},
		{name: "bad severity", body: `{"tenant_id":7,"name":"bad","metric":"gateway.requests","comparator":"gte","threshold":100,"severity":"emergency","window_seconds":60}`},
		{name: "bad window", body: `{"tenant_id":7,"name":"bad","metric":"gateway.requests","comparator":"gte","threshold":100,"severity":"critical","window_seconds":0}`},
		{name: "window too large", body: `{"tenant_id":7,"name":"bad","metric":"gateway.requests","comparator":"gte","threshold":100,"severity":"critical","window_seconds":86401}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-rules", []byte(tt.body))
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestAlertMetricCatalogRequiresAdminAndReturnsProductionMetrics(t *testing.T) {
	// 变异：绕过相邻 admin 鉴权会让 401/403 请求放行；漏接目录或手写错误键/错标签
	// 会让成功响应中的两个生产指标断言变红。
	svc := alerting.NewService(alerting.NewMemoryStore())
	for _, authCase := range []struct {
		err    error
		status int
	}{
		{err: admin.ErrAdminUnauthorized, status: http.StatusUnauthorized},
		{err: admin.ErrAdminForbidden, status: http.StatusForbidden},
	} {
		rec := serveAlerting(t, svc, fakeAdminAuth{err: authCase.err}, http.MethodGet, "/v1/admin/alert-rules/metric-catalog", nil)
		assertStatus(t, rec, authCase.status)
	}

	auth := fakeAdminAuth{identity: admintest.Platform(99)}
	rec := serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-rules/metric-catalog", nil)
	assertStatus(t, rec, http.StatusOK)
	var entries []alertmetrics.CatalogEntry
	decodeJSON(t, rec, &entries)
	byName := make(map[string]alertmetrics.CatalogEntry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	wantLabels := map[string]string{
		"usage.request_count":     "请求总数",
		"account.unhealthy_count": "异常账号总数",
	}
	for name, wantLabel := range wantLabels {
		entry, ok := byName[name]
		if !ok {
			t.Fatalf("目录缺生产指标 %q：%+v", name, entries)
		}
		if entry.Label != wantLabel {
			t.Fatalf("目录指标 %q label=%q，want %q", name, entry.Label, wantLabel)
		}
	}
	if prefix := byName["account.unhealthy_"]; !prefix.IsPrefix || prefix.Label == "" {
		t.Fatalf("健康状态前缀目录项错误：%+v", prefix)
	}
}

func TestAlertEventsAndSilencesAdmin(t *testing.T) {
	// 变异:忽略 rule_id/state 事件过滤,或忽略 silence 删除时的 tenant 谓词;过滤后的列表或删除后的 silence 列表会出错。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := alerting.NewService(alerting.NewMemoryStore(), alerting.WithClock(func() time.Time { return now }))
	auth := fakeAdminAuth{identity: admintest.Platform(99)}
	rule := mustCreateHTTPRule(t, svc, alerting.CreateRuleInput{
		TenantID:      7,
		Name:          "request spike",
		Metric:        "gateway.requests",
		Comparator:    alerting.ComparatorGTE,
		Threshold:     100,
		Severity:      alerting.SeverityCritical,
		WindowSeconds: 60,
	})
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 150}); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}

	rec := serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-events?tenant_id=7&rule_id="+strconv.FormatInt(rule.ID, 10)+"&state=firing", nil)
	assertStatus(t, rec, http.StatusOK)
	var events eventListResponse
	decodeJSON(t, rec, &events)
	if len(events.Items) != 1 || events.Items[0].RuleID != rule.ID || events.Items[0].State != "firing" {
		t.Fatalf("events=%+v want one firing event for rule", events.Items)
	}
	if events.Items[0].ThresholdValue == nil || *events.Items[0].ThresholdValue != 100 ||
		events.Items[0].MetricValue == nil || *events.Items[0].MetricValue != 150 {
		t.Fatalf("event enrichment=%+v want threshold 100 metric 150", events.Items[0])
	}
	rec = serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-events/"+strconv.FormatInt(events.Items[0].ID, 10)+"/manual-resolve?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var manual eventResponse
	decodeJSON(t, rec, &manual)
	if manual.State != "manual_resolved" || manual.ResolvedAt == nil {
		t.Fatalf("manual event=%+v want manual_resolved with resolved_at", manual)
	}
	rec = serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-events?tenant_id=7&state=resolved", nil)
	assertStatus(t, rec, http.StatusOK)
	decodeJSON(t, rec, &events)
	if len(events.Items) != 0 {
		t.Fatalf("resolved events=%+v want none", events.Items)
	}

	silenceBody := `{"tenant_id":7,"rule_id":` + strconv.FormatInt(rule.ID, 10) + `,"reason":"maintenance","starts_at":"2026-06-06T11:59:00Z","ends_at":"2026-06-06T12:30:00Z","platform":"p1","group_id":"g1","region":"us"}`
	rec = serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-silences", []byte(silenceBody))
	assertStatus(t, rec, http.StatusCreated)
	var silence silenceResponse
	decodeJSON(t, rec, &silence)
	if silence.ID <= 0 || silence.RuleID == nil || *silence.RuleID != rule.ID ||
		silence.Platform != "p1" || silence.GroupID != "g1" || silence.Region != "us" {
		t.Fatalf("silence=%+v want rule-specific scoped silence", silence)
	}

	rec = serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-silences?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var silences silenceListResponse
	decodeJSON(t, rec, &silences)
	if len(silences.Items) != 1 || silences.Items[0].ID != silence.ID {
		t.Fatalf("silences=%+v want created silence", silences.Items)
	}
	rec = serveAlerting(t, svc, auth, http.MethodDelete, "/v1/admin/alert-silences/"+strconv.FormatInt(silence.ID, 10)+"?tenant_id=8", nil)
	assertStatus(t, rec, http.StatusNotFound)
	rec = serveAlerting(t, svc, auth, http.MethodDelete, "/v1/admin/alert-silences/"+strconv.FormatInt(silence.ID, 10)+"?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusNoContent)
}

func TestAlertAdminTenantScope(t *testing.T) {
	// 变异:不结合 admin 身份的 scope 就信任 body/query 里的 tenant_id;租户 7 的 tenant_operator 能创建租户 8 的规则。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := alerting.NewService(alerting.NewMemoryStore(), alerting.WithClock(func() time.Time { return now }))
	auth := fakeAdminAuth{identity: admintest.TenantOperator(99, 7)}

	createWrongTenant := `{"tenant_id":8,"name":"bad","metric":"gateway.requests","comparator":"gte","threshold":100,"severity":"critical","window_seconds":60}`
	rec := serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-rules", []byte(createWrongTenant))
	assertStatus(t, rec, http.StatusForbidden)

	createScoped := `{"name":"scoped","metric":"gateway.requests","comparator":"gte","threshold":100,"severity":"critical","window_seconds":60}`
	rec = serveAlerting(t, svc, auth, http.MethodPost, "/v1/admin/alert-rules", []byte(createScoped))
	assertStatus(t, rec, http.StatusCreated)
	var created ruleResponse
	decodeJSON(t, rec, &created)
	if created.TenantID != 7 {
		t.Fatalf("created tenant=%d want operator scope tenant 7", created.TenantID)
	}

	rec = serveAlerting(t, svc, auth, http.MethodGet, "/v1/admin/alert-rules", nil)
	assertStatus(t, rec, http.StatusOK)
	var list ruleListResponse
	decodeJSON(t, rec, &list)
	if len(list.Items) != 1 || list.Items[0].TenantID != 7 {
		t.Fatalf("list=%+v want tenant operator scoped row", list.Items)
	}
}

type fakeAdminAuth struct {
	identity admin.AdminIdentity
	err      error
}

func (a fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.identity, nil
}

func serveAlerting(t *testing.T, svc *alerting.Service, auth AdminAuth, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	MountAdminRoutes(router, AdminDeps{Auth: auth, Service: svc})
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func mustCreateHTTPRule(t *testing.T, svc *alerting.Service, in alerting.CreateRuleInput) alerting.AlertRule {
	t.Helper()
	rule, err := svc.CreateRule(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateRule %q: %v", in.Name, err)
	}
	return rule
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), want)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode json: %v body=%s", err, rec.Body.String())
	}
}
