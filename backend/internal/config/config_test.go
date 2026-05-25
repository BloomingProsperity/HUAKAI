package config

import "testing"

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
