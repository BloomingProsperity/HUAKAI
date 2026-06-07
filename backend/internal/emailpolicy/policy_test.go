package emailpolicy

import (
	"errors"
	"testing"
)

func TestEmailDomainAllowlist(t *testing.T) {
	if err := CheckDomain("user@evil.com", true, "example.com"); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Fatalf("evil.com err=%v want ErrEmailDomainNotAllowed; MUTATION: reversing membership so out-of-set domains pass turns this red", err)
	}
	if err := CheckDomain("user@example.com", true, "example.com"); err != nil {
		t.Fatalf("example.com should be allowed: %v; MUTATION: rejecting in-set domains turns this red", err)
	}
	if err := CheckDomain("user@evil.com", false, "example.com"); err != nil {
		t.Fatalf("disabled allowlist err=%v want nil", err)
	}
}

func TestEmailAliasRestriction(t *testing.T) {
	cases := []struct {
		email   string
		wantErr error
	}{
		{email: "a+tag@example.com", wantErr: ErrEmailAliasNotAllowed},
		{email: "a.b@example.com", wantErr: ErrEmailAliasNotAllowed},
		{email: "a@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			err := CheckAlias(tc.email, true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckAlias err=%v want %v; MUTATION: only rejecting '+' but not '.' lets a.b@example.com pass", err, tc.wantErr)
			}
		})
	}
	if err := CheckAlias("a+tag@example.com", false); err != nil {
		t.Fatalf("disabled alias restriction err=%v want nil", err)
	}
}

func TestReservedEmail(t *testing.T) {
	cases := []struct {
		email   string
		wantErr error
	}{
		{email: "admin@example.com", wantErr: ErrEmailReserved},
		{email: "Admin@example.com", wantErr: ErrEmailReserved},
		{email: "user@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			err := CheckReserved(tc.email, "admin")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckReserved err=%v want %v; MUTATION: case-sensitive reserved comparison lets Admin@example.com pass", err, tc.wantErr)
			}
		})
	}
	if err := CheckReserved("admin@example.com", ""); err != nil {
		t.Fatalf("empty reserved list err=%v want nil", err)
	}
}
