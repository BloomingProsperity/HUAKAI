package platformsettings

import (
	"errors"
	"testing"
)

// TestSiteBrandingKeysRegisteredWithEmptyDefault:这四个扩展品牌化 key 都在
// 白名单中且默认为空,因此未配置的部署不会显示任何 subtitle/contact/doc/api-base
// 字符串。
// 捕获的回归:若某个 key 漏写进 orderedSettingKeys / defaultSettingValueMap,
// 这里 DefaultValue/IsAllowedKey 会返回 false。
func TestSiteBrandingKeysRegisteredWithEmptyDefault(t *testing.T) {
	for _, key := range []SettingKey{KeySiteSubtitle, KeySiteContactInfo, KeySiteDocURL, KeySiteAPIBaseURL} {
		v, ok := DefaultValue(key)
		if !ok {
			t.Fatalf("%s must be a registered key", key)
		}
		if v != "" {
			t.Fatalf("%s default must be empty, got %q", key, v)
		}
		if !IsAllowedKey(key) {
			t.Fatalf("%s must be allowed", key)
		}
	}
}

// TestSiteBrandingTextValidation:subtitle/contact 是可选的公开文本。
// 空值必须被接受(证明可选路由生效),纯文本能通过,但控制字符会被拒绝
// (证明文本校验器确实在跑)。
// 捕获的回归:把 KeySiteSubtitle/KeySiteContactInfo 从可选公开文本分支里去掉,
// Validate(key,"") 会撞上 value=="" 守卫而被拒绝 -> 空值接受断言变红;若把它们
// 路由到空操作,控制字符用例就会被接受 -> 那条断言变红。
func TestSiteBrandingTextValidation(t *testing.T) {
	for _, key := range []SettingKey{KeySiteSubtitle, KeySiteContactInfo} {
		if v, err := ValidateValue(key, ""); err != nil || v != "" {
			t.Fatalf("%s empty must be accepted, got %q err=%v", key, v, err)
		}
		if v, err := ValidateValue(key, "Fast, clean gateway"); err != nil || v != "Fast, clean gateway" {
			t.Fatalf("%s plain text rejected: %q err=%v", key, v, err)
		}
		if _, err := ValidateValue(key, "line\nbreak"); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("%s must reject control characters, got err=%v", key, err)
		}
	}
}

// TestSiteBrandingURLValidation:doc/api-base 是可选的 http(s) URL。空值接受、
// http(s) 接受,但非 URL / 非 http scheme 会被拒绝。
// 捕获的回归:把它们路由到可选文本而非 URL 校验器,"not-a-url" /
// "javascript:alert(1)" 就会被接受 -> 这里变红。判别性 fixture:裸 token 与
// 非 http scheme 都能通过文本校验器,但必须在 URL 校验器处失败。
func TestSiteBrandingURLValidation(t *testing.T) {
	for _, key := range []SettingKey{KeySiteDocURL, KeySiteAPIBaseURL} {
		if v, err := ValidateValue(key, ""); err != nil || v != "" {
			t.Fatalf("%s empty must be accepted, got %q err=%v", key, v, err)
		}
		if v, err := ValidateValue(key, "https://docs.huakai.example/v1"); err != nil || v != "https://docs.huakai.example/v1" {
			t.Fatalf("%s https URL rejected: %q err=%v", key, v, err)
		}
		for _, bad := range []string{
			"not-a-url",               // 无 scheme/host
			"javascript:alert(1)",     // 非 http scheme(若被展示则成 XSS 向量)
			"ftp://files.example.com", // 非 http scheme
			"//host/path",             // scheme 相对、无 scheme
		} {
			if _, err := ValidateValue(key, bad); !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("%s must reject %q as ErrInvalidValue, got err=%v", key, bad, err)
			}
		}
	}
}
