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
			// namespace 之后 24 个 base32 字符 = 共 32 个字符。
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

// TestGenerateBearer_Uniqueness —— N 次运行产生 N 个互不相同的 bearer。
// 在 120 bit、几千次抽取下发生碰撞的概率可忽略不计;该测试用于防止
// rand.Reader 被打桩(stub)的回归。
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
