package admin

import (
	"context"
	"testing"
)

// 守身份 context 往返: 注入后可原样读回; 未注入时 ok=false(调用方据此不回退去信任请求体)。
// mutation: IdentityFromContext 用错 key / IdentityToContext 不存值 → 读回 ok=false 或字段不符 → 红。
func TestIdentityContextRoundTrip(t *testing.T) {
	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Fatal("空 context 不应有身份, 但 ok=true")
	}
	want := AdminIdentity{TokenID: 4242, Role: RolePlatformAdmin, ScopeTenantID: 7}
	ctx := IdentityToContext(context.Background(), want)
	got, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("注入后应能读回身份, 但 ok=false")
	}
	if got.TokenID != 4242 || got.Role != RolePlatformAdmin || got.ScopeTenantID != 7 {
		t.Fatalf("读回身份=%+v, want TokenID=4242 / platform_admin / scope=7", got)
	}
}
