package hermeshttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// executeMutatingTool runs the WAVE H4 5-layer safety flow for a mutating tool:
//
//	L1 RBAC      — AuthorizeMutating checks the role floor (dlq_replay =
//	               platform_admin only; the others admit tenant_operator). A
//	               denial records a denied row + 403. Tenant scope was already
//	               enforced by the H1 middleware; Resolve re-checks the target row.
//	L2 dry-run + confirm — confirm=false performs a READ-ONLY preview (via the
//	               SAME Resolve used by execution) and returns a single-use
//	               correlation_id; confirm=true with a valid correlation_id runs
//	               the mutation exactly once. A stale/absent/mismatched
//	               correlation_id is 400 and never executes.
//	L3 atomic audit — the orchestrator commits the mutation + tool_calls row +
//	               admin_audit_events row together (audit-verified-before-commit).
//	L4 advisory lock — the orchestrator serializes concurrent mutations on the
//	               same target via a pg advisory xact lock keyed on the plan.
//	L5 idempotency — the correlation_id is single-use (consumed on execution);
//	               dlq_replay additionally dedupes on the record's idempotency key.
func (h handler) executeMutatingTool(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest) {
	// L1: role floor + mutating-tool sanity. AuthorizeMutating refuses a
	// read-only tool and an under-privileged role.
	spec, authErr := h.tools.AuthorizeMutating(req.ToolName, actor.Role)
	if authErr != nil {
		switch {
		case errors.Is(authErr, hermesops.ErrToolUnknown):
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusNotFound, "hermes_tool_unknown", "unknown tool")
		case errors.Is(authErr, hermesops.ErrNotMutating):
			// Should not happen (caller checked Mutating), but fail closed.
			writeError(w, http.StatusBadRequest, "hermes_tool_not_mutating", "tool is not a mutating tool")
		case errors.Is(authErr, hermesops.ErrDependencyUnwired):
			writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "mutating tool is not fully wired")
		default:
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusForbidden, "hermes_tool_forbidden", "operator role may not run this mutating tool")
		}
		return
	}

	toolReq := hermesops.ToolRequest{
		TenantID:    ident.TenantID,
		ActorUserID: ident.UserID,
		Role:        actor.Role,
		Args:        req.Args,
	}

	// L2 + target resolution. Resolve is READ-ONLY and shared by preview AND
	// execution, so the preview cannot diverge from the action.
	plan, resolveErr := spec.Resolve(r.Context(), toolReq)
	if resolveErr != nil {
		h.recordMutatingError(r, ident, actor, req, resolveErr, req.Confirm)
		writeMutatingResolveError(w, resolveErr)
		return
	}

	if !req.Confirm {
		h.previewMutation(w, r, ident, actor, req, spec, plan)
		return
	}
	h.confirmMutation(w, r, ident, actor, req, spec, plan, toolReq)
}

// previewMutation handles confirm=false: it records a dry-run tool-call row
// (status ok, dry_run=true), issues a single-use correlation_id bound to the
// exact tool/tenant/actor/target, and returns the "what would change" preview.
// It does NOT mutate.
func (h handler) previewMutation(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, spec hermesops.ToolSpec, plan hermesops.MutationPlan) {
	if h.confirmCache == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "confirmation cache unavailable")
		return
	}
	correlationID, err := h.confirmCache.issue(pendingConfirmation{
		ToolName: spec.Name,
		TenantID: ident.TenantID,
		ActorID:  ident.UserID,
		TokenID:  actor.TokenID,
		TargetID: plan.TargetID,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "could not issue correlation id")
		return
	}
	// Record the dry-run on the authoritative ledger (best-effort: a preview did
	// not mutate, so a transient ledger error must not block the operator from
	// seeing the preview). dry_run=true distinguishes it from a real execution.
	h.recordMutatingDryRun(r, ident, actor, req, plan)

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":             spec.Name,
		"mutating":              true,
		"dry_run":               true,
		"requires_confirmation": true,
		"correlation_id":        correlationID,
		"expires_in_seconds":    int(confirmTTL.Seconds()),
		"preview":               plan.Preview,
	})
}

// confirmMutation handles confirm=true: it consumes the correlation_id
// (single-use), and on a valid match runs the mutation through the orchestrator
// (L3 atomic audit + L4 advisory lock). A stale/absent/mismatched correlation_id
// is 400 and NEVER executes.
func (h handler) confirmMutation(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, spec hermesops.ToolSpec, plan hermesops.MutationPlan, toolReq hermesops.ToolRequest) {
	if h.mutator == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "mutation orchestrator unavailable")
		return
	}
	if h.confirmCache == nil || req.CorrelationID == "" {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_required", "confirm=true requires a correlation_id from a prior dry-run")
		return
	}
	// L2/L5: single-use consume. A re-used / stale / wrong-tool / wrong-tenant /
	// wrong-actor correlation_id finds nothing and is rejected — no mutation.
	entry, ok := h.confirmCache.consume(req.CorrelationID, spec.Name, ident.TenantID, ident.UserID)
	if !ok {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_invalid", "correlation_id is stale, unknown, or does not match this tool")
		return
	}
	// Defense in depth: the confirmed target must be the one the preview pinned.
	// Resolve is deterministic for a fixed target, so the freshly-resolved plan's
	// TargetID must equal the pinned one (it would differ only if the args named
	// a different target than the preview — reject rather than mutate the wrong row).
	if entry.TargetID != plan.TargetID {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_invalid", "confirmation target does not match the previewed target")
		return
	}

	now := time.Now().UTC()
	rec := hermesops.MutationAuditRecord{
		TenantID:          ident.TenantID,
		ActorUserID:       ident.UserID,
		AdminActorTokenID: actor.TokenID,
		ToolName:          spec.Name,
		Args:              req.Args,
		Status:            hermesops.ResultOK,
		CorrelationID:     correlationID(r),
		RequestID:         requestID(r),
		CalledAt:          now,
		ReturnedAt:        now,
		DryRun:            false,
		AdminAction:       mutatingAuditAction(spec.Name),
		AdminRole:         actor.Role,
		TargetType:        plan.TargetType,
		TargetID:          plan.TargetID,
		AuditPayload:      mutationAuditPayload(spec.Name, plan, req.Args),
	}

	lockKey := plan.LockKey
	if lockKey == "" {
		lockKey = fmt.Sprintf("hermes:mutate:%d:%s:%d", ident.TenantID, spec.Name, plan.TargetID)
	}

	result, execErr := h.mutator.Execute(r.Context(), lockKey, rec, func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error) {
		// The mutation runs inside the orchestrator tx (audit rows already
		// accepted). The orchestrator threads the tx via ctx for tx-bound tools.
		return spec.Mutate(ctx, toolReq, plan)
	})
	if execErr != nil {
		// The orchestrator rolled back: no mutation, no audit rows. Record a
		// best-effort error row on the standalone ledger so the failed attempt is
		// visible even though the atomic path aborted.
		h.recordMutatingError(r, ident, actor, req, execErr, false)
		writeMutatingExecError(w, execErr)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":   spec.Name,
		"mutating":    true,
		"dry_run":     false,
		"result":      result.Summary,
		"error_class": emptyStringToNil(result.ErrorClass),
		"target_type": plan.TargetType,
		"target_id":   plan.TargetID,
	})
}

// mutatingAuditAction maps a mutating tool name to its admin_audit_events action
// (hermes.tool.<name>). Only the four H4 mutating tools are mapped; an unknown
// name yields "" which the orchestrator's CHECK would reject (fail closed).
func mutatingAuditAction(toolName string) string {
	switch toolName {
	case hermesops.ToolDLQReplay:
		return "hermes.tool.dlq_replay"
	case hermesops.ToolAccountPause:
		return "hermes.tool.account_pause"
	case hermesops.ToolAccountResume:
		return "hermes.tool.account_resume"
	case hermesops.ToolRenewTrigger:
		return "hermes.tool.renew_trigger"
	default:
		return ""
	}
}

// mutationAuditPayload builds the sanitized previous->next state payload for the
// admin_audit_events row. It uses the plan's Preview (enums/ids/state-names) +
// the admin actor attribution. The raw args are NOT folded in here (the
// tool_calls row already records the sanitized args); the credential payload of
// renew_trigger therefore never reaches the admin audit payload.
func mutationAuditPayload(toolName string, plan hermesops.MutationPlan, _ map[string]any) map[string]any {
	payload := map[string]any{
		"tool_name":   toolName,
		"target_type": plan.TargetType,
		"target_id":   plan.TargetID,
	}
	for k, v := range plan.Preview {
		payload[k] = v
	}
	return payload
}

// recordMutatingDryRun records the dry-run preview as a hermes_tool_calls row
// (status ok, dry_run=true) on the standalone ledger. Best-effort: a preview did
// not change state, so a transient ledger error is logged, not surfaced.
func (h handler) recordMutatingDryRun(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, plan hermesops.MutationPlan) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultOK, "", true, plan.Preview)
}

// recordMutatingDenied records an L1 denial (status denied) on the standalone
// ledger so a rejected mutating-tool attempt is auditable.
func (h handler) recordMutatingDenied(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultDenied, "", false, nil)
}

// recordMutatingError records a failed mutating-tool attempt (resolve failure or
// aborted execution) on the standalone ledger (status error).
func (h handler) recordMutatingError(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, err error, dryRun bool) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultError, classifyMutatingError(err), dryRun, nil)
}

// recordMutatingRow appends one standalone hermes_tool_calls row for a path that
// does NOT go through the atomic orchestrator (denial / dry-run / pre-mutation
// error). Best-effort (logged on failure); the atomic path's own row is the
// authoritative record for a committed mutation.
func (h handler) recordMutatingRow(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, status hermesops.ResultStatus, errorClass string, dryRun bool, summary map[string]any) {
	if h.toolCalls == nil {
		return
	}
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
		DryRun:            dryRun,
	}
	if err := hermesops.RecordToolCall(r.Context(), h.toolCalls, rec); err != nil {
		logToolCallWriteFailure(req.ToolName, err)
	}
}

func classifyMutatingError(err error) string {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, hermesops.ErrTargetResolution):
		return "target_resolution_failed"
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		return "dependency_unwired"
	default:
		return "mutation_failed"
	}
}

func writeMutatingResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid or missing tool arguments")
	case errors.Is(err, hermesops.ErrTargetResolution):
		writeError(w, http.StatusNotFound, "hermes_tool_target_not_found", "mutation target not found for this tenant")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "could not resolve mutation target")
	}
}

func writeMutatingExecError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermes.ErrInvalidInput), errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid mutation request")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "mutation failed and was rolled back")
	}
}
