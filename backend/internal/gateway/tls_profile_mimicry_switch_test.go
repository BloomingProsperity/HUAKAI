package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// TestApplyTLSProfile_MimicryGloballyDisabled 锁定落点 B:全局伪装关闭
// (HUAKAI_TRANSPORT_MIMICRY=false)时,即便账号绑定了 DB TLS profile、accountID
// 有效、mode 是 mimicry,applyTLSProfile 也必须保持 builtin rt 不套 profile RT。
// 否则 factory.For 已把 mode 降级标准 transport 后,这里又把 uTLS profile 换回来,
// 留下 DB profile 旁路死角。
//
// 自证式:先在默认开下断言 profile RT 确实会被换上(证明 fixture 有效、resolver
// 真被调用),再在关闭下断言保持 builtin——两条对照,排除"恒返回 origRT"的假绿。
// 变异验证:删 gate 里的 !transport.MimicryEnabled(),关闭分支会返回 profile marker
// 而非 origRT,转红。
func TestApplyTLSProfile_MimicryGloballyDisabled(t *testing.T) {
	ctx := context.Background()
	marker := &tlsProfileMarkerRT{name: "profile"}
	orig := &tlsProfileMarkerRT{name: "builtin"}
	var origRT http.RoundTripper = orig
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{rt: marker}}

	// 默认开(env 未设):绑定 profile 的 mimicry 账号应换上 profile RT。
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != http.RoundTripper(marker) {
		t.Fatalf("默认开:应换上 DB profile RT,got %#v", got)
	}

	// 全局关闭:同样的输入必须保持 builtin rt,不套 profile。
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "false")
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != origRT {
		t.Fatalf("全局关闭伪装:必须保持 builtin rt 不套 DB profile,got %#v", got)
	}
}
