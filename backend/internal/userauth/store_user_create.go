package userauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// CreateUser 是所有最终用户建号入口的唯一持久化实现。它与租户停用共用行锁，
// 保证“注册完整提交后再停用”或“停用先提交并拒绝注册”，不会出现停用后落下半套新事实。
func (s *PostgresStore) CreateUser(ctx context.Context, in CreateUserParams) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tx, ok := s.db.(pgx.Tx); ok {
		return createUserInTx(ctx, tx, in)
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return User{}, ErrStoreNotConfigured
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	user, err := createUserInTx(ctx, tx, in)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func createUserInTx(ctx context.Context, tx pgx.Tx, in CreateUserParams) (User, error) {
	if err := tenancy.LockActiveForWrite(ctx, tx, in.TenantID); err != nil {
		if errors.Is(err, tenancy.ErrTenantInactive) {
			return User{}, ErrRegistrationDisabled
		}
		return User{}, fmt.Errorf("userauth: lock active tenant: %w", err)
	}
	status := in.Status
	if status == "" {
		status = UserStatusPendingVerification
	}
	const query = `
INSERT INTO users (
    tenant_id, email, display_name, password_hash, email_verified,
    invite_code_used, social_login_provider, status
) VALUES (
    $1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), NULLIF($7, ''), $8
)
RETURNING id, tenant_id, email, display_name, password_hash, email_verified,
          invite_code_used, social_login_provider, status, password_version,
          failed_login_count, locked_until, created_at, updated_at`
	return scanUser(tx.QueryRow(ctx, query,
		in.TenantID,
		NormalizeEmail(in.Email),
		strings.TrimSpace(in.DisplayName),
		strings.TrimSpace(in.PasswordHash),
		in.EmailVerified,
		strings.TrimSpace(in.InviteCodeUsed),
		strings.TrimSpace(in.SocialLoginProvider),
		status,
	))
}
