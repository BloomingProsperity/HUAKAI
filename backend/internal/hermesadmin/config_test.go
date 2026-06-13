package hermesadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// fakeSettings is a settingGetter returning a fixed value/error for the admin
// notification email key.
type fakeSettings struct {
	value string
	err   error
}

func (f fakeSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if f.err != nil {
		return platformsettings.StoredSetting{}, f.err
	}
	return platformsettings.StoredSetting{Key: key, Value: f.value, Source: platformsettings.SourceDB}, nil
}

// TestResolveRecipientSettingWins: a non-empty platform setting takes precedence
// over the env fallback.
// Regression caught: if precedence were reversed, the env value would shadow an
// operator's explicit setting.
func TestResolveRecipientSettingWins(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: "setting@huakai.test"})
	if got != "setting@huakai.test" || src != "setting" {
		t.Fatalf("expected setting to win, got %q (%s)", got, src)
	}
}

// TestResolveRecipientEnvFallback: an empty setting falls back to the env value.
// Regression caught: if the empty-setting case did not fall through, a configured
// env recipient would be ignored and the worker would never start.
func TestResolveRecipientEnvFallback(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: ""})
	if got != "env@huakai.test" || src != "env" {
		t.Fatalf("expected env fallback, got %q (%s)", got, src)
	}
}

// TestResolveRecipientNeither: neither a setting nor an env value yields the
// empty "none" result — the signal the worker uses to stay off.
// Regression caught: if a default recipient were ever injected, an unconfigured
// deployment would start emailing.
func TestResolveRecipientNeither(t *testing.T) {
	t.Setenv(EnvRecipient, "")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: ""})
	if got != "" || src != "none" {
		t.Fatalf("expected no recipient, got %q (%s)", got, src)
	}
}

// TestResolveRecipientSettingErrorFallsToEnv: a settings read error must not
// block startup — it falls through to the env fallback.
// Regression caught: if a transient settings error returned early, the env
// recipient would be ignored and a configured deployment would not start.
func TestResolveRecipientSettingErrorFallsToEnv(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{err: errors.New("db down")})
	if got != "env@huakai.test" || src != "env" {
		t.Fatalf("expected env fallback on settings error, got %q (%s)", got, src)
	}
}

// TestEnabledDefaultsOff: the enable flag is opt-in — unset / non-truthy is OFF.
// Regression caught: if the default flipped to ON, an unconfigured deploy would
// start emailing — the exact fail-safe this wave guards.
func TestEnabledDefaultsOff(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	if EnabledFromEnv() {
		t.Fatalf("enable flag must default OFF when unset")
	}
	t.Setenv(EnvEnabled, "false")
	if EnabledFromEnv() {
		t.Fatalf("enable flag must be OFF for 'false'")
	}
	t.Setenv(EnvEnabled, "true")
	if !EnabledFromEnv() {
		t.Fatalf("enable flag must be ON for 'true'")
	}
}

// TestIntervalFromEnv: default when unset, parsed when set, error on garbage /
// non-positive.
// Regression caught: a non-positive interval would make time.NewTicker panic at
// runtime; the boot-time error stops that.
func TestIntervalFromEnv(t *testing.T) {
	t.Setenv(EnvInterval, "")
	if d, err := IntervalFromEnv(); err != nil || d != DefaultInterval {
		t.Fatalf("unset => default, got %s err=%v", d, err)
	}
	t.Setenv(EnvInterval, "6h")
	if d, err := IntervalFromEnv(); err != nil || d != 6*time.Hour {
		t.Fatalf("6h => 6h, got %s err=%v", d, err)
	}
	t.Setenv(EnvInterval, "0s")
	if _, err := IntervalFromEnv(); err == nil {
		t.Fatalf("non-positive interval must error")
	}
	t.Setenv(EnvInterval, "garbage")
	if _, err := IntervalFromEnv(); err == nil {
		t.Fatalf("garbage interval must error")
	}
}

// TestLoadConfigComposition: LoadConfig threads enable + interval + recipient +
// tenant together; a disabled, unconfigured deployment yields Enabled=false and
// Recipient="" (the worker won't start).
func TestLoadConfigComposition(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvRecipient, "")
	t.Setenv(EnvInterval, "")
	cfg, err := LoadConfig(context.Background(), fakeSettings{value: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Enabled || cfg.Recipient != "" {
		t.Fatalf("unconfigured deploy must be disabled with no recipient, got %+v", cfg)
	}
	if cfg.Interval != DefaultInterval || cfg.TenantID != 1 {
		t.Fatalf("expected default interval+tenant, got %+v", cfg)
	}
}
