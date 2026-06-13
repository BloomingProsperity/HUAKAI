package hermesadmin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// Environment variables governing the daily inspection. The enable flag defaults
// FALSE (opt-in) so an unconfigured deployment never starts emailing — mirroring
// the retention workers' default-off posture.
const (
	EnvEnabled   = "HUAKAI_HERMES_DAILY_INSPECTION_ENABLED"
	EnvInterval  = "HUAKAI_HERMES_INSPECTION_INTERVAL"
	EnvRecipient = "HUAKAI_ADMIN_NOTIFICATION_EMAIL"
	EnvTenantID  = "HUAKAI_HERMES_INSPECTION_TENANT_ID"

	// DefaultInterval is the run cadence when EnvInterval is unset.
	DefaultInterval = 24 * time.Hour
)

// Config is the resolved worker configuration read at wiring time.
type Config struct {
	Enabled  bool
	Interval time.Duration
	// Recipient is the resolved admin address. Empty means "no recipient
	// resolved" — the worker must NOT start in that case.
	Recipient string
	// RecipientSource records where Recipient came from ("setting" / "env" /
	// "none") for the startup log line.
	RecipientSource string
	// TenantID scopes the tenant-bound diagnostic reads.
	TenantID int64
}

// EnabledFromEnv reports whether the daily inspection is opt-in enabled. Anything
// other than a truthy token (true/1/yes/on) is OFF — the safe default.
func EnabledFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvEnabled))) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// IntervalFromEnv parses the run interval; unset => DefaultInterval. A malformed
// or non-positive value is an error so a typo fails loud at boot rather than
// silently scheduling at a surprising cadence.
func IntervalFromEnv() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(EnvInterval))
	if raw == "" {
		return DefaultInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", EnvInterval, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: must be positive, got %s", EnvInterval, raw)
	}
	return d, nil
}

// settingGetter is the read-only slice of platformsettings.Service the recipient
// resolver needs. Kept as a narrow interface so tests can fake it without a store.
type settingGetter interface {
	Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// ResolveRecipient resolves the admin address with precedence:
//  1. the admin_notification_email platform setting (when present + non-empty),
//  2. the HUAKAI_ADMIN_NOTIFICATION_EMAIL env fallback,
//  3. none — returns ("", "none").
//
// A settings read error falls through to the env fallback (never blocks startup
// on a transient DB hiccup). The returned source string feeds the startup log.
func ResolveRecipient(ctx context.Context, settings settingGetter) (string, string) {
	if settings != nil {
		if s, err := settings.Get(ctx, platformsettings.KeyAdminNotificationEmail); err == nil {
			if v := strings.TrimSpace(s.Value); v != "" {
				return v, "setting"
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvRecipient)); v != "" {
		return v, "env"
	}
	return "", "none"
}

// TenantIDFromEnv reads the deployment tenant for the tenant-scoped reads;
// unset or non-positive => 1 (the single-tenant deployment default).
func TenantIDFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv(EnvTenantID))
	if raw == "" {
		return 1
	}
	var v int64
	if _, err := fmt.Sscan(raw, &v); err != nil || v <= 0 {
		return 1
	}
	return v
}

// LoadConfig resolves the full worker config from env + the platform settings.
// It does not decide whether to start — the caller checks Enabled && Recipient.
func LoadConfig(ctx context.Context, settings settingGetter) (Config, error) {
	interval, err := IntervalFromEnv()
	if err != nil {
		return Config{}, err
	}
	recipient, source := ResolveRecipient(ctx, settings)
	return Config{
		Enabled:         EnabledFromEnv(),
		Interval:        interval,
		Recipient:       recipient,
		RecipientSource: source,
		TenantID:        TenantIDFromEnv(),
	}, nil
}
