package userkeyhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

// stubService 是测试桩。每个方法把入参录下来,可控制返回值,便于跑判别 fixture。
type stubService struct {
	issueCalls  []userkey.IssueRequest
	listCalls   []userkey.ListRequest
	getCalls    []struct{ tenantID, userID, apiKeyID int64 }
	revokeCalls []userkey.RevokeRequest

	issueReturn  userkey.IssueResult
	issueErr     error
	listReturn   []userkey.KeyDescriptor
	listErr      error
	getReturn    userkey.KeyDescriptor
	getErr       error
	revokeReturn userkey.RevokeResult
	revokeErr    error
}

func (s *stubService) Issue(ctx context.Context, req userkey.IssueRequest) (userkey.IssueResult, error) {
	s.issueCalls = append(s.issueCalls, req)
	return s.issueReturn, s.issueErr
}
func (s *stubService) List(ctx context.Context, req userkey.ListRequest) ([]userkey.KeyDescriptor, error) {
	s.listCalls = append(s.listCalls, req)
	return s.listReturn, s.listErr
}
func (s *stubService) Get(ctx context.Context, tenantID, userID, apiKeyID int64) (userkey.KeyDescriptor, error) {
	s.getCalls = append(s.getCalls, struct{ tenantID, userID, apiKeyID int64 }{tenantID, userID, apiKeyID})
	return s.getReturn, s.getErr
}
func (s *stubService) Revoke(ctx context.Context, req userkey.RevokeRequest) (userkey.RevokeResult, error) {
	s.revokeCalls = append(s.revokeCalls, req)
	return s.revokeReturn, s.revokeErr
}

// mountWithSession 把 ident 注入 request context 后挂载路由,模拟 SessionMiddleware 已跑过。
//
// 没有 session ident → 测 401 路径 (passIdent=false)。
func mountWithSession(t *testing.T, svc UserKeyService, ident sessionauth.SessionIdentity, passIdent bool) *chi.Mux {
	t.Helper()
	r := chi.NewMux()
	r.Route("/v1/api-keys", func(r chi.Router) {
		if passIdent {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := sessionauth.ContextWithSession(req.Context(), ident)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
		}
		MountUserAPIKeyRoutes(r, Deps{Service: svc})
	})
	return r
}

// T1: POST /v1/api-keys — 成功路径返 plaintext 且只此一次。
//
// 判别 fixture:断言 plaintext 字段在 response 里出现一次;若 mutation 把
// plaintext 字段也加进 List/Get 响应(回归泄密),T2/T4 (List/Get 不含 plaintext) 会变红。
func TestPostAPIKeys_Success(t *testing.T) {
	created := time.Now().UTC()
	svc := &stubService{
		issueReturn: userkey.IssueResult{
			APIKeyID:  101,
			Plaintext: "hk_live_super_secret_xyz_123456789",
			KeyPrefix: "hk_live_super_se",
			Status:    "active",
			CreatedAt: created,
		},
	}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	body := `{"name":"my-key","environment":"live"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-abc")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201; got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp createResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Plaintext != "hk_live_super_secret_xyz_123456789" {
		t.Fatalf("plaintext should appear once in create response; got %q", resp.Plaintext)
	}
	if resp.APIKeyID != 101 || resp.KeyPrefix != "hk_live_super_se" {
		t.Fatalf("response id/prefix drift: %+v", resp)
	}
	if !strings.Contains(resp.Notice, "shown only once") {
		t.Fatalf("notice missing one-time disclaimer: %q", resp.Notice)
	}
	if len(svc.issueCalls) != 1 {
		t.Fatalf("Issue should be called once; got %d", len(svc.issueCalls))
	}
	c := svc.issueCalls[0]
	if c.TenantID != 7 || c.UserID != 42 {
		t.Fatalf("tenant/user from session: want (7,42); got (%d,%d) — body MUST NOT override session", c.TenantID, c.UserID)
	}
	if c.Name != "my-key" {
		t.Fatalf("name drift: %s", c.Name)
	}
	if c.RequestID != "req-abc" {
		t.Fatalf("request_id drift: %s", c.RequestID)
	}
}

// T2: POST 用户身份**只来自 session**,body 的 tenant_id/user_id 字段被 DisallowUnknownFields 拒。
//
// 判别 fixture:这是 [[feedback_no_fake_pass]] 防御 — 防有人改 decodeJSON
// 取消 DisallowUnknownFields 后接受 body 的 tenant_id/user_id 越权字段。
// Mutation 自检:去掉 DisallowUnknownFields → 这个测试 red。
func TestPostAPIKeys_BodyTenantUserIDIgnored(t *testing.T) {
	svc := &stubService{
		issueReturn: userkey.IssueResult{
			APIKeyID:  102,
			Plaintext: "hk_live_some_token_value_12345678",
			KeyPrefix: "hk_live_some_tok",
			Status:    "active",
			CreatedAt: time.Now().UTC(),
		},
	}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	// 攻击 payload:body 试图 spoof tenant_id=999, user_id=666 越权签发
	body := `{"name":"spoof","tenant_id":999,"user_id":666}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// DisallowUnknownFields → 400 (含 tenant_id/user_id 未知字段)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body with extra tenant_id/user_id MUST be rejected; got status %d body=%s",
			rec.Code, rec.Body.String())
	}
	if len(svc.issueCalls) != 0 {
		t.Fatalf("Issue MUST NOT be called when body has unknown fields; got %d calls", len(svc.issueCalls))
	}
}

// T3: 没有 session → 401。
//
// 这是 fail-closed 防御 — 如果有人 mount 时漏了 session middleware,handler
// 必须仍能拒;不能默默把 UserID=0 当合法 caller。
func TestPostAPIKeys_NoSessionFails(t *testing.T) {
	svc := &stubService{}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{}, false)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401; got %d", rec.Code)
	}
	if len(svc.issueCalls) != 0 {
		t.Fatalf("Issue MUST NOT be called without session; got %d", len(svc.issueCalls))
	}
}

// T4: GET /v1/api-keys/ — list 调用把 session ident 透传给 service,offset/limit 校验。
func TestListAPIKeys_PassesSessionAndPagination(t *testing.T) {
	svc := &stubService{
		listReturn: []userkey.KeyDescriptor{
			{APIKeyID: 1, Name: "a", KeyPrefix: "hk_live_aaaaaaaa", Status: "active", CreatedAt: time.Now()},
			{APIKeyID: 2, Name: "b", KeyPrefix: "hk_live_bbbbbbbb", Status: "revoked", CreatedAt: time.Now()},
		},
	}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/api-keys/?offset=5&limit=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("count: want 2 got %d", resp.Count)
	}
	if len(svc.listCalls) != 1 {
		t.Fatalf("List called %d times; want 1", len(svc.listCalls))
	}
	c := svc.listCalls[0]
	if c.TenantID != 7 || c.UserID != 42 || c.Offset != 5 || c.Limit != 10 {
		t.Fatalf("List args drift: %+v", c)
	}
	// 关键:plaintext 字段绝不应出现在 list 响应里 (apiKeyView 没此字段,但要锁死)
	// 通过 raw JSON 检查 — 防有人加 *,interface{} 字段意外把内部 secret 序列化
	rawBytes, _ := json.Marshal(resp)
	if bytes.Contains(rawBytes, []byte("plaintext")) {
		t.Fatalf("list response MUST NOT contain plaintext field; got %s", string(rawBytes))
	}
}

// T5: GET /v1/api-keys/{id} — 404 时不区分"不存在"和"别人的"。
//
// 判别 fixture:Service 返 ErrNotFound,handler 返 404 + 公开 code 是
// "api_key_not_found"(不是 "forbidden" 不是 "wrong_owner")— 防 ID 枚举。
// Mutation 自检:改成返 403 → 攻击者可区分"键 ID 存在但归别人"还是"不存在"
// → ID 枚举攻击面打开 → 本测试 red。
func TestGetAPIKeys_NotFoundCodeIsGeneric(t *testing.T) {
	svc := &stubService{getErr: userkey.ErrNotFound}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/api-keys/999", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404; got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"api_key_not_found"`) {
		t.Fatalf("404 code must be api_key_not_found (generic); got %s", body)
	}
	if strings.Contains(body, "forbidden") || strings.Contains(body, "wrong_owner") {
		t.Fatalf("404 must not leak ownership distinction; got %s", body)
	}
}

// T6: GET 调用把 session (tenant, user) + path id 一起传给 service.Get。
//
// 判别 fixture:断言传入 tenant 和 user 来自 session ident,id 来自 path;
// Mutation 自检:把 ident.UserID 改成 0 / 把 path id 改成 0 → assertion 红。
func TestGetAPIKeys_ScopeFromSessionAndPath(t *testing.T) {
	svc := &stubService{getReturn: userkey.KeyDescriptor{APIKeyID: 55, Name: "x", KeyPrefix: "hk_live_xxxxxxxx", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/api-keys/55", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.getCalls) != 1 {
		t.Fatalf("Get called %d times; want 1", len(svc.getCalls))
	}
	c := svc.getCalls[0]
	if c.tenantID != 7 || c.userID != 42 || c.apiKeyID != 55 {
		t.Fatalf("Get scope drift: want (7,42,55); got (%d,%d,%d)", c.tenantID, c.userID, c.apiKeyID)
	}
}

// T7: DELETE /v1/api-keys/{id} — revoke 调用把 session 透传 + body reason 可选。
func TestDeleteAPIKeys_RevokesWithSessionScope(t *testing.T) {
	svc := &stubService{revokeReturn: userkey.RevokeResult{APIKeyID: 88, AlreadyRevoked: false}}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodDelete, "/v1/api-keys/88", strings.NewReader(`{"reason":"rotation"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.revokeCalls) != 1 {
		t.Fatalf("Revoke called %d times; want 1", len(svc.revokeCalls))
	}
	c := svc.revokeCalls[0]
	if c.TenantID != 7 || c.UserID != 42 || c.APIKeyID != 88 || c.Reason != "rotation" {
		t.Fatalf("revoke args drift: %+v", c)
	}
}

// T8: DELETE 已 revoked → 200 + already_revoked=true (幂等)。
func TestDeleteAPIKeys_IdempotentBody(t *testing.T) {
	svc := &stubService{revokeReturn: userkey.RevokeResult{APIKeyID: 88, AlreadyRevoked: true}}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodDelete, "/v1/api-keys/88", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200; got %d", rec.Code)
	}
	var resp revokeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.AlreadyRevoked {
		t.Fatalf("already_revoked must be true; got %+v", resp)
	}
}

// T9: ErrActiveKeyCapHit → 409 (Conflict),不是 503。
//
// 判别 fixture:cap 命中是用户输入问题不是后端故障;mapping 错 → 用户拿到 503
// 后无法理解原因。Mutation:把 cap mapping 改成 default 503 → 测试 red。
func TestPostAPIKeys_CapMappedToConflict(t *testing.T) {
	svc := &stubService{issueErr: userkey.ErrActiveKeyCapHit}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(`{"name":"too-many"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cap hit: want 409; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "active_key_cap_reached") {
		t.Fatalf("error code must be active_key_cap_reached; got %s", rec.Body.String())
	}
}

// T10: invalid expires_at (非 RFC3339) → 400。
func TestPostAPIKeys_InvalidExpiresAt(t *testing.T) {
	svc := &stubService{}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(`{"name":"x","expires_at":"tomorrow"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid expires_at: want 400; got %d", rec.Code)
	}
}

// T11: Service nil → 503 (fail-closed)。
//
// 判别 fixture:防 wiring 漏装 Service 时 handler 默默崩或返 200/204。
// Mutation 自检:去掉 resolveSession 里 d.Service == nil 检查 → service.Issue
// nil-pointer panic → 测试是 500 / panic 不是 503 → red。
func TestPostAPIKeys_NilServiceFailsClosed(t *testing.T) {
	mux := mountWithSession(t, nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	defer func() {
		// 不应 panic; 但如果发生了,要明显失败,不能默默吞
		if r := recover(); r != nil {
			t.Fatalf("nil service must not panic; got panic %v", r)
		}
	}()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service: want 503; got %d body=%s", rec.Code, rec.Body.String())
	}
}

// T12: 自定义 backend error (非已知错误) → 503 (fall-through)。
func TestErrorMappingFallthrough(t *testing.T) {
	svc := &stubService{issueErr: errors.New("unexpected db lost")}
	mux := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown err: want 503; got %d", rec.Code)
	}
}
