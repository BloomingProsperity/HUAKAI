package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// internal_tool_handler.go is the gateway-side authority for the conversational
// READ-ONLY tool loop (WAVE H3b, Option B). The Python runner's ops assistant
// calls back into the gateway mid-conversation to run a diagnostic tool; this
// handler is the ONLY place that authorizes + executes + audits that call.
//
// SAFETY CONTRACT (every item is enforced here, fail-closed):
//  1. Authentication: the caller MUST present a valid internal_token (the same
//     HMAC tenant|user|request_id|exp the bridge minted for this session). An
//     invalid/expired token => 401, no tool runs.
//  2. Session binding: the token's request_id MUST resolve to a bound operator
//     identity (role + tenant scope + admin_token_id). No binding => 401.
//  3. Identity consistency: the token's tenant/user MUST match the bound
//     session's tenant/user. A mismatch => 401 (a token for session A can never
//     drive session B's operator scope).
//  4. READ-ONLY filter (the structural mutation guard): the requested tool MUST
//     be registered AND non-mutating. A mutating tool name (account_pause,
//     dlq_replay, renew_trigger, ...) is REJECTED before dispatch — and even if
//     this check were removed, hermesops.Registry.Run itself refuses a mutating
//     tool with ErrNotMutating. Two independent gates; a mutation is unreachable.
//  5. RBAC role floor: Registry.Run authorizes the bound operator's role against
//     the tool's RequiredRole. Below floor => denied (recorded).
//  6. Tenant scope: the tool ALWAYS runs with the bound session's TenantID. The
//     runner cannot name a tenant; there is no tenant arg. A tool can never read
//     another tenant's data through this path.
//  7. Audit: every call (ok / error / denied / rejected) records a sanitized
//     hermes_tool_calls row with the operator attribution (admin_actor_token_id).
//  8. Privacy: only the tool's sanitized Summary (enums/counts/ids) crosses back
//     to the runner; the store re-sanitizes args + summary as defense in depth.

// ReadOnlyToolRunner is the narrow read-only dispatch surface this handler needs
// from the tool registry. *hermesops.Registry satisfies it. It deliberately does
// NOT expose AuthorizeMutating / Resolve / Mutate — the mutating capability is
// structurally absent from this handler's dependency, so there is no method here
// that could run a mutation even if mis-called.
type ReadOnlyToolRunner interface {
	// Get reports a registered spec (used only to classify a name as mutating for
	// the explicit pre-dispatch rejection + audit). It never executes anything.
	Get(name string) (hermesops.ToolSpec, bool)
	// Run authorizes (role floor) + dispatches a READ-ONLY tool. It refuses a
	// mutating tool with ErrNotMutating, so it is itself a mutation guard.
	Run(ctx context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error)
	// ReadOnlyCatalog returns the LLM-facing read-only tool catalog.
	ReadOnlyCatalog() []hermesops.CatalogTool
}

// InternalToolHandler serves the runner's mid-conversation tool calls.
type InternalToolHandler struct {
	secret    []byte
	bindings  *SessionBindings
	tools     ReadOnlyToolRunner
	toolCalls hermesops.ToolCallInserter
	now       func() time.Time
}

// NewInternalToolHandler wires the handler. secret is the internal-token HMAC
// secret (same as the bridge's). A nil registry / inserter / bindings makes the
// handler fail closed (503 / 401) rather than panic.
func NewInternalToolHandler(secret []byte, bindings *SessionBindings, tools ReadOnlyToolRunner, toolCalls hermesops.ToolCallInserter, now func() time.Time) *InternalToolHandler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &InternalToolHandler{
		secret:    append([]byte(nil), secret...),
		bindings:  bindings,
		tools:     tools,
		toolCalls: toolCalls,
		now:       now,
	}
}

// internalToolRequest is the runner -> gateway tool-call request body. It carries
// ONLY a tool name + args. It deliberately has NO tenant / role / actor field:
// the operator scope comes from the verified session binding, never from the
// runner, so the runner cannot escalate scope or impersonate another operator.
type internalToolRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
}

// internalToolResponse is the gateway -> runner tool-result body. It returns the
// sanitized diagnostic summary the agent feeds back into the conversation. On a
// denial / error it carries a short status + error_class (no PII, no secrets).
type internalToolResponse struct {
	ToolName   string         `json:"tool_name"`
	Status     string         `json:"status"`
	Result     map[string]any `json:"result,omitempty"`
	ErrorClass string         `json:"error_class,omitempty"`
}

// ServeHTTP handles POST <internal_base>/tool-execute. It shares the gateway's
// listener with the runner's other /internal/* callbacks (bootstrap/refresh/keys);
// the protection is the application-layer internal_token (HMAC-SHA256, short TTL,
// constant-time compare), NOT network isolation — the route is on the same public
// listener, so the token gate is the only thing standing between a caller and a
// tool. A separate loopback listener / source ACL on /internal/* is a hardening
// follow-up.
func (h *InternalToolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || len(h.secret) == 0 || h.bindings == nil || h.tools == nil {
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_spine_unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeInternalToolError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	// (1) Authenticate the runner via the internal_token. The bridge minted it for
	// THIS session; an invalid/expired token never reaches a tool.
	op, ok := h.resolveOperator(r)
	if !ok {
		writeInternalToolError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req internalToolRequest
	if !decodeInternalJSON(w, r, &req) {
		return
	}
	req.ToolName = strings.TrimSpace(req.ToolName)
	if req.ToolName == "" {
		writeInternalToolError(w, http.StatusBadRequest, "tool_name_required")
		return
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}

	// (4) READ-ONLY filter — the structural mutation guard. Symmetric with the
	// catalog's allow-test (catalog.go): a tool is dispatchable ONLY if it is
	// explicitly ReadOnly AND not Mutating. Rejecting on (Mutating || !ReadOnly)
	// also excludes any future tool that is neither (an unset ReadOnly flag),
	// fail-safe by default. The LLM cannot reach account_pause / dlq_replay /
	// renew_trigger (or any non-read-only tool) here.
	if spec, found := h.tools.Get(req.ToolName); found && (spec.Mutating || !spec.ReadOnly) {
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "mutating_tool_rejected")
		writeInternalToolError(w, http.StatusForbidden, "mutating_tool_forbidden")
		return
	}

	// (5)(6) Dispatch through the read-only Run: it enforces the role floor and
	// refuses a mutating tool (ErrNotMutating) as the SECOND independent guard.
	// TenantID is pinned to the bound session — the runner supplied no tenant.
	toolReq := hermesops.ToolRequest{
		TenantID:    op.TenantID,
		ActorUserID: op.ActorUserID,
		Role:        op.Role,
		Args:        req.Args,
	}
	result, runErr := h.tools.Run(r.Context(), req.ToolName, toolReq)
	if runErr != nil {
		h.writeRunError(w, r, op, req, runErr)
		return
	}

	// (7) Record the ok row with operator attribution; (8) return only the
	// sanitized summary to the runner.
	h.recordCall(r, op, req, hermesops.ResultOK, result.Summary, result.ErrorClass)
	writeInternalToolJSON(w, http.StatusOK, internalToolResponse{
		ToolName:   req.ToolName,
		Status:     string(hermesops.ResultOK),
		Result:     result.Summary,
		ErrorClass: result.ErrorClass,
	})
}

// resolveOperator verifies the internal_token and resolves the bound operator.
// It enforces (1) token validity, (2) a present+unexpired binding, and (3)
// tenant/user consistency between the token and the binding. Any failure returns
// (zero, false) so the caller fails closed with 401.
func (h *InternalToolHandler) resolveOperator(r *http.Request) (SessionOperator, bool) {
	token := bearerFromRequest(r)
	if token == "" {
		return SessionOperator{}, false
	}
	claims, err := VerifyInternalToken(token, h.secret, h.now().UTC())
	if err != nil {
		return SessionOperator{}, false
	}
	op, ok := h.bindings.Lookup(claims.RequestID)
	if !ok {
		return SessionOperator{}, false
	}
	// Identity consistency: a token issued for (tenant,user) must match the bound
	// session's (tenant,user). Prevents a valid token for one session from being
	// paired with another session's request_id binding.
	if op.TenantID != claims.TenantID || op.ActorUserID != claims.UserID {
		return SessionOperator{}, false
	}
	// The conversational path is admin-only: a binding with no operator role / no
	// admin actor token can never authorize a tool.
	if strings.TrimSpace(op.Role) == "" || op.AdminActorTokenID <= 0 {
		return SessionOperator{}, false
	}
	return op, true
}

// writeRunError maps a Run error to a status + audited denied/error row. A role
// or mutating-guard rejection is a denied row (RBAC outcome); a dependency /
// args / execution failure is an error row.
func (h *InternalToolHandler) writeRunError(w http.ResponseWriter, r *http.Request, op SessionOperator, req internalToolRequest, runErr error) {
	switch {
	case errors.Is(runErr, hermesops.ErrToolUnknown):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "unknown_tool")
		writeInternalToolError(w, http.StatusNotFound, "unknown_tool")
	case errors.Is(runErr, hermesops.ErrToolForbidden):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "role_forbidden")
		writeInternalToolError(w, http.StatusForbidden, "tool_forbidden")
	case errors.Is(runErr, hermesops.ErrNotMutating):
		// The registry's own mutation guard fired (a mutating tool reached Run).
		// Recorded distinctly so the audit shows the second gate caught it.
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "mutating_tool_rejected")
		writeInternalToolError(w, http.StatusForbidden, "mutating_tool_forbidden")
	case errors.Is(runErr, hermesops.ErrInvalidArgs):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "invalid_args")
		writeInternalToolError(w, http.StatusBadRequest, "invalid_args")
	case errors.Is(runErr, hermesops.ErrDependencyUnwired):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "dependency_unwired")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_unavailable")
	default:
		h.recordCall(r, op, req, hermesops.ResultError, nil, "tool_execution_failed")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_failed")
	}
}

// recordCall appends one sanitized hermes_tool_calls row with the operator
// attribution. Best-effort: a transient audit-write failure is swallowed (the
// tool result is already computed) — it must not surface the operator's data or
// block the conversation. The store sanitizes args + summary before insert.
func (h *InternalToolHandler) recordCall(r *http.Request, op SessionOperator, req internalToolRequest, status hermesops.ResultStatus, summary map[string]any, errorClass string) {
	if h.toolCalls == nil {
		return
	}
	now := h.now().UTC()
	_ = hermesops.RecordToolCall(r.Context(), h.toolCalls, hermesops.ToolCallAudit{
		TenantID:          op.TenantID,
		ActorUserID:       op.ActorUserID,
		AdminActorTokenID: op.AdminActorTokenID,
		ToolName:          req.ToolName,
		Args:              req.Args,
		ResultSummary:     summary,
		Status:            status,
		ErrorClass:        errorClass,
		CorrelationID:     strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		RequestID:         strings.TrimSpace(r.Header.Get("X-Request-ID")),
		CalledAt:          now,
		ReturnedAt:        now,
	})
}

func bearerFromRequest(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func decodeInternalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeInternalToolError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func writeInternalToolJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeInternalToolError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}
