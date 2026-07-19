// Package tenantcapability 管理部署者授予下级租户管理员的高风险运营能力。
package tenantcapability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const AdvancedAccountIntake = "advanced_account_intake"

var (
	ErrNotConfigured     = errors.New("tenantcapability: 存储未配置")
	ErrInvalidInput      = errors.New("tenantcapability: 输入无效")
	ErrTenantNotFound    = errors.New("tenantcapability: 租户不存在")
	ErrCapabilityUnknown = errors.New("tenantcapability: 能力不存在")
)

type Grant struct {
	TenantID   int64      `json:"tenant_id"`
	Capability string     `json:"capability"`
	Enabled    bool       `json:"enabled"`
	Configured bool       `json:"configured"`
	UpdatedBy  string     `json:"updated_by,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	GrantedAt  *time.Time `json:"granted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type SetInput struct {
	TenantID   int64
	Capability string
	Enabled    bool
	Actor      string
	ActorRole  string
	Reason     string
	RequestID  string
}

type SetResult struct {
	Grant   Grant `json:"grant"`
	Changed bool  `json:"changed"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Allowed 对缺失记录返回 false，保证新租户默认没有高风险能力。
func (s *Store) Allowed(ctx context.Context, tenantID int64, capability string) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrNotConfigured
	}
	capability, err := normalizeCapability(capability)
	if err != nil || tenantID <= 0 {
		return false, ErrInvalidInput
	}
	var enabled bool
	err = s.pool.QueryRow(ctx, `
SELECT enabled
FROM tenant_admin_capability_grants
WHERE tenant_id = $1 AND capability = $2`, tenantID, capability).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

func (s *Store) List(ctx context.Context, tenantID int64) ([]Grant, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotConfigured
	}
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.pool.Query(ctx, `
SELECT tenant_id, capability, enabled, updated_by, reason,
       granted_at, revoked_at, updated_at
FROM tenant_admin_capability_grants
WHERE tenant_id = $1
ORDER BY capability`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Grant, 0)
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = append(out, Grant{
			TenantID: tenantID, Capability: AdvancedAccountIntake,
			Enabled: false, Configured: false,
		})
	}
	return out, nil
}

func (s *Store) Set(ctx context.Context, in SetInput) (SetResult, error) {
	if s == nil || s.pool == nil {
		return SetResult{}, ErrNotConfigured
	}
	capability, err := normalizeCapability(in.Capability)
	if err != nil {
		return SetResult{}, err
	}
	in.Capability = capability
	in.Actor = strings.TrimSpace(in.Actor)
	in.ActorRole = strings.TrimSpace(in.ActorRole)
	in.Reason = strings.TrimSpace(in.Reason)
	in.RequestID = strings.TrimSpace(in.RequestID)
	if in.TenantID <= 0 || in.Actor == "" || in.ActorRole == "" || in.Reason == "" || len(in.Reason) > 1000 {
		return SetResult{}, ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SetResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedTenantID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id = $1 FOR KEY SHARE`, in.TenantID).Scan(&lockedTenantID); errors.Is(err, pgx.ErrNoRows) {
		return SetResult{}, ErrTenantNotFound
	} else if err != nil {
		return SetResult{}, err
	}
	current, found, err := loadGrantForUpdate(ctx, tx, in.TenantID, capability)
	if err != nil {
		return SetResult{}, err
	}
	if found && current.Enabled == in.Enabled {
		if err := tx.Commit(ctx); err != nil {
			return SetResult{}, err
		}
		return SetResult{Grant: current, Changed: false}, nil
	}
	grant, err := upsertGrant(ctx, tx, in)
	if err != nil {
		return SetResult{}, err
	}
	payload, _ := json.Marshal(map[string]any{"capability": capability, "enabled": in.Enabled})
	action := "grant_tenant_capability"
	if !in.Enabled {
		action = "revoke_tenant_capability"
	}
	var requestID *string
	if in.RequestID != "" {
		requestID = &in.RequestID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id,
    request_id, reason, payload, log_category
) VALUES (NULL, $1, $2, $3, 'tenant', $4, $5, $6, $7, 'security')`,
		in.Actor, in.ActorRole, action, in.TenantID, requestID, in.Reason, payload); err != nil {
		return SetResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SetResult{}, err
	}
	return SetResult{Grant: grant, Changed: true}, nil
}

func normalizeCapability(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != AdvancedAccountIntake {
		return "", fmt.Errorf("%w: %s", ErrCapabilityUnknown, value)
	}
	return value, nil
}

func loadGrantForUpdate(ctx context.Context, tx pgx.Tx, tenantID int64, capability string) (Grant, bool, error) {
	row := tx.QueryRow(ctx, `
SELECT tenant_id, capability, enabled, updated_by, reason,
       granted_at, revoked_at, updated_at
FROM tenant_admin_capability_grants
WHERE tenant_id = $1 AND capability = $2
FOR UPDATE`, tenantID, capability)
	grant, err := scanGrant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, false, nil
	}
	return grant, err == nil, err
}

func upsertGrant(ctx context.Context, tx pgx.Tx, in SetInput) (Grant, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO tenant_admin_capability_grants (
    tenant_id, capability, enabled, updated_by, reason, granted_at, revoked_at
) VALUES (
    $1, $2, $3, $4, $5,
    CASE WHEN $3 THEN clock_timestamp() ELSE NULL END,
    CASE WHEN $3 THEN NULL ELSE clock_timestamp() END
)
ON CONFLICT (tenant_id, capability) DO UPDATE
SET enabled = EXCLUDED.enabled,
    updated_by = EXCLUDED.updated_by,
    reason = EXCLUDED.reason,
    granted_at = CASE WHEN EXCLUDED.enabled THEN clock_timestamp() ELSE tenant_admin_capability_grants.granted_at END,
    revoked_at = CASE WHEN EXCLUDED.enabled THEN NULL ELSE clock_timestamp() END,
    updated_at = clock_timestamp()
RETURNING tenant_id, capability, enabled, updated_by, reason,
          granted_at, revoked_at, updated_at`,
		in.TenantID, in.Capability, in.Enabled, in.Actor, in.Reason)
	return scanGrant(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanGrant(row rowScanner) (Grant, error) {
	var grant Grant
	var grantedAt, revokedAt *time.Time
	var updatedAt time.Time
	err := row.Scan(&grant.TenantID, &grant.Capability, &grant.Enabled, &grant.UpdatedBy, &grant.Reason,
		&grantedAt, &revokedAt, &updatedAt)
	grant.Configured = err == nil
	grant.GrantedAt, grant.RevokedAt = grantedAt, revokedAt
	if err == nil {
		grant.UpdatedAt = &updatedAt
	}
	return grant, err
}
