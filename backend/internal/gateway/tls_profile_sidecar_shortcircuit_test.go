package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// TestApplyTLSProfile_SidecarRTShortCircuitsDBProfile 锁定 ②-2b 收编 DB profile 旁路:
// 当传入的 rt 已经是走 Rust tls-sidecar 的 RT 时,applyTLSProfile 必须【短路】原样返回它,
// 绝不能用 per-account DB uTLS profile 整体替换——否则绑定 DB profile 的账号会让 sidecar
// 永远轮不到用、退回 Go uTLS 占位指纹(命门旁路)。
//
// 【关键】sidecar RT 用【生产构造器 mimicry.NewSidecarRoundTripper】真实构造,而非手搓
// 一个实现 SidecarProfileID() 的假 RT——后者会绕过真实接线、即便标记挂错类型也"绿",
// 给假信心(§14)。这里直接喂生产对象,标记若挂错(如挂在内部 dialer 而非返回的 wrapper 上)
// 此测试会失败,真实守住接线。
//
// 自证式双对照,排除假绿:
//   - 对照①:普通非 sidecar RT → 仍被换上 DB profile RT(证 resolver 真被调、既有行为不破);
//   - 对照②:生产 sidecar RT → 短路返回它本身(证检测精确、未误伤、且真实接线成立)。
func TestApplyTLSProfile_SidecarRTShortCircuitsDBProfile(t *testing.T) {
	ctx := context.Background()
	dbProfile := &tlsProfileMarkerRT{name: "db-profile"}
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{rt: dbProfile}}

	// 对照①:非 sidecar RT,绑定 DB profile 的 mimicry 账号应换上 profile RT(既有行为)。
	plain := &tlsProfileMarkerRT{name: "plain-builtin"}
	if got := d.applyTLSProfile(ctx, plain, transport.TransportModeMimicryClaudeCode, 7); got != http.RoundTripper(dbProfile) {
		t.Fatalf("非 sidecar RT:应换上 DB profile RT(既有行为),got %#v", got)
	}

	// 对照②:用生产构造器造真 sidecar RT(构造不拨号,socket 路径仅占位),必须被短路保留。
	sidecar := mimicry.NewSidecarRoundTripper(mimicry.NewSidecarClient("/tmp/huakai-sidecar.sock"), "anthropic-cli-mimicry-v1")
	// 前置自检:生产构造器的输出必须真满足标记接口(这正是原 bug——标记挂错类型则此处即暴露)。
	if _, ok := sidecar.(interface{ SidecarProfileID() string }); !ok {
		t.Fatalf("生产 sidecar RT 必须实现 SidecarProfileID() 标记,got %T", sidecar)
	}
	if got := d.applyTLSProfile(ctx, sidecar, transport.TransportModeMimicryClaudeCode, 7); got != sidecar {
		t.Fatalf("sidecar RT:必须短路保留不被 DB profile 替换,got %#v", got)
	}
}
