package credentialstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// RefreshPersistenceError 表示远端刷新已经发生，但本地状态没有得到可证明的
// 持久化结果。调度器不得因此重新调用远端，避免重复消费会轮换的 refresh token。
type RefreshPersistenceError struct {
	err error
}

func (e *RefreshPersistenceError) Error() string {
	if e == nil || e.err == nil {
		return "credentialstore: refresh persistence outcome uncertain"
	}
	return "credentialstore: refresh persistence outcome uncertain: " + e.err.Error()
}

func (e *RefreshPersistenceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (*RefreshPersistenceError) RetryableRefresh() bool { return false }

func (*RefreshPersistenceError) SuppressRemoteRetry() bool { return true }

func refreshPersistenceError(err error) error {
	if err == nil {
		return nil
	}
	return &RefreshPersistenceError{err: err}
}

func (s *Store) refreshSuccessAlreadyCommitted(ctx context.Context, rec CredentialRecord, version int32, payloadFingerprint *string, outcome string) bool {
	current, err := s.getRecord(ctx, rec.TenantID, rec.ProviderAccountID, rec.ID, false)
	if err != nil || current.CredentialVersion != version || payloadFingerprint == nil || current.PayloadFingerprint == nil || *current.PayloadFingerprint != *payloadFingerprint {
		return false
	}
	return current.LastRefreshOutcome != nil && *current.LastRefreshOutcome == outcome
}

func retryableRefreshPersistence(err error) bool {
	var phaseErr credentialAuditTxPhaseError
	if !errors.As(err, &phaseErr) {
		return false
	}
	switch phaseErr.phase {
	case credentialAuditTxPhaseBegin, credentialAuditTxPhaseCommit:
		return true
	case credentialAuditTxPhaseMutation:
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || strings.HasPrefix(pgErr.Code, "08"))
	default:
		return false
	}
}

func (s *Store) SaveRefreshFailure(ctx context.Context, rec CredentialRecord, failureClass string, nextAttemptAt time.Time) error {
	return s.SaveRefreshFailureWithAudit(ctx, rec, failureClass, nextAttemptAt, nil)
}

// SaveRefreshFailureWithAudit 将失败状态和附加日志原子落库。
func (s *Store) SaveRefreshFailureWithAudit(ctx context.Context, rec CredentialRecord, failureClass string, nextAttemptAt time.Time, extraAudits []AuditEvent) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	if err := s.ensureProviderAccountTenant(ctx, rec.TenantID, rec.ProviderAccountID); err != nil {
		return err
	}
	state := refreshFailureState(failureClass)
	const q = `
UPDATE account_credentials
SET state = $1,
    failure_class = $2,
    failure_count = failure_count + 1,
    next_attempt_at = $3,
    last_refresh_at = NOW(),
    last_refresh_outcome = 'refresh_failed',
    updated_at = NOW()
WHERE id = $4
  AND tenant_id = $5
  AND provider_account_id = $6
  AND deleted_at IS NULL
  AND credential_version = $7`
	err := s.withCredentialMutationAuditTx(ctx, func(txStore *Store) error {
		tag, err := txStore.db.Exec(ctx, q, state, failureClass, nullableTime(nextAttemptAt), rec.ID, rec.TenantID, rec.ProviderAccountID, rec.CredentialVersion)
		if err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, err)
		}
		if tag.RowsAffected() != 1 {
			return credentialAuditPhaseError(credentialAuditTxPhaseMutation, ErrCredentialNotFound)
		}
		for _, event := range extraAudits {
			if err := txStore.insertAuditEventStrict(ctx, event); err != nil {
				return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
			}
		}
		if err := txStore.insertAuditEventStrict(ctx, AuditEvent{
			TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID, CredentialID: rec.ID,
			EventType: CredentialEventRefreshFailed, Vendor: rec.Vendor, AuthMode: rec.AuthMode,
			CredentialVersion: rec.CredentialVersion, Payload: map[string]any{"failure_class": failureClass, "state": state},
		}); err != nil {
			return credentialAuditPhaseError(credentialAuditTxPhaseAudit, err)
		}
		return nil
	})
	return refreshPersistenceError(err)
}

func refreshFailureState(failureClass string) string {
	switch failureClass {
	case "invalid_grant", "auth_expired":
		return StateRevoked
	case "decrypt_failed", "payload_invalid", "operator_config_required", "project_metadata_conflict", "project_metadata_unavailable":
		return StateOperatorAttention
	default:
		return StateTempUnschedulable
	}
}
