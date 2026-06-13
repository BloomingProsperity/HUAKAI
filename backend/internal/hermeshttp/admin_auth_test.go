package hermeshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// fakeAdminResolver injects a fixed identity / error, standing in for
// *admin.AdminResolver so the middleware RBAC is testable without a DB.
type fakeAdminResolver struct {
	identity admin.AdminIdentity
	err      error
	called   bool
}

func (f *fakeAdminResolver) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	f.called = true
	return f.identity, f.err
}

// captureNext records the identity + admin actor the middleware injected so a
// success case can assert the threaded context, and returns 200.
type captureNext struct {
	gotIdentity sessionauth.Identity
	gotActor    adminActor
	gotActorOK  bool
	served      bool
}

func (c *captureNext) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.served = true
	if id, ok := r.Context().Value(authContextKey{}).(sessionauth.Identity); ok {
		c.gotIdentity = id
	}
	c.gotActor, c.gotActorOK = adminActorFromContext(r.Context())
}

func runAdminMiddleware(resolver AdminAuthResolver, path string) (*httptest.ResponseRecorder, *captureNext) {
	next := &captureNext{}
	h := AdminAuthMiddleware(resolver)(next)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, next
}

func TestAdminAuthMiddlewareRejectsCredentialFailure(t *testing.T) {
	// Regression (mutation: revert the routes.go middleware swap back to
	// APIKeyMiddleware): a request whose admin credential fails to resolve must
	// be rejected 401 and must never reach a Hermes handler.
	rec, next := runAdminMiddleware(&fakeAdminResolver{err: admin.ErrAdminUnauthorized}, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler was reached despite unauthorized admin credential")
	}
}

func TestAdminAuthMiddlewareBackendErrorIs503(t *testing.T) {
	// Regression: a transient datastore failure must fail-closed as 503, not be
	// mistaken for a 401 (which would invite credential-enumeration retries) or
	// silently pass.
	rec, next := runAdminMiddleware(&fakeAdminResolver{err: admin.ErrAdminBackend}, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached on backend error")
	}
}

func TestAdminAuthMiddlewareNilResolverIs503(t *testing.T) {
	// Regression: an unconfigured resolver must fail-closed (503), never expose
	// Hermes unauthenticated.
	rec, next := runAdminMiddleware(nil, "/conversations?tenant_id=7&as_user_id=42")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached with nil resolver")
	}
}

func TestAdminAuthMiddlewareTenantOperatorCrossTenant403(t *testing.T) {
	// Regression (mutation: drop the CanIssueForTenant / scope-mismatch check): a
	// tenant_operator scoped to tenant 7 requesting tenant 9's resource must be
	// rejected 403 and must not reach a handler.
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 100, Role: admin.RoleTenantOperator, ScopeTenantID: 7,
	}}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=9&as_user_id=42")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached for cross-tenant operator request")
	}
}

func TestAdminAuthMiddlewarePlatformAdminRequiresTenantParam(t *testing.T) {
	// Regression: a platform_admin has no implicit tenant; omitting ?tenant_id
	// must be a 400, never a silent cross-tenant default that leaks into a
	// tenant-scoped handler.
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 200, Role: admin.RolePlatformAdmin,
	}}
	rec, next := runAdminMiddleware(resolver, "/conversations?as_user_id=42")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached for platform_admin without tenant_id")
	}
}

func TestAdminAuthMiddlewareRequiresAsUserID(t *testing.T) {
	// Regression: admin-mode requires ?as_user_id so the threaded user id
	// resolves the users FK; omitting it must be a 400, not a write with a zero
	// user id that violates the FK at the DB layer.
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 300, Role: admin.RoleTenantOperator, ScopeTenantID: 7,
	}}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=7")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if next.served {
		t.Fatalf("handler reached without as_user_id")
	}
}

func TestAdminAuthMiddlewareOperatorSuccessInjectsScopedIdentityAndActor(t *testing.T) {
	// Regression: on success the middleware must thread the operator's scoped
	// tenant + the requested as_user_id, and record the operator token id/role
	// for audit attribution. A mutation that drops the actor injection makes
	// gotActorOK false; one that ignores ScopeTenantID changes gotIdentity.
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 400, Role: admin.RoleTenantOperator, ScopeTenantID: 7,
	}}
	rec, next := runAdminMiddleware(resolver, "/conversations?as_user_id=42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !next.served {
		t.Fatalf("handler was not reached on authorized operator request")
	}
	if next.gotIdentity.TenantID != 7 || next.gotIdentity.UserID != 42 {
		t.Fatalf("identity=%+v want tenant=7 user=42", next.gotIdentity)
	}
	if !next.gotActorOK || next.gotActor.TokenID != 400 || next.gotActor.Role != admin.RoleTenantOperator {
		t.Fatalf("admin actor=%+v ok=%v want token=400 role=%s", next.gotActor, next.gotActorOK, admin.RoleTenantOperator)
	}
}

func TestWithAdminActorFoldsAttributionOnlyInAdminMode(t *testing.T) {
	// Regression: the audit args must gain admin_actor_id + admin_role when (and
	// only when) an admin actor is in context, so the trail attributes the real
	// operator. Mutation: dropping the fold leaves the args without the operator
	// keys; folding in end-user mode would wrongly tag normal traffic.
	base := map[string]any{"conversation_id": int64(1002)}

	endUser := withAdminActor(context.Background(), base)
	if _, ok := endUser["admin_actor_id"]; ok {
		t.Fatalf("end-user args unexpectedly carry admin attribution: %v", endUser)
	}

	ctx := context.WithValue(context.Background(), adminActorContextKey{}, adminActor{TokenID: 77, Role: admin.RoleTenantOperator})
	adminArgs := withAdminActor(ctx, base)
	if adminArgs["admin_actor_id"] != int64(77) || adminArgs["admin_role"] != admin.RoleTenantOperator {
		t.Fatalf("admin args=%v want admin_actor_id=77 role=%s", adminArgs, admin.RoleTenantOperator)
	}
	// The original map must not be mutated (it is reused on error paths).
	if _, ok := base["admin_actor_id"]; ok {
		t.Fatalf("withAdminActor mutated the caller's args map: %v", base)
	}

	// DISCRIMINATING: attribution must survive the REAL persistence path
	// (RecordAudit applies hermes.SanitizeArgs before writing). The operator id
	// is a non-secret admin_tokens row PK and MUST NOT be redacted. This is the
	// guard the prior test missed (it asserted only on pre-sanitize output).
	persisted := hermes.SanitizeArgs(adminArgs)
	if persisted["admin_actor_id"] != int64(77) {
		t.Fatalf("admin_actor_id did not survive SanitizeArgs: got %v (operator attribution silently dropped — key must not match the sensitive 'token' substring)", persisted["admin_actor_id"])
	}
	if persisted["admin_role"] != admin.RoleTenantOperator {
		t.Fatalf("admin_role did not survive SanitizeArgs: got %v", persisted["admin_role"])
	}
	// Proof the rename matters: the old *_token_id name WOULD be redacted by the
	// sanitizer, which is exactly the defect this fix closes.
	redacted := hermes.SanitizeArgs(map[string]any{"admin_actor_token_id": int64(77)})
	if redacted["admin_actor_token_id"] != "[REDACTED]" {
		t.Fatalf("expected a *_token_id key to be redacted by the sanitizer, got %v", redacted["admin_actor_token_id"])
	}
}

func TestAdminAuthMiddlewarePlatformAdminCrossTenantAllowedWithParam(t *testing.T) {
	// Regression: a platform_admin may act on an explicit tenant; the scoped
	// tenant must be the ?tenant_id value, not the operator's (absent) scope.
	resolver := &fakeAdminResolver{identity: admin.AdminIdentity{
		TokenID: 500, Role: admin.RolePlatformAdmin,
	}}
	rec, next := runAdminMiddleware(resolver, "/conversations?tenant_id=9&as_user_id=42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if next.gotIdentity.TenantID != 9 || next.gotIdentity.UserID != 42 {
		t.Fatalf("identity=%+v want tenant=9 user=42", next.gotIdentity)
	}
}
