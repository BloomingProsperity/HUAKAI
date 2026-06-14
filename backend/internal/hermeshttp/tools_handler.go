package hermeshttp

import (
	"errors"
	"net/http"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// toolDescriptor is the GET /v1/hermes/tools list entry: a tool's identity +
// input schema + required role + read-only flag, so an operator / the assistant
// can discover what is callable without trial-and-error.
type toolDescriptor struct {
	Name                 string            `json:"name"`
	Category             string            `json:"category"`
	Description          string            `json:"description"`
	ReadOnly             bool              `json:"read_only"`
	Mutating             bool              `json:"mutating"`
	RequiresConfirmation bool              `json:"requires_confirmation"`
	RequiredRole         string            `json:"required_role"`
	InputSchema          map[string]string `json:"input_schema"`
}

// listTools serves GET /v1/hermes/tools. It is admin-gated by the H1 middleware
// mount; here it just requires a resolved identity (which the middleware
// guarantees) and lists every registered tool.
func (h handler) listTools(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireIdentity(w, r); !ok {
		return
	}
	if h.tools == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tools_unavailable", "hermes tool registry unset")
		return
	}
	specs := h.tools.List()
	out := make([]toolDescriptor, 0, len(specs))
	for _, s := range specs {
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		out = append(out, toolDescriptor{
			Name:                 s.Name,
			Category:             string(s.Category),
			Description:          s.Description,
			ReadOnly:             s.ReadOnly,
			Mutating:             s.Mutating,
			RequiresConfirmation: s.RequiresConfirmation,
			RequiredRole:         s.RequiredRole,
			InputSchema:          schema,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

type toolExecuteRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	// Confirm + CorrelationID drive the WAVE H4 mutating-tool dry-run+confirm
	// flow. They are ignored for read-only tools. Confirm=false (default) on a
	// mutating tool returns a read-only preview + a correlation_id; Confirm=true
	// with the matching correlation_id executes the mutation exactly once.
	Confirm       bool   `json:"confirm,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// executeTool serves POST /v1/hermes/tool-execute. It:
//  1. resolves the (already scope-checked) identity + operator actor,
//  2. authorizes the tool against the caller's role (RBAC floor),
//  3. on denial — records a denied hermes_tool_calls row + 403,
//  4. on allow — runs the read-only tool, records an ok/error hermes_tool_calls
//     row AND mirrors the invocation into hermes_audit_events as
//     hermes.tool.<name>, then returns the structured result.
//
// Every path records a tool-call row before returning, so success, error, and
// denial are all auditable. A tool that mutates state cannot reach here — only
// read-only tools are registered this wave.
func (h handler) executeTool(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	if h.tools == nil || h.toolCalls == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tools_unavailable", "hermes tool spine unset")
		return
	}

	var req toolExecuteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "hermes_tool_name_required", "tool_name is required")
		return
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}

	actor, _ := adminActorFromContext(r.Context())

	// WAVE H4: a mutating tool is dispatched ONLY through the dry-run + confirm
	// flow (5-layer safety). It must never reach the read-only Authorize/Run path
	// below. Route it before the read-only RBAC so a mutation can never run
	// without the confirm gate + advisory lock + atomic audit.
	if mutSpec, ok := h.tools.Get(req.ToolName); ok && mutSpec.Mutating {
		// KNOB A (runtime kill-switch): when mutating tools are disabled, refuse the
		// mutating branch HERE — before previewMutation OR confirmMutation — so a
		// single choke covers both the dry-run preview and the confirmed execution.
		// Record a denied hermes_tool_calls row (audit parity with the other L1
		// denials, reusing the mutate handler's denial recorder) so a refused attempt
		// is auditable, then 403. The read-only path below is unaffected.
		if h.mutatingDisabled {
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusForbidden, "hermes_mutating_disabled", "hermes mutating tools are disabled at runtime")
			return
		}
		h.executeMutatingTool(w, r, ident, actor, req)
		return
	}

	// RBAC: authorize the role floor. Tenant-scope was already enforced by the
	// H1 middleware (CanIssueForTenant) before this handler ran — a cross-tenant
	// caller never reaches here. An unknown tool or insufficient role is a
	// denial recorded as a denied row.
	spec, authErr := h.tools.Authorize(req.ToolName, actor.Role)
	if authErr != nil {
		h.recordToolCall(r, ident, actor, req, hermesops.ResultDenied, nil, "")
		switch {
		case errors.Is(authErr, hermesops.ErrToolUnknown):
			writeError(w, http.StatusNotFound, "hermes_tool_unknown", "unknown tool")
		default:
			writeError(w, http.StatusForbidden, "hermes_tool_forbidden", "operator role may not run this tool")
		}
		return
	}

	toolReq := hermesops.ToolRequest{
		TenantID:    ident.TenantID,
		ActorUserID: ident.UserID,
		Role:        actor.Role,
		Args:        req.Args,
	}
	result, runErr := h.tools.Run(r.Context(), req.ToolName, toolReq)
	if runErr != nil {
		errorClass := classifyToolRunError(runErr)
		h.recordToolCall(r, ident, actor, req, hermesops.ResultError, nil, errorClass)
		h.mirrorToolAudit(r, ident, spec.Name, hermes.AuditResultFailure)
		writeToolRunError(w, runErr)
		return
	}

	// Success: record the ok row (with sanitized summary) + mirror to the hermes
	// audit ledger, then return the structured result.
	h.recordToolCall(r, ident, actor, req, hermesops.ResultOK, result.Summary, result.ErrorClass)
	h.mirrorToolAudit(r, ident, spec.Name, hermes.AuditResultSuccess)

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":   spec.Name,
		"result":      result.Summary,
		"error_class": emptyStringToNil(result.ErrorClass),
		"read_only":   spec.ReadOnly,
	})
}

// recordToolCall appends one sanitized hermes_tool_calls row. It best-effort
// logs (does not fail the request) on a write error — the diagnostic result is
// already computed and the audit-write transient failure should not mask it; the
// hermes audit mirror provides a second trail. The summary is sanitized inside
// hermesops.RecordToolCall (defense in depth on top of the diagnostic-only tools).
func (h handler) recordToolCall(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, status hermesops.ResultStatus, summary map[string]any, errorClass string) {
	now := time.Now().UTC()
	rec := hermesops.ToolCallAudit{
		TenantID:          ident.TenantID,
		ActorUserID:       ident.UserID,
		AdminActorTokenID: actor.TokenID,
		ToolName:          req.ToolName,
		Args:              req.Args,
		ResultSummary:     summary,
		Status:            status,
		ErrorClass:        errorClass,
		CorrelationID:     correlationID(r),
		RequestID:         requestID(r),
		CalledAt:          now,
		ReturnedAt:        now,
	}
	if err := hermesops.RecordToolCall(r.Context(), h.toolCalls, rec); err != nil {
		logToolCallWriteFailure(req.ToolName, err)
	}
}

// mirrorToolAudit writes the per-action hermes_audit_events row (reusing the
// existing RecordAudit path, which folds in the operator's admin_actor
// attribution + sanitizes args). A tool with no whitelisted action (should never
// happen for a registered tool) is skipped. A transient audit failure is logged,
// not surfaced — the authoritative tool-call row is the hermes_tool_calls entry.
func (h handler) mirrorToolAudit(r *http.Request, ident sessionauth.Identity, toolName, result string) {
	if h.svc == nil {
		return
	}
	action, ok := hermes.ToolAuditAction(toolName)
	if !ok {
		return
	}
	args := map[string]any{"tool_name": toolName}
	if err := h.svc.RecordAudit(r.Context(), ident.TenantID, ident.UserID, action,
		withAdminActor(r.Context(), args), result, correlationID(r), requestID(r)); err != nil {
		logToolAuditMirrorFailure(toolName, err)
	}
}

func classifyToolRunError(err error) string {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		return "dependency_unwired"
	default:
		return "tool_execution_failed"
	}
}

func writeToolRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid or missing tool arguments")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "tool execution failed")
	}
}

func emptyStringToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
