package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultBillingPolicyVersionServesFreshDeploymentSeed(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_BILLING_POLICY_VERSION", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const wantVersion = "1.0"
	const insertActor = "migration:0068_default_pricing_bootstrap"
	const updatePrivateActor = "migration:0068_default_pricing_bootstrap:updated_empty_private_placeholder"
	const updatePublicActor = "migration:0068_default_pricing_bootstrap:updated_empty_public_placeholder"
	if cfg.BillingPolicyVersion != wantVersion {
		t.Fatalf("BillingPolicyVersion=%q want %q so a migrated fresh deployment can reserve requests", cfg.BillingPolicyVersion, wantVersion)
	}

	example, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	if !strings.Contains(string(example), `policy_version: "`+wantVersion+`"`) {
		t.Fatalf("config.example.yaml must advertise billing policy %q", wantVersion)
	}

	migration, err := os.ReadFile("../../sql/migrations/0068_default_pricing_bootstrap.up.sql")
	if err != nil {
		t.Fatalf("read pricing bootstrap migration: %v", err)
	}
	migrationText := string(migration)
	if !strings.Contains(migrationText, "'"+wantVersion+"'") {
		t.Fatalf("pricing bootstrap migration must seed version %q", wantVersion)
	}
	if !strings.Contains(migrationText, "pricing_data = '{}'::jsonb") {
		t.Fatalf("pricing bootstrap migration must only replace the empty placeholder table")
	}
	if !strings.Contains(migrationText, "billing_pricing_versions.created_by_actor IS NULL") {
		t.Fatalf("pricing bootstrap migration must not overwrite operator-owned existing pricing rows")
	}
	for _, actor := range []string{updatePrivateActor, updatePublicActor} {
		if !strings.Contains(migrationText, "'"+actor+"'") {
			t.Fatalf("pricing bootstrap migration must mark empty-placeholder updates by prior public state: missing %q", actor)
		}
	}

	rollback, err := os.ReadFile("../../sql/migrations/0068_default_pricing_bootstrap.down.sql")
	if err != nil {
		t.Fatalf("read pricing bootstrap rollback: %v", err)
	}
	rollbackText := string(rollback)
	if strings.Contains(rollbackText, "created_by_actor IS DISTINCT FROM") {
		t.Fatalf("pricing bootstrap rollback must not match unrelated operator pricing rows")
	}
	if !strings.Contains(rollbackText, "is_public = CASE") {
		t.Fatalf("pricing bootstrap rollback must restore the prior public/private placeholder state")
	}
	for _, actor := range []string{insertActor, updatePrivateActor, updatePublicActor} {
		if !strings.Contains(rollbackText, "'"+actor+"'") {
			t.Fatalf("pricing bootstrap rollback must target rows marked by %q", actor)
		}
	}

	var pricing struct {
		Models map[string]map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(extractDollarQuotedJSON(t, migrationText, "$pricing$"), &pricing); err != nil {
		t.Fatalf("bootstrap pricing JSON invalid: %v", err)
	}
	if _, ok := pricing.Models["default"]; ok {
		t.Fatalf("bootstrap pricing JSON must not include a wildcard default rate that masks missing real model prices")
	}
	if _, ok := pricing.Models["*"]; ok {
		t.Fatalf("bootstrap pricing JSON must not include a wildcard rate that masks missing real model prices")
	}
	smokeModel, ok := pricing.Models["gpt-4.1-mini"]
	if !ok {
		t.Fatalf("bootstrap pricing JSON must include the smoke setup model: %+v", pricing.Models)
	}
	for field, want := range map[string]string{
		"input_micro_usd":      "0.40",
		"output_micro_usd":     "1.60",
		"cache_read_micro_usd": "0.10",
	} {
		raw, ok := smokeModel[field]
		if !ok {
			t.Fatalf("bootstrap smoke model missing %s", field)
		}
		if got := strings.TrimSpace(string(raw)); got != want {
			t.Fatalf("bootstrap smoke model %s=%s want %s", field, got, want)
		}
	}
}

func extractDollarQuotedJSON(t *testing.T, src, marker string) []byte {
	t.Helper()
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("missing %s marker", marker)
	}
	start += len(marker)
	end := strings.Index(src[start:], marker)
	if end < 0 {
		t.Fatalf("missing closing %s marker", marker)
	}
	return []byte(strings.TrimSpace(src[start : start+end]))
}

func TestLoadIncludesTransportSidecarSocket(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET", "/tmp/huakai-tls-sidecar.sock")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TransportSidecarSocket != "/tmp/huakai-tls-sidecar.sock" {
		t.Fatalf("TransportSidecarSocket=%q want env value", cfg.TransportSidecarSocket)
	}
	if cfg.TransportSidecarFallback {
		t.Fatal("TransportSidecarFallback default must be false for production fail-closed")
	}
}

func TestLoadIncludesTransportSidecarFallbackFlag(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_TRANSPORT_SIDECAR_FALLBACK", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TransportSidecarFallback {
		t.Fatal("TransportSidecarFallback=false want true from HUAKAI_TRANSPORT_SIDECAR_FALLBACK")
	}
}

func TestLoadQuotaEnforceDefaultOff(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_QUOTA_ENFORCE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QuotaEnforce {
		t.Fatal("QuotaEnforce=true want default false so dormant quota cannot change hot-path behavior without opt-in")
	}
}

func TestLoadQuotaEnforceFlag(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_QUOTA_ENFORCE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.QuotaEnforce {
		t.Fatal("QuotaEnforce=false want true from HUAKAI_QUOTA_ENFORCE")
	}
}

func TestLoadBudgetDefaultsOffWithMemoryFallback(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Budget.Enabled {
		t.Fatal("Budget.Enabled=true want default false")
	}
	if cfg.Budget.FailMode != "memory_fallback" {
		t.Fatalf("Budget.FailMode=%q want memory_fallback", cfg.Budget.FailMode)
	}
}

func TestLoadBudgetFlagRedisAndDefaultLimits(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_BUDGET_ENABLED", "true")
	t.Setenv("HUAKAI_BUDGET_FAIL_MODE", "closed")
	t.Setenv("HUAKAI_BUDGET_REDIS_URL", "redis://localhost:6379/1")
	t.Setenv("HUAKAI_BUDGET_DEFAULT_RPM", "60")
	t.Setenv("HUAKAI_BUDGET_DEFAULT_TPM", "12000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Budget.Enabled {
		t.Fatal("Budget.Enabled=false want true")
	}
	if cfg.Budget.FailMode != "closed" || cfg.Budget.RedisURL != "redis://localhost:6379/1" {
		t.Fatalf("budget fail_mode/redis=%q/%q", cfg.Budget.FailMode, cfg.Budget.RedisURL)
	}
	if cfg.Budget.DefaultRPM != 60 || cfg.Budget.DefaultTPM != 12000 {
		t.Fatalf("budget defaults rpm/tpm=%d/%d want 60/12000", cfg.Budget.DefaultRPM, cfg.Budget.DefaultTPM)
	}
}

func TestLoadIncludesCredentialAcqBootstrapTTLOverrides(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_SHORT_TTL_SECONDS", "1800")
	t.Setenv("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_LONG_TTL_SECONDS", "172800")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CredentialAcqBootstrapShortTTL != 30*time.Minute {
		t.Fatalf("short bootstrap ttl=%s want 30m", cfg.CredentialAcqBootstrapShortTTL)
	}
	if cfg.CredentialAcqBootstrapLongTTL != 48*time.Hour {
		t.Fatalf("long bootstrap ttl=%s want 48h", cfg.CredentialAcqBootstrapLongTTL)
	}
}

func TestLoadRejectsInvalidCredentialAcqBootstrapTTL(t *testing.T) {
	cases := []struct {
		name string
		env  string
		raw  string
	}{
		{name: "short non numeric", env: "HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_SHORT_TTL_SECONDS", raw: "soon"},
		{name: "long zero", env: "HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_LONG_TTL_SECONDS", raw: "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv(tc.env, tc.raw)

			err := loadOnlyError()

			if err == nil {
				t.Fatalf("invalid %s=%q was accepted", tc.env, tc.raw)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v must name %s", err, tc.env)
			}
		})
	}
}

func TestLoadRejectsInvalidQuotaEnforceFlag(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_QUOTA_ENFORCE", "sometimes")

	err := loadOnlyError()

	if err == nil {
		t.Fatal("invalid HUAKAI_QUOTA_ENFORCE was accepted")
	}
	if !strings.Contains(err.Error(), "HUAKAI_QUOTA_ENFORCE") {
		t.Fatalf("err=%v must name HUAKAI_QUOTA_ENFORCE", err)
	}
}

func TestLoadRejectsInvalidBudgetConfig(t *testing.T) {
	cases := []struct {
		name string
		env  string
		raw  string
	}{
		{name: "enabled bool", env: "HUAKAI_BUDGET_ENABLED", raw: "sometimes"},
		{name: "fail mode", env: "HUAKAI_BUDGET_FAIL_MODE", raw: "panic"},
		{name: "rpm", env: "HUAKAI_BUDGET_DEFAULT_RPM", raw: "-1"},
		{name: "tpm", env: "HUAKAI_BUDGET_DEFAULT_TPM", raw: "NaN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv(tc.env, tc.raw)

			err := loadOnlyError()

			if err == nil {
				t.Fatalf("invalid %s=%q was accepted", tc.env, tc.raw)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v must name %s", err, tc.env)
			}
		})
	}
}

func TestLoadRejectsInvalidTransportSidecarFallbackFlag(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_TRANSPORT_SIDECAR_FALLBACK", "sometimes")

	err := loadOnlyError()

	if err == nil {
		t.Fatal("invalid HUAKAI_TRANSPORT_SIDECAR_FALLBACK was accepted")
	}
	if !strings.Contains(err.Error(), "HUAKAI_TRANSPORT_SIDECAR_FALLBACK") {
		t.Fatalf("err=%v must name HUAKAI_TRANSPORT_SIDECAR_FALLBACK", err)
	}
}

func TestLoadIncludesVendorOAuthConfigs(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_CURSOR_OAUTH_AUTH_URL", " https://cursor.example.test/authorize ")
	t.Setenv("HUAKAI_CURSOR_OAUTH_TOKEN_URL", " https://cursor.example.test/token ")
	t.Setenv("HUAKAI_CURSOR_OAUTH_CLIENT_ID", " cursor-client ")
	t.Setenv("HUAKAI_CURSOR_OAUTH_SCOPE", " cursor scope ")
	t.Setenv("HUAKAI_KIRO_OAUTH_TOKEN_URL", "https://kiro.example.test/token")
	t.Setenv("HUAKAI_KIRO_OAUTH_CLIENT_ID", "kiro-client")
	t.Setenv("HUAKAI_KIRO_OAUTH_CLIENT_SECRET", "kiro-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cursor := cfg.VendorOAuth[VendorOAuthCursor]
	if cursor.AuthURL != "https://cursor.example.test/authorize" ||
		cursor.TokenURL != "https://cursor.example.test/token" ||
		cursor.ClientID != "cursor-client" ||
		cursor.Scope != "cursor scope" {
		t.Fatalf("cursor oauth=%+v, want trimmed operator config", cursor)
	}
	kiro := cfg.VendorOAuth[VendorOAuthKiro]
	if kiro.TokenURL != "https://kiro.example.test/token" || kiro.ClientID != "kiro-client" || kiro.ClientSecret != "kiro-secret" {
		t.Fatalf("kiro oauth=%+v, want AWS SSO client secret preserved", kiro)
	}
	if got := len(cfg.VendorOAuth); got != 5 {
		t.Fatalf("vendor oauth config count=%d, want 5 fixed vendor entries", got)
	}
}

func TestVendorOAuthConfigsConfiguredSkipsBlankTokenURL(t *testing.T) {
	cfgs := VendorOAuthConfigs{
		VendorOAuthCursor: {
			ClientID: "cursor-client",
			Scope:    "cursor scope",
		},
		VendorOAuthWindsurf: {
			TokenURL: " https://windsurf.example.test/token ",
			ClientID: " windsurf-client ",
			Scope:    " windsurf scope ",
		},
	}

	got := cfgs.Configured()
	if _, ok := got[VendorOAuthCursor]; ok {
		t.Fatalf("cursor with blank token_url must not be configured: %+v", got[VendorOAuthCursor])
	}
	windsurf, ok := got[VendorOAuthWindsurf]
	if !ok {
		t.Fatal("windsurf with token_url should be configured")
	}
	if windsurf.TokenURL != "https://windsurf.example.test/token" || windsurf.ClientID != "windsurf-client" || windsurf.Scope != "windsurf scope" {
		t.Fatalf("windsurf configured=%+v, want trimmed config", windsurf)
	}
}

func TestLoadIncludesPaymentProviderSecretsWithoutLoggingValues(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_PAYMENT_HMAC_SECRETS", " hmacpay = secret-one , second: secret-two ")
	t.Setenv("HUAKAI_PAYMENT_ENABLE_MOCK", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PaymentHMACSecrets["hmacpay"] != "secret-one" || cfg.PaymentHMACSecrets["second"] != "secret-two" {
		t.Fatalf("PaymentHMACSecrets=%+v, want trimmed provider secret map", cfg.PaymentHMACSecrets)
	}
	if !cfg.PaymentEnableMock {
		t.Fatal("PaymentEnableMock=false want true from explicit env")
	}
}

func TestLoadPaymentExpireSweepDefaultsDisabledWithBatchDefault(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL", "")
	t.Setenv("HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PaymentExpireSweepInterval != 0 {
		t.Fatalf("PaymentExpireSweepInterval=%s want 0 so empty env keeps worker disabled", cfg.PaymentExpireSweepInterval)
	}
	if cfg.PaymentExpireSweepBatchLimit != DefaultPaymentExpireSweepBatchLimit {
		t.Fatalf("PaymentExpireSweepBatchLimit=%d want %d", cfg.PaymentExpireSweepBatchLimit, DefaultPaymentExpireSweepBatchLimit)
	}
}

func TestLoadPaymentExpireSweepReadsEnv(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL", "45s")
	t.Setenv("HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", "37")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PaymentExpireSweepInterval != 45*time.Second {
		t.Fatalf("PaymentExpireSweepInterval=%s want 45s", cfg.PaymentExpireSweepInterval)
	}
	if cfg.PaymentExpireSweepBatchLimit != 37 {
		t.Fatalf("PaymentExpireSweepBatchLimit=%d want 37", cfg.PaymentExpireSweepBatchLimit)
	}
}

func TestLoadAPIKeyExpirySweepDefaultsEnabledBounded(t *testing.T) {
	// AUTH-150: default boot should materialize expired key status without
	// operator ceremony, while still bounding cadence and rows per batch.
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_ENABLED", "")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", "")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.APIKeyExpirySweepEnabled {
		t.Fatal("APIKeyExpirySweepEnabled=false want default true")
	}
	if cfg.APIKeyExpirySweepInterval != DefaultAPIKeyExpirySweepInterval {
		t.Fatalf("APIKeyExpirySweepInterval=%s want %s", cfg.APIKeyExpirySweepInterval, DefaultAPIKeyExpirySweepInterval)
	}
	if cfg.APIKeyExpirySweepBatchLimit != DefaultAPIKeyExpirySweepBatchLimit {
		t.Fatalf("APIKeyExpirySweepBatchLimit=%d want %d", cfg.APIKeyExpirySweepBatchLimit, DefaultAPIKeyExpirySweepBatchLimit)
	}
}

func TestLoadAPIKeyExpirySweepReadsEnvAndCanDisable(t *testing.T) {
	// MUTATION: ignore HUAKAI_API_KEY_EXPIRY_SWEEP_ENABLED=false; operators
	// cannot pause the display-state worker during incident response.
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_ENABLED", "false")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", "30s")
	t.Setenv("HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", "17")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKeyExpirySweepEnabled {
		t.Fatal("APIKeyExpirySweepEnabled=true want false from env")
	}
	if cfg.APIKeyExpirySweepInterval != 30*time.Second {
		t.Fatalf("APIKeyExpirySweepInterval=%s want 30s", cfg.APIKeyExpirySweepInterval)
	}
	if cfg.APIKeyExpirySweepBatchLimit != 17 {
		t.Fatalf("APIKeyExpirySweepBatchLimit=%d want 17", cfg.APIKeyExpirySweepBatchLimit)
	}
}

func TestLoadAlertingEvalDefaultsDisabled(t *testing.T) {
	// MUTATION: default AlertingEvalEnabled to true; fresh installs start the alert evaluator without operator opt-in.
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_ALERTING_EVAL_ENABLED", "")
	t.Setenv("HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AlertingEvalEnabled {
		t.Fatal("AlertingEvalEnabled=true want default false")
	}
	if cfg.AlertingEvalInterval != time.Minute {
		t.Fatalf("AlertingEvalInterval=%s want 1m default bounded ticker", cfg.AlertingEvalInterval)
	}
}

func TestLoadAlertingEvalReadsEnv(t *testing.T) {
	// MUTATION: ignore HUAKAI_ALERTING_EVAL_* env; operator opt-in never starts the evaluator at the requested cadence.
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_ALERTING_EVAL_ENABLED", "true")
	t.Setenv("HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS", "15")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AlertingEvalEnabled {
		t.Fatal("AlertingEvalEnabled=false want true from env")
	}
	if cfg.AlertingEvalInterval != 15*time.Second {
		t.Fatalf("AlertingEvalInterval=%s want 15s", cfg.AlertingEvalInterval)
	}
}

func TestLoadRejectsInvalidAlertingEvalConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		raw  string
	}{
		{name: "enabled garbage", env: "HUAKAI_ALERTING_EVAL_ENABLED", raw: "sometimes"},
		{name: "interval zero", env: "HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS", raw: "0"},
		{name: "interval negative", env: "HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS", raw: "-1"},
		{name: "interval garbage", env: "HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS", raw: "soon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// MUTATION: accept malformed alert eval env; boot silently runs with an unintended lifecycle.
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv(tc.env, tc.raw)

			err := loadOnlyError()

			if err == nil {
				t.Fatalf("invalid %s=%q was accepted", tc.env, tc.raw)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v must name %s", err, tc.env)
			}
		})
	}
}

func TestLoadRejectsInvalidPaymentExpireSweepConfig(t *testing.T) {
	cases := []struct {
		name string
		env  string
		raw  string
	}{
		{name: "interval garbage", env: "HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL", raw: "soon"},
		{name: "interval negative", env: "HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL", raw: "-1s"},
		{name: "batch zero", env: "HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", raw: "0"},
		{name: "batch negative", env: "HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", raw: "-1"},
		{name: "batch garbage", env: "HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", raw: "many"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv(tc.env, tc.raw)

			err := loadOnlyError()

			if err == nil {
				t.Fatalf("invalid %s=%q was accepted", tc.env, tc.raw)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v must name %s", err, tc.env)
			}
		})
	}
}

func TestLoadRejectsInvalidAPIKeyExpirySweepConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		raw  string
	}{
		{name: "enabled garbage", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_ENABLED", raw: "sometimes"},
		{name: "interval zero", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", raw: "0"},
		{name: "interval garbage", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", raw: "soon"},
		{name: "interval negative", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", raw: "-1s"},
		{name: "batch zero", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", raw: "0"},
		{name: "batch negative", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", raw: "-1"},
		{name: "batch garbage", env: "HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", raw: "many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// MUTATION: accept malformed worker env; boot silently runs with
			// an unintended expiry sweep lifecycle.
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv(tc.env, tc.raw)

			err := loadOnlyError()

			if err == nil {
				t.Fatalf("invalid %s=%q was accepted", tc.env, tc.raw)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v must name %s", err, tc.env)
			}
		})
	}
}

func TestLoadPaymentProviderSecretsRejectsMalformedEntries(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_PAYMENT_HMAC_SECRETS", "missing-delimiter")

	err := loadOnlyError()
	if err == nil {
		t.Fatal("malformed HUAKAI_PAYMENT_HMAC_SECRETS was accepted")
	}
	if !strings.Contains(err.Error(), "HUAKAI_PAYMENT_HMAC_SECRETS") {
		t.Fatalf("err=%v must name HUAKAI_PAYMENT_HMAC_SECRETS", err)
	}
}

func loadOnlyError() error {
	_, err := Load()
	return err
}

func TestLoadDBPoolDefaultsZeroWhenUnset(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_DB_MAX_CONNS", "")
	t.Setenv("HUAKAI_DB_MIN_CONNS", "")
	t.Setenv("HUAKAI_DB_MAX_CONN_LIFETIME_SECONDS", "")
	t.Setenv("HUAKAI_DB_MAX_CONN_IDLE_TIME_SECONDS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Default-preserving contract: unset pool envs leave all fields zero so the
	// db package keeps its defaults (16/2/30m/5m).
	if cfg.DBMaxConns != 0 || cfg.DBMinConns != 0 || cfg.DBMaxConnLifetime != 0 || cfg.DBMaxConnIdleTime != 0 {
		t.Fatalf("expected zero overrides when unset, got %d/%d/%s/%s", cfg.DBMaxConns, cfg.DBMinConns, cfg.DBMaxConnLifetime, cfg.DBMaxConnIdleTime)
	}
}

func TestLoadDBPoolReadsEnv(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_DB_MAX_CONNS", "64")
	t.Setenv("HUAKAI_DB_MIN_CONNS", "8")
	t.Setenv("HUAKAI_DB_MAX_CONN_LIFETIME_SECONDS", "2700")
	t.Setenv("HUAKAI_DB_MAX_CONN_IDLE_TIME_SECONDS", "120")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBMaxConns != 64 {
		t.Fatalf("DBMaxConns = %d, want 64", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 8 {
		t.Fatalf("DBMinConns = %d, want 8", cfg.DBMinConns)
	}
	if cfg.DBMaxConnLifetime != 45*time.Minute {
		t.Fatalf("DBMaxConnLifetime = %s, want 45m", cfg.DBMaxConnLifetime)
	}
	if cfg.DBMaxConnIdleTime != 2*time.Minute {
		t.Fatalf("DBMaxConnIdleTime = %s, want 2m", cfg.DBMaxConnIdleTime)
	}
}

func TestLoadDBMaxConnsInvalidIsError(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_DB_MAX_CONNS", "abc")
	// Fail-loud: a malformed pool size aborts boot, not silently default.
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid HUAKAI_DB_MAX_CONNS, got nil")
	}
}
