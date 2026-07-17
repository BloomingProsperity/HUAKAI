package accountbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
)

func TestRecoveryRoundTripAndTamperDetection(t *testing.T) {
	now := time.Date(2026, 7, 17, 3, 30, 0, 0, time.UTC)
	manifest := Manifest{
		Version: ManifestVersion, BundleID: "bundle-1", Mode: ModeRecovery,
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
		Accounts: []Account{{
			SourceAccountID: 11,
			Template:        accountsource.AccountTemplate{Name: "acct", SourceProvider: "openai", AccountType: "oauth", Enabled: true},
			Vendor:          "openai", AuthMode: "codex_cli_oauth", Credential: json.RawMessage(`{"access_token":"secret"}`),
		}},
	}
	passphrase := "a-strong-transfer-passphrase-2026"
	raw, err := EncodeRecovery(manifest, passphrase, now)
	if err != nil {
		t.Fatalf("EncodeRecovery: %v", err)
	}
	if bytes.Contains(raw, []byte("secret")) {
		t.Fatal("恢复包外层不得出现凭据明文")
	}
	decoded, err := DecodeRecovery(raw, passphrase, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("DecodeRecovery: %v", err)
	}
	if got := string(decoded.Accounts[0].Credential); got != `{"access_token":"secret"}` {
		t.Fatalf("credential=%s", got)
	}
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Ciphertext = envelope.Ciphertext[:len(envelope.Ciphertext)-1] + "A"
	tampered, _ := json.Marshal(envelope)
	if _, err := DecodeRecovery(tampered, passphrase, now.Add(time.Minute)); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("tampered err=%v want signature mismatch", err)
	}
}

func TestRecoveryRejectsWrongPassphraseAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 17, 3, 30, 0, 0, time.UTC)
	manifest := Manifest{Version: ManifestVersion, BundleID: "bundle-2", Mode: ModeRecovery,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute), Accounts: []Account{{
			Template: accountsource.AccountTemplate{Name: "acct", AccountType: "api_key"},
			Vendor:   "openai", AuthMode: "api_key", Credential: json.RawMessage(`{"api_key":"secret"}`),
		}}}
	raw, err := EncodeRecovery(manifest, "correct-passphrase-at-least-20", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecovery(raw, "wrong-passphrase-at-least-20xx", now); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("wrong passphrase err=%v", err)
	}
	if _, err := DecodeRecovery(raw, "correct-passphrase-at-least-20", now.Add(2*time.Minute)); !errors.Is(err, ErrBundleExpired) {
		t.Fatalf("expired err=%v", err)
	}
}

func TestStructureBundleRejectsCredentialFields(t *testing.T) {
	now := time.Date(2026, 7, 17, 3, 30, 0, 0, time.UTC)
	manifest := Manifest{Version: ManifestVersion, BundleID: "bundle-3", Mode: ModeStructure, CreatedAt: now,
		Accounts: []Account{{Template: accountsource.AccountTemplate{Name: "acct", AccountType: "api_key"}, Credential: json.RawMessage(`{"api_key":"leak"}`)}}}
	if _, err := EncodeStructure(manifest, now); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("EncodeStructure err=%v", err)
	}
}

func TestRecoveryItemsRejectsPartiallyMissingCredentials(t *testing.T) {
	manifest := Manifest{Mode: ModeRecovery, Accounts: []Account{
		{Template: accountsource.AccountTemplate{Name: "complete"}, Vendor: "openai", AuthMode: "api_key", Credential: json.RawMessage(`{"api_key":"secret"}`)},
		{Template: accountsource.AccountTemplate{Name: "incomplete"}, Vendor: "openai", AuthMode: "api_key"},
	}}
	items, err := RecoveryItems(manifest)
	if !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("RecoveryItems err=%v，期望拒绝部分恢复", err)
	}
	if items != nil {
		t.Fatalf("部分恢复失败时不得返回候选，items=%+v", items)
	}
}
