// 包 provider — PostgresProxyResolver 私有 parseProxyURLValue 单测。
// 不需要真 DB，集成测试见 postgres_proxy_resolver_test.go (integration_pg 标签)。
package provider

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
)

func strPtr(s string) *string { return &s }

func testProxySecretKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("proxy-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}

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

func TestPostgresProxyResolverDecryptsStoredAuthSecretBeforeURLBuild(t *testing.T) {
	ctx := context.Background()
	keys := testProxySecretKeys(t)
	plaintext := "proxy-secret:with@reserved?chars"
	stored, err := proxysecret.Encode(ctx, keys, 77, plaintext)
	if err != nil {
		t.Fatalf("Encode proxy secret: %v", err)
	}
	if stored == plaintext {
		t.Fatal("test setup stored plaintext; need encrypted fixture")
	}

	username := "proxy-user"
	row := proxyRow{
		tenantID: 77,
		protocol: "http",
		host:     "proxy.example.com",
		port:     3128,
		username: &username,
		secret:   &stored,
	}
	resolver := &PostgresProxyResolver{keys: keys}
	if err := resolver.decryptRowAuthSecret(ctx, &row); err != nil {
		t.Fatalf("decryptRowAuthSecret: %v", err)
	}
	if row.secret == nil || *row.secret != plaintext {
		t.Fatalf("decrypted row secret=%v want original plaintext", row.secret)
	}
	u, err := buildProxyURL(row, 42)
	if err != nil {
		t.Fatalf("buildProxyURL: %v", err)
	}
	got, ok := u.User.Password()
	if !ok || got != plaintext {
		t.Fatalf("proxy URL password=%q ok=%v want decrypted original", got, ok)
	}
	if strings.Contains(u.String(), stored) {
		t.Fatal("proxy URL contains encrypted envelope instead of decrypted credential")
	}
}

func TestMigration0038RejectsMalformedProxyURLBeforeDroppingLegacyColumn(t *testing.T) {
	raw, err := os.ReadFile("../../sql/migrations/0038_proxies_pool_and_account_links.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !migration0038HasMalformedProxyURLGuard(sql) {
		t.Fatal("0038 must reject malformed non-empty provider_accounts.proxy_url rows before dropping proxy_url")
	}

	guardStart := strings.Index(sql, "S1-020 guard:")
	dropStart := strings.Index(sql, "ALTER TABLE provider_accounts DROP COLUMN proxy_url;")
	if guardStart < 0 || dropStart < 0 || guardStart >= dropStart {
		t.Fatalf("guardStart=%d dropStart=%d; malformed proxy_url guard must appear before DROP COLUMN", guardStart, dropStart)
	}
	mutated := sql[:guardStart] + sql[dropStart:]
	if migration0038HasMalformedProxyURLGuard(mutated) {
		t.Fatal("mutation check failed: removing the guard must make this test red")
	}

	forwardRaw, err := os.ReadFile("../../sql/migrations/0069_s1_020_proxy_backfill_validation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !migration0066HasForwardProxyBackfillValidation(string(forwardRaw)) {
		t.Fatal("0066 must validate already-applied imported proxy rows and document the pre-0038 backup recovery path")
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

func migration0038HasMalformedProxyURLGuard(sql string) bool {
	dropStart := strings.Index(sql, "ALTER TABLE provider_accounts DROP COLUMN proxy_url;")
	guardStart := strings.Index(sql, "S1-020 guard:")
	if dropStart < 0 || guardStart < 0 || guardStart >= dropStart {
		return false
	}
	guard := sql[guardStart:dropStart]
	if strings.Contains(guard, "^[a-z]+://") {
		return false
	}
	required := []string{
		"RAISE EXCEPTION",
		"malformed non-empty provider_accounts.proxy_url",
		"^(?:http|https|socks5)://",
		"WHERE pa.proxy_url IS NOT NULL AND pa.proxy_url != ''",
		"src.has_supported_shape IS NOT TRUE",
		"src.protocol IS NULL",
		"src.host IS NULL",
		"src.explicit_port IS NOT NULL AND src.explicit_port !~ '^[0-9]{1,5}$'",
		"src.port IS NULL",
		"src.port < 1",
		"src.port > 65535",
		"src.has_supported_shape IS TRUE",
	}
	for _, want := range required {
		if !strings.Contains(guard, want) {
			return false
		}
	}
	return true
}

func migration0066HasForwardProxyBackfillValidation(sql string) bool {
	required := []string{
		"restore from a pre-0038 backup",
		"p.name LIKE 'imported-%'",
		"p.host !~ '^[^:/@?#]+$'",
		"RAISE EXCEPTION",
		"cannot validate S1-020 proxy backfill",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			return false
		}
	}
	return true
}

// contains 是简易子串检查，保留给历史测试路径。
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
