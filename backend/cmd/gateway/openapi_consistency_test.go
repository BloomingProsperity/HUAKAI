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
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI provider/channel catalog schema missing snippet %q", snippet)
		}
	}
}

func TestAdminUsersReadRoutesAndOpenAPISchemasStayInSync(t *testing.T) {
	r := buildTestRouter(t)
	implOps := openapicheck.WalkChiOperations(r)
	readOps := []string{
		"/admin/v1/users",
		"/admin/v1/users/{id}",
		"/admin/v1/users/{id}/balance-history",
	}
	for _, path := range readOps {
		if !hasOperationEquivalent(implOps, http.MethodGet, path) {
			t.Fatalf("runtime missing GET %s", path)
		}
	}
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/v1/users"},
		{http.MethodPatch, "/admin/v1/users/{id}"},
		{http.MethodPut, "/admin/v1/users/{id}"},
		{http.MethodDelete, "/admin/v1/users/{id}"},
		{http.MethodPost, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPatch, "/admin/v1/users/{id}/balance-history"},
		{http.MethodDelete, "/admin/v1/users/{id}/balance-history"},
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
	for _, op := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/v1/users"},
		{http.MethodPatch, "/admin/v1/users/{id}"},
		{http.MethodPut, "/admin/v1/users/{id}"},
		{http.MethodDelete, "/admin/v1/users/{id}"},
		{http.MethodPost, "/admin/v1/users/{id}/balance-history"},
		{http.MethodPatch, "/admin/v1/users/{id}/balance-history"},
		{http.MethodDelete, "/admin/v1/users/{id}/balance-history"},
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
		"balance-history",
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
	} {
		if !strings.Contains(spec, snippet) {
			t.Fatalf("OpenAPI model capabilities schema missing snippet %q", snippet)
		}
	}
}
