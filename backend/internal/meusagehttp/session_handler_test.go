package meusagehttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// 会话级用量端点:身份只来自会话上下文,按 user_id 收敛(不按单个 api_key)。
func TestSessionHandlerScopesToSessionUser(t *testing.T) {
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		{ID: 1, TenantID: 7, UserID: 42, APIKeyID: 100, RequestedModel: "claude", ActualCost: decimal.RequireFromString("0.01"), CreatedAt: pgtype.Timestamptz{Valid: true}},
	}}
	h := NewSessionHandler(store)

	// 故意带 ?user_id=999 与 ?api_key_id=888:处理器必须忽略请求里的身份输入,只用会话身份。
	req := httptest.NewRequest(http.MethodGet, "/v1/me/usage-records?user_id=999&api_key_id=888", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200;体=%s", rec.Code, rec.Body.String())
	}
	// 收敛维度必须是会话 user_id=42,且绝不按 api_key 收敛(那是 API-key 端点的事)。
	if store.listArg.UserID == nil || *store.listArg.UserID != 42 {
		t.Fatalf("UserID 过滤=%v 期望 *42(身份须来自会话非请求)", store.listArg.UserID)
	}
	if store.listArg.APIKeyID != nil {
		t.Fatalf("APIKeyID 应为 nil(会话端点不按单 key 收敛),实得 %v", *store.listArg.APIKeyID)
	}
	if store.listArg.TenantID == nil || *store.listArg.TenantID != 7 {
		t.Fatalf("TenantID 过滤=%v 期望 *7", store.listArg.TenantID)
	}
	var resp listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items=%d 期望 1", len(resp.Items))
	}
}

// 无会话身份(未经 SessionMiddleware)→ 401,且不触达 store。
func TestSessionHandlerRequiresSession(t *testing.T) {
	store := &usageStoreStub{}
	h := NewSessionHandler(store)
	req := httptest.NewRequest(http.MethodGet, "/v1/me/usage-records", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d 期望 401", rec.Code)
	}
	if store.listArg.TenantID != nil {
		t.Fatalf("无会话时不应查询 store")
	}
}

// 会话身份不完整(TenantID/UserID <= 0)→ 401(失败闭合)。
func TestSessionHandlerRejectsIncompleteSession(t *testing.T) {
	store := &usageStoreStub{}
	h := NewSessionHandler(store)
	req := httptest.NewRequest(http.MethodGet, "/v1/me/usage-records", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 0}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d 期望 401(UserID<=0 须拒)", rec.Code)
	}
}
