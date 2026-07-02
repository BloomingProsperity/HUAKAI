package adminquotahttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

// fakeQuotaStore:非 nil 后端,良性零值——让 handler 越过鉴权前 nil 后端 503 兜底走到真鉴权。
type fakeQuotaStore struct{}

func (fakeQuotaStore) ListQuotaPoliciesForAdmin(context.Context, dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error) {
	return nil, nil
}
func (fakeQuotaStore) GetQuotaPolicyByID(context.Context, dbquota.GetQuotaPolicyByIDParams) (dbquota.QuotaPolicy, error) {
	return dbquota.QuotaPolicy{}, nil
}
func (fakeQuotaStore) CreateQuotaPolicyWithAudit(context.Context, quotaPolicyCreateParams, auditInput) (dbquota.QuotaPolicy, error) {
	return dbquota.QuotaPolicy{}, nil
}
func (fakeQuotaStore) UpdateQuotaPolicyWithAudit(context.Context, quotaPolicyUpdateParams, auditInput) (dbquota.QuotaPolicy, error) {
	return dbquota.QuotaPolicy{}, nil
}
func (fakeQuotaStore) DeleteQuotaPolicyWithAudit(context.Context, quotaPolicyDeleteParams, auditInput) (int64, error) {
	return 0, nil
}

func mountQuota() http.Handler {
	r := chi.NewRouter()
	MountQuotaPolicyRoutes(r, Deps{Auth: adminsessionauthtest.Resolver(), Store: fakeQuotaStore{}})
	return r
}

// SessionSafe:配额策略创建/更新过鉴权≠401;token-only 的删除对 session fail-closed 401。
// 变异:摘 POST/PUT 的 safe → 该路由 session 写 401 → 首断言 RED;给 DELETE 误挂 safe → 不再 401 → 次断言 RED。
func TestQuotaPolicyWriteGate(t *testing.T) {
	h := mountQuota()
	sess := adminsessionauthtest.SessionBearer
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/admin/v1/quota-policies"},
		{http.MethodPut, "/admin/v1/quota-policies/5"},
	} {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, sess); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写 %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
	// token-only:删除配额策略(分级表对抗验证降档)对 session-admin fail-closed 401。
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/admin/v1/quota-policies/5", sess); code != http.StatusUnauthorized {
		t.Fatalf("token-only DELETE 对 session-admin 应 fail-closed 401,得 %d", code)
	}
}

// token 通道豁免:hk_admin 令牌写 token-only 删除也过鉴权≠401。
func TestQuotaPolicyTokenExempt(t *testing.T) {
	h := mountQuota()
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/admin/v1/quota-policies/5", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写 token-only 删除应过鉴权(≠401),得 401")
	}
}
