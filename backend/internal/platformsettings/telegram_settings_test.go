package platformsettings

import (
	"errors"
	"testing"
)

// TestTelegramBotUsernameValidate 校验公开 Telegram bot 用户名的硬化规则。
// 每个用例都对应一处真实缺陷,变异其守卫即应变红:
//   - 空值放行(空=关闭 telegram 登录):删 value=="" 短路 → 空值落到长度判定被拒,首断言红。
//   - 前导 "@" 剥除:删 TrimPrefix → "@HuakaiBot" 因 "@" 非法字符被拒,断言红。
//   - 长度 5–32:删长度判定 → "ab"(2 字符)放行,断言红。
//   - 字符集 [A-Za-z0-9_]:删字符循环 → "Bad-Bot!" 放行(含 "-"/"!" 可破坏 HTML 属性),断言红。
//   - 必须 bot 结尾:删后缀判定 → "HuakaiUser" 放行,断言红。
func TestTelegramBotUsernameValidate(t *testing.T) {
	// 空值 = 关闭,合法且原样返回空串。
	if v, err := ValidateValue(KeyTelegramBotUsername, ""); err != nil || v != "" {
		t.Fatalf("空值应放行返回空串,得 v=%q err=%v", v, err)
	}
	// 合法用户名原样返回。
	if v, err := ValidateValue(KeyTelegramBotUsername, "HuakaiLoginBot"); err != nil || v != "HuakaiLoginBot" {
		t.Fatalf("合法用户名应放行,得 v=%q err=%v", v, err)
	}
	// 前导 "@" 应被剥除后返回裸名(运营便利)。
	if v, err := ValidateValue(KeyTelegramBotUsername, "@HuakaiLoginBot"); err != nil || v != "HuakaiLoginBot" {
		t.Fatalf("前导 @ 应剥除,得 v=%q err=%v", v, err)
	}

	// 各类非法输入必须以 ErrInvalidValue 拒绝。
	bad := map[string]string{
		"过短(<5)":      "abot",
		"过长(>32)":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaBot", // 38 字符
		"含连字符":        "Bad-Bot",
		"含 HTML 危险字符": `e"><script>Bot`,
		"缺 bot 后缀":    "HuakaiLoginUser",
		"含空格":         "Huakai Bot",
	}
	for name, in := range bad {
		if _, err := ValidateValue(KeyTelegramBotUsername, in); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("%s(%q)应被 ErrInvalidValue 拒绝,得 err=%v", name, in, err)
		}
	}

	// 判别性自证:合法名通过、危险名被拒——证明校验真的在区分,而非恒真/恒假。
	_, okErr := ValidateValue(KeyTelegramBotUsername, "GoodBot")
	_, badErr := ValidateValue(KeyTelegramBotUsername, `x"onerror=Bot`)
	if okErr != nil || badErr == nil {
		t.Fatalf("判别失败:合法名 err=%v(应 nil)危险名 err=%v(应非 nil)", okErr, badErr)
	}
}

// TestTelegramBotUsernameRegisteredAsPublicKey 锁定该 key 已注册为公开可写 key
// 且默认空(关闭)。变异:从 defaultSettingValueMap/orderedSettingKeys 漏注册 →
// IsAllowedKey 假 / 默认值缺失,断言红。这是 admin 能写、sitepublic 能读的前提。
func TestTelegramBotUsernameRegisteredAsPublicKey(t *testing.T) {
	if !IsAllowedKey(KeyTelegramBotUsername) {
		t.Fatal("telegram_bot_username 必须是已注册的可写 key")
	}
	if v, ok := DefaultValue(KeyTelegramBotUsername); !ok || v != "" {
		t.Fatalf("默认值应为空(关闭),得 v=%q ok=%v", v, ok)
	}
	found := false
	for _, k := range AllKeys() {
		if k == KeyTelegramBotUsername {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("telegram_bot_username 必须出现在 orderedSettingKeys 中(List/导出会用到)")
	}
}
