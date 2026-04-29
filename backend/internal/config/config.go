// Package config loads HUAKAI gateway runtime configuration from environment
// variables. YAML support is deferred until a multi-deployment story exists.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Config is the typed snapshot of all settings the gateway needs at boot.
// All fields are populated from env vars; missing required fields → typed
// error. There is no silent default for security-sensitive values.
type Config struct {
	// DatabaseURL is the PostgreSQL DSN. Required.
	DatabaseURL string

	// Listen is the HTTP bind address (e.g. ":8080" or ":0" for tests).
	Listen string

	// BillingPolicyVersion is recorded on every claim row. Default "1.0".
	BillingPolicyVersion string

	// RequestClass tags the claim for downstream policy routing. Default "standard".
	RequestClass string

	// SmokeBearerToken is the inbound bearer for Phase C smoke testing only.
	// Phase E replaces this with an api_keys-table-backed resolver.
	// Required for the gateway route to be live.
	SmokeBearerToken string

	// SmokeTenantID / SmokeAPIKeyID / SmokeUserID are the identities returned
	// by the smoke auth resolver when the bearer matches. All required.
	SmokeTenantID  int64
	SmokeAPIKeyID  int64
	SmokeUserID    int64
}

// ErrMissingRequired indicates one or more required env vars were not set.
var ErrMissingRequired = errors.New("config: missing required env var")

// Load reads env vars into a Config. Required vars: HUAKAI_DATABASE_URL.
// Smoke-auth vars are optional during boot but the chat handler will
// return 503 if they are not set.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:          os.Getenv("HUAKAI_DATABASE_URL"),
		Listen:               envDefault("HUAKAI_ADDR", ":8080"),
		BillingPolicyVersion: envDefault("HUAKAI_BILLING_POLICY_VERSION", "1.0"),
		RequestClass:         envDefault("HUAKAI_REQUEST_CLASS", "standard"),
		SmokeBearerToken:     os.Getenv("HUAKAI_SMOKE_BEARER_TOKEN"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%w: HUAKAI_DATABASE_URL", ErrMissingRequired)
	}

	var err error
	cfg.SmokeTenantID, err = envInt64Optional("HUAKAI_SMOKE_TENANT_ID")
	if err != nil {
		return nil, err
	}
	cfg.SmokeAPIKeyID, err = envInt64Optional("HUAKAI_SMOKE_API_KEY_ID")
	if err != nil {
		return nil, err
	}
	cfg.SmokeUserID, err = envInt64Optional("HUAKAI_SMOKE_USER_ID")
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// SmokeAuthConfigured returns true only when ALL smoke auth env vars are
// non-empty. Used by the chat handler to fail-closed with 503 when smoke
// auth is half-configured.
func (c *Config) SmokeAuthConfigured() bool {
	return c.SmokeBearerToken != "" &&
		c.SmokeTenantID != 0 &&
		c.SmokeAPIKeyID != 0 &&
		c.SmokeUserID != 0
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt64Optional(name string) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be int64: %w", name, err)
	}
	return v, nil
}
