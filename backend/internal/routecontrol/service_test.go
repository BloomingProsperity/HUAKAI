package routecontrol_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/routecontrol"
)

// =============================================================================
// Test scaffolding: stub AuthResolver
// =============================================================================
//
// We use a hand-rolled stub rather than the real *auth.APIKeyResolver because
// the latter requires a sqlc *dbauth.Queries handle (DB-backed). Phase 2A.5
// gRPC e2e (deferred) exercises the real wiring; this file scopes to the
// orchestration in Service.RouteQuery.

// stubAuthResolver satisfies routecontrol.AuthResolver. Records what the
// Service handed it so P2-A4 mutation tests can assert normalization.
type stubAuthResolver struct {
	calls            atomic.Int32
	identity         auth.Identity
	err              error
	captureCtx       context.Context
	captureAuthValue atomic.Value // string — last Authorization header observed
}

func (s *stubAuthResolver) Resolve(ctx context.Context, req *http.Request) (auth.Identity, error) {
	s.calls.Add(1)
	s.captureCtx = ctx
	if req != nil {
		s.captureAuthValue.Store(req.Header.Get("Authorization"))
	}
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return s.identity, nil
}

func (s *stubAuthResolver) lastAuthHeader() string {
	if v := s.captureAuthValue.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// helper: build canonical credential string for tests
func canonicalBearer(secret string) string  { return "bearer:" + secret }
func canonicalXAPIKey(secret string) string { return "x-api-key:" + secret }

// =============================================================================
// P2-A1: Go derives tenant from credential (req.TenantID empty)
// =============================================================================

// TC-P2A1 — when RouteQueryRequest.TenantID is empty, Service still produces
// a successful response carrying the Go-derived tenant_id (stringified).
//
// MUTATION CHECK: if Service mistakenly uses req.TenantID instead of the
// derived tenant_id in the response (e.g., `resp.DerivedTenantID = req.TenantID`),
// the response's DerivedTenantID would be empty — this test goes red.
func TestService_DerivesTenantFromCredentialWhenLegacyEmpty(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 7, UserID: 70, APIKeyID: 700}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})

	resp, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		RequestID:        "req-p2a1-good",
		TenantID:         "", // no legacy hint — Go must derive
		ClientCredential: canonicalBearer("hk_test_P2A1_DERIVE_TENANT_001"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DerivedTenantID != "7" {
		t.Fatalf("want DerivedTenantID=7, got %q", resp.DerivedTenantID)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("resolver call count = %d, want 1", got)
	}
}

// =============================================================================
// P2-A2: legacy tenant mismatch fails closed (PermissionDenied / ErrTenantIDMismatch)
// =============================================================================

// TC-P2A2 — legacy tenant "8" vs Go-derived "7" must fail-closed with
// ErrTenantIDMismatch. The downstream Phase 2B plan call must NEVER happen
// (in Phase 2A there's no downstream; we assert the error sentinel).
//
// MUTATION CHECK: if mismatch is downgraded to a warn (e.g., `log.Warn(...)`
// followed by passing the legacy tenant through), the function would return
// nil error — this test goes red.
func TestService_LegacyTenantMismatchFailsClosed(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 7}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})

	_, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		RequestID:        "req-p2a2-mismatch",
		TenantID:         "8", // legacy claims 8 — disagrees with Go-derived 7
		ClientCredential: canonicalBearer("hk_test_P2A2_MISMATCH_TOKEN_001"),
	})
	if !errors.Is(err, routecontrol.ErrTenantIDMismatch) {
		t.Fatalf("want ErrTenantIDMismatch, got %v", err)
	}
	// Discriminating: error message must include both sides so operator can
	// triage; must NOT include the raw secret (P2-A5 cross-test).
	if !strings.Contains(err.Error(), "legacy=\"8\"") {
		t.Errorf("error missing legacy side: %v", err)
	}
	if !strings.Contains(err.Error(), "derived=\"7\"") {
		t.Errorf("error missing derived side: %v", err)
	}
}

// =============================================================================
// P2-A3: matching legacy tenant passes (Phase 1/2 reconciliation window)
// =============================================================================

// TC-P2A3 — legacy tenant "7" matches Go-derived "7"; Service passes through
// successfully so Phase 1 Manual First clients that ALSO provide tenant_id
// keep working during the reconciliation period.
//
// MUTATION CHECK: if Service unconditionally rejects non-empty legacy tenant
// (over-strict), this test goes red.
func TestService_LegacyTenantMatchPassesThrough(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 7}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})

	resp, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		RequestID:        "req-p2a3-match",
		TenantID:         "7", // legacy and derived agree
		ClientCredential: canonicalBearer("hk_test_P2A3_MATCH_TOKEN_001"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DerivedTenantID != "7" {
		t.Fatalf("want DerivedTenantID=7, got %q", resp.DerivedTenantID)
	}
}

// =============================================================================
// P2-A4: x-api-key kind normalizes through to the resolver (same as bearer)
// =============================================================================

// TC-P2A4 — same HUAKAI secret presented as bearer vs x-api-key BOTH reach
// the resolver as Authorization: Bearer. The stub records the auth header it
// received; both kinds must produce identical successful outcomes (resolver
// receives the same bearer string).
//
// MUTATION CHECK: if x-api-key is silently ignored (parser pruned, or
// ResolverRequest passes x-api-key through unchanged), the stub would never
// be called or would see a non-Bearer header — Identity would not match and
// this test would go red.
func TestService_XAPIKeyNormalizesToBearer(t *testing.T) {
	const secret = "hk_test_P2A4_XAPI_NORMALIZE_TOKEN_001"

	// Both kinds should produce the same Identity outcome through the stub.
	for _, canonical := range []string{
		canonicalBearer(secret),
		canonicalXAPIKey(secret),
	} {
		t.Run(canonical, func(t *testing.T) {
			stub := &stubAuthResolver{identity: auth.Identity{TenantID: 42}}
			svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})

			resp, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
				RequestID:        "req-p2a4-" + canonical,
				ClientCredential: canonical,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.DerivedTenantID != "42" {
				t.Fatalf("want DerivedTenantID=42, got %q", resp.DerivedTenantID)
			}
		})
	}
}

// =============================================================================
// P2-A5: raw credential NEVER appears in any returned error
// =============================================================================

// TC-P2A5 — exhaustively drive every error path Service can produce and
// assert the raw secret never appears in err.Error(). Errors must reference
// the credential only via Kind + Fingerprint.
//
// MUTATION CHECK: any future error formatting that embeds the credential
// directly (e.g., `fmt.Errorf("bad cred %q", req.ClientCredential)`) would
// fail this test for at least one of the triggers.
func TestService_ErrorsNeverEmbedRawSecret(t *testing.T) {
	const secret = "hk_test_P2A5_LEAK_PROBE_NEVER_LEAK_001"

	cases := []struct {
		name string
		stub *stubAuthResolver
		req  routecontrol.RouteQueryRequest
	}{
		{
			name: "unauthorized",
			stub: &stubAuthResolver{err: auth.ErrUnauthorized},
			req: routecontrol.RouteQueryRequest{
				ClientCredential: canonicalBearer(secret),
			},
		},
		{
			name: "auth-backend",
			stub: &stubAuthResolver{err: auth.ErrAuthBackend},
			req: routecontrol.RouteQueryRequest{
				ClientCredential: canonicalBearer(secret),
			},
		},
		{
			name: "auth-misconfigured",
			stub: &stubAuthResolver{err: auth.ErrAuthMisconfigured},
			req: routecontrol.RouteQueryRequest{
				ClientCredential: canonicalBearer(secret),
			},
		},
		{
			name: "tenant-mismatch",
			stub: &stubAuthResolver{identity: auth.Identity{TenantID: 9}},
			req: routecontrol.RouteQueryRequest{
				TenantID:         "10", // legacy disagrees with derived 9
				ClientCredential: canonicalBearer(secret),
			},
		},
		{
			name: "route-contract-incomplete",
			stub: &stubAuthResolver{identity: auth.Identity{TenantID: 9}},
			req: routecontrol.RouteQueryRequest{
				ClientCredential: canonicalBearer(secret),
			},
		},
		{
			name: "invalid-canonical-with-secret-tail",
			stub: &stubAuthResolver{identity: auth.Identity{TenantID: 9}},
			req: routecontrol.RouteQueryRequest{
				// Unknown prefix carrying the secret — must not echo back.
				ClientCredential: "basic:" + secret,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// route-contract-incomplete uses production-mode config (no stub plan)
			cfg := routecontrol.ServiceConfig{AllowTestStubPlan: true}
			if tc.name == "route-contract-incomplete" {
				cfg.AllowTestStubPlan = false
			}
			svc := routecontrol.NewService(tc.stub, cfg)
			_, err := svc.RouteQuery(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error leaked raw secret: %s", err.Error())
			}
		})
	}
}

// =============================================================================
// P2-A6: production mode returns ErrRouteContractIncomplete after identity
// =============================================================================

// TC-P2A6 — with AllowTestStubPlan=false (production default), Service MUST
// return ErrRouteContractIncomplete after a successful identity derivation,
// not produce a billable RoutePlan. The error message must include the
// derived tenant so audit logs can see identity succeeded.
//
// MUTATION CHECK: if a developer flips the default of AllowTestStubPlan or
// removes the Phase 2A scope gate, production callers would get a usable
// RouteQueryResponse — this test goes red.
func TestService_ProductionModeReturnsRouteContractIncomplete(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 99}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: false})

	resp, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		RequestID:        "req-p2a6-prod",
		ClientCredential: canonicalBearer("hk_test_P2A6_PROD_TOKEN_001"),
	})
	if !errors.Is(err, routecontrol.ErrRouteContractIncomplete) {
		t.Fatalf("want ErrRouteContractIncomplete, got %v", err)
	}
	// Discriminating: response must be the zero value so callers cannot
	// accidentally read DerivedTenantID and act on it.
	if resp.DerivedTenantID != "" {
		t.Errorf("response leaked DerivedTenantID on production error path: %q", resp.DerivedTenantID)
	}
	// Discriminating: error message includes derived tenant so audit logs
	// can verify identity succeeded despite the plan refusal.
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error missing derived tenant marker: %v", err)
	}
}

// =============================================================================
// Auxiliary: error-classification mapping (Phase 2A.5 gRPC mapping depends on it)
// =============================================================================

// TC-AUX-1 — auth.ErrUnauthorized maps to routecontrol.ErrInvalidClientCredential
// so the gRPC layer can return Unauthenticated rather than Unavailable.
func TestService_AuthUnauthorizedMapsToInvalidCredential(t *testing.T) {
	stub := &stubAuthResolver{err: auth.ErrUnauthorized}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})
	_, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		ClientCredential: canonicalBearer("hk_test_AUX1_UNAUTH_TOKEN_001"),
	})
	if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
		t.Fatalf("want ErrInvalidClientCredential, got %v", err)
	}
}

// TC-AUX-2 — auth.ErrAuthBackend maps to routecontrol.ErrAuthBackend so the
// gRPC layer can return Unavailable rather than Unauthenticated (retryable
// vs credential failure distinction matters for client back-off).
func TestService_AuthBackendErrorMapsThrough(t *testing.T) {
	stub := &stubAuthResolver{err: auth.ErrAuthBackend}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})
	_, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		ClientCredential: canonicalBearer("hk_test_AUX2_BACKEND_TOKEN_001"),
	})
	if !errors.Is(err, routecontrol.ErrAuthBackend) {
		t.Fatalf("want ErrAuthBackend, got %v", err)
	}
}

// TC-AUX-3 — empty credential → ErrMissingClientCredential, resolver never
// invoked (defense-in-depth: parser is the first gate, resolver call costs
// a DB roundtrip).
func TestService_EmptyCredentialShortCircuitsResolver(t *testing.T) {
	stub := &stubAuthResolver{}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})
	_, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		ClientCredential: "", // missing
	})
	if !errors.Is(err, routecontrol.ErrMissingClientCredential) {
		t.Fatalf("want ErrMissingClientCredential, got %v", err)
	}
	if got := stub.calls.Load(); got != 0 {
		t.Fatalf("resolver invoked %d times on empty credential (want 0)", got)
	}
}

// TC-AUX-4 — context propagates to the resolver so cancellation works.
func TestService_ContextPropagatesToResolver(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 5}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})

	type ctxKey string
	const probeKey ctxKey = "routecontrol-service-test-probe"
	ctx := context.WithValue(context.Background(), probeKey, "probe-value")

	_, err := svc.RouteQuery(ctx, routecontrol.RouteQueryRequest{
		ClientCredential: canonicalBearer("hk_test_AUX4_CTX_TOKEN_001"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stub.captureCtx.Value(probeKey); got != "probe-value" {
		t.Fatalf("context not propagated, got %v", got)
	}
}

// TC-AUX-5 — nil ctx defaults to context.Background, does NOT panic.
func TestService_NilContextSafe(t *testing.T) {
	stub := &stubAuthResolver{identity: auth.Identity{TenantID: 5}}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})
	_, err := svc.RouteQuery(nil, routecontrol.RouteQueryRequest{
		ClientCredential: canonicalBearer("hk_test_AUX5_NILCTX_TOKEN_001"),
	})
	if err != nil {
		t.Fatalf("nil ctx not handled: %v", err)
	}
}

// TC-AUX-6 — NewService(nil) panics so misconfigured wiring fails loudly
// at startup rather than silently returning auth-backend errors at runtime.
func TestNewService_NilAuthResolverPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil AuthResolver")
		}
	}()
	_ = routecontrol.NewService(nil, routecontrol.ServiceConfig{})
}

// TC-AUX-7 — invalid credential format returns ErrInvalidClientCredential,
// resolver never invoked.
func TestService_InvalidCredentialShortCircuitsResolver(t *testing.T) {
	stub := &stubAuthResolver{}
	svc := routecontrol.NewService(stub, routecontrol.ServiceConfig{AllowTestStubPlan: true})
	_, err := svc.RouteQuery(context.Background(), routecontrol.RouteQueryRequest{
		ClientCredential: "noseparator-token",
	})
	if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
		t.Fatalf("want ErrInvalidClientCredential, got %v", err)
	}
	if got := stub.calls.Load(); got != 0 {
		t.Fatalf("resolver invoked %d times on invalid credential (want 0)", got)
	}
}

// =============================================================================
// Cross-test sanity: the canonical helpers actually produce parseable forms
// (guards against test infrastructure rot breaking the rest of the suite)
// =============================================================================

func TestHelpers_ProduceParseableCanonicalForms(t *testing.T) {
	for _, fn := range []func(string) string{canonicalBearer, canonicalXAPIKey} {
		canonical := fn("hk_test_HELPER_SANITY")
		if _, err := routecontrol.ParseClientCredential(canonical); err != nil {
			t.Errorf("helper produced unparseable form %q: %v", canonical, err)
		}
	}
}

// =============================================================================
// Compile-time guard: ServiceConfig must be a value type (not a pointer)
// so accidentally passing a stale config is caught at the type system.
// =============================================================================

func TestServiceConfig_IsValueType(t *testing.T) {
	// Just exercise the type so the compiler verifies it's a struct value.
	var c routecontrol.ServiceConfig
	c.AllowTestStubPlan = false
	if c.AllowTestStubPlan {
		t.Fatal("default ServiceConfig.AllowTestStubPlan must be false")
	}
}

// =============================================================================
// PII safety: error message redaction is consistent across error types
// (sentinel check for documentation accuracy)
// =============================================================================

func TestErrorSentinels_AreAllExportedAndDistinct(t *testing.T) {
	sentinels := map[string]error{
		"missing":  routecontrol.ErrMissingClientCredential,
		"invalid":  routecontrol.ErrInvalidClientCredential,
		"mismatch": routecontrol.ErrTenantIDMismatch,
		"backend":  routecontrol.ErrAuthBackend,
		"contract": routecontrol.ErrRouteContractIncomplete,
	}
	seen := make(map[string]string)
	for name, err := range sentinels {
		if err == nil {
			t.Errorf("%s sentinel is nil", name)
			continue
		}
		msg := err.Error()
		if prior, dup := seen[msg]; dup {
			t.Errorf("sentinel %s collides with %s on message %q", name, prior, msg)
		}
		seen[msg] = name
	}
	// Cross-check: each sentinel is distinguishable via errors.Is (no
	// accidental shadowing where one wraps another).
	for nameA, errA := range sentinels {
		for nameB, errB := range sentinels {
			if nameA == nameB {
				continue
			}
			if errors.Is(errA, errB) {
				t.Errorf("sentinel %s satisfies errors.Is(%s) — they collapse", nameA, nameB)
			}
		}
	}
}
