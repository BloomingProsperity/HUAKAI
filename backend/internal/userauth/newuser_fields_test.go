package userauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRegister_RejectsMalformedEmailAndOutOfBoundsPassword 是 UC-08 的判别测试:公开注册
// 现在与其它建号入口共用同一字段校验。变异刀:删掉 Register 里的 ValidateNewUserEmail /
// ValidateNewUserPassword 调用(退回原「只查非空」),下面各 case 会拿到 nil 而转红。
func TestRegister_RejectsMalformedEmailAndOutOfBoundsPassword(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	newSvc := func() *Service {
		store := newMemoryAuthStore(now)
		svc := NewService(store)
		svc.RequireVerified = false
		svc.PasswordPolicy = cheapPasswordPolicy()
		svc.Now = func() time.Time { return now }
		return svc
	}

	cases := []struct {
		name     string
		email    string
		password string
		display  string
	}{
		{"无@的邮箱", "notanemail", "goodpass1", ""},
		{"显示名形态邮箱", "Name <a@b.test>", "goodpass1", ""},
		{"口令过短", "ok@example.test", "short", ""},
		{"口令过长", "ok2@example.test", strings.Repeat("a", MaxPasswordLength+1), ""},
		{"显示名含控制字符", "ok3@example.test", "goodpass1", "bad\x01name"},
		{"显示名过长", "ok4@example.test", "goodpass1", strings.Repeat("名", MaxDisplayNameLength+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc()
			if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: tc.email, Password: tc.password, DisplayName: tc.display}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Register(%q,%q) err=%v want ErrInvalidInput", tc.email, tc.password, err)
			}
		})
	}

	// 反向:合法邮箱 + 合法口令应放行(证明上面拒绝的是字段非法,而非注册整体坏了)。
	svc := newSvc()
	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "good@example.test", Password: "goodpass1"}); err != nil {
		t.Fatalf("合法注册应成功: %v", err)
	}
}

// TestValidateNewUserFields_SharedPrimitives 直接锁定共享原语的边界,四入口都依赖它。
func TestValidateNewUserFields_SharedPrimitives(t *testing.T) {
	if _, err := ValidateNewUserEmail("a@b.test"); err != nil {
		t.Fatalf("合法邮箱被拒: %v", err)
	}
	if got, _ := ValidateNewUserEmail("  USER@Example.Test "); got != "user@example.test" {
		t.Fatalf("邮箱规范化=%q want user@example.test", got)
	}
	if err := ValidateNewUserPassword(strings.Repeat("x", MinPasswordLength)); err != nil {
		t.Fatalf("下限口令应通过: %v", err)
	}
	if err := ValidateNewUserPassword(strings.Repeat("x", MinPasswordLength-1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("低于下限应拒: %v", err)
	}
	// 控制字符名必须拒(此前管理端仅 Trim,可让控制字符进库污染 UI/日志)。
	if _, err := ValidateOptionalDisplayName("ab\x01cd"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("控制字符名应拒: %v", err)
	}
	if got, err := ValidateOptionalDisplayName("  "); err != nil || got != "" {
		t.Fatalf("空白名应规范为空且不报错; got=%q err=%v", got, err)
	}
}
