package hermesops

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// This file declares the WAVE H4 MUTATING tool specs (the "fix" capability).
// Each tool WRAPS an existing gateway mutation function — it never reimplements
// the mutation. A mutating tool sets Mutating=true + RequiresConfirmation=true
// and provides Resolve (read-only target resolution + dry-run preview) and
// Mutate (the actual change), NOT Run. The orchestration of the 5-layer safety
// contract (RBAC floor, dry-run+confirm, atomic audit, advisory lock,
// idempotency) lives in the HTTP/orchestrator layer; these specs supply the
// read (Resolve) and the wrapped mutation (Mutate).
//
// PRIVACY: every Preview / ToolResult.Summary carries ONLY enums / counts / ids
// / state-names. Rotated credential material is NEVER returned to the caller —
// renew_trigger surfaces only the post-rotation version number + state.

// ---------------------------------------------------------------------------
// account_pause / account_resume
// ---------------------------------------------------------------------------

// AccountMutationDeps wires the account pause/resume tools to the EXISTING
// provider-account enabled mutation + the channelhealth manual override
// coordination. GetAccount is the read used by Resolve to fetch current state +
// verify tenant ownership. SetEnabledTx flips provider_accounts.enabled inside
// the orchestrator transaction (so the flip is atomic with the audit rows).
// Coordinate (optional) performs the channelhealth manual pause/resume AFTER the
// enable/disable commits — it is a derived-cache coordination, not the source of
// truth, so a transient coordination error is reported but does not roll back
// the committed enable/disable (mirroring how the existing admin handlers treat
// enabled vs channel-health as separate authorities).
type AccountMutationDeps struct {
	GetAccount func(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	// Coordinate performs the channelhealth ManualPause/ManualResume for the
	// account after the enabled flip commits. Optional (nil => skipped). It is
	// passed the resolved account row + actor so it can build the channel key.
	Coordinate func(ctx context.Context, account admindb.AdminProviderAccountRow, pause bool, actorID, reason string) error
}

// AccountPauseSpec builds the account_pause mutating tool: it disables a provider
// account (enabled=false) via the same path the admin handler uses, plus the
// channelhealth manual-pause coordination. Args: { "account_id": <int64> }.
func AccountPauseSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountPause, "Pause (disable) a provider account so the dispatcher stops selecting it; coordinates channel-health manual pause. MUTATING — dry-run + confirm required.", false, deps)
}

// AccountResumeSpec builds the account_resume mutating tool: it re-enables a
// provider account (enabled=true) + the channelhealth manual-resume
// coordination. Args: { "account_id": <int64> }.
func AccountResumeSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountResume, "Resume (re-enable) a provider account so the dispatcher can select it again; coordinates channel-health manual resume. MUTATING — dry-run + confirm required.", true, deps)
}

// accountToggleSpec is the shared builder for pause/resume — they differ only in
// the target enabled value + audit action, so the resolve/mutate logic is one
// path (the dry-run preview cannot diverge from the action because both derive
// the same intended next-state here).
func accountToggleSpec(name, description string, targetEnabled bool, deps AccountMutationDeps) ToolSpec {
	return ToolSpec{
		Name:                 name,
		Category:             CategoryMutating,
		Description:          description,
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		// account enable/disable is a REVERSIBLE B-level mutation → LLM-proposable
		// (the LLM may propose it; an operator still confirms before it runs). Contrast
		// renew_trigger (credential rotation), which leaves Proposable false: operator
		// may drive it directly, the LLM never proposes it.
		Proposable: true,
		// pause/resume are scoped: platform_admin OR a tenant_operator within the
		// target's tenant (the H1 middleware + Resolve re-check enforce the tenant
		// scope; this floor admits tenant_operator).
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"account_id": "provider account id to toggle (int64, required)",
		},
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.GetAccount == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return MutationPlan{}, err
			}
			account, err := deps.GetAccount(ctx, admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: req.TenantID})
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: account %d not found for tenant %d", ErrTargetResolution, accountID, req.TenantID)
			}
			// Re-check tenant ownership against the resolved row (defense in depth
			// on top of the GetAccount tenant filter): a mutation must never touch
			// a row outside the resolved tenant.
			if account.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: account tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "provider_account",
				TargetID:   accountID,
				LockKey:    fmt.Sprintf("hermes:account_toggle:%d:%d", req.TenantID, accountID),
				Preview: map[string]any{
					"target_type":     "provider_account",
					"account_id":      accountID,
					"current_enabled": account.Enabled,
					"next_enabled":    targetEnabled,
					"health_state":    account.HealthState,
					"no_op":           account.Enabled == targetEnabled,
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.GetAccount == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tx := txFromContext(ctx)
			if tx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID := plan.TargetID
			actorID := fmt.Sprintf("%d", req.ActorUserID)
			// Flip enabled INSIDE the orchestrator transaction so the state change
			// is atomic with the tool_calls + admin_audit rows.
			if err := admindb.New(tx).UpdateProviderAccountEnabled(ctx, admindb.UpdateProviderAccountEnabledParams{
				Enabled: targetEnabled, ActorID: &actorID, ID: accountID, TenantID: req.TenantID,
			}); err != nil {
				return ToolResult{}, err
			}
			// channelhealth coordination runs AFTER the enabled flip is staged in
			// the tx; it uses its own store/tx. A transient coordination failure is
			// surfaced in the summary but does NOT abort the committed flip — the
			// enabled column is the dispatcher's source of truth.
			coordinated := false
			coordErr := ""
			if deps.Coordinate != nil {
				account, err := deps.GetAccount(ctx, admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: req.TenantID})
				if err == nil {
					if cErr := deps.Coordinate(ctx, account, !targetEnabled, actorID, "hermes ops "+req.Role); cErr != nil {
						coordErr = "channel_health_coordination_failed"
					} else {
						coordinated = true
					}
				} else {
					coordErr = "channel_health_lookup_failed"
				}
			}
			summary := map[string]any{
				"account_id":       accountID,
				"previous_enabled": plan.Preview["current_enabled"],
				"enabled":          targetEnabled,
				"coordinated":      coordinated,
			}
			return ToolResult{Summary: summary, ErrorClass: coordErr}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// txFromContext: the orchestrator threads its tx via context so a tool's Mutate
// can run tx-bound writes without widening the Mutate signature.
// ---------------------------------------------------------------------------

type mutationTxKey struct{}

// withMutationTx stashes the orchestrator transaction in the context for the
// mutate callback. Used by the orchestrator only.
func withMutationTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, mutationTxKey{}, tx)
}

// txFromContext recovers the orchestrator transaction. Returns nil when absent
// (a mutating tool that needs the tx fails closed with ErrDependencyUnwired).
func txFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(mutationTxKey{}).(pgx.Tx)
	return tx
}
