package twofa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	dbtwofa "github.com/BloomingProsperity/HUAKAI/internal/db/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// EnableWithSessionInvalidation 在启用 2FA 的同时保留当前会话族并撤销其他会话。
// PostgreSQL 路径把两项变化放进同一事务；测试存储走可回退的内存路径。
func (s *Service) EnableWithSessionInvalidation(
	ctx context.Context,
	in VerifyInput,
	currentFamilyID string,
	fallback SessionInvalidator,
) (Status, error) {
	if strings.TrimSpace(currentFamilyID) == "" {
		return Status{}, ErrInvalidInput
	}
	if runner, ok := s.store.(sessionSecurityMutationStore); ok {
		var status Status
		revoked, err := runner.RunSessionSecurityMutation(
			ctx,
			in.TenantID,
			in.UserID,
			currentFamilyID,
			"two_factor_state_changed",
			s.now().UTC(),
			func(txStore Store) error {
				var err error
				status, err = s.cloneWithStore(txStore).Enable(ctx, in)
				return err
			},
		)
		if err != nil {
			return Status{}, err
		}
		status.SessionsRevoked = revoked
		return status, nil
	}
	if fallback == nil {
		return Status{}, ErrStoreNotConfigured
	}
	status, err := s.Enable(ctx, in)
	if err != nil {
		return Status{}, err
	}
	revoked, err := fallback.RevokeOthers(ctx, usersession.RevokeOthersInput{
		TenantID:        in.TenantID,
		UserID:          in.UserID,
		CurrentFamilyID: currentFamilyID,
		Reason:          "two_factor_state_changed",
	})
	if err != nil {
		_ = s.store.SetEnabled(ctx, in.TenantID, in.UserID, false, s.now().UTC())
		return Status{}, wrapSessionInvalidationError(err)
	}
	status.SessionsRevoked = revoked
	return status, nil
}

// DisableWithSessionInvalidation 校验一次新鲜 2FA 证明，并原子关闭 2FA、撤销其他会话。
func (s *Service) DisableWithSessionInvalidation(
	ctx context.Context,
	in VerifyInput,
	currentFamilyID string,
	fallback SessionInvalidator,
) (int64, error) {
	if strings.TrimSpace(currentFamilyID) == "" {
		return 0, ErrInvalidInput
	}
	if runner, ok := s.store.(sessionSecurityMutationStore); ok {
		revoked, err := runner.RunSessionSecurityMutation(
			ctx,
			in.TenantID,
			in.UserID,
			currentFamilyID,
			"two_factor_state_changed",
			s.now().UTC(),
			func(txStore Store) error {
				txService := s.cloneWithStore(txStore)
				if _, err := txService.VerifyLogin(ctx, in); err != nil {
					return err
				}
				return txService.Disable(ctx, in.TenantID, in.UserID)
			},
		)
		if err != nil {
			return 0, err
		}
		return revoked, nil
	}
	if fallback == nil {
		return 0, ErrStoreNotConfigured
	}
	if _, err := s.VerifyLogin(ctx, in); err != nil {
		return 0, err
	}
	if err := s.Disable(ctx, in.TenantID, in.UserID); err != nil {
		return 0, err
	}
	revoked, err := fallback.RevokeOthers(ctx, usersession.RevokeOthersInput{
		TenantID:        in.TenantID,
		UserID:          in.UserID,
		CurrentFamilyID: currentFamilyID,
		Reason:          "two_factor_state_changed",
	})
	if err != nil {
		_ = s.store.SetEnabled(ctx, in.TenantID, in.UserID, true, s.now().UTC())
		return 0, wrapSessionInvalidationError(err)
	}
	return revoked, nil
}

func (s *Service) cloneWithStore(store Store) *Service {
	cloned := *s
	cloned.store = store
	cloned.failureStore = nil
	if failureStore, ok := store.(atomicFailureStore); ok {
		cloned.failureStore = failureStore
	}
	return &cloned
}

func wrapSessionInvalidationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrInvalidCode) ||
		errors.Is(err, ErrCodeReused) ||
		errors.Is(err, ErrLocked) ||
		errors.Is(err, ErrDisabled) ||
		errors.Is(err, ErrNotSetup) ||
		errors.Is(err, ErrAlreadyEnabled) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrSessionInvalidation, err)
}

// RunSessionSecurityMutation 把 2FA 状态变化与其他会话失效放进同一事务。
// 锁顺序固定为用户会话事务锁、2FA 设置行，避免与并发登录和其他安全变更交叉穿透。
func (s *PostgresStore) RunSessionSecurityMutation(
	ctx context.Context,
	tenantID, userID int64,
	currentFamilyID, reason string,
	now time.Time,
	mutate func(Store) error,
) (int64, error) {
	if s == nil || s.pool == nil || mutate == nil {
		return 0, ErrStoreNotConfigured
	}
	var revoked int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		revoked, err = usersession.RevokeOtherFamiliesInTransaction(
			ctx,
			tx,
			tenantID,
			userID,
			currentFamilyID,
			reason,
			now,
		)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSessionInvalidation, err)
		}

		var locked int
		if err := tx.QueryRow(ctx, `
SELECT 1
FROM two_factor_settings
WHERE tenant_id=$1
  AND user_id=$2
FOR UPDATE`, tenantID, userID).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotSetup
			}
			return err
		}
		return mutate(&PostgresStore{q: dbtwofa.New(tx)})
	})
	return revoked, err
}

func (s *PostgresStore) RunActiveSessionMutation(
	ctx context.Context,
	tenantID, userID int64,
	currentFamilyID string,
	authVersion int,
	mutate func(Store) error,
) error {
	if s == nil || s.pool == nil || mutate == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || authVersion <= 0 {
		return ErrInvalidInput
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := usersession.LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var familyVersion, userVersion int
		err := tx.QueryRow(ctx, `
SELECT sf.auth_version, u.password_version
FROM session_families sf
INNER JOIN users u
  ON u.tenant_id = sf.tenant_id
 AND u.id = sf.user_id
WHERE sf.tenant_id = $1
  AND sf.user_id = $2
  AND sf.id = $3::uuid
  AND sf.status IN ('active', 'suspicious')
  AND u.status = 'active'
  AND u.deleted_at IS NULL
FOR UPDATE OF sf, u`, tenantID, userID, currentFamilyID).Scan(&familyVersion, &userVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAuthenticationStale
			}
			return err
		}
		if familyVersion != authVersion || userVersion != authVersion {
			return ErrAuthenticationStale
		}
		return mutate(&PostgresStore{q: dbtwofa.New(tx)})
	})
}
