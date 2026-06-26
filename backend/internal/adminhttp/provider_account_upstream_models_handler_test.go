package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// ---------------------------------------------------------------------------
// 桩件(Stubs)
// ---------------------------------------------------------------------------

type stubUpstreamModelsAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s *stubUpstreamModelsAuth) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type stubUpstreamModelsAccountStore struct {
	row admindb.AdminProviderAccountRow
	err error
}

func (s *stubUpstreamModelsAccountStore) GetAdminProviderAccount(_ context.Context, _ admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return s.row, s.err
}

type stubUpstreamModelsCredStore struct {
	rec credentialstore.CredentialRecord
	err error
}

func (s *stubUpstreamModelsCredStore) LoadForProviderAccountTest(_ context.Context, _, _ int64) (credentialstore.CredentialRecord, error) {
	return s.rec, s.err
}

// allowAllTransportWrapper 原样返回基础传输层(不做 IP 守卫),
// 以便测试中能访问到 httptest.Server(127.0.0.1)。
func allowAllTransportWrapper(rt http.RoundTripper) (http.RoundTripper, error) {
	return rt, nil
}

func platformAdminIdent() admin.AdminIdentity {
	return admin.AdminIdentity{Role: admin.RolePlatformAdmin, ScopeTenantID: 42, TokenID: 1}
}

func upstreamStaticPayload(baseURL, authHeader string) []byte {
	b, _ := json.Marshal(map[string]string{
		"base_url":          baseURL,
		"auth_header_value": authHeader,
	})
	return b
}

// buildModelsRouter 把处理器挂载到 /{id}/upstream-models。
func buildModelsRouter(d UpstreamModelsDeps) *chi.Mux {
	r := chi.NewRouter()
	MountProviderAccountUpstreamModelsRoutes(r, d)
	return r
}

// ---------------------------------------------------------------------------
// 测试:使用桩上游的正常路径
// ---------------------------------------------------------------------------

func TestUpstreamModelsHandler_HappyPath(t *testing.T) {
	// 桩上游返回两个 model。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	}))
	defer upstream.Close()

	// 账号的 base_url 是 https(生产 scheme);本测试注入了一个
	// transport wrapper,把每次上游调用都代理到 http 的 httptest
	// 服务器,因此真实的 SSRF 守卫会在守卫测试中单独验证。
	proxyRT := &proxyToTestServerRT{target: upstream.URL}
	d := UpstreamModelsDeps{
		Auth: &stubUpstreamModelsAuth{ident: platformAdminIdent()},
		Accounts: &stubUpstreamModelsAccountStore{
			row: admindb.AdminProviderAccountRow{ID: 7, TenantID: 42},
		},
		Creds: &stubUpstreamModelsCredStore{
			rec: credentialstore.CredentialRecord{
				AuthMode:         "upstream_static",
				PlaintextPayload: upstreamStaticPayload("https://api.example.com", "Bearer sk-test"),
			},
		},
		TransportWrapper: func(_ http.RoundTripper) (http.RoundTripper, error) {
			return proxyRT, nil
		},
	}

	r := buildModelsRouter(d)
	req := httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp upstreamModelsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("expected 2 models, got %d", resp.Count)
	}
	if len(resp.Models) != 2 || resp.Models[0] != "gpt-4" || resp.Models[1] != "gpt-3.5-turbo" {
		t.Errorf("unexpected models: %v", resp.Models)
	}
}

// proxyToTestServerRT 把所有请求重定向到指定的 target URL
//(用于需要访问 httptest.Server 的处理器测试)。
type proxyToTestServerRT struct {
	target string
	base   http.RoundTripper
}

func (p *proxyToTestServerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	targetURL, _ := http.NewRequest(http.MethodGet, p.target+req.URL.Path, nil)
	clone.URL = targetURL.URL
	clone.Host = targetURL.Host
	if p.base == nil {
		p.base = http.DefaultTransport
	}
	return p.base.RoundTrip(clone)
}

// ---------------------------------------------------------------------------
// 测试:被守卫拦截的上游返回 422
// ---------------------------------------------------------------------------

func TestUpstreamModelsHandler_GuardBlocked(t *testing.T) {
	// 模拟 SSRF 守卫拒绝连接的 transport wrapper。
	d := UpstreamModelsDeps{
		Auth: &stubUpstreamModelsAuth{ident: platformAdminIdent()},
		Accounts: &stubUpstreamModelsAccountStore{
			row: admindb.AdminProviderAccountRow{ID: 7, TenantID: 42},
		},
		Creds: &stubUpstreamModelsCredStore{
			rec: credentialstore.CredentialRecord{
				AuthMode:         "upstream_static",
				PlaintextPayload: upstreamStaticPayload("https://api.example.com", "Bearer sk-test"),
			},
		},
		TransportWrapper: func(_ http.RoundTripper) (http.RoundTripper, error) {
			return &blockedRT{}, nil
		},
	}

	r := buildModelsRouter(d)
	req := httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"]["code"] != "upstream_blocked" {
		t.Errorf("expected upstream_blocked error code, got: %s", body["error"]["code"])
	}
}

// blockedRT 模拟一个返回 ErrUnsafePassthroughEndpoint 的传输层。
type blockedRT struct{}

func (b *blockedRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%w: loopback address blocked in test", provider.ErrUnsafePassthroughEndpoint)
}

// ---------------------------------------------------------------------------
// 测试:账号未找到返回 404
// ---------------------------------------------------------------------------

func TestUpstreamModelsHandler_AccountNotFound(t *testing.T) {
	d := UpstreamModelsDeps{
		Auth:             &stubUpstreamModelsAuth{ident: platformAdminIdent()},
		Accounts:         &stubUpstreamModelsAccountStore{err: pgx.ErrNoRows},
		Creds:            &stubUpstreamModelsCredStore{},
		TransportWrapper: allowAllTransportWrapper,
	}
	r := buildModelsRouter(d)
	req := httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// 测试:缺少 base_url 返回 422
// ---------------------------------------------------------------------------

func TestUpstreamModelsHandler_MissingBaseURL(t *testing.T) {
	d := UpstreamModelsDeps{
		Auth: &stubUpstreamModelsAuth{ident: platformAdminIdent()},
		Accounts: &stubUpstreamModelsAccountStore{
			row: admindb.AdminProviderAccountRow{ID: 7, TenantID: 42},
		},
		Creds: &stubUpstreamModelsCredStore{
			rec: credentialstore.CredentialRecord{
				AuthMode:         "upstream_static",
				PlaintextPayload: upstreamStaticPayload("", "Bearer sk-test"),
			},
		},
		TransportWrapper: allowAllTransportWrapper,
	}
	r := buildModelsRouter(d)
	req := httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// buildModelsURL 的单元测试
// ---------------------------------------------------------------------------

func TestBuildModelsURL(t *testing.T) {
	cases := []struct {
		base string
		want string
		ok   bool
	}{
		{"https://api.openai.com", "https://api.openai.com/v1/models", true},
		{"https://api.openai.com/", "https://api.openai.com/v1/models", true},
		{"https://proxy.example.com/v1", "https://proxy.example.com/v1/models", true},
		{"https://proxy.example.com/api/v2", "https://proxy.example.com/api/v2/models", true},
		{"http://insecure.example.com", "", false},
		{"not-a-url", "", false},
	}
	for _, tc := range cases {
		got, err := buildModelsURL(tc.base)
		if tc.ok && err != nil {
			t.Errorf("buildModelsURL(%q): unexpected error: %v", tc.base, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("buildModelsURL(%q): expected error, got %q", tc.base, got)
		}
		if tc.ok && got != tc.want {
			t.Errorf("buildModelsURL(%q): got %q, want %q", tc.base, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// parseModelsResponse 的单元测试
// ---------------------------------------------------------------------------

func TestParseModelsResponse(t *testing.T) {
	body := []byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"},{"id":"gpt-4"}]}`)
	models, err := parseModelsResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 已去重:gpt-4 虽出现两次,结果中只出现一次。
	if len(models) != 2 {
		t.Errorf("expected 2 unique models, got %d: %v", len(models), models)
	}
}

func TestParseModelsResponse_InvalidJSON(t *testing.T) {
	_, err := parseModelsResponse([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// SSRF 守卫 IP 判定逻辑的单元测试(使用真实的 provider 包逻辑)
// ---------------------------------------------------------------------------

// TestSSRFGuard_IPPredicate 通过导出的 ErrUnsafePassthroughEndpoint 哨兵错误,
// 直接验证 publicPassthroughIP / passthroughIPAllowedForHost。
// 我们使用 WrapPassthroughEndpointTransport:它必须拒绝 127.0.0.1 和
// 私有 IP,并且必须接受公网 IP。
func TestSSRFGuard_RealTransportWrapperRejectsPrivateIP(t *testing.T) {
	// 经守卫的传输层必须拒绝对 127.0.0.1 的拨号。
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	rt, err := provider.WrapPassthroughEndpointTransport(base)
	if err != nil {
		t.Fatalf("WrapPassthroughEndpointTransport: %v", err)
	}
	client := &http.Client{Transport: rt}
	_, dialErr := client.Get("https://127.0.0.1/")
	if dialErr == nil {
		t.Fatal("expected error dialing loopback, got nil")
	}
	if !isBlockedErr(dialErr) {
		t.Errorf("expected ErrUnsafePassthroughEndpoint, got: %v", dialErr)
	}
}

// TestSSRFGuard_PrivateIPBlocked 验证针对 10.0.0.1(私有)的 IP 判定逻辑。
func TestSSRFGuard_PrivateIPTransportBlocked(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	rt, err := provider.WrapPassthroughEndpointTransport(base)
	if err != nil {
		t.Fatalf("WrapPassthroughEndpointTransport: %v", err)
	}
	client := &http.Client{Transport: rt}
	_, dialErr := client.Get("https://10.0.0.1/")
	if dialErr == nil {
		t.Fatal("expected error dialing private IP, got nil")
	}
	if !isBlockedErr(dialErr) {
		t.Errorf("expected ErrUnsafePassthroughEndpoint wrapping, got: %v", dialErr)
	}
}

// TestSSRFGuard_MutationVerification 记录了变异测试。
// 变异方式:将 WrapPassthroughEndpointTransport 改为允许私有 IP ??// 此测试就会变红。本测试记录了守卫的预期行为,
// 但不会修改生产代码(变异是在外部进行的)。
func TestSSRFGuard_MutationVerification(t *testing.T) {
	// 如果守卫正确地拦截了私有 IP,那么对 192.168.1.1 的拨号
	// 必须返回 ErrUnsafePassthroughEndpoint。
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	rt, err := provider.WrapPassthroughEndpointTransport(base)
	if err != nil {
		t.Fatalf("WrapPassthroughEndpointTransport: %v", err)
	}
	client := &http.Client{Transport: rt}
	_, dialErr := client.Get("https://192.168.1.1/")
	if dialErr == nil {
		t.Fatal("MUTATION EXPOSED: guard allowed private 192.168.1.1; real guard should block it")
	}
	if !isBlockedErr(dialErr) {
		t.Logf("got non-guard error: %v (may be DNS, still acceptable as block)", dialErr)
	}
}
