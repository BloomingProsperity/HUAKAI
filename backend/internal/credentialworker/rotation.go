package credentialworker

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// RotationCandidate is a provider-account credential old enough to warrant
// rotation (CRED-288): nothing currently ages keys into rotation on a schedule,
// so a long-lived credential can silently expire and brown out the account. The
// scheduled scan classifies such credentials and routes each into a recovery
// action so the account heals automatically (CRED-288c) instead of being
// stranded.
type RotationCandidate struct {
	TenantID          int64
	ProviderAccountID int64
	CredentialID      int64
	LastRefreshAt     time.Time
	// Vendor/AuthMode drive the refreshability classification: an OAuth-style
	// credential can be healed by the existing refresh flow, whereas a static
	// API key cannot be refreshed and must be left in service (only alerted) so
	// it does not brown out with no path back.
	Vendor   string
	AuthMode string
}

// RotationStore is the minimal persistence surface the rotation-due scan needs.
// Kept tiny so the scan logic is unit-testable against a fake and the
// production impl is a thin raw-pgx adapter (deliberately avoids adding a new
// sqlc query while the committed generated code is drifted from a clean regen).
type RotationStore interface {
	// DueForRotation returns up to limit active provider-account credentials
	// whose last successful refresh is strictly older than olderThan.
	DueForRotation(ctx context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error)
	// MarkForRefreshRecovery brings a refreshable (OAuth) credential back into
	// the existing refresh flow without taking it out of service: it stays
	// 'active' (so requests keep being served while its access token is still
	// valid) but its refresh_before_at is pulled to now so the refresh scan
	// picks it up on the next tick and re-mints the token through the audited
	// SaveRefreshSuccess path. This is the CRED-288c recovery closure for the
	// "old but still refreshable" credential — the only credentials it touches.
	MarkForRefreshRecovery(ctx context.Context, c RotationCandidate, refreshBeforeAt time.Time) error
	// FlagNeedsRotation transitions a candidate into the needs_rotation state.
	// Reserved for credentials that cannot be auto-healed by a refresh and must
	// be taken offline pending operator action (kept for explicit operator
	// "force-rotate" semantics); the age scan does NOT route static keys here,
	// to avoid browning out a key that aging alone never invalidated.
	FlagNeedsRotation(ctx context.Context, c RotationCandidate) error
}

// RotationAlert is invoked once per scanned candidate so operators learn a
// credential aged out (reuses the provider-account notify pipeline). Optional.
type RotationAlert func(ctx context.Context, c RotationCandidate)

// RefreshClassifier reports whether a (vendor, auth_mode) credential can be
// healed by the existing OAuth refresh flow. Returning false means the
// credential is a static secret (API key, AWS SigV4, …) the refresh flow cannot
// re-mint — such a credential must NOT be flagged offline on age alone.
type RefreshClassifier func(vendor, authMode string) bool

// DefaultRefreshClassifier classifies refreshability from the canonical
// credentialstore mode-handler registry: a mode is refreshable iff its handler
// declares Refreshable() (every OAuth/session mode) — static api_key / bedrock /
// aistudio_api_key modes are not. Unknown modes are treated as non-refreshable
// (the conservative choice: never auto-touch a credential we cannot classify).
func DefaultRefreshClassifier() RefreshClassifier {
	registry := credentialstore.DefaultHandlerRegistry()
	return func(vendor, authMode string) bool {
		handler, ok := registry.Lookup(vendor, authMode)
		if !ok {
			return false
		}
		return handler.Refreshable()
	}
}

// ScanRotationDue finds credentials whose last refresh is older than maxAge and
// routes each into a recovery action, then alerts. maxAge <= 0 (or a nil store)
// disables the scan entirely — it is opt-in and OFF by default, so existing
// deployments keep their exact current behavior. Returns the number processed.
//
// Recovery closure (CRED-288c): a due credential is no longer merely flagged and
// stranded. The classifier splits the two safe outcomes:
//   - refreshable (OAuth/session): MarkForRefreshRecovery keeps it 'active' and
//     pulls refresh_before_at to now so the existing refresh scan re-mints the
//     token next tick (SaveRefreshSuccess → fresh token). No brownout, and the
//     serving layer still fail-closes on an actually-expired access token, so a
//     dead token is never served just because the row stayed 'active'.
//   - non-refreshable (static API key): left in service, alert only. Age alone
//     never invalidates a static key, so taking it offline would brown out an
//     account with no automatic path back; the operator rotates it out-of-band.
//
// A nil classifier falls back to DefaultRefreshClassifier. The scan stops at the
// first store error so a transient DB fault is not swallowed.
func ScanRotationDue(ctx context.Context, store RotationStore, classifier RefreshClassifier, alert RotationAlert, maxAge time.Duration, now time.Time, limit int) (int, error) {
	if store == nil || maxAge <= 0 {
		return 0, nil
	}
	if classifier == nil {
		classifier = DefaultRefreshClassifier()
	}
	if limit <= 0 {
		limit = 100
	}
	olderThan := now.Add(-maxAge)
	cands, err := store.DueForRotation(ctx, olderThan, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, c := range cands {
		if classifier(c.Vendor, c.AuthMode) {
			// Refreshable: heal via the refresh flow, stay in service.
			if err := store.MarkForRefreshRecovery(ctx, c, now); err != nil {
				return processed, err
			}
		}
		// Non-refreshable static keys take no state change here (alert only) —
		// see the function doc: avoiding a no-recovery brownout.
		processed++
		if alert != nil {
			alert(ctx, c)
		}
	}
	return processed, nil
}
