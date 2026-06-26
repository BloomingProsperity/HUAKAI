package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// 用指针身份做区分的 RT: 空结构体的「值」一旦装箱进 interface 就会比较相等,
// 这会掩盖「保留 builtin 而非替换」这种回归。用不同的指针让替换变得可观测。
type tlsProfileMarkerRT struct{ name string }

func (*tlsProfileMarkerRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

type fakeTLSProfileResolver struct {
	rt  http.RoundTripper
	err error
}

func (f fakeTLSProfileResolver) ResolveRoundTripper(context.Context, int64) (http.RoundTripper, error) {
	return f.rt, f.err
}

// UTLS-03 dispatcher 接线: 对 mimicry 模式的账号, applyTLSProfile 必须换入
// 按账号粒度的 DB profile RoundTripper; 对 standard 模式 / account 0 / nil
// resolver / resolver 出错, 则保留 builtin(orig rt)。
func TestApplyTLSProfile_Wiring(t *testing.T) {
	ctx := context.Background()
	marker := &tlsProfileMarkerRT{name: "profile"}
	orig := &tlsProfileMarkerRT{name: "builtin"}
	var origRT http.RoundTripper = orig

	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{rt: marker}}

	// 变异:防护。让 applyTLSProfile 返回 rt 而非解析出的 profileRT, 会使
	// got == orig(!= marker)-> 变红。从而能抓到「按账号粒度的 DB 指纹始终
	// 未生效」的情况。
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != http.RoundTripper(marker) {
		t.Fatalf("mimicry mode + bound profile: expected profile RT to replace builtin, got %#v", got)
	}
	// standard 模式绝不能拿到指纹(Owner 为普通路径留的豁免)
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeStandard, 7); got != origRT {
		t.Fatalf("standard mode must keep builtin RT")
	}
	// 无 account id -> builtin
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 0); got != origRT {
		t.Fatalf("account 0 must keep builtin RT")
	}
	// resolver 出错 -> builtin(绝不让 dispatch 失败)
	dErr := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{err: context.DeadlineExceeded}}
	if got := dErr.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != origRT {
		t.Fatalf("resolver error must fall back to builtin RT")
	}
	// nil 的 resolver -> builtin
	dNil := &UpstreamDispatcher{}
	if got := dNil.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != origRT {
		t.Fatalf("nil resolver must keep builtin RT")
	}
}
