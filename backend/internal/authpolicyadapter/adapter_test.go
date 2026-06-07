package authpolicyadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestRegPolicyAdapterReadsSettings(t *testing.T) {
	ctx := context.Background()
	settings := stubSettings{
		values: map[platformsettings.SettingKey]string{
			platformsettings.KeyPasswordRegisterEnabled:      "false",
			platformsettings.KeyPasswordLoginEnabled:         "false",
			platformsettings.KeyEmailDomainAllowlistEnabled:  "true",
			platformsettings.KeyEmailDomainAllowlist:         "example.com",
			platformsettings.KeyEmailAliasRestrictionEnabled: "true",
			platformsettings.KeyReservedEmailLocalparts:      "admin,root",
		},
	}

	gate := NewRegistrationGate(settings)
	registerAllowed, err := gate.PasswordRegistrationAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed err=%v want nil", err)
	}
	if registerAllowed {
		t.Fatalf("PasswordRegistrationAllowed=true want false; MUTATION: adapter ignored password_register_enabled=false")
	}
	loginAllowed, err := gate.PasswordLoginAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed err=%v want nil", err)
	}
	if loginAllowed {
		t.Fatalf("PasswordLoginAllowed=true want false; MUTATION: adapter ignored password_login_enabled=false")
	}

	emailPolicy := NewEmailPolicy(settings)
	domainEnabled, domainList, err := emailPolicy.EmailDomainAllowlist(ctx, 42)
	if err != nil {
		t.Fatalf("EmailDomainAllowlist err=%v want nil", err)
	}
	if !domainEnabled || domainList != "example.com" {
		t.Fatalf("EmailDomainAllowlist=(%v,%q) want (true,%q); MUTATION: adapter ignored allowlist settings", domainEnabled, domainList, "example.com")
	}
	aliasEnabled, err := emailPolicy.EmailAliasRestrictionEnabled(ctx, 42)
	if err != nil {
		t.Fatalf("EmailAliasRestrictionEnabled err=%v want nil", err)
	}
	if !aliasEnabled {
		t.Fatalf("EmailAliasRestrictionEnabled=false want true; MUTATION: adapter ignored email_alias_restriction_enabled=true")
	}
	reserved, err := emailPolicy.ReservedEmailLocalparts(ctx, 42)
	if err != nil {
		t.Fatalf("ReservedEmailLocalparts err=%v want nil", err)
	}
	if reserved != "admin,root" {
		t.Fatalf("ReservedEmailLocalparts=%q want %q; MUTATION: adapter ignored reserved_email_localparts", reserved, "admin,root")
	}
}

func TestRegPolicyFailOpen(t *testing.T) {
	ctx := context.Background()
	settings := stubSettings{err: errors.New("settings unavailable")}

	gate := NewRegistrationGate(settings)
	registerAllowed, err := gate.PasswordRegistrationAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed err=%v want nil fail-open", err)
	}
	if !registerAllowed {
		t.Fatalf("PasswordRegistrationAllowed=false want true fail-open; MUTATION: read error locked registration")
	}
	loginAllowed, err := gate.PasswordLoginAllowed(ctx, 42)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed err=%v want nil fail-open", err)
	}
	if !loginAllowed {
		t.Fatalf("PasswordLoginAllowed=false want true fail-open; MUTATION: read error locked login")
	}

	emailPolicy := NewEmailPolicy(settings)
	domainEnabled, domainList, err := emailPolicy.EmailDomainAllowlist(ctx, 42)
	if err != nil {
		t.Fatalf("EmailDomainAllowlist err=%v want nil fail-open", err)
	}
	if domainEnabled || domainList != "" {
		t.Fatalf("EmailDomainAllowlist=(%v,%q) want (false,\"\") fail-open; MUTATION: read error enabled allowlist", domainEnabled, domainList)
	}
	aliasEnabled, err := emailPolicy.EmailAliasRestrictionEnabled(ctx, 42)
	if err != nil {
		t.Fatalf("EmailAliasRestrictionEnabled err=%v want nil fail-open", err)
	}
	if aliasEnabled {
		t.Fatalf("EmailAliasRestrictionEnabled=true want false fail-open; MUTATION: read error enabled alias restriction")
	}
	reserved, err := emailPolicy.ReservedEmailLocalparts(ctx, 42)
	if err != nil {
		t.Fatalf("ReservedEmailLocalparts err=%v want nil fail-open", err)
	}
	if reserved != "" {
		t.Fatalf("ReservedEmailLocalparts=%q want empty fail-open; MUTATION: read error enabled reserved local-parts", reserved)
	}
}

type stubSettings struct {
	values map[platformsettings.SettingKey]string
	err    error
}

func (s stubSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	if value, ok := s.values[key]; ok {
		return platformsettings.StoredSetting{Key: key, Value: value}, nil
	}
	value, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Key: key, Value: value, Source: platformsettings.SourceDefault}, nil
}
