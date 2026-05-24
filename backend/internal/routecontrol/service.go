package routecontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// AuthResolver is the narrow interface the [Service] uses to verify client
// credentials and derive an authoritative tenant_id.
//
// Production wiring passes *auth.APIKeyResolver directly — it already satisfies
// this interface, so no adapter is needed. Unit tests inject a hand-rolled
// stub that records the *http.Request the Service produced (which lets
// P2-A4 mutation tests assert the x-api-key → Authorization Bearer
// normalization happened correctly).
//
// Keeping this interface inside routecontrol (rather than reaching into the
// auth package directly) lets Phase 2B / 2C decorate the resolver (rate
// limiter, OAuth, mTLS upgrade) without touching the auth package, and lets
// test stubs avoid mocking the entire APIKeyResolver surface.
type AuthResolver interface {
	Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}

// Compile-time guarantee: the production *auth.APIKeyResolver satisfies
// AuthResolver. If a future refactor changes either signature, this line
// catches the regression at compile time rather than at startup.
var _ AuthResolver = (*auth.APIKeyResolver)(nil)

// RouteQueryRequest mirrors huakai.route.v1.RouteQueryRequest in shape. The
// Phase 2A service only reads ClientCredential (the Phase 1 opaque pass-through)
// and TenantID (the legacy reconciliation field). The rest is opaque routing
// metadata that Phase 2B's claim contract will consume.
//
// Field naming matches the proto field naming so the future routepb adapter
// (Phase 2A.5, gated on OD-1 approval) is a one-to-one copy.
type RouteQueryRequest struct {
	RequestID        string
	TenantID         string // legacy carry-over; reconciled against derived tenant (P2-A2)
	RequestedModel   string
	SessionHash      string
	RequestProtocol  string
	Stream           bool
	ClientDeadlineMs uint64
	ClientCredential string // canonical wire form per Phase 1 Rust serializer
}

// RouteQueryResponse is the Phase 2A output. It always carries the
// authoritatively-derived tenant_id so downstream observability can attribute
// the attempt. Phase 2A NEVER returns a billable RoutePlan body; that requires
// the Phase 2B claim contract (deferred per OD-2).
type RouteQueryResponse struct {
	// DerivedTenantID is the authoritative tenant_id Go derived from the
	// credential. Always populated on success; empty on every error path.
	DerivedTenantID string
}

// ServiceConfig configures Service behavior.
//
// AllowTestStubPlan is a unit-test-only switch:
//   - false (production default): RouteQuery returns ErrRouteContractIncomplete
//     after a successful identity derivation, so no caller can accidentally
//     produce billable traffic against a Phase 2A-incomplete plan.
//   - true: RouteQuery returns a successful RouteQueryResponse carrying the
//     derived tenant_id. Used by P2-A1..A4 tests to assert identity outcomes
//     without producing a real plan.
//
// Wiring guidance: production startup must hard-code AllowTestStubPlan=false.
// The Go control plane main / DI never reads this from env to avoid an
// operator flag toggling billable behavior; the only flip happens inside test
// fixtures.
type ServiceConfig struct {
	AllowTestStubPlan bool
}

// Service orchestrates the Phase 2A identity-authoritative route query.
//
// Flow:
//
//  1. Parse [RouteQueryRequest.ClientCredential] into a [ClientCredential].
//  2. Build an *http.Request via [ClientCredential.ResolverRequest] carrying
//     the credential as "Authorization: Bearer <secret>" (both bearer and
//     x-api-key kinds normalize through the same path — P2-A4).
//  3. Resolve via the injected [AuthResolver] to obtain an auth.Identity
//     containing the authoritative TenantID.
//  4. If [RouteQueryRequest.TenantID] is non-empty, reconcile it against the
//     derived tenant; mismatch returns [ErrTenantIDMismatch] fail-closed
//     (P2-A2). Match passes through (P2-A3) so the Phase 1/2 reconciliation
//     period works.
//  5. With AllowTestStubPlan=false (production): return
//     [ErrRouteContractIncomplete] with the derived tenant in the error string.
//     With AllowTestStubPlan=true: return [RouteQueryResponse] containing the
//     derived tenant.
//
// PII safety: every error wraps a sentinel with non-PII detail. The raw
// credential never appears in any returned error or returned response.
// Credentials are referenced by [ClientCredential.Fingerprint] in errors so
// audit logs can correlate without leaking material.
type Service struct {
	auth   AuthResolver
	config ServiceConfig
}

// NewService constructs a Service.
// Panics if authResolver is nil — we never want a Service that silently
// returns ErrAuthBackend because someone forgot to wire the resolver.
func NewService(authResolver AuthResolver, config ServiceConfig) *Service {
	if authResolver == nil {
		panic("routecontrol: NewService requires a non-nil AuthResolver")
	}
	return &Service{auth: authResolver, config: config}
}

// RouteQuery implements the Phase 2A identity gate. See [Service] doc for the
// detailed flow. Errors returned wrap one of the sentinels in errors.go; the
// caller (Phase 2A.5 gRPC server) maps them to gRPC status codes.
func (s *Service) RouteQuery(ctx context.Context, req RouteQueryRequest) (RouteQueryResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cred, err := ParseClientCredential(req.ClientCredential)
	if err != nil {
		// Parser errors already wrap the Missing / Invalid sentinels.
		return RouteQueryResponse{}, err
	}

	authReq, err := cred.ResolverRequest(ctx)
	if err != nil {
		// IsZero / nil-base — defense in depth, parser should have caught it.
		return RouteQueryResponse{}, err
	}

	identity, err := s.auth.Resolve(ctx, authReq)
	if err != nil {
		return RouteQueryResponse{}, classifyAuthError(err, cred)
	}

	derived := strconv.FormatInt(identity.TenantID, 10)

	// Reconcile against legacy tenant_id if the request carried one. Empty
	// legacy means "trust Go-derived"; non-empty must match.
	if req.TenantID != "" && req.TenantID != derived {
		// Non-PII detail: legacy & derived are both string IDs (never raw
		// credential material), so embedding them is safe. Fingerprint is
		// included for audit correlation across the two log lines that
		// will mention this mismatch (auth-side + this service-side).
		return RouteQueryResponse{}, fmt.Errorf(
			"%w: legacy=%q derived=%q kind=%s sha256=%s",
			ErrTenantIDMismatch, req.TenantID, derived, cred.Kind(), cred.Fingerprint(),
		)
	}

	// P2-A6 / OD-2 gate: Phase 2A intentionally CANNOT produce a billable
	// RoutePlan. Production callers see ErrRouteContractIncomplete after the
	// identity step succeeds, so no part of the call chain can serve
	// real traffic until Phase 2B claim contract lands.
	if !s.config.AllowTestStubPlan {
		return RouteQueryResponse{}, fmt.Errorf(
			"%w: derived tenant_id=%q (set ServiceConfig.AllowTestStubPlan only in tests)",
			ErrRouteContractIncomplete, derived,
		)
	}

	return RouteQueryResponse{DerivedTenantID: derived}, nil
}

// classifyAuthError maps auth-package sentinels into routecontrol-package
// sentinels.
//
// All credential-level failures collapse to ErrInvalidClientCredential —
// preserves the auth-side anti-enumeration property that bad cred / unknown
// user / expired key all look the same to the caller (D10 in auth synthesized
// plan).
//
// Backend / misconfiguration failures become ErrAuthBackend so the caller
// can distinguish "retry later" (503/Unavailable) from "fix your credential"
// (401/Unauthenticated).
//
// Errors NEVER embed the raw secret — the credential is referenced by
// Kind + Fingerprint only (P2-A5 invariant, verified by
// TestService_ErrorsNeverEmbedRawSecret).
func classifyAuthError(err error, cred ClientCredential) error {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		return fmt.Errorf("%w: credential not recognized (kind=%s sha256=%s)",
			ErrInvalidClientCredential, cred.Kind(), cred.Fingerprint())
	case errors.Is(err, auth.ErrAuthMisconfigured):
		return fmt.Errorf("%w: %v", ErrAuthBackend, err)
	case errors.Is(err, auth.ErrAuthBackend):
		return fmt.Errorf("%w: %v", ErrAuthBackend, err)
	default:
		// Unknown error from the resolver — treat as backend failure (503/
		// Unavailable) rather than credential failure (401/Unauthenticated),
		// erring on the side of "tell client to retry" rather than "tell
		// client their credential is bad".
		return fmt.Errorf("%w: unexpected resolver error: %v", ErrAuthBackend, err)
	}
}
