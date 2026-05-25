// 包 provider — PostgresProxyResolver 私有 parseProxyURLValue 单测。
// 不需要真 DB，集成测试见 postgres_proxy_resolver_test.go (integration_pg 标签)。
package provider

import (
	"errors"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestParseProxyURLValue_NilIsDirectConnect(t *testing.T) {
	got, err := parseProxyURLValue(nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("nil 列值应表示直连，得到 %v", got)
	}
}

func TestParseProxyURLValue_EmptyStringIsDirectConnect(t *testing.T) {
	got, err := parseProxyURLValue(strPtr(""), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("空串列值应表示直连，得到 %v", got)
	}
}

func TestParseProxyURLValue_HTTPProxy(t *testing.T) {
	got, err := parseProxyURLValue(strPtr("http://proxy.example.com:3128"), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("got=nil")
	}
	if got.Scheme != "http" || got.Host != "proxy.example.com:3128" {
		t.Errorf("got=%+v want http://proxy.example.com:3128", got)
	}
}

func TestParseProxyURLValue_SOCKS5WithCreds(t *testing.T) {
	got, err := parseProxyURLValue(strPtr("socks5://user:pass@proxy.example.com:1080"), 99)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "socks5" {
		t.Errorf("scheme=%q want socks5", got.Scheme)
	}
	if got.User.Username() != "user" {
		t.Errorf("user=%q want user", got.User.Username())
	}
	pw, ok := got.User.Password()
	if !ok || pw != "pass" {
		t.Errorf("password=%q ok=%v want pass+true", pw, ok)
	}
}

func TestParseProxyURLValue_NoSchemeRejected(t *testing.T) {
	// "not_a_url" 不会让 url.Parse 报错，但也没有 scheme → 显式守住
	_, err := parseProxyURLValue(strPtr("not_a_url"), 5)
	if !errors.Is(err, ErrProxyURLInvalid) {
		t.Errorf("无 scheme 应返回 ErrProxyURLInvalid，得到 %v", err)
	}
}

func TestParseProxyURLValue_MalformedRejected(t *testing.T) {
	// 控制字符触发 url.Parse 真实报错
	_, err := parseProxyURLValue(strPtr("http://exa\x7fmple.com"), 5)
	if !errors.Is(err, ErrProxyURLInvalid) {
		t.Errorf("非法 URL 应返回 ErrProxyURLInvalid，得到 %v", err)
	}
}

func TestPostgresProxyResolver_NilReceiverReturnsMisconfigured(t *testing.T) {
	// nil receiver 是 DI 错误，必须 fail-loud 为 ErrProxyResolverMisconfigured
	// （不混淆为 ErrAccountNotFound，避免 dispatcher fail-open 绕过代理）
	var r *PostgresProxyResolver
	_, err := r.Resolve(nil, 1) //nolint:staticcheck // 故意传 nil ctx 验证 nil receiver 短路
	if !errors.Is(err, ErrProxyResolverMisconfigured) {
		t.Errorf("nil receiver 应返回 ErrProxyResolverMisconfigured，得到 %v", err)
	}
	if errors.Is(err, ErrAccountNotFound) {
		t.Error("nil receiver 不应混淆为 ErrAccountNotFound（fail-open 风险）")
	}
}

func TestPostgresProxyResolver_NilPoolReturnsMisconfigured(t *testing.T) {
	// pool 字段为 nil（构造器传 nil） → 仍应短路为 ErrProxyResolverMisconfigured，不 panic
	r := &PostgresProxyResolver{pool: nil}
	_, err := r.Resolve(nil, 1) //nolint:staticcheck
	if !errors.Is(err, ErrProxyResolverMisconfigured) {
		t.Errorf("nil pool 应返回 ErrProxyResolverMisconfigured，得到 %v", err)
	}
}

func TestParseProxyURLValue_SchemeButNoHostRejected(t *testing.T) {
	// "http://" 有 scheme 但 host 为空，必须拒绝（防止后续 net/http
	// 请求出 panic 或意外路由）
	_, err := parseProxyURLValue(strPtr("http://"), 5)
	if !errors.Is(err, ErrProxyURLInvalid) {
		t.Errorf("scheme 有但 host 为空应返回 ErrProxyURLInvalid，得到 %v", err)
	}
}

func TestParseProxyURLValue_NoSecretLeakInError(t *testing.T) {
	// 安全回归：socks5 with password 解析失败时，错误消息不能含 raw URL
	// （包含 user:pass@host 的话会泄漏代理凭据到日志/error chain）
	rawWithSecret := "socks5://leaky_user:s3cret_password\x7f@host"
	_, err := parseProxyURLValue(strPtr(rawWithSecret), 7)
	if err == nil {
		t.Skip("此用例依赖 url.Parse 拒绝控制字符；如平台行为变化则跳过")
	}
	msg := err.Error()
	if contains(msg, "leaky_user") || contains(msg, "s3cret_password") {
		t.Errorf("error 消息泄漏代理凭据：%s", msg)
	}
}

// contains 是简易子串检查（避免引 strings 包，保持 import 列表整洁）
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
