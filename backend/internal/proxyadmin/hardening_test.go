package proxyadmin

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestProxyTypeIsSecretFree 是一项结构层面的纵深防御断言(审查 F1):读取面
// 的"不含凭据"保证最终落在 Proxy 类型永远不携带凭据字段上。一旦有人给 Proxy
// 加上 AuthSecret(或任何含 "*secret*"/"*password*"/"*credential*" 的字段),
// 加密后的代理凭据离泄露进响应就只差一次粗心的 mapper。该测试在此类字段一出现
// 时就转红,赶在任何泄露上线之前。
// 变异:给 Proxy 加 `AuthSecret *string` → 转红。
func TestProxyTypeIsSecretFree(t *testing.T) {
	rt := reflect.TypeOf(Proxy{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, banned := range []string{"secret", "password", "credential", "passwd", "token"} {
			if strings.Contains(name, banned) {
				t.Fatalf("Proxy must stay secret-free; field %q looks credential-bearing (contains %q)", rt.Field(i).Name, banned)
			}
		}
	}
}

// TestValidatePortUpperBound 守护审查 F3:代理端口必须落在 1..65535。
// 修复前只拒绝 port<=0,故 70000(或任何 >65535)会被放行,日后在连接时才失败,
// 或与 OpenAPI 的 maximum:65535 产生漂移。
// 变异:删掉 validateCommon 里的 `|| port > 65535` 子句 → 下面的 70000
// 用例被放行 → 转红。
func TestValidatePortUpperBound(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	base := CreateInput{TenantID: 7, Name: "p", Protocol: "http", Host: "h"}

	// 超过 16 位上限:在校验阶段即被拒绝,先于任何 DB 调用。
	in := base
	in.Port = 70000
	if _, err := New(&mockProxyQuerier{}, keys).Create(ctx, in); err != ErrInvalidInput {
		t.Fatalf("port 70000 must be ErrInvalidInput, got %v", err)
	}
	upd := UpdateInput{TenantID: 7, ID: 3, Name: "p", Protocol: "http", Host: "h", Port: 70000}
	if _, err := New(&mockProxyQuerier{}, keys).Update(ctx, upd); err != ErrInvalidInput {
		t.Fatalf("update port 70000 must be ErrInvalidInput, got %v", err)
	}

	// 恰好的上界 65535 仍然有效(区分性:证明守卫是 `> 65535`,
	// 而非差一错误 `>= 65535`)。
	ok := base
	ok.Port = 65535
	if _, err := New(&mockProxyQuerier{}, keys).Create(ctx, ok); err != nil {
		t.Fatalf("port 65535 must be accepted, got %v", err)
	}
}
