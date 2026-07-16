// openapi_consistency_test 覆盖 P2.3：docs/openapi/openapi.yaml 与
// cmd/gateway 实际 chi 路由的一致性检查。
//
// 验证目标：
//   - spec 中声明的 path 都能在 main.go mountRoutes 后真实命中
//   - main.go 注册但 spec 未声明的 path 必须列出（用于触发文档补救）
//
// 与 main.go 同 package main：直接调用 mountRoutes(nil-deps)，依赖：
//   - mountRoutes 自身只用 chi 注册，不读 deps 字段
//   - handler 内部对 nil-deps 的处理留给运行期；本测试只验 path 集合
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
)

// 构造与 main.go run() 路径等价的 chi 路由树（仅 path 维度，handler
// body 不会被执行）。这要求 mountRoutes 在 nil-deps 下也能完成注册。
func buildTestRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	logger := zap.NewNop()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("build Hermes test key: %v", err)
	}
	hermesRunner, err := hermes.NewRunnerClient(hermes.RunnerConfig{
		RunnerURL:     "http://runner.local",
		JWTPrivateKey: privateKey,
		JWTKID:        "kid-test",
	})
	if err != nil {
		t.Fatalf("build Hermes test runner: %v", err)
	}
	// mountRoutes 在注册期会 deref d.cfg.BillingPolicyVersion / RequestClass，
	// 因此 deps.cfg 必须非 nil；其它字段保持零值（handler 本身不会 invoke）。
	d := &deps{
		cfg: &config.Config{
			BillingPolicyVersion: "test-1.0",
			RequestClass:         "standard",
		},
		hermesService: hermes.NewService(nil),
		hermesRunner:  hermesRunner,
	}
	mountRoutes(r, d, logger)
	return r
}

// 主一致性测试：用 openapicheck.Compare 算 spec ↔ impl 漂移。
// 期望：spec_only 子集为空（或仅有已知占位）；impl_only 也必须为空。
// 如果 main.go 暴露了新路由，必须同步补 OpenAPI，否则本测试 fail。
func TestOpenAPI_ImplementationConsistency(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specPaths, err := openapicheck.ParseSpecPaths(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI spec %s: %v", specAbs, err)
	}
	if len(specPaths) == 0 {
		t.Fatalf("spec 解析出 0 path — parser 漏了 paths: 块？")
	}

	r := buildTestRouter(t)
	implPaths := openapicheck.WalkChiPaths(r)
	if len(implPaths) == 0 {
		t.Fatalf("impl 走出 0 path — mountRoutes 未注册任何 chi 路由")
	}

	rep := openapicheck.Compare(specPaths, implPaths)
	t.Logf("openapi-check report:\n%s", openapicheck.FormatReport(rep))

	// 主断言：spec 声明的每条 path 都必须有 impl 兜底。
	// 这一条 fail 意味着前端 / 第三方按 OpenAPI 调用会撞 404。
	// 软白名单（KnownSpecOnly）允许列出"spec 已声明但 impl 暂未落地"
	// 的占位条目；目前为空。
	knownSpecOnly := map[string]struct{}{}
	residualSpecOnly := make([]string, 0, len(rep.SpecOnly))
	for _, p := range rep.SpecOnly {
		if _, ok := knownSpecOnly[p]; !ok {
			residualSpecOnly = append(residualSpecOnly, p)
		}
	}
	if len(residualSpecOnly) > 0 {
		t.Errorf("OpenAPI 声明但 main.go 未注册的 %d 条 path（白名单后剩余）：%v",
			len(residualSpecOnly), residualSpecOnly)
	}

	// impl-only 是硬失败，避免实现已暴露但契约假绿。
	knownImplOnly := map[string]struct{}{}
	residualImplOnly := make([]string, 0, len(rep.ImplOnly))
	for _, p := range rep.ImplOnly {
		if _, ok := knownImplOnly[p]; !ok {
			residualImplOnly = append(residualImplOnly, p)
		}
	}
	if len(residualImplOnly) > 0 {
		t.Errorf("main.go 已注册但 OpenAPI 未声明的 %d 条 path（白名单后剩余）：%v",
			len(residualImplOnly), residualImplOnly)
	}
}

// TestOpenAPI_SecurityContractMatchesImpl 校验 security 维度的契约一致性:OpenAPI 标 security:[]
// (公开)的操作,实现里不得挂会话认证中间件。否则前端/第三方按 spec 当公开调用会撞 401,且这类漂移
// 是 IDOR 类回归(实现加了租户/会话校验却忘同步 spec)的征兆——本次正是审计 IDOR 修复后 receipts
// verify 的 spec 漏改(impl 已挂 SessionMiddleware 守退款队列跨租户,spec 仍标公开)被此检查抓出。
// 本检查只覆盖"spec 公开但 impl 认证"方向(反向因 admin 等在 handler 内鉴权、无中间件,无法纯靠
// 中间件内省判定,纳入会误报)。两处 len==0 守卫确保 parser/内省失效时不会"空集假绿"。
func TestOpenAPI_SecurityContractMatchesImpl(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specPublic, err := openapicheck.ParseSpecPublicOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI 公开操作: %v", err)
	}
	if len(specPublic) == 0 {
		t.Fatalf("spec 解析出 0 条 security:[] 公开操作 — parser 可能漏了 security 行")
	}

	r := buildTestRouter(t)
	// "SessionMiddleware" 是会话认证中间件(internal/auth)的函数名标记。
	gated := openapicheck.OperationsGatedByMiddleware(r, "SessionMiddleware")
	if len(gated) == 0 {
		t.Fatalf("impl 走出 0 条会话认证路由 — 中间件内省失效")
	}

	drift := openapicheck.SecurityContractDrift(specPublic, gated)
	if len(drift) > 0 {
		msgs := make([]string, 0, len(drift))
		for _, op := range drift {
			msgs = append(msgs, op.Method+" "+op.Path)
		}
		t.Errorf("OpenAPI 标 security:[](公开)但实现挂了会话认证的 %d 条操作(契约漂移,客户端会撞 401):%v",
			len(drift), msgs)
	}
}

func TestOpenAPI_ChatCompletionsMethodMatchesRuntimePOST(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	const chatPath = "/v1/chat/completions"
	if !hasOperation(implOps, http.MethodPost, chatPath) {
		t.Fatalf("runtime missing POST %s; test premise no longer matches routes.go", chatPath)
	}
	if hasOperation(implOps, http.MethodGet, chatPath) {
		t.Fatalf("runtime unexpectedly serves GET %s; S1-027 premise no longer matches routes.go", chatPath)
	}
	if !hasOperation(specOps, http.MethodPost, chatPath) {
		t.Fatalf("OpenAPI must declare POST %s so generated clients call the mounted runtime route", chatPath)
	}
	if hasOperation(specOps, http.MethodGet, chatPath) {
		t.Fatalf("OpenAPI must not declare GET %s; chi mounts only POST for the public chat route", chatPath)
	}
}

func TestCodexResponsesIngressRouteMounted(t *testing.T) {
	// 变异:省略 r.Post("/backend-api/codex/responses", ...);chi 会对 Codex CLI
	// 返回 404,而非到达共享的 Responses handler。
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses",
		strings.NewReader(`{"model":"gpt-4o","stream":false,"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("POST /backend-api/codex/responses returned 404; route must be mounted for Codex CLI")
	}
}

func TestOpenAPI_ModuleFReadOnlyRoutesMountedAndDocumented(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	if !hasOperation(implOps, http.MethodGet, "/v1/me/voucher-redemptions") {
		t.Fatalf("runtime missing GET /v1/me/voucher-redemptions")
	}
	if hasOperation(implOps, http.MethodPost, "/v1/me/voucher-redemptions") {
		t.Fatalf("runtime must not expose mutation method on /v1/me/voucher-redemptions")
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/me/voucher-redemptions"},
		{http.MethodGet, "/v1/admin/subscriptions/assignments"},
		{http.MethodGet, "/v1/users/me/payments/config"},
	} {
		if !hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI missing %s %s", op.method, op.path)
		}
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/me/voucher-redemptions"},
		{http.MethodPatch, "/v1/me/voucher-redemptions"},
		{http.MethodDelete, "/v1/me/voucher-redemptions"},
	} {
		if hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI unexpectedly declares mutation %s %s", op.method, op.path)
		}
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"listMyVoucherRedemptions",
		"VoucherRedemptionHistoryItem:",
		"preset_amount_cents:",
		"name: group",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI Module F schema missing snippet %q", snippet)
		}
	}
}

func TestOpenAPI_ModuleGPerfHealthRoutesMountedAndDocumented(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodHead, "/healthz"},
		{http.MethodGet, "/v1/admin/usage/perf-metrics/summary"},
		{http.MethodGet, "/v1/admin/usage/perf-metrics/by-bucket"},
		{http.MethodGet, "/v1/admin/usage/health-score"},
		{http.MethodGet, "/v1/admin/usage/provider-account-counts"},
	} {
		if !hasOperation(implOps, op.method, op.path) {
			t.Fatalf("runtime missing %s %s", op.method, op.path)
		}
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodHead, "/healthz"},
		{http.MethodGet, "/v1/admin/usage/perf-metrics/summary"},
		{http.MethodGet, "/v1/admin/usage/perf-metrics/by-bucket"},
		{http.MethodGet, "/v1/admin/usage/health-score"},
		{http.MethodGet, "/v1/admin/usage/provider-account-counts"},
	} {
		if !hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI missing %s %s", op.method, op.path)
		}
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/healthz"},
		{http.MethodPost, "/v1/admin/usage/perf-metrics/summary"},
		{http.MethodPatch, "/v1/admin/usage/perf-metrics/by-bucket"},
		{http.MethodDelete, "/v1/admin/usage/health-score"},
		{http.MethodPost, "/v1/admin/usage/provider-account-counts"},
	} {
		if hasOperation(implOps, op.method, op.path) || hasOperation(specOps, op.method, op.path) {
			t.Fatalf("Module G read-only routes must not expose mutation %s %s", op.method, op.path)
		}
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"getAdminUsagePerfMetricsSummary",
		"getAdminUsagePerfMetricsByBucket",
		"getAdminUsageHealthScore",
		"getAdminUsageProviderAccountCounts",
		"latency_percentiles_ms",
		"overall_score",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI Module G schema missing snippet %q", snippet)
		}
	}
}

func TestOpenAPI_ModuleBInboundRoutesMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/v1/responses/compact"},
		{method: http.MethodPost, path: "/backend-api/codex/responses/compact"},
		{method: http.MethodPost, path: "/engines/{model}/embeddings"},
		{method: http.MethodGet, path: "/v1/models/{model}"},
		{method: http.MethodPost, path: "/mj/submit/{action}"},
		{method: http.MethodPost, path: "/mj/insight-face/swap"},
		{method: http.MethodGet, path: "/mj/task/{id}/fetch"},
		{method: http.MethodGet, path: "/mj/task/{id}/image-seed"},
		{method: http.MethodPost, path: "/mj/task/list-by-condition"},
		{method: http.MethodPost, path: "/suno/submit"},
		{method: http.MethodPost, path: "/suno/submit/{action}"},
		{method: http.MethodGet, path: "/suno/fetch"},
		{method: http.MethodGet, path: "/suno/fetch/{id}"},
		{method: http.MethodPost, path: "/video/submit"},
		{method: http.MethodGet, path: "/video/fetch"},
		{method: http.MethodGet, path: "/video/fetch/{id}"},
	} {
		if !hasOperation(implOps, tc.method, tc.path) {
			t.Fatalf("runtime missing %s %s", tc.method, tc.path)
		}
		if !hasOperation(specOps, tc.method, tc.path) {
			t.Fatalf("OpenAPI missing %s %s", tc.method, tc.path)
		}
	}

	compactRec := httptest.NewRecorder()
	compactReq := httptest.NewRequest(http.MethodPost, "/v1/responses/compact",
		strings.NewReader(`{"model":"m","input":"x","stream":true}`))
	compactReq.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(compactRec, compactReq)
	if compactRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/responses/compact status=%d body=%s want 400 stream guard", compactRec.Code, compactRec.Body.String())
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/backend-api/codex/responses/compact", body: `{"model":"m","input":"x","stream":true}`},
		{method: http.MethodPost, path: "/engines/text-embed-3/embeddings", body: `{"input":"x"}`},
		{method: http.MethodGet, path: "/v1/models/gpt-x"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s %s returned 404; route must be mounted", tc.method, tc.path)
		}
	}
}

func TestOpenAPI_CompletionsAndCountTokensMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	for _, path := range []string{"/v1/completions", "/v1/messages/count_tokens"} {
		if !hasOperation(implOps, http.MethodPost, path) {
			t.Fatalf("runtime missing POST %s; relay endpoint must be mounted", path)
		}
		if !hasOperation(specOps, http.MethodPost, path) {
			t.Fatalf("OpenAPI missing POST %s; generated clients would not see mounted relay", path)
		}
		if hasOperation(implOps, http.MethodGet, path) || hasOperation(specOps, http.MethodGet, path) {
			t.Fatalf("%s must be POST-only in runtime and OpenAPI", path)
		}
	}
}

func TestOpenAPI_GeminiV1BetaRoutesMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	checks := []struct {
		method   string
		implPath string
		specPath string
	}{
		{method: http.MethodGet, implPath: "/v1beta/models", specPath: "/v1beta/models"},
		{method: http.MethodPost, implPath: "/v1beta/models/{rest:.*}", specPath: "/v1beta/models/{rest}"},
		{method: http.MethodGet, implPath: "/v1beta/models/{rest:.*}", specPath: "/v1beta/models/{rest}"},
	}
	for _, check := range checks {
		if !hasOperation(implOps, check.method, check.implPath) {
			t.Fatalf("runtime missing %s %s; Gemini native v1beta route must be mounted", check.method, check.implPath)
		}
		if !hasOperation(specOps, check.method, check.specPath) {
			t.Fatalf("OpenAPI missing %s %s; generated clients would not see Gemini native v1beta route", check.method, check.specPath)
		}
	}
}

func TestOpenAPI_UserAuditEventsMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	const path = "/v1/me/audit-events"
	if !hasOperation(implOps, http.MethodGet, path) {
		t.Fatalf("runtime missing GET %s; user self-service audit log route must be mounted", path)
	}
	if !hasOperation(specOps, http.MethodGet, path) {
		t.Fatalf("OpenAPI missing GET %s; generated clients would not see user audit log route", path)
	}
}

func TestOpenAPI_MeQuotaMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	const path = "/v1/me/quota"
	if !hasOperation(implOps, http.MethodGet, path) {
		t.Fatalf("runtime missing GET %s", path)
	}
	if !hasOperation(specOps, http.MethodGet, path) {
		t.Fatalf("OpenAPI missing GET %s; generated clients would not see self-service quota status", path)
	}
}

func TestOpenAPI_OrderReceiptMountedAndDocumented(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}

	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	const path = "/v1/me/orders/{id}/receipt"
	if !hasOperation(implOps, http.MethodGet, path) {
		t.Fatalf("runtime missing GET %s", path)
	}
	if !hasOperation(specOps, http.MethodGet, path) {
		t.Fatalf("OpenAPI missing GET %s; generated clients would not see the self-service order receipt", path)
	}
}

func hasOperation(ops []openapicheck.Operation, method, path string) bool {
	for _, op := range ops {
		if op.Method == method && op.Path == path {
			return true
		}
	}
	return false
}

func TestAT_GATEWAY_route_uncovered_404(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/receipts/foo/bar", nil)

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	// 旧 wildcard 会把未声明子路径交给 receipt verify；这里必须完全不进 handler。
	if strings.Contains(rec.Body.String(), "receipt_verify_route_not_found") {
		t.Fatalf("unexpected receipt verify handler response: %s", rec.Body.String())
	}
}

func TestAT_RT_001_RealtimeRouteReturnsExplicitRoadmapError(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s want 501", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("error body is not valid JSON: %v body=%s", err, rec.Body.String())
	}
	if parsed.Error.Code != "realtime_not_available" {
		t.Fatalf("error.code=%q want realtime_not_available body=%s", parsed.Error.Code, rec.Body.String())
	}
	if !strings.Contains(parsed.Error.Message, "Phase 9") {
		t.Fatalf("error.message=%q must mention Phase 9 roadmap status", parsed.Error.Message)
	}
}

func TestAT_GATEWAY_001_001_ReceiptRequestIDWithSlashRoutes(t *testing.T) {
	r := buildTestRouter(t)
	cases := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/receipts/host/random-000001"},
		{method: http.MethodPost, path: "/v1/receipts/host/random-000001/verify"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)

		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s want route hit", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// 单独覆盖 parser，避免 spec 文件结构变动时一致性测试整个报错却
// 看不清原因。
func TestOpenAPI_ParserSmoke(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	paths, err := openapicheck.ParseSpecPaths(specAbs)
	if err != nil {
		t.Fatalf("ParseSpecPaths: %v", err)
	}
	if len(paths) < 60 {
		t.Errorf("OpenAPI 解析出 %d 条 path — 当前 spec 应 ~65 条。"+
			"parser 退化或 spec 大幅缩减？", len(paths))
	}
	// 抽样断言几个 anchor path 必须能解析出。
	mustHave := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/messages",
		"/v1/messages/count_tokens",
		"/admin/v1/api-keys",
		"/admin/v1/usage",
	}
	got := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		got[p] = struct{}{}
	}
	for _, p := range mustHave {
		if _, ok := got[p]; !ok {
			t.Errorf("parser 漏 anchor path %q", p)
		}
	}
}

func TestAccountModesRouteAndOpenAPISchemaStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	if !hasOperation(implOps, http.MethodGet, "/admin/v1/account-modes") {
		t.Fatalf("runtime missing GET /admin/v1/account-modes")
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	if !hasOperation(specOps, http.MethodGet, "/admin/v1/account-modes") {
		t.Fatalf("OpenAPI missing GET /admin/v1/account-modes")
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"AccountModeCatalogResponse:",
		"AccountMode:",
		"AccountModeField:",
		"vendor:",
		"auth_mode:",
		"flow_kind:",
		"client_identity_source:",
		"allowed_helpers:",
		"required_fields:",
		"is_enabled:",
		"is_experimental:",
		"feature_flag:",
		"risk_level:",
		"risk_reasons:",
		"one_of_group:",
		"redaction:",
		"json_object",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI account mode schema missing snippet %q", snippet)
		}
	}
}

func TestProviderChannelCatalogRoutesAndOpenAPISchemasStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	for _, path := range []string{"/admin/v1/providers", "/admin/v1/channels"} {
		if !hasOperation(implOps, http.MethodGet, path) {
			t.Fatalf("runtime missing GET %s", path)
		}
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/v1/providers"},
		{http.MethodPut, "/admin/v1/providers/{code}"},
		{http.MethodDelete, "/admin/v1/providers/{code}"},
		{http.MethodPost, "/admin/v1/channels"},
		{http.MethodGet, "/admin/v1/channels/{id}"},
		{http.MethodPut, "/admin/v1/channels/{id}"},
		{http.MethodDelete, "/admin/v1/channels/{id}"},
		{http.MethodGet, "/admin/v1/channel-test-templates"},
		{http.MethodPost, "/admin/v1/channel-test-templates"},
		{http.MethodGet, "/admin/v1/channel-test-templates/{id}"},
		{http.MethodPut, "/admin/v1/channel-test-templates/{id}"},
		{http.MethodDelete, "/admin/v1/channel-test-templates/{id}"},
	} {
		if !hasOperation(implOps, op.method, op.path) {
			t.Fatalf("runtime missing %s %s", op.method, op.path)
		}
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	for _, path := range []string{"/admin/v1/providers", "/admin/v1/channels"} {
		if !hasOperation(specOps, http.MethodGet, path) {
			t.Fatalf("OpenAPI missing GET %s", path)
		}
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/v1/providers"},
		{http.MethodPut, "/admin/v1/providers/{code}"},
		{http.MethodDelete, "/admin/v1/providers/{code}"},
		{http.MethodPost, "/admin/v1/channels"},
		{http.MethodGet, "/admin/v1/channels/{id}"},
		{http.MethodPut, "/admin/v1/channels/{id}"},
		{http.MethodDelete, "/admin/v1/channels/{id}"},
		{http.MethodGet, "/admin/v1/channel-test-templates"},
		{http.MethodPost, "/admin/v1/channel-test-templates"},
		{http.MethodGet, "/admin/v1/channel-test-templates/{id}"},
		{http.MethodPut, "/admin/v1/channel-test-templates/{id}"},
		{http.MethodDelete, "/admin/v1/channel-test-templates/{id}"},
	} {
		if !hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI missing %s %s", op.method, op.path)
		}
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"AdminProviderCatalogList:",
		"AdminProviderCatalogItem:",
		"AdminProviderCatalogCreateRequest:",
		"AdminProviderCatalogUpdateRequest:",
		"AdminProviderCatalogDeleteResponse:",
		"admin_providers_list",
		"admin_provider_deleted",
		"code:",
		"display_name:",
		"upstream_protocol:",
		"AdminChannelCatalogList:",
		"AdminChannelCatalogItem:",
		"admin_channels_list",
		"pool_group_id:",
		"failover_status_codes:",
		"body_param_strips:",
		"param_override:",
		"sensitive_words:",
		"AdminChannelTestTemplateItem:",
		"AdminChannelTestTemplateRequest:",
		"AdminChannelTestTemplateList:",
		"AdminChannelTestTemplateDeleteResponse:",
		"admin_channel_test_templates_list",
		"admin_channel_test_template_deleted",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI provider/channel catalog schema missing snippet %q", snippet)
		}
	}
}

// TestQuotaPoliciesRoutesAndOpenAPISchemasStayInSync 是 BILL-122 的接线绊线:
// 它断言全部 5 条 quota-policy 路由在 chi 实现与 openapi.yaml 中都存在,
// 并且 spec 中带有相应的 schema/enum 片段。若忘了写 openapi 条目,否则只会
// 触发 impl-only 的 TestOpenAPI_ImplementationConsistency 硬失败;这里改为给出
// 一个具名的、针对性的失败。
func TestQuotaPoliciesRoutesAndOpenAPISchemasStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/v1/quota-policies"},
		{http.MethodPost, "/admin/v1/quota-policies"},
		{http.MethodGet, "/admin/v1/quota-policies/{id}"},
		{http.MethodPut, "/admin/v1/quota-policies/{id}"},
		{http.MethodDelete, "/admin/v1/quota-policies/{id}"},
	}
	for _, op := range routes {
		if !hasOperation(implOps, op.method, op.path) {
			t.Fatalf("runtime missing %s %s", op.method, op.path)
		}
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	for _, op := range routes {
		if !hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI missing %s %s", op.method, op.path)
		}
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"AdminQuotaPolicyList:",
		"AdminQuotaPolicyItem:",
		"AdminQuotaPolicyCreateRequest:",
		"AdminQuotaPolicyUpdateRequest:",
		"AdminQuotaPolicyDeleteResponse:",
		"admin_quota_policies_list",
		"admin_quota_policy_deleted",
		"scope_kind:",
		"metric:",
		"window_kind:",
		"mode:",
		"limit_value:",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI quota policy schema missing snippet %q", snippet)
		}
	}
}

func TestAdminUsersRoutesAndOpenAPISchemasStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	readOps := []string{
		"/admin/v1/users",
		"/admin/v1/users/{id}",
		"/admin/v1/users/{id}/balance-history",
		"/admin/v1/users/{id}/usage",
	}
	for _, path := range readOps {
		if !hasOperationEquivalent(implOps, http.MethodGet, path) {
			t.Fatalf("runtime missing GET %s", path)
		}
	}
	if !hasOperationEquivalent(implOps, http.MethodPost, "/admin/v1/users/{id}/unlock") {
		t.Fatalf("runtime missing POST /admin/v1/users/{id}/unlock")
	}
	// S4 单租户开箱即用:admin 创建用户 + 软删除现在是本切片上
	// 刻意保留的 mutation。
	if !hasOperationEquivalent(implOps, http.MethodPost, "/admin/v1/users") {
		t.Fatalf("runtime missing POST /admin/v1/users (admin create user)")
	}
	if !hasOperationEquivalent(implOps, http.MethodDelete, "/admin/v1/users/{id}") {
		t.Fatalf("runtime missing DELETE /admin/v1/users/{id} (admin soft-delete user)")
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPatch, "/admin/v1/users/{id}"},
		{http.MethodPut, "/admin/v1/users/{id}"},
		{http.MethodPost, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPatch, "/admin/v1/users/{id}/balance-history"},
		{http.MethodDelete, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPost, "/admin/v1/users/{id}/usage"},
		{http.MethodPatch, "/admin/v1/users/{id}/usage"},
		{http.MethodDelete, "/admin/v1/users/{id}/usage"},
	} {
		if hasOperationEquivalent(implOps, op.method, op.path) {
			t.Fatalf("runtime unexpectedly exposes read-only slice mutation %s %s", op.method, op.path)
		}
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	for _, path := range readOps {
		if !hasOperation(specOps, http.MethodGet, path) {
			t.Fatalf("OpenAPI missing GET %s", path)
		}
	}
	if !hasOperation(specOps, http.MethodPost, "/admin/v1/users/{id}/unlock") {
		t.Fatalf("OpenAPI missing POST /admin/v1/users/{id}/unlock")
	}
	if !hasOperation(specOps, http.MethodPost, "/admin/v1/users") {
		t.Fatalf("OpenAPI missing POST /admin/v1/users (admin create user)")
	}
	if !hasOperation(specOps, http.MethodDelete, "/admin/v1/users/{id}") {
		t.Fatalf("OpenAPI missing DELETE /admin/v1/users/{id} (admin soft-delete user)")
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPatch, "/admin/v1/users/{id}"},
		{http.MethodPut, "/admin/v1/users/{id}"},
		{http.MethodPost, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPatch, "/admin/v1/users/{id}/balance-history"},
		{http.MethodDelete, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPost, "/admin/v1/users/{id}/usage"},
		{http.MethodPatch, "/admin/v1/users/{id}/usage"},
		{http.MethodDelete, "/admin/v1/users/{id}/usage"},
	} {
		if hasOperation(specOps, op.method, op.path) {
			t.Fatalf("OpenAPI unexpectedly declares read-only slice mutation %s %s", op.method, op.path)
		}
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"AdminUserList:",
		"AdminUser:",
		"AdminUserBalanceHistoryList:",
		"AdminUserBalanceHistoryItem:",
		"listAdminUsers",
		"getAdminUser",
		"listAdminUserBalanceHistory",
		"listAdminUserUsage",
		"adminUnlockUser",
		"balance-history",
		"unlock_user",
		"source_type:",
		"fingerprint:",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI admin users schema missing snippet %q", snippet)
		}
	}
}

func hasOperationEquivalent(ops []openapicheck.Operation, method, path string) bool {
	if hasOperation(ops, method, path) {
		return true
	}
	if strings.HasSuffix(path, "/") {
		return false
	}
	return hasOperation(ops, method, path+"/")
}

func TestModelCapabilitiesRouteAndOpenAPISchemaStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	if !hasOperation(implOps, http.MethodPut, "/v1/admin/models/{id}/capabilities") {
		t.Fatalf("runtime missing PUT /v1/admin/models/{id}/capabilities")
	}
	if !hasOperation(implOps, http.MethodPost, "/v1/admin/models/aliases/bulk-import") {
		t.Fatalf("runtime missing POST /v1/admin/models/aliases/bulk-import")
	}
	if !hasOperation(implOps, http.MethodGet, "/v1/admin/models/{id}/capability-bindings") {
		t.Fatalf("runtime missing GET /v1/admin/models/{id}/capability-bindings")
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specOps, err := openapicheck.ParseSpecOperations(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI operations %s: %v", specAbs, err)
	}
	if !hasOperation(specOps, http.MethodPut, "/v1/admin/models/{id}/capabilities") {
		t.Fatalf("OpenAPI missing PUT /v1/admin/models/{id}/capabilities")
	}
	if !hasOperation(specOps, http.MethodPost, "/v1/admin/models/aliases/bulk-import") {
		t.Fatalf("OpenAPI missing POST /v1/admin/models/aliases/bulk-import")
	}
	if !hasOperation(specOps, http.MethodGet, "/v1/admin/models/{id}/capability-bindings") {
		t.Fatalf("OpenAPI missing GET /v1/admin/models/{id}/capability-bindings")
	}

	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	spec := string(raw)
	for _, snippet := range []string{
		"ModelCapabilitiesUpdateRequest:",
		"ModelCapabilitiesUpdateResponse:",
		"capabilities:",
		"max_output_tokens:",
		"model_mode:",
		"mode:",
		"function_calling",
		"response_schema",
		"prompt_caching",
		"ModelAliasBulkImportRequest:",
		"ModelAliasBulkImportResponse:",
		"ModelCapabilityBinding:",
		"capability-bindings",
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI model capabilities schema missing snippet %q", snippet)
		}
	}
}

// TestPublicPricingPageItemSchemaListsCatalogMetadata 把公开定价页的响应形状
// 绑定到其 OpenAPI schema。handler 会把 owned_by/mode/max_output_tokens/capabilities
// 投影到响应上,而 PublicPricingPageItem 是 additionalProperties:false——所以若这些
// property 从 schema 中被删掉、handler 却仍在产出它们,文档化的契约就会悄悄偏离。
// 变异:从 openapi.yaml 的 PublicPricingPageItem 块中删掉这四条 property 中的任意一条,
// 本测试即变红(该块不再包含那个字段)。
func TestPublicPricingPageItemSchemaListsCatalogMetadata(t *testing.T) {
	r := buildTestRouter(t)
	if !hasOperation(openapicheck.WalkChiOperations(r), http.MethodGet, "/v1/pricing/page") {
		t.Fatalf("runtime missing GET /v1/pricing/page")
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	block := yamlSchemaBlock(string(raw), "PublicPricingPageItem:")
	if block == "" {
		t.Fatalf("PublicPricingPageItem schema block not found in OpenAPI")
	}
	for _, field := range []string{"owned_by:", "mode:", "max_output_tokens:", "capabilities:"} {
		if !strings.Contains(block, field) {
			t.Fatalf("PublicPricingPageItem schema missing %q property (handler emits it; schema is additionalProperties:false)", field)
		}
	}
}

// yamlSchemaBlock 返回某个具名 schema 的 YAML 文本(从其缩进 4 空格的头行起,
// 到同一缩进层级的下一个兄弟 schema 为止);不存在则返回 ""。
func yamlSchemaBlock(spec, header string) string {
	lines := strings.Split(spec, "\n")
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "    ") && strings.TrimSpace(ln) == header {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		ln := lines[i]
		// 下一个兄弟 schema = 恰好 4 个前导空格、第 5 列为非空格、且以 ':' 结尾
		if len(ln) > 4 && ln[:4] == "    " && ln[4] != ' ' && strings.HasSuffix(strings.TrimSpace(ln), ":") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// TestMeUsageRecordSchemaListsStreamShapeAndTiming 把自助用量记录的响应形状
// 绑定到其 OpenAPI schema。handler 会投影 stream/stream_terminated_reason/requested_at,
// 而 MeUsageRecord 是 additionalProperties:false——所以若这些 property 从 schema 中被删掉、
// handler 却仍在产出它们,文档化的契约就会悄悄偏离。变异:从 openapi.yaml 的 MeUsageRecord
// 块中删掉这三条 property 中的任意一条,本测试即变红。
func TestMeUsageRecordSchemaListsStreamShapeAndTiming(t *testing.T) {
	r := buildTestRouter(t)
	if !hasOperation(openapicheck.WalkChiOperations(r), http.MethodGet, "/v1/me/usage") {
		t.Fatalf("runtime missing GET /v1/me/usage")
	}

	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	raw, err := os.ReadFile(specAbs)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	block := yamlSchemaBlock(string(raw), "MeUsageRecord:")
	if block == "" {
		t.Fatalf("MeUsageRecord schema block not found in OpenAPI")
	}
	for _, field := range []string{"stream:", "stream_terminated_reason:", "requested_at:"} {
		if !strings.Contains(block, field) {
			t.Fatalf("MeUsageRecord schema missing %q property (handler emits it; schema is additionalProperties:false)", field)
		}
	}
}
