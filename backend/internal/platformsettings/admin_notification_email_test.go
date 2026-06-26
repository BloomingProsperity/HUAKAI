package platformsettings

import (
	"errors"
	"testing"
)

// TestAdminNotificationEmailValidation:admin_notification_email 设置接受空值
// (保持每日巡检关闭的安全默认)和单个看似合理的地址,并拒绝可能破坏 SMTP
// 头部的含空白/多地址/格式错误的输入。
// 捕获的回归:若 email 校验器被去掉(退化为普通的公开文本),像 "a@b, c@d"
// 或 "no-at-sign" 这样的值会被接受,之后污染收件人头部。
func TestAdminNotificationEmailValidation(t *testing.T) {
	if v, err := ValidateValue(KeyAdminNotificationEmail, ""); err != nil || v != "" {
		t.Fatalf("empty must be accepted (default off), got %q err=%v", v, err)
	}
	if v, err := ValidateValue(KeyAdminNotificationEmail, "ops@huakai.example"); err != nil || v != "ops@huakai.example" {
		t.Fatalf("valid address rejected: %q err=%v", v, err)
	}
	for _, bad := range []string{
		"no-at-sign",
		"a@b",                 // 主机名不含点
		"a@b.com, c@d.com",    // 多地址列表
		"a@b.com;c@d.com",     // 分号列表
		"with space@b.com",    // 内部含空白
		"trailing@",           // 主机为空
		"@leading.com",        // 本地部分为空
		"two@@at.com",         // 两个 @
		"line@break.com\nX:1", // 头部注入尝试
	} {
		if _, err := ValidateValue(KeyAdminNotificationEmail, bad); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("expected %q to be rejected as ErrInvalidValue, got err=%v", bad, err)
		}
	}
}

// TestAdminNotificationEmailDefaultEmpty:该 key 以空默认值注册,
// 因此未配置的部署解析不出任何收件人。
func TestAdminNotificationEmailDefaultEmpty(t *testing.T) {
	v, ok := DefaultValue(KeyAdminNotificationEmail)
	if !ok {
		t.Fatalf("admin_notification_email must be a registered key")
	}
	if v != "" {
		t.Fatalf("default must be empty, got %q", v)
	}
	if !IsAllowedKey(KeyAdminNotificationEmail) {
		t.Fatalf("key must be allowed")
	}
}
