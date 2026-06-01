package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDefaultBillingPolicyVersionServesFreshDeploymentSeed(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_BILLING_POLICY_VERSION", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const wantVersion = "1.0"
	const insertActor = "migration:0066_default_pricing_bootstrap"
	const updatePrivateActor = "migration:0066_default_pricing_bootstrap:updated_empty_private_placeholder"
	const updatePublicActor = "migration:0066_default_pricing_bootstrap:updated_empty_public_placeholder"
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

	migration, err := os.ReadFile("../../sql/migrations/0066_default_pricing_bootstrap.up.sql")
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

	rollback, err := os.ReadFile("../../sql/migrations/0066_default_pricing_bootstrap.down.sql")
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
