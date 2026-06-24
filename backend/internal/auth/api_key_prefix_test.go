package auth

import "testing"

// TestValidBearerFormatHonorsConfiguredPrefix 守护入站过滤随 HUAKAI_API_KEY_PREFIX
// 走,与 admin/keygen 签发同源。变异证伪:把 validBearerFormat 改回写死 hk_live_/
// hk_test_ → base=sk 时"接受 sk_ / 拒绝 hk_"断言红。
func TestValidBearerFormatHonorsConfiguredPrefix(t *testing.T) {
	t.Setenv("HUAKAI_API_KEY_PREFIX", "sk")
	if !validBearerFormat("sk_live_abcdefgh") || !validBearerFormat("sk_test_abcdefgh") {
		t.Fatal("base=sk 应接受 sk_ 客户前缀")
	}
	if validBearerFormat("hk_live_abcdefgh") {
		t.Fatal("base=sk 不应接受旧 hk_ 默认前缀(签发已换 sk,校验须同源)")
	}
	// 默认(unset)仍接受 hk_live_,默认行为不变。
	t.Setenv("HUAKAI_API_KEY_PREFIX", "")
	if !validBearerFormat("hk_live_abcdefgh") {
		t.Fatal("默认应接受 hk_live_")
	}
}
