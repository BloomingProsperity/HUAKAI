package admin

import (
	"strings"
	"testing"
)

// TestGenerateBearerHonorsConfiguredPrefix 守护:客户 live/test 签发前缀随
// HUAKAI_API_KEY_PREFIX 走,admin 前缀恒 hk_admin_(不可配),默认仍 hk_。
// 变异证伪:把 namespace() 的 live/test 改回写死 hk_live_/hk_test_ → sk 用例红。
func TestGenerateBearerHonorsConfiguredPrefix(t *testing.T) {
	t.Setenv("HUAKAI_API_KEY_PREFIX", "sk")
	for _, tc := range []struct {
		env  Environment
		want string
	}{
		{EnvLive, "sk_live_"},
		{EnvTest, "sk_test_"},
		{EnvAdmin, "hk_admin_"}, // admin 不随 base 走
	} {
		bearer, prefix, err := GenerateBearer(tc.env)
		if err != nil {
			t.Fatalf("GenerateBearer(%v): %v", tc.env, err)
		}
		if !strings.HasPrefix(bearer, tc.want) {
			t.Errorf("GenerateBearer(%v)=%q want 前缀 %q", tc.env, bearer, tc.want)
		}
		// prefix(入库 key_prefix)同样应带配置前缀。
		if !strings.HasPrefix(prefix, tc.want) {
			t.Errorf("GenerateBearer(%v) prefix=%q want 前缀 %q", tc.env, prefix, tc.want)
		}
	}
	// 默认(unset)live 前缀仍 hk_live_,默认行为不变。
	t.Setenv("HUAKAI_API_KEY_PREFIX", "")
	if b, _, _ := GenerateBearer(EnvLive); !strings.HasPrefix(b, "hk_live_") {
		t.Errorf("默认 live 前缀应仍 hk_live_,got %q", b)
	}
}
