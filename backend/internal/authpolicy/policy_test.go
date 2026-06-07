package authpolicy

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestPasswordPolicyDefaultsAllow(t *testing.T) {
	svc := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	policy := New(svc)

	registerAllowed, err := policy.PasswordRegistrationAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed default: %v", err)
	}
	if !registerAllowed {
		t.Fatal("password registration default=false want true")
	}
	loginAllowed, err := policy.PasswordLoginAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed default: %v", err)
	}
	if !loginAllowed {
		t.Fatal("password login default=false want true")
	}
}

func TestPasswordPolicyReadsPlatformSettings(t *testing.T) {
	store := platformsettings.NewMemoryStore()
	svc := platformsettings.NewService(store, nil)
	if _, err := svc.Upsert(context.Background(), platformsettings.UpsertInput{Key: platformsettings.KeyPasswordRegisterEnabled, Value: "false", UpdatedBy: "test"}); err != nil {
		t.Fatalf("seed password_register_enabled: %v", err)
	}
	if _, err := svc.Upsert(context.Background(), platformsettings.UpsertInput{Key: platformsettings.KeyPasswordLoginEnabled, Value: "false", UpdatedBy: "test"}); err != nil {
		t.Fatalf("seed password_login_enabled: %v", err)
	}
	policy := New(svc)

	registerAllowed, err := policy.PasswordRegistrationAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordRegistrationAllowed: %v", err)
	}
	if registerAllowed {
		t.Fatal("password registration allowed=true want false")
	}
	loginAllowed, err := policy.PasswordLoginAllowed(context.Background(), 7)
	if err != nil {
		t.Fatalf("PasswordLoginAllowed: %v", err)
	}
	if loginAllowed {
		t.Fatal("password login allowed=true want false")
	}
}
