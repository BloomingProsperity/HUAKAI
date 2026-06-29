package adminhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// adminTokenIssuerStub 是 adminTokensIssuer 接口的可控 fake。enforceRBAC=true
// 时,会像生产 issuer 那样对非 platform_admin 返回 ErrAdminForbidden,
// 用于验证 handler 把 403 正确透出且不放宽。
type adminTokenIssuerStub struct {
	enforceRBAC bool

	issueResult admin.TokenIssueResult
	issueErr    error
	issueCalled bool
	issueGot    admin.TokenIssueRequest

	revokeResult admin.TokenRevokeResult
	revokeErr    error
	revokeCalled bool
	revokeGot    admin.TokenRevokeRequest

	listItems  []admin.TokenListItem
	listErr    error
	listCalled bool
	listCaller admin.AdminIdentity
}

func (s *adminTokenIssuerStub) IssueToken(_ context.Context, req admin.TokenIssueRequest) (admin.TokenIssueResult, error) {
	s.issueCalled = true
	s.issueGot = req
	if s.enforceRBAC && req.Caller.Role != admin.RolePlatformAdmin {
		return admin.TokenIssueResult{}, admin.ErrAdminForbidden
	}
	if s.issueErr != nil {
		return admin.TokenIssueResult{}, s.issueErr
	}
	if s.issueResult.TokenID == 0 {
		s.issueResult = admin.TokenIssueResult{
			TokenID:   77,
			Plaintext: "hk_admin_plaintext_shown_once",
			KeyPrefix: "hk_admin_plainte",
			Role:      req.Role,
			Status:    "active",
			CreatedAt: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
		}
	}
	return s.issueResult, nil
}

func (s *adminTokenIssuerStub) RevokeToken(_ context.Context, req admin.TokenRevokeRequest) (admin.TokenRevokeResult, error) {
	s.revokeCalled = true
	s.revokeGot = req
	if s.enforceRBAC && req.Caller.Role != admin.RolePlatformAdmin {
		return admin.TokenRevokeResult{}, admin.ErrAdminForbidden
	}
	if s.revokeErr != nil {
		return admin.TokenRevokeResult{}, s.revokeErr
	}
	if s.revokeResult.TokenID == 0 {
		s.revokeResult.TokenID = req.TokenID
	}
	return s.revokeResult, nil
}

func (s *adminTokenIssuerStub) ListTokens(_ context.Context, caller admin.AdminIdentity, _ int32, _ int32) ([]admin.TokenListItem, error) {
	s.listCalled = true
	s.listCaller = caller
	if s.enforceRBAC && caller.Role != admin.RolePlatformAdmin {
		return nil, admin.ErrAdminForbidden
	}
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listItems, nil
}

func invokeAdminTokens(t *testing.T, deps AdminTokensDeps, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/admin-tokens", func(r chi.Router) {
		MountAdminTokenRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, adminAPIKeyReader(t, body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// 签发:鉴权 / RBAC / 校验 / happy path
// -----------------------------------------------------------------------------

func TestAdminTokenIssueHandler(t *testing.T) {
	t.Run("缺鉴权返回 401 且不触发 issuer", func(t *testing.T) {
		iss := &adminTokenIssuerStub{}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/", `{"role":"platform_admin"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusUnauthorized)
		if iss.issueCalled {
			t.Fatal("未授权请求竟触发了 issuer")
		}
	})

	t.Run("tenant_operator 签发被拒 403(越权守卫)", func(t *testing.T) {
		iss := &adminTokenIssuerStub{enforceRBAC: true}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: tenantOperator(7)},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/", `{"role":"platform_admin"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
		// 身份必须取自鉴权上下文,不信 body;issuer 收到的 Caller 是 operator。
		if !iss.issueCalled || iss.issueGot.Caller.Role != admin.RoleTenantOperator {
			t.Fatalf("issuer 未收到鉴权身份: called=%v got=%+v", iss.issueCalled, iss.issueGot.Caller)
		}
	})

	t.Run("过去的 expires_at 返回 400 且不触发 issuer", func(t *testing.T) {
		iss := &adminTokenIssuerStub{}
		past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/", `{"role":"platform_admin","expires_at":"`+past+`"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
		if iss.issueCalled {
			t.Fatal("过去时间戳竟触发了 issuer")
		}
	})

	t.Run("happy path 返回 201 + 一次性明文 + once-only 头", func(t *testing.T) {
		iss := &adminTokenIssuerStub{}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/", `{"role":"platform_admin","note":"rotate"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusCreated)
		if got := rec.Header().Get("X-Huakai-Key-Display"); got != "once-only" {
			t.Fatalf("X-Huakai-Key-Display=%q want once-only", got)
		}
		var body tokenIssueResponseBody
		decodeAdminAPIKeyBody(t, rec, &body)
		if body.ID != 77 || body.PlaintextBearer == "" || body.Role != "platform_admin" {
			t.Fatalf("签发响应不符: %+v", body)
		}
		if iss.issueGot.Note != "rotate" {
			t.Fatalf("note 未透传: %+v", iss.issueGot)
		}
	})
}

// -----------------------------------------------------------------------------
// 列举:secret-mask(绝不漏明文/hash)
// -----------------------------------------------------------------------------

func TestAdminTokenListHandler_NoSecretLeak(t *testing.T) {
	iss := &adminTokenIssuerStub{
		listItems: []admin.TokenListItem{{
			ID:        9,
			Name:      "ci-token",
			KeyPrefix: "hk_admin_abcdef0",
			Role:      "platform_admin",
			Bootstrap: false,
			Status:    "active",
			CreatedAt: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
		}},
	}
	rec := invokeAdminTokens(t, AdminTokensDeps{
		Auth:   apiKeyAuthStub{ident: platformAdmin()},
		Issuer: iss,
	}, http.MethodGet, "/admin/v1/admin-tokens/", nil)
	assertAdminAPIKeyStatus(t, rec, http.StatusOK)

	raw := rec.Body.String()
	// 区分性断言:正常元数据(prefix)应出现,证明确实序列化了列表。
	if !strings.Contains(raw, "hk_admin_abcdef0") {
		t.Fatalf("列举响应未含 key_prefix,列表序列化失效: %s", raw)
	}
	// secret-mask:响应体绝不能出现 key_hash / plaintext 这类字段名。
	// 若有人给 tokenListItemBody 误加了 key_hash/plaintext_bearer 字段,
	// 本断言会变红。
	for _, leaked := range []string{"key_hash", "plaintext", "bcrypt", "$2a$", "$2b$"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("列举响应泄露了 %q: %s", leaked, raw)
		}
	}
}

func TestAdminTokenListHandler_ForbiddenForOperator(t *testing.T) {
	iss := &adminTokenIssuerStub{enforceRBAC: true}
	rec := invokeAdminTokens(t, AdminTokensDeps{
		Auth:   apiKeyAuthStub{ident: tenantOperator(7)},
		Issuer: iss,
	}, http.MethodGet, "/admin/v1/admin-tokens/", nil)
	assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
}

// -----------------------------------------------------------------------------
// 吊销:id 校验 / 幂等 / RBAC
// -----------------------------------------------------------------------------

func TestAdminTokenRevokeHandler(t *testing.T) {
	t.Run("非法 id 返回 400", func(t *testing.T) {
		iss := &adminTokenIssuerStub{}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/0/revoke", `{}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
		if iss.revokeCalled {
			t.Fatal("非法 id 竟触发了 revoker")
		}
	})

	t.Run("tenant_operator 吊销被拒 403", func(t *testing.T) {
		iss := &adminTokenIssuerStub{enforceRBAC: true}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: tenantOperator(7)},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/5/revoke", `{"reason":"x"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
	})

	t.Run("happy path 幂等返回 200", func(t *testing.T) {
		iss := &adminTokenIssuerStub{revokeResult: admin.TokenRevokeResult{TokenID: 5, AlreadyRevoked: true}}
		rec := invokeAdminTokens(t, AdminTokensDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: iss,
		}, http.MethodPost, "/admin/v1/admin-tokens/5/revoke", `{"reason":"rotate"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusOK)
		var body tokenRevokeResponseBody
		decodeAdminAPIKeyBody(t, rec, &body)
		if body.ID != 5 || !body.AlreadyRevoked {
			t.Fatalf("吊销响应不符: %+v", body)
		}
		if iss.revokeGot.TokenID != 5 || iss.revokeGot.Reason != "rotate" {
			t.Fatalf("revoker 未收到正确入参: %+v", iss.revokeGot)
		}
	})
}
