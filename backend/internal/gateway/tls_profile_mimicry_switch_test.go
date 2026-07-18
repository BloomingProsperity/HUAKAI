package gateway

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
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
	profile := validGatewayInlineProfile()
	orig := mimicry.NewSidecarRoundTripper(mimicry.NewSidecarClient("/run/huakai/tls-sidecar.sock"), mimicry.SidecarProfileAnthropicCLIMimicryV1)
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{profile: profile}}

	// 默认开：绑定 profile 的 mimicry 账号应派生 inline sidecar transport。
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	got, err := d.applyTLSProfile(ctx, orig, transport.TransportModeMimicryClaudeCode, 7)
	if err != nil || got == orig {
		t.Fatalf("默认开：应绑定动态 profile，got=%#v err=%v", got, err)
	}

	// 全局关闭：同样的输入必须保持 builtin transport。
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "false")
	got, err = d.applyTLSProfile(ctx, orig, transport.TransportModeMimicryClaudeCode, 7)
	if err != nil || got != orig {
		t.Fatalf("全局关闭伪装：必须保持 builtin，got=%#v err=%v", got, err)
	}
}
