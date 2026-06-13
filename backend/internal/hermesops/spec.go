// Package hermesops is the gateway-mediated tool-execution spine for the
// admin-gated Hermes ops assistant (WAVE H3).
//
// It exposes a registry of READ-ONLY diagnostic tools that wrap EXISTING
// gateway read functions so an operator (and later the assistant LLM, in a
// later wave) can run root-cause diagnostics through a single audited endpoint.
// Every tool in this wave is read-only: a tool MUST NOT mutate state. Mutating
// tools (replay / pause / resume / renew) are explicitly out of this wave and
// require a dry-run + confirm contract that does not exist yet.
//
// Design:
//   - ToolSpec declares a tool's identity, category, min required role, and a
//     Run that wraps the underlying read function and returns a sanitized,
//     structured ToolResult (system-diagnostic data only).
//   - Registry holds the specs and performs RBAC + dispatch. It fails closed:
//     an unknown tool is denied, a tool whose dependency is unwired returns an
//     error (never a panic), and a caller lacking the role is denied.
//   - Privacy: a tool result carries ONLY enums / counts / ids / fingerprints —
//     never prompts, completions, raw bodies, secrets, or PII. The persisting
//     layer (store.go) additionally routes args + summary through the hermes
//     sanitizer as defense in depth.
package hermesops

import (
	"context"
	"errors"
)

// Tool names. These are the authoritative identifiers; they MUST match the
// hermes_tool_calls.tool_name CHECK list and the hermes.tool.<name> audit
// actions. H4 mutating tools add new names via a DROP+ADD migration.
const (
	ToolCredentialDiagnose    = "credential_diagnose"
	ToolAccountHealthDiagnose = "account_health_diagnose"
	ToolRequestDiagnose       = "request_diagnose"
	ToolDLQInspect            = "dlq_inspect"
	ToolAuditLookup           = "audit_lookup"
	ToolLogAnalyze            = "log_analyze"
)

// Roles mirror internal/admin role identifiers. Kept as local constants so this
// package does not import the admin package for two strings (the RBAC check
// itself is performed by the caller via admin.AdminIdentity.CanIssueForTenant,
// which this package never bypasses).
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)

// Category groups tools for listing/UX. All H3 tools are diagnostic reads.
type Category string

const (
	CategoryDiagnostic Category = "diagnostic"
)

// ResultStatus is the persisted hermes_tool_calls.result_status enum.
type ResultStatus string

const (
	ResultOK     ResultStatus = "ok"
	ResultError  ResultStatus = "error"
	ResultDenied ResultStatus = "denied"
)

// Sentinel errors. Tools and the registry return these so the HTTP layer can
// map them to status codes without string matching.
var (
	// ErrToolUnknown is returned when a tool name is not registered.
	ErrToolUnknown = errors.New("hermesops: unknown tool")
	// ErrToolForbidden is returned when the caller's role is below the tool's
	// minimum required role. Tenant-scope denial is enforced separately by the
	// caller via CanIssueForTenant and surfaces as a denied row too.
	ErrToolForbidden = errors.New("hermesops: tool forbidden for role")
	// ErrDependencyUnwired is returned by a tool whose underlying read
	// dependency is nil. Tools MUST fail closed with this rather than panic.
	ErrDependencyUnwired = errors.New("hermesops: tool dependency unwired")
	// ErrInvalidArgs is returned for malformed / missing required tool args.
	ErrInvalidArgs = errors.New("hermesops: invalid tool args")
)

// ToolRequest is the resolved, already-authorized invocation context handed to
// a tool's Run. TenantID is the middleware-derived, scope-checked tenant; the
// HTTP layer guarantees CanIssueForTenant passed before Run is called.
type ToolRequest struct {
	// TenantID is the resolved tenant the tool must scope its reads to. Always
	// > 0 (the HTTP layer rejects a non-positive tenant before dispatch).
	TenantID int64
	// ActorUserID is the tenant user whose ops context the operator acts within.
	ActorUserID int64
	// Role is the operator's admin role (platform_admin / tenant_operator).
	Role string
	// Args is the raw, decoded tool argument map from the request body. A tool
	// reads only the keys it understands and ignores the rest. Never persisted
	// raw — the store sanitizes it.
	Args map[string]any
}

// ToolResult is a tool's structured, sanitized output. Summary holds ONLY
// system-diagnostic enums / counts / ids; it is the body returned to the caller
// and (after a second sanitize pass) persisted to hermes_tool_calls.result_summary.
type ToolResult struct {
	// Summary is the diagnostic payload (enums/counts/ids only).
	Summary map[string]any
	// ErrorClass is an optional non-PII classification when the diagnostic
	// surfaced a problem (e.g. "invalid_grant", "rate_limit_exceeded"). It is a
	// short enum, never a free-form message containing user data.
	ErrorClass string
}

// ArgInt extracts a positive int64 arg, returning ErrInvalidArgs when missing
// or non-positive. JSON decodes numbers as float64, so both float64 and int64
// are accepted.
func ArgInt(args map[string]any, key string) (int64, error) {
	v, ok := args[key]
	if !ok {
		return 0, ErrInvalidArgs
	}
	switch n := v.(type) {
	case float64:
		if n <= 0 || n != float64(int64(n)) {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	case int64:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return n, nil
	case int:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	default:
		return 0, ErrInvalidArgs
	}
}

// ArgString extracts a trimmed non-empty string arg (optional). Returns
// ("", false) when absent; ("", false) for a non-string value (caller decides
// whether absence is an error). It never returns user prompt content — callers
// only pull identifier-shaped args (request_id, claim_id, status filters).
func ArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// ToolSpec declares one diagnostic tool.
type ToolSpec struct {
	Name         string
	Category     Category
	Description  string
	ReadOnly     bool
	RequiredRole string
	// InputSchema is a small map describing accepted args (name -> human hint),
	// surfaced by GET /v1/hermes/tools. It is documentation only, not validation.
	InputSchema map[string]string
	// Run wraps the underlying read function(s). It MUST NOT mutate state and
	// MUST return an error (never panic) on a missing dependency.
	Run func(ctx context.Context, req ToolRequest) (ToolResult, error)
}
