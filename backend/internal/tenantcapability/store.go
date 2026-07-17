package tenantcapability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: time.Now}
}

func (s *Store) Require(ctx context.Context, tenantID int64, capability Capability) error {
	if s == nil || s.pool == nil || tenantID <= 0 {
		return ErrDenied
	}
	if _, err := Parse(string(capability)); err != nil {
		return ErrDenied
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, `
SELECT status='granted' AND (expires_at IS NULL OR expires_at>$3)
FROM tenant_capability_grants
WHERE tenant_id=$1 AND capability=$2`, tenantID, capability, s.nowTime()).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !allowed {
		return ErrDenied
	}
	if err != nil {
		return fmt.Errorf("tenant capability lookup: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, tenantID int64) ([]Grant, error) {
	if s == nil || s.pool == nil || tenantID <= 0 {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `
SELECT tenant_id,capability,status,expires_at,revision,granted_by_actor,revoked_by_actor,
       reason,created_at,updated_at
FROM tenant_capability_grants
WHERE tenant_id=$1
ORDER BY capability`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make([]Grant, 0, len(known))
	for rows.Next() {
		var grant Grant
		if err = rows.Scan(&grant.TenantID, &grant.Capability, &grant.Status, &grant.ExpiresAt,
			&grant.Revision, &grant.GrantedByActor, &grant.RevokedByActor, &grant.Reason,
			&grant.CreatedAt, &grant.UpdatedAt); err != nil {
			return nil, err
		}
		grant.Effective = grant.Status == "granted" && (grant.ExpiresAt == nil || grant.ExpiresAt.After(s.nowTime()))
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *Store) Set(ctx context.Context, mutation Mutation) (Grant, error) {
	mutation, err := normalizeMutation(mutation)
	if err != nil {
		return Grant{}, err
	}
	if s == nil || s.pool == nil {
		return Grant{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Grant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1 AND deleted_at IS NULL)`, mutation.TenantID).Scan(&tenantExists); err != nil {
		return Grant{}, err
	}
	if !tenantExists {
		return Grant{}, fmt.Errorf("%w: tenant does not exist", ErrInvalid)
	}
	status, action := "revoked", "revoke"
	var grantedActor, revokedActor *string
	if mutation.Enabled {
		status, action = "granted", "grant"
		grantedActor = &mutation.ActorID
	} else {
		revokedActor = &mutation.ActorID
	}
	var grant Grant
	err = tx.QueryRow(ctx, `
INSERT INTO tenant_capability_grants (
    tenant_id,capability,status,expires_at,granted_by_actor,revoked_by_actor,reason,created_at,updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
ON CONFLICT (tenant_id,capability) DO UPDATE SET
    status=EXCLUDED.status,expires_at=EXCLUDED.expires_at,
    granted_by_actor=CASE WHEN EXCLUDED.status='granted' THEN EXCLUDED.granted_by_actor ELSE tenant_capability_grants.granted_by_actor END,
    revoked_by_actor=EXCLUDED.revoked_by_actor,reason=EXCLUDED.reason,
    revision=tenant_capability_grants.revision+1,updated_at=EXCLUDED.updated_at
RETURNING tenant_id,capability,status,expires_at,revision,granted_by_actor,revoked_by_actor,
          reason,created_at,updated_at`, mutation.TenantID, mutation.Capability, status,
		mutation.ExpiresAt, grantedActor, revokedActor, mutation.Reason, mutation.Now).Scan(
		&grant.TenantID, &grant.Capability, &grant.Status, &grant.ExpiresAt, &grant.Revision,
		&grant.GrantedByActor, &grant.RevokedByActor, &grant.Reason, &grant.CreatedAt, &grant.UpdatedAt)
	if err != nil {
		return Grant{}, err
	}
	if _, err = tx.Exec(ctx, `
INSERT INTO tenant_capability_grant_events
    (tenant_id,capability,action,revision,actor_id,reason,expires_at,occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, grant.TenantID, grant.Capability, action,
		grant.Revision, mutation.ActorID, mutation.Reason, grant.ExpiresAt, mutation.Now); err != nil {
		return Grant{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Grant{}, err
	}
	grant.Effective = grant.Status == "granted" && (grant.ExpiresAt == nil || grant.ExpiresAt.After(mutation.Now))
	return grant, nil
}

func normalizeMutation(mutation Mutation) (Mutation, error) {
	if mutation.TenantID <= 0 {
		return Mutation{}, ErrInvalid
	}
	capability, err := Parse(string(mutation.Capability))
	if err != nil {
		return Mutation{}, err
	}
	mutation.Capability = capability
	mutation.ActorID = strings.TrimSpace(mutation.ActorID)
	mutation.Reason = strings.TrimSpace(mutation.Reason)
	if len(mutation.ActorID) < 3 || len(mutation.ActorID) > 200 || len(mutation.Reason) < 8 || len(mutation.Reason) > 1000 {
		return Mutation{}, ErrInvalid
	}
	if mutation.Now.IsZero() {
		mutation.Now = time.Now().UTC()
	} else {
		mutation.Now = mutation.Now.UTC()
	}
	if !mutation.Enabled {
		mutation.ExpiresAt = nil
	} else if mutation.ExpiresAt != nil {
		expires := mutation.ExpiresAt.UTC()
		if !expires.After(mutation.Now) || expires.After(mutation.Now.Add(366*24*time.Hour)) {
			return Mutation{}, ErrInvalid
		}
		mutation.ExpiresAt = &expires
	}
	return mutation, nil
}

func (s *Store) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
