package hermesops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// This file declares the dlq_replay + renew_trigger MUTATING tools. Like the
// account toggles, each WRAPS an existing mutation. Unlike the account toggles,
// the underlying mutation (dlq.Service.Replay, credentialstore.Store.Rotate)
// manages its OWN transaction + side effects (handler invocation / nested
// credential-audit tx), so it cannot be folded into the orchestrator tx. For
// these two, the orchestrator's verified-before-commit ordering applies: the
// hermes_tool_calls + admin_audit_events rows are inserted + accepted by the DB
// inside the orchestrator tx BEFORE Replay/Rotate runs, so a broken audit path
// aborts with the target unchanged.

// ---------------------------------------------------------------------------
// dlq_replay  (platform_admin ONLY)
// ---------------------------------------------------------------------------

// DLQReplayDeps wires the dlq_replay tool. Lookup is the read used by Resolve to
// fetch the target record (tenant-scoped) for the preview + tenant re-check.
// Replay is the EXISTING dlq.Service.Replay, which re-claims the record by id
// (using its IdempotencyKey to dedupe) and re-runs the delivery handler.
type DLQReplayDeps struct {
	Lookup func(ctx context.Context, id, tenantID int64) (dlq.Record, error)
	Replay func(ctx context.Context, id int64, actorID string) (*dlq.Record, error)
}

// DLQReplaySpec builds the dlq_replay mutating tool. platform_admin ONLY — a
// tenant_operator is forbidden (the RBAC floor is RolePlatformAdmin, mirroring
// the admin DLQ handler's platform-admin gate). Args: { "id": <int64> }.
func DLQReplaySpec(deps DLQReplayDeps) ToolSpec {
	return ToolSpec{
		Name:                 ToolDLQReplay,
		Category:             CategoryMutating,
		Description:          "Re-deliver a dead-lettered event by id (idempotent on the record's idempotency key). platform_admin ONLY. MUTATING — dry-run + confirm required.",
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		RequiredRole:         RolePlatformAdmin,
		InputSchema: map[string]string{
			"id": "dead-letter record id to replay (int64, required)",
		},
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.Lookup == nil || deps.Replay == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			id, err := ArgInt(req.Args, "id")
			if err != nil {
				return MutationPlan{}, err
			}
			rec, err := deps.Lookup(ctx, id, req.TenantID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: dlq record %d not found for tenant %d", ErrTargetResolution, id, req.TenantID)
			}
			if rec.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: dlq record tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "dlq_event",
				TargetID:   id,
				LockKey:    fmt.Sprintf("hermes:dlq_replay:%d:%d", req.TenantID, id),
				Preview: map[string]any{
					"target_type":     "dlq_event",
					"dlq_id":          id,
					"event_kind":      string(rec.EventKind),
					"lane":            string(rec.Lane),
					"current_status":  string(rec.Status),
					"replay_attempts": rec.ReplayAttempts,
					"intended_action": "re_deliver",
					// already_delivered makes the preview honest about a replay that
					// the idempotency guard will no-op.
					"already_delivered": rec.Status == dlq.StatusDelivered,
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.Replay == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			// L5 idempotency: Replay re-claims by id and the record's
			// IdempotencyKey dedupes — an already-delivered record is not
			// re-processed (ClaimByID refuses an active/closed claim). actorID is
			// the operator's threaded user id.
			actorID := fmt.Sprintf("%d", req.ActorUserID)
			rec, err := deps.Replay(ctx, plan.TargetID, actorID)
			if err != nil {
				return ToolResult{}, err
			}
			summary := map[string]any{
				"dlq_id":          plan.TargetID,
				"previous_status": plan.Preview["current_status"],
			}
			if rec != nil {
				summary["status"] = string(rec.Status)
				summary["replay_attempts"] = rec.ReplayAttempts
			}
			return ToolResult{Summary: summary}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// renew_trigger  (platform_admin OR tenant_operator within tenant)
// ---------------------------------------------------------------------------

// RenewTriggerDeps wires the renew_trigger tool. ListByAccount is the read used
// by Resolve to fetch the credential's current version + verify tenant
// ownership. Rotate is the EXISTING credentialstore.Store.Rotate, which
// atomically supersedes the prior credential version (optimistic
// credential_version match -> version+1) and returns metadata ONLY (never the
// rotated payload).
type RenewTriggerDeps struct {
	ListByAccount func(ctx context.Context, tenantID, accountID int64) ([]credentialstore.CredentialMetadata, error)
	Rotate        func(ctx context.Context, in credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error)
}

// RenewTriggerSpec builds the renew_trigger mutating tool: it rotates a provider
// account credential to a new payload, invalidating the prior version. Scoped:
// platform_admin OR tenant_operator within the target tenant. Args:
// { "account_id": <int64>, "credential_id": <int64>, "credentials": <object> }.
// PRIVACY: the new credential material ("credentials") is accepted but is a
// sensitive arg (redacted in the audit row); the rotated material is NEVER
// returned — only the resulting version + state.
func RenewTriggerSpec(deps RenewTriggerDeps) ToolSpec {
	return ToolSpec{
		Name:                 ToolRenewTrigger,
		Category:             CategoryMutating,
		Description:          "Rotate a provider account credential to a new payload, invalidating the prior version. MUTATING — dry-run + confirm required. Rotated material is never returned.",
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		RequiredRole:         RoleTenantOperator,
		InputSchema: map[string]string{
			"account_id":    "provider account id owning the credential (int64, required)",
			"credential_id": "credential id to rotate (int64, required)",
			"credentials":   "new credential payload (object, required, redacted in audit)",
		},
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.ListByAccount == nil || deps.Rotate == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return MutationPlan{}, err
			}
			credentialID, err := ArgInt(req.Args, "credential_id")
			if err != nil {
				return MutationPlan{}, err
			}
			rows, err := deps.ListByAccount(ctx, req.TenantID, accountID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: account %d credentials not readable for tenant %d", ErrTargetResolution, accountID, req.TenantID)
			}
			var target *credentialstore.CredentialMetadata
			for i := range rows {
				if rows[i].ID == credentialID {
					target = &rows[i]
					break
				}
			}
			if target == nil {
				return MutationPlan{}, fmt.Errorf("%w: credential %d not found on account %d", ErrTargetResolution, credentialID, accountID)
			}
			if target.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: credential tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "account_credential",
				TargetID:   credentialID,
				LockKey:    fmt.Sprintf("hermes:renew_trigger:%d:%d", req.TenantID, credentialID),
				Preview: map[string]any{
					"target_type":     "account_credential",
					"account_id":      accountID,
					"credential_id":   credentialID,
					"vendor":          target.Vendor,
					"current_version": target.Version,
					"current_state":   target.State,
					"next_version":    target.Version + 1,
					"intended_action": "rotate",
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.Rotate == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return ToolResult{}, err
			}
			payload, err := rotatePayload(req.Args)
			if err != nil {
				return ToolResult{}, err
			}
			meta, err := deps.Rotate(ctx, credentialstore.RotateCredentialInput{
				TenantID:          req.TenantID,
				ProviderAccountID: accountID,
				CredentialID:      plan.TargetID,
				Payload:           payload,
				ActorID:           fmt.Sprintf("%d", req.ActorUserID),
			})
			if err != nil {
				return ToolResult{}, err
			}
			// PRIVACY: surface ONLY the resulting version + state — never the
			// rotated payload (CredentialMetadata carries no payload field).
			return ToolResult{Summary: map[string]any{
				"credential_id":    plan.TargetID,
				"previous_version": plan.Preview["current_version"],
				"new_version":      meta.Version,
				"state":            meta.State,
			}}, nil
		},
	}
}

// rotatePayload extracts the new credential payload from the args and re-encodes
// it as JSON for Rotate. The payload is NEVER persisted raw — it flows only into
// the rotation; the audit row redacts the "credentials" key.
func rotatePayload(args map[string]any) ([]byte, error) {
	raw, ok := args["credentials"]
	if !ok {
		return nil, fmt.Errorf("%w: credentials payload required", ErrInvalidArgs)
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("%w: credentials payload empty", ErrInvalidArgs)
		}
		return []byte(v), nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%w: credentials payload not encodable", ErrInvalidArgs)
		}
		return encoded, nil
	}
}
