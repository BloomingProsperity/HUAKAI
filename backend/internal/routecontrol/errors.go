// Package routecontrol implements the W11-A D-1b Phase 2A authoritative
// client-credential → tenant resolution path in the Go control plane.
//
// Phase 2A scope (per docs/process/plans/2026-05-24-w11a-d1b-phase2-synthesis.md):
//
//   - Parse the canonical credential wire form produced by the Rust data
//     plane in [RouteQueryRequest.client_credential]:
//     "bearer:<token>" or "x-api-key:<token>".
//   - Hand the parsed secret to [auth.APIKeyResolver.Resolve] to derive
//     the authoritative tenant_id (Go is the SOLE authority — A3 invariant
//     of Phase 1 still holds; Rust never trusts x-tenant-id).
//   - Reconcile against any legacy tenant_id the Rust Manual First
//     short-circuit asserted; fail-closed on mismatch (P2-A2).
//   - Refuse to return a billable RoutePlan until Phase 2B claim contract
//     lands; until then, return [ErrRouteContractIncomplete].
//
// Cross-module boundaries (CMB):
//
//   - routecontrol DEPENDS on auth.APIKeyResolver (for secret verification)
//     and registry/router (for plan derivation). It does NOT depend on
//     gatewayhttp / gateway / proto (frozen packages per CLAUDE.md #13).
//   - The raw secret stays inside [ClientCredential.secret] (unexported);
//     fmt verbs and log calls on the value emit kind + SHA-256 fingerprint
//     only. Verified by TestFormat_NoRawSecretLeak +
//     TestErrors_NeverEmbedRawSecret.
//
// Phase 1 invariants preserved (must not regress):
//
//   - A3: x-tenant-id is NEVER trusted (Rust does not set it; Go ignores
//     it even if a malicious client tries).
//   - A4: raw credential NEVER appears in log lines, span attributes,
//     metric labels, or error.Error() output.
//   - β scheme: Rust data plane holds no identity authority.
package routecontrol

import "errors"

// Phase 2A acceptance-gate error sentinels (per synthesis §8 P2-A1..A6).
//
// Wire mapping to gRPC status codes (set by Phase 2A.2 service.go):
//
//	ErrMissingClientCredential  → codes.Unauthenticated
//	ErrInvalidClientCredential  → codes.Unauthenticated  (no enumeration leak — see D10 in auth)
//	ErrTenantIDMismatch         → codes.PermissionDenied (per D-21)
//	ErrAuthBackend              → codes.Unavailable      (transient, retryable)
//	ErrRouteContractIncomplete  → codes.FailedPrecondition (Phase 2B not implemented)
//
// Callers wrap these via fmt.Errorf("%w: <detail>", ErrXxx). Detail strings
// MUST NEVER contain the raw secret; tests in credential_test.go assert
// this for every error path the parser can produce.
var (
	// ErrMissingClientCredential — RouteQueryRequest.client_credential was empty.
	// The Rust data plane must always supply a canonical credential string;
	// an empty value indicates a Rust-side regression of the Phase 1 A1 gate
	// or an unauthenticated probe.
	ErrMissingClientCredential = errors.New("routecontrol: missing client credential")

	// ErrInvalidClientCredential — canonical form was present but malformed:
	// missing the "<kind>:" prefix, unknown kind, empty token after the
	// separator, etc. This NEVER means "wrong password" (that maps to the
	// auth.ErrUnauthorized 401 path further downstream); it means the wire
	// format itself was unparseable.
	ErrInvalidClientCredential = errors.New("routecontrol: invalid client credential")

	// ErrTenantIDMismatch — RouteQueryRequest.tenant_id (legacy field carried
	// across the contract for compatibility during Phase 2 reconciliation) does
	// not equal the tenant the credential resolver derived. Fail-closed per
	// P2-A2: do not silently trust either side, refuse the request.
	ErrTenantIDMismatch = errors.New("routecontrol: tenant_id mismatch")

	// ErrAuthBackend — auth.APIKeyResolver returned a transient datastore
	// failure (DB connection broken, mid-query cancel, missing table). Caller
	// should map to gRPC Unavailable so the Rust attempt-reporter knows the
	// failure is retryable rather than a credential-level reject.
	ErrAuthBackend = errors.New("routecontrol: auth backend error")

	// ErrRouteContractIncomplete — Phase 2B fields required to safely return
	// a billable RoutePlan are absent (idempotency_key, normalized_payload_hash,
	// billing_policy_version, request_class, etc.). Phase 2A always returns
	// this from RouteQuery so we cannot accidentally serve money-path traffic
	// before the claim contract lands.
	ErrRouteContractIncomplete = errors.New("routecontrol: route contract incomplete (Phase 2B not implemented)")
)
