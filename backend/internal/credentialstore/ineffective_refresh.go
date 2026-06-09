package credentialstore

import (
	"context"
	"time"
)

// IneffectiveRefreshBackoff is the throttle applied when a refresh "succeeds"
// but the resulting token is still immediately due for refresh (ineffective), or
// when no refresh was required. This prevents tight refresh storms against the
// upstream provider.
const IneffectiveRefreshBackoff = 30 * time.Second

// ineffectiveRefreshNextAttempt returns the next_attempt_at value to persist
// after a refresh success. If the freshly-computed refreshBeforeAt is already
// <= now the refresh was ineffective (upstream returned a near-stale token), so
// we throttle the next attempt. Otherwise the normal value (normalNext) is
// returned unchanged — this is the DEFAULT/SAFE path and MUST NOT be altered.
func ineffectiveRefreshNextAttempt(refreshBeforeAt, now time.Time, normalNext time.Time) time.Time {
	if !refreshBeforeAt.After(now) {
		// Token is still immediately due for refresh: throttle.
		return now.Add(IneffectiveRefreshBackoff)
	}
	return normalNext
}

// SetNextAttemptThrottle sets next_attempt_at on the credential record without
// changing its state, failure_class, or failure_count. It is called on the
// ErrNoRefreshRequired path to prevent a tight re-attempt loop.
func (s *Store) SetNextAttemptThrottle(ctx context.Context, rec CredentialRecord, nextAttemptAt time.Time) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	const q = `
UPDATE account_credentials
SET next_attempt_at = $1,
    updated_at = NOW()
WHERE id = $2
  AND tenant_id = $3
  AND provider_account_id = $4
  AND deleted_at IS NULL
  AND credential_version = $5`
	tag, err := s.db.Exec(ctx, q, nullableTime(nextAttemptAt), rec.ID, rec.TenantID, rec.ProviderAccountID, rec.CredentialVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentialNotFound
	}
	return nil
}
