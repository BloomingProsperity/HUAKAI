package admin

import (
	"strings"
	"testing"
)

func TestGenerateBearer_AllEnvironments(t *testing.T) {
	cases := []struct {
		env       Environment
		wantNS    string
	}{
		{EnvLive, "hk_live_"},
		{EnvTest, "hk_test_"},
		{EnvAdmin, "hk_admin_"},
	}
	for _, tc := range cases {
		t.Run(string(tc.env), func(t *testing.T) {
			bearer, prefix, err := GenerateBearer(tc.env)
			if err != nil {
				t.Fatalf("GenerateBearer(%q): %v", tc.env, err)
			}
			if !strings.HasPrefix(bearer, tc.wantNS) {
				t.Errorf("bearer = %q; want prefix %q", bearer, tc.wantNS)
			}
			if len(prefix) != PrefixLen {
				t.Errorf("prefix len = %d; want %d", len(prefix), PrefixLen)
			}
			if !strings.HasPrefix(bearer, prefix) {
				t.Errorf("prefix %q is not a prefix of bearer %q", prefix, bearer)
			}
			// 24 base32 chars after the namespace = 32-char total.
			if got := len(bearer) - len(tc.wantNS); got != 24 {
				t.Errorf("suffix len = %d; want 24 (120 bits entropy)", got)
			}
		})
	}
}

func TestGenerateBearer_RejectsInvalidEnvironment(t *testing.T) {
	if _, _, err := GenerateBearer(Environment("staging")); err == nil {
		t.Fatal("expected error for unknown environment; got nil")
	}
}

// TestGenerateBearer_Uniqueness — N runs produce N distinct bearers.
// Probability of collision at 120 bits over a few thousand draws is
// negligible; the test guards against a stub rand.Reader regression.
func TestGenerateBearer_Uniqueness(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		b, _, err := GenerateBearer(EnvLive)
		if err != nil {
			t.Fatalf("GenerateBearer: %v", err)
		}
		if _, dup := seen[b]; dup {
			t.Fatalf("duplicate bearer at iteration %d: %q", i, b)
		}
		seen[b] = struct{}{}
	}
}
