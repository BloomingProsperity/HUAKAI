package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestStaticVaultSetAndResolve(t *testing.T) {
	v := NewStaticVault()
	credential := Credential{
		Type:  CredentialTypeAPIKey,
		Value: "sk-test",
		Extra: map[string]string{"project_id": "proj_1"},
	}
	account := AccountInfo{
		AccountID:   11,
		Platform:    "openai",
		AccountType: "apikey",
	}

	if err := v.Set(11, credential, account); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	gotCredential, gotAccount, err := v.Resolve(context.Background(), 11)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if gotCredential.Type != credential.Type {
		t.Fatalf("Resolve() credential type = %q, want %q", gotCredential.Type, credential.Type)
	}
	if gotCredential.Value != credential.Value {
		t.Fatalf("Resolve() credential value = %q, want %q", gotCredential.Value, credential.Value)
	}
	if gotCredential.Extra["project_id"] != "proj_1" {
		t.Fatalf("Resolve() credential extra project_id = %q, want %q", gotCredential.Extra["project_id"], "proj_1")
	}
	if gotAccount != account {
		t.Fatalf("Resolve() account = %#v, want %#v", gotAccount, account)
	}
}

func TestStaticVaultResolveNotFound(t *testing.T) {
	v := NewStaticVault()

	_, _, err := v.Resolve(context.Background(), 404)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrAccountNotFound", err)
	}
}

func TestStaticVaultNilReceiverResolveNotFound(t *testing.T) {
	var v *StaticVault

	_, _, err := v.Resolve(context.Background(), 1)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrAccountNotFound", err)
	}
}

func TestStaticVaultSetRejectsZeroAccountID(t *testing.T) {
	v := NewStaticVault()

	err := v.Set(0, Credential{Type: CredentialTypeAPIKey, Value: "sk-test"}, AccountInfo{})
	if err == nil {
		t.Fatal("Set() error = nil, want error")
	}
}

func TestStaticVaultSetRejectsEmptyCredentialType(t *testing.T) {
	v := NewStaticVault()

	err := v.Set(1, Credential{Value: "sk-test"}, AccountInfo{AccountID: 1})
	if err == nil {
		t.Fatal("Set() error = nil, want error")
	}
}

func TestStaticVaultSetOverwritesExistingEntry(t *testing.T) {
	v := NewStaticVault()
	first := Credential{Type: CredentialTypeAPIKey, Value: "first"}
	second := Credential{Type: CredentialTypeOAuthAccessToken, Value: "second"}
	firstAccount := AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"}
	secondAccount := AccountInfo{AccountID: 7, Platform: "gemini", AccountType: "oauth"}

	if err := v.Set(7, first, firstAccount); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	if err := v.Set(7, second, secondAccount); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}

	gotCredential, gotAccount, err := v.Resolve(context.Background(), 7)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if gotCredential.Type != second.Type {
		t.Fatalf("Resolve() credential type = %q, want %q", gotCredential.Type, second.Type)
	}
	if gotCredential.Value != second.Value {
		t.Fatalf("Resolve() credential value = %q, want %q", gotCredential.Value, second.Value)
	}
	if gotAccount != secondAccount {
		t.Fatalf("Resolve() account = %#v, want %#v", gotAccount, secondAccount)
	}
}

func TestStaticVaultSize(t *testing.T) {
	v := NewStaticVault()

	if got := v.Size(); got != 0 {
		t.Fatalf("empty Size() = %d, want 0", got)
	}
	if err := v.Set(1, Credential{Type: CredentialTypeAPIKey, Value: "one"}, AccountInfo{AccountID: 1}); err != nil {
		t.Fatalf("Set(1) error = %v", err)
	}
	if err := v.Set(2, Credential{Type: CredentialTypeSessionToken, Value: "two"}, AccountInfo{AccountID: 2}); err != nil {
		t.Fatalf("Set(2) error = %v", err)
	}
	if got := v.Size(); got != 2 {
		t.Fatalf("Size() = %d, want 2", got)
	}
}

func TestStaticVaultNilReceiverSize(t *testing.T) {
	var v *StaticVault

	if got := v.Size(); got != 0 {
		t.Fatalf("nil Size() = %d, want 0", got)
	}
}

func TestStaticVaultConcurrentSetAndResolve(t *testing.T) {
	v := NewStaticVault()
	const goroutines = 64

	var wg sync.WaitGroup
	for i := 1; i <= goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			accountID := int64(i)
			credential := Credential{
				Type:  CredentialTypeAPIKey,
				Value: "shared-test-value",
			}
			account := AccountInfo{
				AccountID:   accountID,
				Platform:    "openai",
				AccountType: "apikey",
			}
			if err := v.Set(accountID, credential, account); err != nil {
				t.Errorf("Set(%d) error = %v", accountID, err)
				return
			}

			gotCredential, gotAccount, err := v.Resolve(context.Background(), accountID)
			if err != nil {
				t.Errorf("Resolve(%d) error = %v", accountID, err)
				return
			}
			if gotCredential.Type != credential.Type || gotCredential.Value != credential.Value {
				t.Errorf("Resolve(%d) credential = %#v, want %#v", accountID, gotCredential, credential)
			}
			if gotAccount != account {
				t.Errorf("Resolve(%d) account = %#v, want %#v", accountID, gotAccount, account)
			}
		}()
	}
	wg.Wait()

	if got := v.Size(); got != goroutines {
		t.Fatalf("Size() = %d, want %d", got, goroutines)
	}
}
