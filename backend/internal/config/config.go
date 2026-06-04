// Package config loads HUAKAI gateway runtime configuration from environment
// variables. YAML support is deferred until a multi-deployment story exists.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the typed snapshot of all settings the gateway needs at boot.
// All fields are populated from env vars; missing required fields → typed
// error. There is no silent default for security-sensitive values.
type Config struct {
	// DatabaseURL is the PostgreSQL DSN. Required.
	DatabaseURL string

	// Listen is the HTTP bind address (e.g. ":8080" or ":0" for tests).
	Listen string

	// BillingPolicyVersion is recorded on every claim row.
	BillingPolicyVersion string

	// RequestClass tags the claim for downstream policy routing. Default "standard".
	RequestClass string

	// TransportSidecarSocket points mimicry transport modes at the local TLS
	// sidecar Unix socket. Empty keeps the existing Go uTLS path.
	TransportSidecarSocket string
	// TransportSidecarFallback is explicit opt-in. Default false keeps production
	// fail-closed when Rust sidecar is configured but unavailable.
	TransportSidecarFallback bool

	// QuotaEnforce wires the quota reservation/finalization path into chat
	// admission. Default false leaves the hot path unchanged.
	QuotaEnforce bool
	// Budget wires per-minute RPM/TPM budget tracking. Default disabled keeps
	// the hot path unchanged; fail mode defaults to memory_fallback when enabled.
	Budget BudgetConfig

	// VendorOAuth holds operator-owned OAuth refresh settings for vendor
	// refreshers. Empty TokenURL means that vendor refresher is not wired.
	VendorOAuth VendorOAuthConfigs

	// CredentialAcqBootstrap*TTL optionally override credential acquisition
	// OAuth bootstrap windows. Zero means the credentialacq package default.
	CredentialAcqBootstrapShortTTL time.Duration
	CredentialAcqBootstrapLongTTL  time.Duration

	// PaymentHMACSecrets maps payment provider name -> webhook HMAC secret.
	// Values come from HUAKAI_PAYMENT_HMAC_SECRETS and must never be logged.
	PaymentHMACSecrets map[string]string
	PaymentEnableMock  bool
}

type BudgetConfig struct {
	Enabled    bool
	FailMode   string
	RedisURL   string
	DefaultRPM int64
	DefaultTPM int64
}

const (
	DefaultBillingPolicyVersion = "1.0"
	VendorOAuthCursor           = "cursor"
	VendorOAuthWindsurf         = "windsurf"
	VendorOAuthOpenAICodex      = "openai_codex"
	VendorOAuthKiro             = "kiro"
	VendorOAuthGemini           = "gemini"
	vendorOAuthAuthURL          = "AUTH_URL"
	vendorOAuthTokenURL         = "TOKEN_URL"
	vendorOAuthClientID         = "CLIENT_ID"
	vendorOAuthClientSecret     = "CLIENT_SECRET"
	vendorOAuthScope            = "SCOPE"
)

// VendorOAuth is one operator-provided OAuth client configuration.
type VendorOAuth struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	AuthURL      string
}

// VendorOAuthConfigs is keyed by vendor name:
// cursor, windsurf, openai_codex, kiro, gemini.
type VendorOAuthConfigs map[string]VendorOAuth

// ErrMissingRequired indicates one or more required env vars were not set.
var ErrMissingRequired = errors.New("config: missing required env var")

// Load reads env vars into a Config. Required vars: HUAKAI_DATABASE_URL.
//
// Removed Smoke* fields — replaced by api_keys-table-
// backed inbound auth (auth.APIKeyResolver). Rolling back to env-injected
// auth requires a code revert (no build-tag escape hatch).
func Load() (*Config, error) {
	paymentHMACSecrets, err := loadPaymentHMACSecrets()
	if err != nil {
		return nil, err
	}
	paymentEnableMock, err := envBool("HUAKAI_PAYMENT_ENABLE_MOCK")
	if err != nil {
		return nil, err
	}
	transportSidecarFallback, err := envBool("HUAKAI_TRANSPORT_SIDECAR_FALLBACK")
	if err != nil {
		return nil, err
	}
	quotaEnforce, err := envBool("HUAKAI_QUOTA_ENFORCE")
	if err != nil {
		return nil, err
	}
	budgetCfg, err := loadBudgetConfig()
	if err != nil {
		return nil, err
	}
	credentialAcqBootstrapShortTTL, err := envOptionalDurationSeconds("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_SHORT_TTL_SECONDS")
	if err != nil {
		return nil, err
	}
	credentialAcqBootstrapLongTTL, err := envOptionalDurationSeconds("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_LONG_TTL_SECONDS")
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		DatabaseURL:                    os.Getenv("HUAKAI_DATABASE_URL"),
		Listen:                         envDefault("HUAKAI_ADDR", ":8080"),
		BillingPolicyVersion:           envDefault("HUAKAI_BILLING_POLICY_VERSION", DefaultBillingPolicyVersion),
		RequestClass:                   envDefault("HUAKAI_REQUEST_CLASS", "standard"),
		TransportSidecarSocket:         os.Getenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET"),
		TransportSidecarFallback:       transportSidecarFallback,
		QuotaEnforce:                   quotaEnforce,
		Budget:                         budgetCfg,
		VendorOAuth:                    loadVendorOAuthConfigs(),
		CredentialAcqBootstrapShortTTL: credentialAcqBootstrapShortTTL,
		CredentialAcqBootstrapLongTTL:  credentialAcqBootstrapLongTTL,
		PaymentHMACSecrets:             paymentHMACSecrets,
		PaymentEnableMock:              paymentEnableMock,
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%w: HUAKAI_DATABASE_URL", ErrMissingRequired)
	}
	return cfg, nil
}

func loadBudgetConfig() (BudgetConfig, error) {
	enabled, err := envBool("HUAKAI_BUDGET_ENABLED")
	if err != nil {
		return BudgetConfig{}, err
	}
	failMode := strings.TrimSpace(os.Getenv("HUAKAI_BUDGET_FAIL_MODE"))
	if failMode == "" {
		failMode = "memory_fallback"
	}
	switch failMode {
	case "open", "closed", "memory_fallback":
	default:
		return BudgetConfig{}, fmt.Errorf("HUAKAI_BUDGET_FAIL_MODE must be open, closed, or memory_fallback, got %q", failMode)
	}
	rpm, err := envNonNegativeInt64("HUAKAI_BUDGET_DEFAULT_RPM")
	if err != nil {
		return BudgetConfig{}, err
	}
	tpm, err := envNonNegativeInt64("HUAKAI_BUDGET_DEFAULT_TPM")
	if err != nil {
		return BudgetConfig{}, err
	}
	return BudgetConfig{
		Enabled:    enabled,
		FailMode:   failMode,
		RedisURL:   firstNonEmptyEnv("HUAKAI_BUDGET_REDIS_URL", "HUAKAI_REDIS_URL"),
		DefaultRPM: rpm,
		DefaultTPM: tpm,
	}, nil
}

func (configs VendorOAuthConfigs) Configured() VendorOAuthConfigs {
	out := VendorOAuthConfigs{}
	for _, vendor := range []string{
		VendorOAuthCursor,
		VendorOAuthWindsurf,
		VendorOAuthOpenAICodex,
		VendorOAuthKiro,
		VendorOAuthGemini,
	} {
		cfg := configs[vendor].normalized()
		if cfg.TokenURL == "" {
			continue
		}
		out[vendor] = cfg
	}
	return out
}

func (cfg VendorOAuth) normalized() VendorOAuth {
	return VendorOAuth{
		TokenURL:     strings.TrimSpace(cfg.TokenURL),
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scope:        strings.TrimSpace(cfg.Scope),
		AuthURL:      strings.TrimSpace(cfg.AuthURL),
	}
}

func loadVendorOAuthConfigs() VendorOAuthConfigs {
	return VendorOAuthConfigs{
		VendorOAuthCursor:      loadVendorOAuth("HUAKAI_CURSOR_OAUTH"),
		VendorOAuthWindsurf:    loadVendorOAuth("HUAKAI_WINDSURF_OAUTH"),
		VendorOAuthOpenAICodex: loadVendorOAuth("HUAKAI_OPENAI_CODEX_OAUTH"),
		VendorOAuthKiro:        loadVendorOAuth("HUAKAI_KIRO_OAUTH"),
		VendorOAuthGemini:      loadVendorOAuth("HUAKAI_GEMINI_OAUTH"),
	}
}

func loadVendorOAuth(prefix string) VendorOAuth {
	return VendorOAuth{
		AuthURL:      envTrim(prefix + "_" + vendorOAuthAuthURL),
		TokenURL:     envTrim(prefix + "_" + vendorOAuthTokenURL),
		ClientID:     envTrim(prefix + "_" + vendorOAuthClientID),
		ClientSecret: envTrim(prefix + "_" + vendorOAuthClientSecret),
		Scope:        envTrim(prefix + "_" + vendorOAuthScope),
	}
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func envTrim(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func loadPaymentHMACSecrets() (map[string]string, error) {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_PAYMENT_HMAC_SECRETS"))
	secrets := map[string]string{}
	if raw == "" {
		return secrets, nil
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, secret, ok := strings.Cut(item, "=")
		if !ok {
			name, secret, ok = strings.Cut(item, ":")
		}
		name = strings.ToLower(strings.TrimSpace(name))
		secret = strings.TrimSpace(secret)
		if !ok || name == "" || secret == "" {
			return nil, fmt.Errorf("HUAKAI_PAYMENT_HMAC_SECRETS entry %q must be provider=secret", item)
		}
		secrets[name] = secret
	}
	return secrets, nil
}

func envBool(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", name, raw, err)
	}
	return v, nil
}

func envOptionalDurationSeconds(name string) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be positive seconds, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be positive seconds, got %q", name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envNonNegativeInt64(name string) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be non-negative integer, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be non-negative integer, got %q", name, raw)
	}
	return value, nil
}
