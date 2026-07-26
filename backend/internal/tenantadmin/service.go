package tenantadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

const (
	maxTenantNameRunes = 120
	maxReasonRunes     = 1000
)

type Service struct {
	pool             *pgxpool.Pool
	platformTenantID int64
	now              func() time.Time
}

func NewService(pool *pgxpool.Pool, platformTenantID int64) *Service {
	return &Service{pool: pool, platformTenantID: platformTenantID, now: time.Now}
}

func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, name, status, version, COALESCE(status_reason, ''),
       status_changed_at, COALESCE(status_changed_by, ''),
       created_at, updated_at, deleted_at
FROM tenants
WHERE id > 0
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("tenantadmin: list tenants: %w", err)
	}
	defer rows.Close()
	items := make([]Tenant, 0)
	for rows.Next() {
		item, err := scanTenant(rows, s.platformTenantID)
		if err != nil {
			return nil, fmt.Errorf("tenantadmin: scan tenant: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenantadmin: list tenant rows: %w", err)
	}
	return items, nil
}

func (s *Service) Get(ctx context.Context, tenantID int64) (Tenant, error) {
	if err := s.configured(); err != nil {
		return Tenant{}, err
	}
	if tenantID <= 0 {
		return Tenant{}, ErrInvalidInput
	}
	item, err := scanTenant(s.pool.QueryRow(ctx, `
SELECT id, name, status, version, COALESCE(status_reason, ''),
       status_changed_at, COALESCE(status_changed_by, ''),
       created_at, updated_at, deleted_at
FROM tenants
WHERE id=$1`, tenantID), s.platformTenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("tenantadmin: get tenant: %w", err)
	}
	return item, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if err := s.configured(); err != nil {
		return CreateResult{}, err
	}
	name, err := normalizeTenantName(input.Name)
	if err != nil {
		return CreateResult{}, err
	}
	email, err := userauth.ValidateNewUserEmail(input.AdminEmail)
	if err != nil {
		return CreateResult{}, ErrInvalidInput
	}
	if err := userauth.ValidateNewUserPassword(input.AdminPassword); err != nil {
		return CreateResult{}, ErrInvalidInput
	}
	displayName, err := userauth.ValidateOptionalDisplayName(input.AdminDisplayName)
	if err != nil {
		return CreateResult{}, ErrInvalidInput
	}
	if displayName == "" {
		displayName = name + " 管理员"
	}
	audit, err := normalizeAudit(input.Audit, true)
	if err != nil {
		return CreateResult{}, err
	}
	passwordHash, err := userauth.HashPassword(input.AdminPassword, userauth.DefaultPasswordPolicy())
	if err != nil {
		return CreateResult{}, fmt.Errorf("tenantadmin: hash first admin password: %w", err)
	}

	var result CreateResult
	now := s.clockNow()
	err = runSerializableMutation(ctx, s.pool, func(tx pgx.Tx) error {
		tenant, err := scanTenant(tx.QueryRow(ctx, `
INSERT INTO tenants (
    name, status, version, status_reason, status_changed_at, status_changed_by,
    created_at, updated_at
) VALUES ($1, 'active', 1, $2, $3, $4, $3, $3)
RETURNING id, name, status, version, COALESCE(status_reason, ''),
          status_changed_at, COALESCE(status_changed_by, ''),
          created_at, updated_at, deleted_at`,
			name, audit.Reason, now, audit.ActorID,
		), s.platformTenantID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO tenant_wallets (tenant_id, balance, version, updated_at)
VALUES ($1, 0, 1, $2)`, tenant.ID, now); err != nil {
			return err
		}
		adminUser, err := userauth.NewPostgresStore(tx).CreateUser(ctx, userauth.CreateUserParams{
			TenantID: tenant.ID, Email: email, DisplayName: displayName,
			PasswordHash: passwordHash, EmailVerified: true, Status: userauth.UserStatusActive,
		})
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
UPDATE users
SET role='admin', updated_at=$3
WHERE tenant_id=$1 AND id=$2 AND principal_kind='human'`,
			tenant.ID, adminUser.ID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		payload, err := json.Marshal(map[string]any{
			"name": name, "first_admin_user_id": adminUser.ID,
		})
		if err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, tenant.ID, tenant.ID, "create_tenant", audit, payload); err != nil {
			return err
		}
		result = CreateResult{Tenant: tenant, FirstAdminID: adminUser.ID}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CreateResult{}, ErrConflict
		}
		return CreateResult{}, fmt.Errorf("tenantadmin: create tenant: %w", err)
	}
	return result, nil
}

func (s *Service) SetStatus(ctx context.Context, input StatusInput) (StatusResult, error) {
	if err := s.configured(); err != nil {
		return StatusResult{}, err
	}
	if input.TenantID <= 0 || input.ExpectedVersion <= 0 ||
		(input.Status != StatusActive && input.Status != StatusDisabled) {
		return StatusResult{}, ErrInvalidInput
	}
	audit, err := normalizeAudit(input.Audit, true)
	if err != nil {
		return StatusResult{}, err
	}
	var result StatusResult
	now := s.clockNow()
	err = runSerializableMutation(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := lockTenant(ctx, tx, input.TenantID, s.platformTenantID)
		if err != nil {
			return err
		}
		if before.IsPlatform {
			return ErrPlatformTenant
		}
		if before.Status == StatusDeleted {
			return ErrNotFound
		}
		if before.Status == input.Status {
			result = StatusResult{Tenant: before}
			return nil
		}
		if before.Version != input.ExpectedVersion {
			return ErrVersionConflict
		}
		if !validTransition(before.Status, input.Status) {
			return ErrInvalidTransition
		}
		after, err := scanTenant(tx.QueryRow(ctx, `
UPDATE tenants
SET status=$2, version=version+1, status_reason=$3,
    status_changed_at=$4, status_changed_by=$5, updated_at=$4
WHERE id=$1 AND version=$6
RETURNING id, name, status, version, COALESCE(status_reason, ''),
          status_changed_at, COALESCE(status_changed_by, ''),
          created_at, updated_at, deleted_at`,
			input.TenantID, input.Status, audit.Reason, now, audit.ActorID, input.ExpectedVersion,
		), s.platformTenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
		var sessionsRevoked int64
		if input.Status == StatusDisabled {
			sessionsRevoked, err = revokeTenantSessions(ctx, tx, input.TenantID, "tenant_disabled", now)
			if err != nil {
				return err
			}
		}
		payload, err := json.Marshal(map[string]any{
			"status_before": before.Status, "status_after": after.Status,
			"version_before": before.Version, "version_after": after.Version,
			"sessions_revoked": sessionsRevoked,
		})
		if err != nil {
			return err
		}
		action := "disable_tenant"
		if input.Status == StatusActive {
			action = "enable_tenant"
		}
		if err := insertAudit(ctx, tx, input.TenantID, input.TenantID, action, audit, payload); err != nil {
			return err
		}
		result = StatusResult{Tenant: after, Changed: true, SessionsRevoked: sessionsRevoked}
		return nil
	})
	if err != nil {
		return StatusResult{}, fmt.Errorf("tenantadmin: set status: %w", err)
	}
	return result, nil
}

func (s *Service) configured() error {
	if s == nil || s.pool == nil || s.platformTenantID <= 0 {
		return ErrNotConfigured
	}
	return nil
}

func (s *Service) clockNow() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func normalizeTenantName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxTenantNameRunes {
		return "", ErrInvalidInput
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidInput
		}
	}
	return name, nil
}

func normalizeAudit(input AuditInput, reasonRequired bool) (AuditInput, error) {
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ActorRole = strings.TrimSpace(input.ActorRole)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ActorID == "" || input.ActorRole == "" ||
		(reasonRequired && input.Reason == "") ||
		utf8.RuneCountInString(input.Reason) > maxReasonRunes {
		return AuditInput{}, ErrInvalidInput
	}
	return input, nil
}

func validTransition(from, to string) bool {
	return (from == StatusActive && to == StatusDisabled) ||
		(from == StatusDisabled && to == StatusActive)
}

type rowScanner interface {
	Scan(...any) error
}

func scanTenant(row rowScanner, platformTenantID int64) (Tenant, error) {
	var item Tenant
	if err := row.Scan(
		&item.ID, &item.Name, &item.Status, &item.Version, &item.StatusReason,
		&item.StatusChangedAt, &item.StatusChangedBy,
		&item.CreatedAt, &item.UpdatedAt, &item.DeletedAt,
	); err != nil {
		return Tenant{}, err
	}
	item.IsPlatform = item.ID == platformTenantID
	return item, nil
}

func lockTenant(ctx context.Context, tx pgx.Tx, tenantID, platformTenantID int64) (Tenant, error) {
	item, err := scanTenant(tx.QueryRow(ctx, `
SELECT id, name, status, version, COALESCE(status_reason, ''),
       status_changed_at, COALESCE(status_changed_by, ''),
       created_at, updated_at, deleted_at
FROM tenants
WHERE id=$1
FOR UPDATE`, tenantID), platformTenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	return item, err
}

func revokeTenantSessions(ctx context.Context, tx pgx.Tx, tenantID int64, reason string, now time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `
UPDATE session_families
SET status='revoked', revoked_at=$3, revoked_reason=$2, last_active_at=$3
WHERE tenant_id=$1 AND status IN ('active', 'suspicious')`, tenantID, reason, now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE refresh_tokens
SET status='revoked', consumed_at=COALESCE(consumed_at, $2)
WHERE tenant_id=$1 AND status='active'`, tenantID, now); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE session_tokens
SET revoked_at=COALESCE(revoked_at, $2)
WHERE tenant_id=$1 AND revoked_at IS NULL`, tenantID, now); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, targetID int64,
	action string,
	audit AuditInput,
	payload []byte,
) error {
	var requestID *string
	if audit.RequestID != "" {
		requestID = &audit.RequestID
	}
	var reason *string
	if audit.Reason != "" {
		reason = &audit.Reason
	}
	_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: audit.ActorID, ActorRole: audit.ActorRole,
		Action: action, TargetType: "tenant", TargetID: &targetID,
		RequestID: requestID, Reason: reason, Payload: payload,
	})
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
