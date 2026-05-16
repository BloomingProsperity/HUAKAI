package credentialacq

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func AcquireCredentialRefreshAdvisoryLock(ctx context.Context, tx db.DBTX, accountCredentialID int64) error {
	if tx == nil {
		return errors.New("credentialacq: refresh lock requires transaction db")
	}
	if accountCredentialID <= 0 {
		return errors.New("credentialacq: account_credential_id must be positive")
	}
	_, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock(hashtext('credential_refresh:' || $1::text))
`, accountCredentialID)
	return err
}

func WithRefreshLock(ctx context.Context, tx db.DBTX, accountCredentialID int64, fn func(db.DBTX) error) error {
	if err := AcquireCredentialRefreshAdvisoryLock(ctx, tx, accountCredentialID); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn(tx)
}
