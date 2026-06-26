package mimicry

import (
	"net/url"
	"strings"
	"testing"
)

// PROXYHDR-01:proxy URL 脱敏绝不能让 basic-auth 密码出现在日志行里。
func TestRedactProxyURL(t *testing.T) {
	cases := map[string]string{
		"socks5://u:secret@h:1080":   "socks5://redacted@h:1080",
		"http://h:8080":              "http://h:8080",
		"https://user@h:443":         "https://redacted@h:443",
		"socks5h://a:b@1.2.3.4:1080": "socks5h://redacted@1.2.3.4:1080",
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		got := RedactProxyURL(u)
		// 变异守卫:输出原始 userinfo(密码)而非
		// 'redacted' 会泄露密钥 -> 变红。
		if got != want {
			t.Fatalf("RedactProxyURL(%q)=%q want %q", raw, got, want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, ":b@") {
			t.Fatalf("RedactProxyURL leaked a password: %q", got)
		}
	}
	if RedactProxyURL(nil) != "" {
		t.Fatal("nil url -> empty string")
	}
}
