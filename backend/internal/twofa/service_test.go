package twofa

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestSetupEncryptsSecretAndEnableRequiresCurrentTOTP(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := NewService(store, mustKeyProvider(t), WithNow(func() time.Time { return now }))

	setup, err := svc.Setup(ctx, SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if setup.Secret == "" || len(setup.BackupCodes) != DefaultBackupCodeCount {
		t.Fatalf("setup response missing secret or backup codes: %+v", setup)
	}
	if !strings.Contains(setup.QRData, "otpauth://totp/") || !strings.Contains(setup.QRData, "issuer=HUAKAI") || !strings.Contains(setup.QRData, setup.Secret) {
		t.Fatalf("qr_data=%q missing otpauth issuer or one-time setup secret", setup.QRData)
	}
	stored, found, err := store.GetSettings(ctx, 1, 1001)
	if err != nil || !found {
		t.Fatalf("stored settings found=%v err=%v", found, err)
	}
	if bytes.Contains(stored.SecretEnc, []byte(setup.Secret)) {
		t.Fatalf("encrypted secret contains setup secret plaintext: %q", string(stored.SecretEnc))
	}
	if stored.Enabled {
		t.Fatal("Setup must not enable 2FA before a correct TOTP proof")
	}

	code := codeFromSetupSecret(t, setup.Secret, now)
	status, err := svc.Enable(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: code})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !status.Enabled || status.BackupCodesRemaining != DefaultBackupCodeCount {
		t.Fatalf("status after enable=%+v", status)
	}
}

func TestBackupCodeLoginCanBeUsedOnlyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 10, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return now }))
	setup := setupAndEnable(t, ctx, svc, now)
	backup := setup.BackupCodes[0]

	result, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: backup})
	if err != nil {
		t.Fatalf("first backup VerifyLogin: %v", err)
	}
	if result.Method != MethodBackupCode || result.BackupCodesRemaining != DefaultBackupCodeCount-1 {
		t.Fatalf("first backup result=%+v", result)
	}
	_, err = svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: backup})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second backup VerifyLogin err=%v want ErrInvalidCode", err)
	}
}

func TestFailedAttemptsLockTOTPUntilLockWindowExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 20, 0, 0, time.UTC)
	svc := NewService(
		NewMemoryStore(),
		mustKeyProvider(t),
		WithNow(func() time.Time { return now }),
		WithMaxFailedAttempts(3),
		WithLockDuration(10*time.Minute),
	)
	setup := setupAndEnable(t, ctx, svc, now)
	valid := codeFromSetupSecret(t, setup.Secret, now)

	for i := 1; i <= 2; i++ {
		_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"})
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("wrong attempt %d err=%v want ErrInvalidCode", i, err)
		}
	}
	_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("third wrong attempt err=%v want ErrLocked", err)
	}
	_, err = svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: valid})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("valid code while locked err=%v want ErrLocked", err)
	}
	now = now.Add(11 * time.Minute)
	valid = codeFromSetupSecret(t, setup.Secret, now)
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: valid}); err != nil {
		t.Fatalf("valid code after lock expiry: %v", err)
	}
}

func TestLoginChallengeRequiresValidSignatureAndCode(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return now }))
	setup := setupAndEnable(t, ctx, svc, now)

	challenge, err := svc.StartLoginChallenge(ctx, 1, 1001)
	if err != nil {
		t.Fatalf("StartLoginChallenge: %v", err)
	}
	if challenge.ID == "" || !challenge.ExpiresAt.After(now) {
		t.Fatalf("challenge=%+v", challenge)
	}
	tampered := challenge.ID[:len(challenge.ID)-1] + "x"
	_, err = svc.VerifyLoginChallenge(ctx, ChallengeVerifyInput{ChallengeID: tampered, Code: codeFromSetupSecret(t, setup.Secret, now)})
	if !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("tampered challenge err=%v want ErrChallengeInvalid", err)
	}
	result, err := svc.VerifyLoginChallenge(ctx, ChallengeVerifyInput{ChallengeID: challenge.ID, Code: codeFromSetupSecret(t, setup.Secret, now)})
	if err != nil {
		t.Fatalf("VerifyLoginChallenge: %v", err)
	}
	if result.TenantID != 1 || result.UserID != 1001 || result.Method != MethodTOTP {
		t.Fatalf("challenge result=%+v", result)
	}
}

func setupAndEnable(t *testing.T, ctx context.Context, svc *Service, now time.Time) SetupResult {
	t.Helper()
	setup, err := svc.Setup(ctx, SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := svc.Enable(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: codeFromSetupSecret(t, setup.Secret, now)}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return setup
}

func codeFromSetupSecret(t *testing.T, encoded string, now time.Time) string {
	t.Helper()
	secret, err := DecodeSecret(encoded)
	if err != nil {
		t.Fatalf("DecodeSecret: %v", err)
	}
	code, err := GenerateTOTP(secret, now, DefaultTOTPDigits, DefaultTOTPStep)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	return code
}

func mustKeyProvider(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("twofa-test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return keys
}
