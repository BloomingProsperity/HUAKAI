package userkeyhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeyhttp"
)

// fakeKeyServicePatch 只桩出 Patch 方法;其余方法 panic 以捕获误用。
type fakeKeyServicePatch struct {
	patchResult userkey.PatchResult
	patchErr    error
	patchCalled *userkey.PatchRequest
}

func (f *fakeKeyServicePatch) Issue(_ context.Context, _ userkey.IssueRequest) (userkey.IssueResult, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) List(_ context.Context, _ userkey.ListRequest) ([]userkey.KeyDescriptor, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Count(_ context.Context, _, _ int64) (int, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Get(_ context.Context, _, _, _ int64) (userkey.KeyDescriptor, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Revoke(_ context.Context, _ userkey.RevokeRequest) (userkey.RevokeResult, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Patch(_ context.Context, req userkey.PatchRequest) (userkey.PatchResult, error) {
	f.patchCalled = &req
	return f.patchResult, f.patchErr
}

func buildPatchRouter(svc userkeyhttp.UserKeyService) chi.Router {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident := sessionauth.SessionIdentity{TenantID: 1, UserID: 2}
			ctx := sessionauth.ContextWithSession(r.Context(), ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	userkeyhttp.MountUserAPIKeyRoutes(r, userkeyhttp.Deps{Service: svc})
	return r
}

// TestKeyPatchPartial 是 KEY-026 的判别性测试。
//
// 变异:PATCH 重置了被省略的字段(例如未提供 status 时把 status 设为 "")->
// service 收到非 nil 的 Status -> status 被清空 -> 红。
func TestKeyPatchPartial(t *testing.T) {
	const keyID = "42"
	wantName := "new-name"
	wantStatus := "active"

	svc := &fakeKeyServicePatch{
		patchResult: userkey.PatchResult{APIKeyID: 42, Name: wantName, Status: wantStatus},
	}
	r := buildPatchRouter(svc)

	// 只带 name 的 PATCH —— 发给 service 的请求中 status 绝不能被设置。
	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPatch, "/"+keyID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 验证 service 收到的 Status 为 nil(只改 name 的 patch)。
	if svc.patchCalled == nil {
		t.Fatal("Patch was not called")
	}
	if svc.patchCalled.Status != nil {
		t.Errorf("MUTATION: Status must be nil when not provided in PATCH body, got %q", *svc.patchCalled.Status)
	}
	if svc.patchCalled.Name == nil || *svc.patchCalled.Name != "new-name" {
		t.Errorf("Name mismatch: got %v", svc.patchCalled.Name)
	}

	// 验证响应 body
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != wantName {
		t.Errorf("response name: want %q, got %v", wantName, resp["name"])
	}
	if resp["status"] != wantStatus {
		t.Errorf("response status: want %q, got %v", wantStatus, resp["status"])
	}
}

func TestKeyPatchBothFields(t *testing.T) {
	svc := &fakeKeyServicePatch{
		patchResult: userkey.PatchResult{APIKeyID: 5, Name: "n", Status: "revoked"},
	}
	r := buildPatchRouter(svc)

	body, _ := json.Marshal(map[string]string{"name": "n", "status": "revoked"})
	req := httptest.NewRequest(http.MethodPatch, "/5", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.patchCalled.Name == nil || *svc.patchCalled.Name != "n" {
		t.Errorf("Name not set correctly")
	}
	if svc.patchCalled.Status == nil || *svc.patchCalled.Status != "revoked" {
		t.Errorf("Status not set correctly")
	}
}

// expires_at 三态解码遵守 CLAUDE.md #16。handler 必须把线上的 *string
// 精确转换为 service 的「值 + 清除标志」二分形式。

// SET:非空 RFC3339 字符串 -> service 收到解析出的截止时间,ClearExpiry=false。
// 变异:handler 丢弃 expires_at / 解析错误 -> ExpiresAt 为 nil -> 红。
func TestKeyPatchExpiresAtSet(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"2027-01-02T03:04:05Z"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled == nil {
		t.Fatal("Patch not called")
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be false for a set")
	}
	if svc.patchCalled.ExpiresAt == nil {
		t.Fatalf("ExpiresAt must be non-nil for a set")
	}
	want := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if !svc.patchCalled.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt=%v want %v", svc.patchCalled.ExpiresAt, want)
	}
}

// CLEAR:空字符串 -> service 收到 ClearExpiry=true、ExpiresAt=nil。
// 变异:handler 把 "" 当作 set 或解析错误 -> ClearExpiry false / 400 -> 红。
func TestKeyPatchExpiresAtClear(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if !svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be true for empty string")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("ExpiresAt must be nil for clear, got %v", svc.patchCalled.ExpiresAt)
	}
}

// UNCHANGED:省略 expires_at -> ClearExpiry=false 且 ExpiresAt=nil(不会误清除)。
// 变异:handler 在省略时默认 clear=true 或凭空捏造一个截止时间 -> 红。这正好守护设计要
// 规避的陷阱(省略时绝不能触碰截止时间)。
func TestKeyPatchExpiresAtUnchanged(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "x", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be false when expires_at omitted (no accidental clear)")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("ExpiresAt must be nil when expires_at omitted, got %v", svc.patchCalled.ExpiresAt)
	}
}

// INVALID:非空且非 RFC3339 的字符串 -> 400 invalid_expires_at,service 不被调用。
// 变异:handler 把坏字符串转发给 service / 返回 200 -> 红。
func TestKeyPatchExpiresAtInvalid(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"nonsense"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_expires_at") {
		t.Errorf("body=%s want invalid_expires_at code", w.Body.String())
	}
	if svc.patchCalled != nil {
		t.Errorf("service must NOT be called on parse failure (no partial write)")
	}
}

// RESPONSE:PatchResult 的截止时间作为 expires_at 回显。
// 变异:handler 从 patchResponse 丢掉 ExpiresAt -> body 缺少该时间戳 -> 红。
func TestKeyPatchExpiresAtResponse(t *testing.T) {
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active", ExpiresAt: &exp}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"2027-01-02T03:04:05Z"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "2027-01-02T03:04:05Z") {
		t.Errorf("response body=%s want expires_at echoed", w.Body.String())
	}
}

// CLEAR RESPONSE:已清除(永不过期)的 key 必须从 body 中省略 expires_at。
// 这个省略依赖 nil 的 *time.Time + `json:"...,omitempty"`(handlers.go 的 patchResponse)。
// 变异:去掉 ,omitempty(会输出 "expires_at":null),或把字段改为值类型 time.Time
// (会输出 "0001-01-01T00:00:00Z",前端会把它渲染成一个真实的过去截止时间)-> body
// 含 expires_at -> 红。已有的 SET 测试钉死了「存在」的情形;本测试钉死「省略」的情形。
func TestKeyPatchExpiresAtClearResponseOmits(t *testing.T) {
	// patchResult.ExpiresAt 为 nil = 清除后(永不过期)的状态。
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "expires_at") {
		t.Errorf("cleared/never-expiring key must omit expires_at, got %s", w.Body.String())
	}
}

// NULL == ABSENT:显式的 JSON null 必须与省略 expires_at 表现完全一致(保持不变)。
// 因为 patchRequest.ExpiresAt 是 *string,null 解码为 nil 指针,与省略在字节层面一致。
// 变异:将来若改用 json.RawMessage / 自定义 unmarshaler 把 JSON null 当作 clear ->
// ClearExpiry true -> 红。钉死 handler/OpenAPI 承诺的 null==absent 契约。
func TestKeyPatchExpiresAtNullIsUnchanged(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":null}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("JSON null must mean unchanged, not clear")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("JSON null must leave ExpiresAt nil, got %v", svc.patchCalled.ExpiresAt)
	}
}
