package email

import (
	"context"
	"strings"
	"testing"
)

// TestAuthLinkBuildsFrontendURL 锁定:配了前端 base URL 就拼出带 tenant_id+token(+email)的完整链接,
// 未配则返回空串(邮件回退裸 token)。
// 变异:authLink 不读 frontendBaseURL 直接返回空 → 首组断言 RED;或不带 token/tenant_id → 对应断言 RED。
func TestAuthLinkBuildsFrontendURL(t *testing.T) {
	ctx := context.Background()
	s := &AuthSender{frontendBaseURL: func(context.Context) string { return "https://huakai.example/" }}

	link := s.authLink(ctx, "/reset-password", 7, "tok-abc", map[string]string{"email": "u@x.test"})
	for _, want := range []string{"https://huakai.example/reset-password?", "tenant_id=7", "token=tok-abc", "email=u%40x.test"} {
		if !strings.Contains(link, want) {
			t.Fatalf("reset 链接缺 %q:%s", want, link)
		}
	}

	// 路径前导斜杠 + base 尾斜杠不应产生双斜杠。
	if strings.Contains(strings.TrimPrefix(link, "https://"), "//") {
		t.Fatalf("链接出现双斜杠:%s", link)
	}

	// 未配 base URL(resolver 返回空)→ 空串(回退裸 token)。
	empty := &AuthSender{frontendBaseURL: func(context.Context) string { return "" }}
	if got := empty.authLink(ctx, "/email-verify", 1, "t", nil); got != "" {
		t.Fatalf("未配 base URL 应返回空串,得 %q", got)
	}
	// resolver 为 nil → 空串。
	if got := (&AuthSender{}).authLink(ctx, "/email-verify", 1, "t", nil); got != "" {
		t.Fatalf("resolver nil 应返回空串,得 %q", got)
	}
	// 空 token → 空串(不发注定 400 的链接)。
	if got := s.authLink(ctx, "/email-verify", 1, "  ", nil); got != "" {
		t.Fatalf("空 token 应返回空串,得 %q", got)
	}
}

// TestBuildAuthActionBody 锁定:有 link 时正文含可点链接(<a href)+ 兜底 token;无 link 时只含裸 token。
// 变异:link 非空却不输出 <a href → 「含链接」断言 RED;link 为空却不输出 token → 「含 token」断言 RED。
func TestBuildAuthActionBody(t *testing.T) {
	withLink := buildVerificationBody("https://huakai.example/email-verify?token=abc", "abc")
	if !strings.Contains(withLink, `<a href="https://huakai.example/email-verify?token=abc"`) {
		t.Fatalf("有 link 时应含可点链接:%s", withLink)
	}
	if !strings.Contains(withLink, "abc") {
		t.Fatalf("有 link 时仍应附兜底 token:%s", withLink)
	}

	noLink := buildPasswordResetBody("", "bare-token-xyz")
	if strings.Contains(noLink, "<a href") {
		t.Fatalf("无 link 时不应出现 <a href:%s", noLink)
	}
	if !strings.Contains(noLink, "bare-token-xyz") {
		t.Fatalf("无 link 时应只投递裸 token:%s", noLink)
	}
}
