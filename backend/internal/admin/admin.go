// Package admin implements the operator-facing surface for HUAKAI:
//   - admin token authentication (separate from customer api_keys)
//   - api_keys issuance / revocation / list (the workflow that replaces
//     manual SQL INSERT into api_keys)
//   - admin_audit_events writer
//
// Boundary contracts (docs/specs/_invariants/cross-module-boundaries.md):
// this package MUST NOT be imported from internal/router or
//     internal/auth's hot resolver path. The inbound resolver looks up
//     api_keys; admin work writes to api_keys. Two different surfaces.
// plaintext bearers are surfaced ONLY in IssueResult.Plaintext
//     for one-time response to the operator. NEVER stored, NEVER logged,
//     NEVER persisted into admin_audit_events.payload.
// this package writes to admin_tokens, api_keys, and
//     admin_audit_events. Never to billing, pool, or registry tables.
//
// Per docs/process/plans/2026-05-01-n4b-admin-keys.md.

package admin

import "errors"

// ErrAdminUnauthorized is returned when the caller's admin credential is
// missing, malformed, expired, revoked, or does not have the required
// role to perform the requested action. The handler maps this to 401.
var ErrAdminUnauthorized = errors.New("admin: unauthorized")

// ErrAdminForbidden is returned when the caller authenticates but the
// scope check rejects the action — e.g. tenant_operator trying to issue
// for another tenant. The handler maps this to 403.
var ErrAdminForbidden = errors.New("admin: forbidden")

// ErrAdminRateLimited is returned when the caller has exceeded the
// per-actor issuance rate window (D4 default: 30 issues / hour).
// The handler maps this to 429.
var ErrAdminRateLimited = errors.New("admin: rate limited")

// ErrAdminBadRequest covers structurally invalid inputs the database
// would reject anyway (e.g. missing required fields). 400.
var ErrAdminBadRequest = errors.New("admin: bad request")

// ErrAdminNotFound is returned when the target resource (api_keys row,
// admin_tokens row) doesn't exist or is soft-deleted. 404.
var ErrAdminNotFound = errors.New("admin: target not found")

// ErrAdminBackend wraps any datastore failure during admin work. The
// handler maps this to 503 — NOT 401 — so legitimate operators are not
// told their valid creds are invalid during an infra outage.
// Mirrors auth.ErrAuthBackend.
var ErrAdminBackend = errors.New("admin: backend datastore error")

// Role enum mirrored from the admin_tokens.role CHECK constraint.
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)
