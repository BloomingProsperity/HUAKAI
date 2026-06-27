package transport

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// buildFallbackWrapper 用【真实生产构造路径】(Factory.For)产出 sidecarFallbackRoundTripper:
//   - SidecarSocketPath 非空 + SidecarFallbackEnabled=true → 走 sidecar+fallback 包装分支;
//   - sidecarProbe 注入成功(不需真 Rust sidecar 在线),primary 仍是 mimicry.NewSidecarRoundTripperForMode
//     产出的真实 sidecarTransport(实现 WithProxy + SidecarProfileID);
//   - HUAKAI_TRANSPORT_PHASE_A_FALLBACK=true → 让 native fallback 用 PhaseADefaultTemplate 构造出
//     真实的 Go uTLS roundTripper(实现 WithProxy)。
//
// 关键:全程不手搓 fake——primary/fallback 都是生产构造器产物,精确复刻 marker 挂错命门
// ([[type-marker-on-returned-type-not-helper]]:标记必须挂在真正返回的类型上)。
func buildFallbackWrapper(t *testing.T) http.RoundTripper {
	t.Helper()
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	f := NewFactory()
	f.SidecarSocketPath = "/run/huakai-test-sidecar.sock"
	f.SidecarFallbackEnabled = true
	// probe 注入成功:绕过真实 Rust sidecar 就绪检查,但 primary 仍走真实构造器。
	f.sidecarProbe = func(context.Context, string, mimicry.TransportMode, string) error { return nil }
	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("Factory.For 应返回 fallback wrapper,却报错: %v", err)
	}
	if _, ok := rt.(*sidecarFallbackRoundTripper); !ok {
		t.Fatalf("Factory.For 未返回 sidecarFallbackRoundTripper,实际 %T(测试前提不成立)", rt)
	}
	return rt
}

// TestSidecarFallbackWrapper_ExposesSidecarProfileID 守护 applyTLSProfile 短路命门:
// wrapper 必须满足 interface{ SidecarProfileID() string } 且转发 primary 的真实 profile id。
// 否则绑定 DB TLS profile 的账号在 dispatcher 处会被 per-account uTLS profile 整体替换、
// sidecar 静默丢失。变异:删除 SidecarProfileID 方法 → ok==false → 本测试 red。
func TestSidecarFallbackWrapper_ExposesSidecarProfileID(t *testing.T) {
	rt := buildFallbackWrapper(t)
	sp, ok := rt.(interface{ SidecarProfileID() string })
	if !ok {
		t.Fatal("wrapper 未暴露 SidecarProfileID():applyTLSProfile 会误把绑 DB profile 的账号换掉 sidecar")
	}
	// 必须是 primary 的真实 profile id,而非空串——空串说明转发逻辑被掏空(no-op 退化)。
	if got := sp.SidecarProfileID(); got != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
		t.Fatalf("SidecarProfileID() 应转发 primary 真实 profile id %q,实际 %q", mimicry.SidecarProfileAnthropicCLIMimicryV1, got)
	}
}

// TestSidecarFallbackWrapper_WithProxyPreservesSidecar 守护 WithProxy 转发的直接行为:
// wrapper.WithProxy 必须返回【仍是 sidecar 包装】的新 RT(两条腿都叠加了代理),
// 而非丢掉 sidecar 标记。变异:删除 WithProxy 方法 → 编译期 wrapper 不再满足
// proxyAwareRoundTripper,下方类型断言 ok==false → red。
func TestSidecarFallbackWrapper_WithProxyPreservesSidecar(t *testing.T) {
	rt := buildFallbackWrapper(t)
	pa, ok := rt.(proxyAwareRoundTripper)
	if !ok {
		t.Fatal("wrapper 未满足 proxyAwareRoundTripper:WrapTransportWithProxy 会落到 fail-loud,代理 mimicry 账号整体不可用")
	}
	proxyURL, _ := url.Parse("http://127.0.0.1:8080")
	proxied, err := pa.WithProxy(proxyURL)
	if err != nil {
		t.Fatalf("wrapper.WithProxy 应在两条腿上叠加代理并成功,却报错: %v", err)
	}
	// 叠加代理后必须仍携带 sidecar 标记(profile id 透传),否则 applyProxy 之后的 RT 又丢了 sidecar。
	sp, ok := proxied.(interface{ SidecarProfileID() string })
	if !ok || sp.SidecarProfileID() != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
		t.Fatalf("WithProxy 返回的 RT 应仍是带 sidecar 标记的 fallback wrapper,实际 %T", proxied)
	}
}

// TestSidecarFallbackWrapper_RealConsumerTakesProxyAwareBranch 通过【真实消费者】
// provider.WrapTransportWithProxy 端到端验证:开 fallback + 绑代理时,wrapper 走 proxy-aware
// 分支(返回仍带 SidecarProfileID 的 wrapper),而非 fail-loud 的 proxyWrappedRoundTripper
// (后者不带 SidecarProfileID)。这正是 #11 命门:不实现 WithProxy 时,每个代理请求都会
// 返回 ErrProxyUnsupportedTransport。变异:删除 wrapper.WithProxy → WrapTransportWithProxy
// 落到 fail-loud 分支 → 返回物不带 SidecarProfileID → ok==false → red。
func TestSidecarFallbackWrapper_RealConsumerTakesProxyAwareBranch(t *testing.T) {
	rt := buildFallbackWrapper(t)
	proxyURL, _ := url.Parse("http://127.0.0.1:8080")
	wrapped := provider.WrapTransportWithProxy(rt, proxyURL)
	if _, ok := wrapped.(interface{ SidecarProfileID() string }); !ok {
		t.Fatalf("WrapTransportWithProxy 落到了 fail-loud 分支(返回 %T 不带 SidecarProfileID):"+
			"wrapper 未被识别为 proxy-aware,代理 mimicry 账号会整体返回 ErrProxyUnsupportedTransport", wrapped)
	}
}
