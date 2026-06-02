// Package config loads HUAKAI gateway runtime configuration from environment
// variables. YAML support is deferred until a multi-deployment story exists.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// VendorOAuth holds operator-owned OAuth refresh settings for vendor
	// refreshers. Empty TokenURL means that vendor refresher is not wired.
	VendorOAuth VendorOAuthConfigs

	// PaymentHMACSecrets maps payment provider name -> webhook HMAC secret.
	// Values come from HUAKAI_PAYMENT_HMAC_SECRETS and must never be logged.
	PaymentHMACSecrets map[string]string
	PaymentEnableMock  bool
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
	cfg := &Config{
		DatabaseURL:            os.Getenv("HUAKAI_DATABASE_URL"),
		Listen:                 envDefault("HUAKAI_ADDR", ":8080"),
		BillingPolicyVersion:   envDefault("HUAKAI_BILLING_POLICY_VERSION", DefaultBillingPolicyVersion),
		RequestClass:           envDefault("HUAKAI_REQUEST_CLASS", "standard"),
		TransportSidecarSocket: os.Getenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET"),
		VendorOAuth:            loadVendorOAuthConfigs(),
		PaymentHMACSecrets:     paymentHMACSecrets,
		PaymentEnableMock:      paymentEnableMock,
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%w: HUAKAI_DATABASE_URL", ErrMissingRequired)
	}
	return cfg, nil
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
