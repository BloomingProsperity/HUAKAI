// KeyRevoker handles api_keys revocation for the admin endpoint. Soft
// revoke only — billing tables FK back to api_keys with ON DELETE
// RESTRICT (per N+4b1 migration 0009), so hard delete is structurally
// impossible while audit history exists. Per CLAUDE.md, that's the
// intended invariant.

package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// RevokeRequest captures the operator's revoke call.
type RevokeRequest struct {
	Caller    AdminIdentity
	APIKeyID  int64
	TenantID  int64 // tenant the key belongs to (RBAC scope check)
	Reason    string
	RequestID string
}

// RevokeResult tells the handler what happened. AlreadyRevoked=true
// when revoke is idempotent (status was not 'active').
type RevokeResult struct {
	APIKeyID        int64
	AlreadyRevoked  bool
}

// KeyRevoker mirrors KeyIssuer's TX shape. Construct via NewKeyRevoker.
type KeyRevoker struct {
	pool *pgxpool.Pool
}

func NewKeyRevoker(pool *pgxpool.Pool) *KeyRevoker {
	return &KeyRevoker{pool: pool}
}

// Revoke flips api_keys.status to 'revoked' for one key. RBAC: same
// rules as Issue — platform_admin global, tenant_operator own-tenant.
// Idempotent: revoking an already-revoked key returns AlreadyRevoked=true.
func (r *KeyRevoker) Revoke(ctx context.Context, req RevokeRequest) (RevokeResult, error) {
	if r == nil || r.pool == nil {
		return RevokeResult{}, fmt.Errorf("%w: revoker not configured", ErrAdminBackend)
	}
	if req.APIKeyID == 0 || req.TenantID == 0 {
		return RevokeResult{}, fmt.Errorf("%w: api_key_id and tenant_id required", ErrAdminBadRequest)
	}
	if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
		// Codex N+4b2 pass-7 P2: denied revoke attempts must hit the
		// audit trail. Best-effort write; the caller still gets the
		// 403 even if audit insertion fails.
		_ = r.auditDeny(ctx, req, "rbac_violation")
		return RevokeResult{}, err
	}

	out := RevokeResult{APIKeyID: req.APIKeyID}
	err := r.tx(ctx, func(qtx *db.Queries) error {
		// Verify the key exists in this tenant first; AdminGetAPIKeyByID
		// returns NoRows for missing or wrong-tenant keys (D7-style 404).
		row, err := qtx.AdminGetAPIKeyByID(ctx, db.AdminGetAPIKeyByIDParams{
			ID:       req.APIKeyID,
			TenantID: req.TenantID,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("%w: api_key %d in tenant %d", ErrAdminNotFound, req.APIKeyID, req.TenantID)
			}
			return fmt.Errorf("%w: get api_key: %v", ErrAdminBackend, err)
		}
		// Codex pass-6 P2: only an already-revoked row is the idempotent
		// path. disabled/expired rows still get flipped to revoked so
		// operators can't be tricked into thinking a disabled key is
		// safely retired when it's actually still revocable.
		if row.Status == "revoked" {
			out.AlreadyRevoked = true
		} else {
			rows, err := qtx.AdminRevokeAPIKey(ctx, db.AdminRevokeAPIKeyParams{
				ID:       req.APIKeyID,
				TenantID: req.TenantID,
				Reason:   req.Reason,
			})
			if err != nil {
				return fmt.Errorf("%w: revoke api_key: %v", ErrAdminBackend, err)
			}
			if rows == 0 {
				// Race: row flipped to 'revoked' between the SELECT and
				// the UPDATE. Treat as idempotent.
				out.AlreadyRevoked = true
			}
		}

		// Audit (always, even idempotent).
		payloadBytes, _ := json.Marshal(map[string]any{
			"api_key_id":      req.APIKeyID,
			"tenant_id":       req.TenantID,
			"already_revoked": out.AlreadyRevoked,
		})
		actorRole := req.Caller.Role
		if actorRole == "" {
			actorRole = RoleTenantOperator
		}
		if _, err := qtx.InsertAdminAuditEvent(ctx, db.InsertAdminAuditEventParams{
			TenantID:   nullableInt64(req.TenantID),
			ActorID:    fmt.Sprintf("%d", req.Caller.TokenID),
			ActorRole:  actorRole,
			Action:     "revoke_api_key",
			TargetType: "api_key",
			TargetID:   nullableInt64(req.APIKeyID),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Reason),
			Payload:    payloadBytes,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		return RevokeResult{}, err
	}
	return out, nil
}

// auditDeny writes a denied 'revoke_api_key' audit row outside any TX.
// Codex pass-7 P2: invoked from RBAC-rejection paths so denied
// revoke attempts still appear in incident review.
func (r *KeyRevoker) auditDeny(ctx context.Context, req RevokeRequest, reason string) error {
	q := db.New(r.pool)
	payload, _ := json.Marshal(map[string]any{
		"outcome":    "denied",
		"reason":     reason,
		"api_key_id": req.APIKeyID,
		"tenant_id":  req.TenantID,
	})
	actorRole := req.Caller.Role
	if actorRole == "" {
		actorRole = RoleTenantOperator
	}
	// Codex N+4b2 pass-10 P2: deny-audit MUST NOT write under the
	// attacker-supplied tenant_id, otherwise a tenant_operator probing
	// other tenants pollutes their audit trails. Use NULL tenant scope;
	// attempted tenant_id stays in the payload jsonb for forensic review.
	_, err := q.InsertAdminAuditEvent(ctx, db.InsertAdminAuditEventParams{
		TenantID:   nil,
		ActorID:    fmt.Sprintf("%d", req.Caller.TokenID),
		ActorRole:  actorRole,
		Action:     "revoke_api_key",
		TargetType: "api_key",
		TargetID:   nullableInt64(req.APIKeyID),
		RequestID:  nullableString(req.RequestID),
		Reason:     nullableString(reason),
		Payload:    payload,
	})
	return err
}

func (r *KeyRevoker) tx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrAdminBackend, err)
	}
	return nil
}
