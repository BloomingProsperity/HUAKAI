// Package config loads HUAKAI gateway runtime configuration from environment
// variables. YAML support is deferred until a multi-deployment story exists.
package config

import (
	"errors"
	"fmt"
	"os"
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
}

// ErrMissingRequired indicates one or more required env vars were not set.
var ErrMissingRequired = errors.New("config: missing required env var")

// Load reads env vars into a Config. Required vars: HUAKAI_DATABASE_URL.
//
// Removed Smoke* fields — replaced by api_keys-table-
// backed inbound auth (auth.APIKeyResolver). Rolling back to env-injected
// auth requires a code revert (no build-tag escape hatch).
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:          os.Getenv("HUAKAI_DATABASE_URL"),
		Listen:               envDefault("HUAKAI_ADDR", ":8080"),
		BillingPolicyVersion: envDefault("HUAKAI_BILLING_POLICY_VERSION", "1.0"),
		RequestClass:         envDefault("HUAKAI_REQUEST_CLASS", "standard"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%w: HUAKAI_DATABASE_URL", ErrMissingRequired)
	}
	return cfg, nil
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
