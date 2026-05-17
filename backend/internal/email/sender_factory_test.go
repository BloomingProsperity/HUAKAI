package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestAT_EMAIL_001_UnconfiguredSenderReturnsExplicitError(t *testing.T) {
	keys := testEmailKeys(t)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: {}}}
	sender, err := BuildEmailSender(context.Background(), store, keys, WithSMTPDispatch(func(context.Context, SMTPSettings, Message) error {
		t.Fatal("dispatch must not run when settings are missing")
		return nil
	}))
	if err != nil {
		t.Fatalf("BuildEmailSender: %v", err)
	}
	err = sender.SendVerification(context.Background(), userauth.User{TenantID: 1, Email: "u@example.test"}, "tok")
	if !errors.Is(err, ErrEmailBackendUnconfigured) {
		t.Fatalf("SendVerification error = %v, want ErrEmailBackendUnconfigured", err)
	}
}

func TestAT_EMAIL_002_003_LoadSettingsDecryptsPassword(t *testing.T) {
	keys := testEmailKeys(t)
	raw := completeRawSettings(t, keys, 1)
	settings, err := SettingsFromStored(context.Background(), raw, keys, 1)
	if err != nil {
		t.Fatalf("SettingsFromStored: %v", err)
	}
	if settings.Password != "smtp-secret" || settings.Host != "smtp.example.test" || settings.Port != 587 {
		t.Fatalf("settings not decoded correctly: %+v", settings)
	}
	if !settings.VerifyEmailEnabled {
		t.Fatal("email verification toggle was not decoded")
	}
}

func TestAT_EMAIL_004_TenantScopedPasswordEnvelope(t *testing.T) {
	keys := testEmailKeys(t)
	raw := completeRawSettings(t, keys, 1)
	_, err := SettingsFromStored(context.Background(), raw, keys, 2)
	if err == nil {
		t.Fatal("tenant 2 decrypted tenant 1 password envelope")
	}
}

func TestAT_EMAIL_005_HeaderSanitizationRemovesCRLF(t *testing.T) {
	settings := SMTPSettings{
		TenantID: 1, Host: "smtp.example.test", Port: 587, PortConfigured: true,
		Username: "user", Password: "pass", From: "noreply@example.test\r\nBcc: x@example.test", FromName: "HUAKAI\nInjected: y",
	}
	payload, from, to := buildSMTPPayload(settings, Message{
		TenantID: 1,
		To:       "target@example.test\r\nCc: leak@example.test",
		Subject:  "Welcome\r\nBcc: injected@example.test",
		HTMLBody: "<p>ok</p>",
	})
	text := string(payload)
	if strings.Contains(text, "\r\nBcc:") || strings.Contains(text, "\r\nCc:") ||
		strings.Contains(from, "\n") || strings.Contains(from, "\r") ||
		strings.Contains(to, "\n") || strings.Contains(to, "\r") {
		t.Fatalf("header injection survived: from=%q to=%q payload=%q", from, to, text)
	}
}

func TestAT_EMAIL_006_007_ProductionGateRequiresCompleteSettingsAndVerifyToggle(t *testing.T) {
	keys := testEmailKeys(t)
	ctx := context.Background()
	store := &fakeSettingsStore{activeTenants: []int64{1}, settings: map[int64]StoredSettings{
		1: {
			SettingMailHost:          "smtp.example.test",
			SettingMailPort:          "587",
			SettingMailUsername:      "user",
			SettingMailFrom:          "noreply@example.test",
			SettingVerifyRequirement: "true",
		},
	}}
	if err := ValidateProductionReleaseGate(ctx, store, keys); !errors.Is(err, ErrEmailBackendUnconfigured) {
		t.Fatalf("missing password gate error = %v, want ErrEmailBackendUnconfigured", err)
	}
	store.settings[1] = completeRawSettings(t, keys, 1)
	store.settings[1][SettingVerifyRequirement] = "false"
	if err := ValidateProductionReleaseGate(ctx, store, keys); !errors.Is(err, ErrEmailBackendUnconfigured) {
		t.Fatalf("disabled verify gate error = %v, want ErrEmailBackendUnconfigured", err)
	}
	store.settings[1][SettingVerifyRequirement] = "true"
	if err := ValidateProductionReleaseGate(ctx, store, keys); err != nil {
		t.Fatalf("complete production gate: %v", err)
	}
}

func TestAT_EMAIL_008_SMTPSenderImplementsEmailSender(t *testing.T) {
	var sender EmailSender = NewSMTPSender(SMTPSettings{})
	if sender == nil {
		t.Fatal("SMTPSender did not satisfy EmailSender")
	}
}

func TestAT_EMAIL_009_VerificationEmailCooldown(t *testing.T) {
	keys := testEmailKeys(t)
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	sends := 0
	sender, err := BuildEmailSender(context.Background(), store, keys,
		WithClock(func() time.Time { return now }),
		WithSMTPDispatch(func(context.Context, SMTPSettings, Message) error {
			sends++
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildEmailSender: %v", err)
	}
	user := userauth.User{TenantID: 1, Email: "u@example.test"}
	if err := sender.SendVerification(context.Background(), user, "one"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := sender.SendVerification(context.Background(), user, "two"); err != nil {
		t.Fatalf("cooldown send: %v", err)
	}
	if sends != 1 {
		t.Fatalf("sends during cooldown = %d, want 1", sends)
	}
	now = now.Add(DefaultVerificationEmailCooldown + time.Second)
	if err := sender.SendVerification(context.Background(), user, "three"); err != nil {
		t.Fatalf("send after cooldown: %v", err)
	}
	if sends != 2 {
		t.Fatalf("sends after cooldown = %d, want 2", sends)
	}
}

func TestAT_EMAIL_010_PasswordResetEmailCooldown(t *testing.T) {
	keys := testEmailKeys(t)
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: completeRawSettings(t, keys, 1)}}
	sends := 0
	sender, err := BuildEmailSender(context.Background(), store, keys,
		WithClock(func() time.Time { return now }),
		WithSMTPDispatch(func(context.Context, SMTPSettings, Message) error {
			sends++
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildEmailSender: %v", err)
	}
	user := userauth.User{TenantID: 1, Email: "u@example.test"}
	if err := sender.SendPasswordReset(context.Background(), user, "one"); err != nil {
		t.Fatalf("first reset send: %v", err)
	}
	if err := sender.SendPasswordReset(context.Background(), user, "two"); err != nil {
		t.Fatalf("cooldown reset send: %v", err)
	}
	if sends != 1 {
		t.Fatalf("reset sends during cooldown = %d, want 1", sends)
	}
	now = now.Add(DefaultPasswordResetCooldown + time.Second)
	if err := sender.SendPasswordReset(context.Background(), user, "three"); err != nil {
		t.Fatalf("reset send after cooldown: %v", err)
	}
	if sends != 2 {
		t.Fatalf("reset sends after cooldown = %d, want 2", sends)
	}
}

func testEmailKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("email-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}

func completeRawSettings(t *testing.T, keys credentialstore.KeyProvider, tenantID int64) StoredSettings {
	t.Helper()
	secret, err := EncodeSecret(context.Background(), keys, tenantID, "smtp-secret")
	if err != nil {
		t.Fatalf("EncodeSecret: %v", err)
	}
	return StoredSettings{
		SettingMailHost:          "smtp.example.test",
		SettingMailPort:          "587",
		SettingMailUsername:      "smtp-user",
		SettingMailPassword:      secret,
		SettingMailFrom:          "noreply@example.test",
		SettingMailFromName:      "HUAKAI",
		SettingMailTLS:           "true",
		SettingVerifyRequirement: "true",
	}
}

type fakeSettingsStore struct {
	settings      map[int64]StoredSettings
	activeTenants []int64
}

func (s *fakeSettingsStore) Load(_ context.Context, tenantID int64) (StoredSettings, error) {
	if s == nil || s.settings == nil {
		return nil, nil
	}
	return s.settings[tenantID], nil
}

func (s *fakeSettingsStore) List(_ context.Context, tenantID int64) ([]StoredSetting, error) {
	raw := s.settings[tenantID]
	out := make([]StoredSetting, 0, len(raw))
	for key, value := range raw {
		out = append(out, StoredSetting{Key: key, Value: value})
	}
	return out, nil
}

func (s *fakeSettingsStore) Save(_ context.Context, tenantID int64, values map[string]string, _ string) error {
	if s.settings == nil {
		s.settings = make(map[int64]StoredSettings)
	}
	if s.settings[tenantID] == nil {
		s.settings[tenantID] = make(StoredSettings)
	}
	for key, value := range values {
		s.settings[tenantID][key] = value
	}
	return nil
}

func (s *fakeSettingsStore) ListActiveTenantIDs(context.Context) ([]int64, error) {
	return append([]int64(nil), s.activeTenants...), nil
}
