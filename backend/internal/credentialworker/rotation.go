package credentialworker

import (
	"context"
	"time"
)

// RotationCandidate is a provider-account credential old enough to warrant
// rotation (CRED-288): nothing currently ages keys into needs_rotation on a
// schedule, so a long-lived credential can silently expire and brown out the
// account. The scheduled scan flags such credentials so operators (and the
// existing refresh flow) act before that happens.
type RotationCandidate struct {
	TenantID          int64
	ProviderAccountID int64
	CredentialID      int64
	LastRefreshAt     time.Time
}

// RotationStore is the minimal persistence surface the rotation-due scan needs.
// Kept tiny so the scan logic is unit-testable against a fake and the
// production impl is a thin raw-pgx adapter (deliberately avoids adding a new
// sqlc query while the committed generated code is drifted from a clean regen).
type RotationStore interface {
	// DueForRotation returns up to limit active provider-account credentials
	// whose last successful refresh is strictly older than olderThan.
	DueForRotation(ctx context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error)
	// FlagNeedsRotation transitions a candidate into the needs_rotation state.
	FlagNeedsRotation(ctx context.Context, c RotationCandidate) error
}

// RotationAlert is invoked once per newly-flagged candidate so operators learn a
// credential aged out (reuses the provider-account notify pipeline). Optional.
type RotationAlert func(ctx context.Context, c RotationCandidate)

// ScanRotationDue finds credentials whose last refresh is older than maxAge,
// flags each needs_rotation, and alerts. maxAge <= 0 (or a nil store) disables
// the scan entirely — it is opt-in and OFF by default, so existing deployments
// keep their exact current behavior. Returns the number flagged.
//
// Flag-only by design: it never performs the actual credential rotation (a
// sensitive operation owned by the operator / existing refresh flow). It only
// closes the "no scheduled job ages keys into needs_rotation -> silent expiry"
// gap, and stops at the first flag error so a transient DB fault doesn't get
// swallowed.
func ScanRotationDue(ctx context.Context, store RotationStore, alert RotationAlert, maxAge time.Duration, now time.Time, limit int) (int, error) {
	if store == nil || maxAge <= 0 {
		return 0, nil
	}
	if limit <= 0 {
		limit = 100
	}
	olderThan := now.Add(-maxAge)
	cands, err := store.DueForRotation(ctx, olderThan, limit)
	if err != nil {
		return 0, err
	}
	flagged := 0
	for _, c := range cands {
		if err := store.FlagNeedsRotation(ctx, c); err != nil {
			return flagged, err
		}
		flagged++
		if alert != nil {
			alert(ctx, c)
		}
	}
	return flagged, nil
}
