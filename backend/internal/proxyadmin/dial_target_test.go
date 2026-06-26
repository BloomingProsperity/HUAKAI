package proxyadmin

import (
	"context"
	"errors"
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5"
)

func TestDialTargetDecryptsCredentialsAndEscapes(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	// 含 URL 保留字符的口令,验证 url.UserPassword 转义而非破坏 URL。
	enc, err := proxysecret.Encode(ctx, keys, 7, "p@ss/w#rd")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	q := &mockProxyQuerier{getRow: admindb.GetProxyRow{
		ID: 5, TenantID: 7, Protocol: "http", Host: "203.0.113.10", Port: 8080,
		AuthUsername: strPtr("user"), AuthSecret: &enc, Status: "active",
	}}
	u, err := New(q, keys).DialTarget(ctx, 7, 5)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	if u.Scheme != "http" || u.Host != "203.0.113.10:8080" {
		t.Fatalf("URL 主机/scheme 错: %s", u.Redacted())
	}
	pw, _ := u.User.Password()
	if u.User.Username() != "user" || pw != "p@ss/w#rd" {
		t.Fatalf("凭据解密/转义错: user=%q pw=%q", u.User.Username(), pw)
	}
	// 查询必须按 (tenant_id, id) 收敛(跨租户隔离的第一道)。
	if q.getArg.TenantID != 7 || q.getArg.ID != 5 {
		t.Fatalf("GetProxy 未按 tenant/id 收敛: %+v", q.getArg)
	}
}

// SSRF 守卫①:代理 host 指向云 metadata/内网 → ErrUnsafeHost,绝不构造拨号 URL。
// 变异:删 DialTarget 里的 proxyHostSafe 复校 → metadata host 被放行返回 URL → 本测试转红。
func TestDialTargetRejectsUnsafeProxyHost(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"169.254.169.254", "127.0.0.1", "0.0.0.0"} {
		q := &mockProxyQuerier{getRow: admindb.GetProxyRow{
			ID: 5, TenantID: 7, Protocol: "http", Host: host, Port: 80, Status: "active",
		}}
		_, err := New(q, testKeys(t)).DialTarget(ctx, 7, 5)
		if !errors.Is(err, ErrUnsafeHost) {
			t.Fatalf("host=%q 必须 ErrUnsafeHost,实得 %v", host, err)
		}
	}
}

func TestDialTargetNotFoundIsTenantScoped(t *testing.T) {
	q := &mockProxyQuerier{getErr: pgx.ErrNoRows}
	_, err := New(q, testKeys(t)).DialTarget(context.Background(), 7, 5)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨租户/不存在应 ErrNotFound,实得 %v", err)
	}
}

func TestDialTargetNoCredentialsOmitsUserinfo(t *testing.T) {
	q := &mockProxyQuerier{getRow: admindb.GetProxyRow{
		ID: 5, TenantID: 7, Protocol: "socks5", Host: "203.0.113.10", Port: 1080, Status: "active",
	}}
	u, err := New(q, testKeys(t)).DialTarget(context.Background(), 7, 5)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	if u.User != nil {
		t.Fatalf("无 auth_username 时不应带 userinfo,实得 %s", u.Redacted())
	}
}
