// KeyIssuer creates new api_keys rows on behalf of an authenticated
// operator. Pipeline:
//
//	RBAC check -> rate-limit check -> generate bearer + bcrypt hash
//	-> open TX:
//	   1. AdminInsertAPIKey
//	   2. InsertAdminAuditEvent (action='issue_api_key')
//	-> commit -> return IssueResult{Plaintext, ...} ONCE.
//
// CMB-5: IssueResult.Plaintext is the only place plaintext appears.
// audit payload is written WITHOUT plaintext or hash. Caller MUST NOT
// log IssueResult after handing it to the HTTP response.
//
// CMB-7: writes only to api_keys + admin_audit_events. No billing/pool/
// registry mutation.

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// IssueRequest is the input to KeyIssuer.Issue. Caller is the resolved
// admin identity. TenantID/UserID are the target tenant + end-user the
// new api_keys row will belong to.
type IssueRequest struct {
	Caller      AdminIdentity
	TenantID    int64
	UserID      int64
	Name        string
	Environment Environment // EnvLive or EnvTest; EnvAdmin is rejected
	ExpiresAt   *time.Time
	Reason      string
	RequestID   string // chi middleware-set; recorded into audit
}

// IssueResult is what the issuer returns. Plaintext is shown ONCE in
// the HTTP response; never logged, never persisted.
type IssueResult struct {
	APIKeyID  int64
	Plaintext string // SECRET — see CMB-5; elided by String()
	KeyPrefix string
	Status    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// String redacts Plaintext so accidental fmt.Printf("%v", res) doesn't
// leak the bearer into logs.
func (r IssueResult) String() string {
	plaintextRedacted := "<redacted>"
	if r.Plaintext == "" {
		plaintextRedacted = "<empty>"
	}
	return fmt.Sprintf("IssueResult{APIKeyID:%d KeyPrefix:%q Plaintext:%s Status:%q}",
		r.APIKeyID, r.KeyPrefix, plaintextRedacted, r.Status)
}

// KeyIssuer mints api_keys rows. Construct via NewKeyIssuer.
type KeyIssuer struct {
	pool                *pgxpool.Pool
	bcryptCost          int
	rateLimitPerHour    int
	rateLimitWindowSecs int
}

// NewKeyIssuer wraps a pgxpool. Defaults: bcrypt cost 10 (consistent
// with customer keys), 30 issues/hour per actor (D4).
func NewKeyIssuer(pool *pgxpool.Pool) *KeyIssuer {
	return &KeyIssuer{
		pool:                pool,
		bcryptCost:          bcrypt.DefaultCost,
		rateLimitPerHour:    30,
		rateLimitWindowSecs: 3600,
	}
}

// Issue runs the full issuance pipeline. Returns ErrAdminUnauthorized /
// ErrAdminForbidden / ErrAdminRateLimited / ErrAdminBadRequest /
// ErrAdminBackend per the synthesized plan §D1+D4.
func (i *KeyIssuer) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
	if i == nil || i.pool == nil {
		return IssueResult{}, fmt.Errorf("%w: issuer not configured", ErrAdminBackend)
	}
	if req.Name == "" || req.TenantID == 0 || req.UserID == 0 {
		return IssueResult{}, fmt.Errorf("%w: name, tenant_id, user_id required", ErrAdminBadRequest)
	}
	if req.Environment != EnvLive && req.Environment != EnvTest {
		return IssueResult{}, fmt.Errorf("%w: environment must be live or test", ErrAdminBadRequest)
	}

	// RBAC.
	if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
		_ = i.audit(ctx, req, "denied", "rbac_violation", 0)
		return IssueResult{}, err
	}

	// validate target tenant + user are active and not
	// soft-deleted BEFORE we mint a bearer. Otherwise an invalid target
	// surfaces as either a 503 (FK violation wrapped as ErrAdminBackend)
	// or — worse — a perfectly-issued key that the customer resolver
	// rejects on the next request because of soft-delete state.
	{
		q := admindb.New(i.pool)
		check, err := q.AdminCheckIssuanceTarget(ctx, admindb.AdminCheckIssuanceTargetParams{
			TenantID: req.TenantID,
			UserID:   req.UserID,
		})
		if err != nil {
			return IssueResult{}, fmt.Errorf("%w: validate target: %v", ErrAdminBackend, err)
		}
		if !check.TenantOk {
			_ = i.audit(ctx, req, "denied", "tenant_inactive_or_missing", 0)
			return IssueResult{}, fmt.Errorf("%w: target tenant inactive or missing", ErrAdminBadRequest)
		}
		if !check.UserOk {
			_ = i.audit(ctx, req, "denied", "user_inactive_or_missing", 0)
			return IssueResult{}, fmt.Errorf("%w: target user inactive or missing for tenant", ErrAdminBadRequest)
		}
	}

	// cheap pre-flight rate-limit check BEFORE bcrypt so
	// an over-quota actor can't burn cost-10 hash CPU per spam request.
	// The authoritative atomic check still runs inside the TX with the
	// per-actor advisory lock — this preflight is best-effort.
	{
		q := admindb.New(i.pool)
		count, err := q.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
			ActorID:       fmt.Sprintf("%d", req.Caller.TokenID),
			WindowSeconds: int32(i.rateLimitWindowSecs),
		})
		if err != nil {
			return IssueResult{}, fmt.Errorf("%w: rate-limit preflight: %v", ErrAdminBackend, err)
		}
		if int(count) >= i.rateLimitPerHour {
			_ = i.audit(ctx, req, "denied", "rate_limited", 0)
			return IssueResult{}, ErrAdminRateLimited
		}
	}

	// Generate bearer + hash. These run BEFORE the TX so a slow bcrypt
	// doesn't hold a row lock.
	bearer, prefix, err := GenerateBearer(req.Environment)
	if err != nil {
		return IssueResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), i.bcryptCost)
	if err != nil {
		return IssueResult{}, fmt.Errorf("%w: bcrypt: %v", ErrAdminBackend, err)
	}

	// TX: lock-per-actor + rate-limit + insert api_keys + audit row
	// atomically. The count and insert must be inside
	// the same TX behind a per-actor advisory lock, otherwise concurrent
	// requests race past the 30/hour cap. The advisory lock auto-releases
	// at TX end.
	actorID := fmt.Sprintf("%d", req.Caller.TokenID)
	rateLimited := false
	out := IssueResult{KeyPrefix: prefix, Status: "active"}
	err = i.tx(ctx, func(qtx *admindb.Queries) error {
		if err := qtx.AcquireAdminIssuanceLock(ctx, actorID); err != nil {
			return fmt.Errorf("%w: advisory lock: %v", ErrAdminBackend, err)
		}
		count, err := qtx.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
			ActorID:       actorID,
			WindowSeconds: int32(i.rateLimitWindowSecs),
		})
		if err != nil {
			return fmt.Errorf("%w: rate-limit count: %v", ErrAdminBackend, err)
		}
		if int(count) >= i.rateLimitPerHour {
			rateLimited = true
			return ErrAdminRateLimited
		}
		row, err := qtx.AdminInsertAPIKey(ctx, admindb.AdminInsertAPIKeyParams{
			TenantID:  req.TenantID,
			UserID:    req.UserID,
			Name:      req.Name,
			KeyHash:   string(hash),
			KeyPrefix: prefix,
			ExpiresAt: pgTimestampPtr(req.ExpiresAt),
		})
		if err != nil {
			// AdminInsertAPIKey is now conditional on
			// tenant + user being active at the moment of write. NoRows
			// = target raced to disabled/deleted between preflight and
			// commit; surface as bad-request, not backend.
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: target tenant or user became inactive", ErrAdminBadRequest)
			}
			return fmt.Errorf("%w: insert api_key: %v", ErrAdminBackend, err)
		}
		out.APIKeyID = row.ID
		out.CreatedAt = row.CreatedAt.Time

		// Audit payload: prefix + tenant + user + environment. NEVER
		// includes plaintext bearer or hash (CMB-5).
		payloadBytes, _ := json.Marshal(map[string]any{
			"key_prefix":  prefix,
			"tenant_id":   req.TenantID,
			"user_id":     req.UserID,
			"environment": req.Environment,
			"name":        req.Name,
		})
		actorRole := req.Caller.Role
		if actorRole == "" {
			actorRole = RoleTenantOperator
		}
		if _, err := qtx.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   nullableInt64(req.TenantID),
			ActorID:    fmt.Sprintf("%d", req.Caller.TokenID),
			ActorRole:  actorRole,
			Action:     "issue_api_key",
			TargetType: "api_key",
			TargetID:   nullableInt64(row.ID),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Reason),
			Payload:    payloadBytes,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		// Rate-limit deny path: TX rolled back, so write the deny audit
		// row in a fresh connection. Best-effort; swallow the audit-insert
		// error because the deny outcome is already returned to the caller.
		if rateLimited {
			_ = i.audit(ctx, req, "denied", "rate_limited", 0)
		}
		return IssueResult{}, err
	}

	// If caller was the bootstrap row and just issued a non-bootstrap
	// admin (would be unusual via /admin/v1/api-keys; bootstrap typically
	// targets api_keys not admin_tokens), auto-disable the bootstrap row
	// so the env-var token stops working. Safe-best-effort.
	// Skipped here because /admin/v1/api-keys issues api_keys rows, not
	// admin_tokens rows; the bootstrap deactivation hook lives in the
	// admin_tokens issuance flow (Phase E).

	out.Plaintext = bearer
	out.ExpiresAt = req.ExpiresAt
	return out, nil
}

// audit records a deny path admin_audit_events row outside any TX.
// Returns the insert error (for logging by caller); we swallow at the
// call sites because the deny outcome is already returned to the caller.
func (i *KeyIssuer) audit(ctx context.Context, req IssueRequest, outcome, reason string, targetID int64) error {
	q := admindb.New(i.pool)
	payload, _ := json.Marshal(map[string]any{
		"outcome":   outcome,
		"reason":    reason,
		"tenant_id": req.TenantID,
		"user_id":   req.UserID,
	})
	actorRole := req.Caller.Role
	if actorRole == "" {
		actorRole = RoleTenantOperator
	}
	// deny-audit ALWAYS sets tenant_id=NULL because the
	// caller may have targeted a tenant that does not exist; the FK on
	// admin_audit_events.tenant_id would otherwise reject the row and we
	// would silently lose the deny event. The attempted tenant_id stays
	// in the payload jsonb for forensic review.
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   nil,
		ActorID:    fmt.Sprintf("%d", req.Caller.TokenID),
		ActorRole:  actorRole,
		Action:     "issue_api_key",
		TargetType: "api_key",
		TargetID:   nullableInt64(targetID),
		RequestID:  nullableString(req.RequestID),
		Reason:     nullableString(reason),
		Payload:    payload,
	})
	return err
}

// tx runs fn inside a fresh transaction. Mirrors the pattern used by
// registry.PostgresRegistry.ResolveModel (which uses REPEATABLE READ
// read-only). Issuance writes so we use the default isolation but keep
// the same shape.
func (i *KeyIssuer) tx(ctx context.Context, fn func(*admindb.Queries) error) error {
	tx, err := i.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(admindb.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrAdminBackend, err)
	}
	return nil
}

// pgTimestampPtr converts an optional *time.Time into pgtype.Timestamptz.
// Nil pointer => zero (Valid=false), which Postgres stores as NULL.
func pgTimestampPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ = errors.New // future expansion
