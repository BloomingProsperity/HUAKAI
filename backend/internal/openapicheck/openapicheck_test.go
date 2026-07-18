package openapicheck

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.yaml.in/yaml/v2"
)

func TestAuthoritativeSpecIsStrictYAML(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", "docs", "openapi", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("读取主 OpenAPI 文件: %v", err)
	}
	var document map[interface{}]interface{}
	if err := yaml.UnmarshalStrict(raw, &document); err != nil {
		t.Fatalf("主 OpenAPI 必须是无重复键的合法 YAML: %v", err)
	}
}

func TestParseSpecPaths_BasicYAML(t *testing.T) {
	spec := `openapi: 3.1.0
info:
  title: test
paths:
  /v1/foo:
    get: {}
  /v1/bar/{id}:
    post: {}
  /admin/v1/baz:
    get: {}
components:
  schemas: {}
`
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	got, err := ParseSpecPaths(tmp)
	if err != nil {
		t.Fatalf("ParseSpecPaths: %v", err)
	}
	want := []string{"/v1/foo", "/v1/bar/{id}", "/admin/v1/baz"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestParseSpecOperations_BasicYAML(t *testing.T) {
	spec := `openapi: 3.1.0
info:
  title: test
paths:
  /v1/foo:
    get: {}
    post:
      requestBody:
        required: true
  /v1/bar/{id}:
    delete: {}
components:
  schemas: {}
`
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	got, err := ParseSpecOperations(tmp)
	if err != nil {
		t.Fatalf("ParseSpecOperations: %v", err)
	}
	want := []Operation{
		{Method: http.MethodDelete, Path: "/v1/bar/{id}"},
		{Method: http.MethodGet, Path: "/v1/foo"},
		{Method: http.MethodPost, Path: "/v1/foo"},
	}
	sortOperations(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operations mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestParseSpecPaths_StopsAtNextTopKey(t *testing.T) {
	// paths: 后面紧跟另一个顶层 key (components) — parser 必须停止。
	spec := `paths:
  /a:
    get: {}
  /b:
    post: {}
components:
  schemas:
    Foo:
      type: object
      properties:
        /not_a_path:
          type: string
`
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	_ = os.WriteFile(tmp, []byte(spec), 0o600)
	got, _ := ParseSpecPaths(tmp)
	sort.Strings(got)
	want := []string{"/a", "/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parser 越界吸了 components 段:\n got=%v\nwant=%v", got, want)
	}
}

func TestCompareOperations_DetectsSamePathMethodMismatch(t *testing.T) {
	spec := []Operation{{Method: http.MethodGet, Path: "/v1/chat/completions"}}
	impl := []Operation{{Method: http.MethodPost, Path: "/v1/chat/completions"}}

	rep := CompareOperations(spec, impl)

	if len(rep.Common) != 0 {
		t.Fatalf("Common=%v want empty for GET-vs-POST drift", rep.Common)
	}
	if !reflect.DeepEqual(rep.SpecOnly, []string{"GET /v1/chat/completions"}) {
		t.Fatalf("SpecOnly=%v want GET /v1/chat/completions", rep.SpecOnly)
	}
	if !reflect.DeepEqual(rep.ImplOnly, []string{"POST /v1/chat/completions"}) {
		t.Fatalf("ImplOnly=%v want POST /v1/chat/completions", rep.ImplOnly)
	}
}

func TestNormalize_ParamNameElision(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/v1/foo/{id}", "/v1/foo/{}"},
		{"/v1/foo/{flow_id}", "/v1/foo/{}"},
		{"/v1/foo/{flowID}", "/v1/foo/{}"},
		{"/v1/a/{x}/b/{y}", "/v1/a/{}/b/{}"},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalize_ChiMountGlob(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/admin/v1/pools/*", "/admin/v1/pools"},
		{"/admin/v1/pools/*/{id}", "/admin/v1/pools/{}"},
		{"/admin/v1/cache/l2/*", "/admin/v1/cache/l2"},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalize_KnownAlias(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/v1/admin/pool-accounts", "/admin/v1/provider-accounts"},
		{"/v1/admin/pool-accounts/{id}", "/admin/v1/provider-accounts/{}"},
		{"/v1/admin/provider-accounts/{id}", "/admin/v1/provider-accounts/{}"},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompare_HappyPath(t *testing.T) {
	spec := []string{"/a", "/b/{id}"}
	impl := []string{"/a", "/b/{user_id}"} // param 名漂移
	rep := Compare(spec, impl)
	if len(rep.Common) != 2 {
		t.Errorf("Common should be 2, got %d: %v", len(rep.Common), rep.Common)
	}
	if len(rep.SpecOnly) != 0 {
		t.Errorf("SpecOnly should be 0, got: %v", rep.SpecOnly)
	}
	if len(rep.ImplOnly) != 0 {
		t.Errorf("ImplOnly should be 0, got: %v", rep.ImplOnly)
	}
}

func TestCompare_DetectsMissingOnBothSides(t *testing.T) {
	spec := []string{"/a", "/spec_only"}
	impl := []string{"/a", "/impl_only"}
	rep := Compare(spec, impl)
	if len(rep.Common) != 1 || rep.Common[0] != "/a" {
		t.Errorf("Common: got %v want [/a]", rep.Common)
	}
	if len(rep.SpecOnly) != 1 || rep.SpecOnly[0] != "/spec_only" {
		t.Errorf("SpecOnly: got %v want [/spec_only]", rep.SpecOnly)
	}
	if len(rep.ImplOnly) != 1 || rep.ImplOnly[0] != "/impl_only" {
		t.Errorf("ImplOnly: got %v want [/impl_only]", rep.ImplOnly)
	}
}

func TestWalkChiPaths_BasicRouter(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/health", func(_ http.ResponseWriter, _ *http.Request) {})
	r.Route("/api", func(r chi.Router) {
		r.Get("/users/{id}", func(_ http.ResponseWriter, _ *http.Request) {})
	})
	paths := WalkChiPaths(r)
	if len(paths) == 0 {
		t.Fatalf("WalkChiPaths 应至少抽出 /health + /api/users/{id}")
	}
	// /api/users/{id} 必须出现（method 维度合并）。
	got := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		got[p] = struct{}{}
	}
	if _, ok := got["/health"]; !ok {
		t.Errorf("缺 /health；got=%v", paths)
	}
	if _, ok := got["/api/users/{id}"]; !ok {
		t.Errorf("缺 /api/users/{id}；got=%v", paths)
	}
}

func TestReadImplRoutesFile_SkipsCommentsAndBlank(t *testing.T) {
	content := `# header comment
/v1/foo

# midline comment
/v1/bar
`
	tmp := filepath.Join(t.TempDir(), "routes.txt")
	_ = os.WriteFile(tmp, []byte(content), 0o600)
	got, err := ReadImplRoutesFile(tmp)
	if err != nil {
		t.Fatalf("ReadImplRoutesFile: %v", err)
	}
	want := []string{"/v1/bar", "/v1/foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadImplRoutesFile: got=%v want=%v", got, want)
	}
}

// secDriftAuthMW 是判别夹具:一个具名的"认证中间件"。OperationsGatedByMiddleware 靠 runtime
// 函数名识别认证中间件(生产里用 "SessionMiddleware"),这里用本函数名 "secDriftAuthMW" 当 marker。
func secDriftAuthMW(next http.Handler) http.Handler { return next }

func TestSecurityContractDrift_ReportsSpecPublicButImplGated(t *testing.T) {
	// 三类操作覆盖判别边界:
	//   POST /v1/x/verify —— spec 标 security:[](公开)且 impl 挂认证中间件 → **应报漂移**
	//   GET  /v1/y        —— spec 公开且 impl 不挂认证 → 公开一致,不报(防"把所有公开都当漂移")
	//   GET  /v1/z        —— spec 声明了 scheme(非公开)且 impl 挂认证 → 认证一致,不报(防"把所有认证都当漂移")
	spec := `openapi: 3.1.0
paths:
  /v1/x/verify:
    post:
      security: []
  /v1/y:
    get:
      security: []
  /v1/z:
    get:
      security:
        - sessionBearerAuth: []
components:
  schemas: {}
`
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, []byte(spec), 0o600); err != nil {
		t.Fatalf("写临时 spec: %v", err)
	}
	pub, err := ParseSpecPublicOperations(tmp)
	if err != nil {
		t.Fatalf("ParseSpecPublicOperations: %v", err)
	}
	// 公开集应仅 {POST /v1/x/verify, GET /v1/y};/v1/z 声明了 scheme,非公开。
	wantPub := []Operation{
		{Method: http.MethodGet, Path: "/v1/y"},
		{Method: http.MethodPost, Path: "/v1/x/verify"},
	}
	sortOperations(wantPub)
	if !reflect.DeepEqual(pub, wantPub) {
		t.Fatalf("公开集解析错:\n got=%v\nwant=%v", pub, wantPub)
	}

	okHandler := func(w http.ResponseWriter, r *http.Request) {}
	r := chi.NewRouter()
	r.With(secDriftAuthMW).Post("/v1/x/verify", okHandler) // 公开声明却挂认证 → 漂移源
	r.Get("/v1/y", okHandler)                              // 公开且无认证
	r.With(secDriftAuthMW).Get("/v1/z", okHandler)         // 认证且 spec 非公开

	gated := OperationsGatedByMiddleware(r, "secDriftAuthMW")
	drift := SecurityContractDrift(pub, gated)

	want := []Operation{{Method: http.MethodPost, Path: "/v1/x/verify"}}
	if !reflect.DeepEqual(drift, want) {
		t.Fatalf("漂移应仅 [POST /v1/x/verify]:\n got=%v\nwant=%v", drift, want)
	}
}
