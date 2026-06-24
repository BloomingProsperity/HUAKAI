package mimicry

import (
	"context"
	"net"
	"net/url"
	"reflect"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// specALPN 从 uTLS spec 里取出 ALPN 扩展广告的协议列表;无 ALPN 扩展返回 nil。
func specALPN(t *testing.T, spec *utls.ClientHelloSpec) []string {
	t.Helper()
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			return alpn.AlpnProtocols
		}
	}
	return nil
}

// TestUtlsDialerDialTLSRejectsNilTemplateBeforeDial 守护构造错误的失败边界:
// 模板为空时必须在网络拨号前 fail-loud。变异证伪:把 nil 模板检查放到 dialRaw
// 之后,本测试的 ProxyDialer 哨兵会触发 t.Fatal。
func TestUtlsDialerDialTLSRejectsNilTemplateBeforeDial(t *testing.T) {
	dialer := &UtlsDialer{
		ProxyDialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.Fatalf("nil 模板应在拨号前失败,不应进入 ProxyDialer: %s %s", network, addr)
			return nil, nil
		},
	}
	_, err := dialer.DialTLS(context.Background(), "tcp", "api.anthropic.com:443")
	if err == nil {
		t.Fatal("nil 模板 DialTLS 应返回错误")
	}
	if got, want := err.Error(), "mimicry: nil clienthello template"; got != want {
		t.Fatalf("nil 模板错误=%q,want %q", got, want)
	}
}

// TestForceH1NarrowsCustomSpecALPN 守护核心不变量:force-h1 把自定义模板的线缆
// ALPN 收窄成只剩 http/1.1。变异证伪:把 narrowSpecALPNToHTTP1 改成 no-op,则
// ALPN 仍是真抓包的 ["h2","http/1.1"],断言转红。
func TestForceH1NarrowsCustomSpecALPN(t *testing.T) {
	spec, err := AnthropicCLIMimicryV1Template().UTLSSpec()
	if err != nil {
		t.Fatalf("构造 spec 失败: %v", err)
	}
	// 前置确认:模板原始 ALPN 含 h2(否则本测试无区分力)。
	if before := specALPN(t, spec); len(before) != 2 || before[0] != "h2" {
		t.Fatalf("前置不成立:模板原始 ALPN=%v,期望 [h2 http/1.1]", before)
	}
	narrowSpecALPNToHTTP1(spec)
	got := specALPN(t, spec)
	if !reflect.DeepEqual(got, []string{"http/1.1"}) {
		t.Fatalf("force-h1 收窄后 ALPN=%v,期望 [http/1.1](h2 必须被剔除)", got)
	}
}

// TestForceH1DisabledKeepsCapturedALPN 守护反向不变量:不收窄时保持真抓包 ALPN,
// 不能误伤非强制路径。变异证伪:若 force-h1 误作用于 off 路径(无条件收窄),
// 此处断言转红。
func TestForceH1DisabledKeepsCapturedALPN(t *testing.T) {
	spec, err := AnthropicCLIMimicryV1Template().UTLSSpec()
	if err != nil {
		t.Fatalf("构造 spec 失败: %v", err)
	}
	got := specALPN(t, spec)
	if !reflect.DeepEqual(got, []string{"h2", "http/1.1"}) {
		t.Fatalf("未收窄路径 ALPN=%v,期望真抓包 [h2 http/1.1]", got)
	}
}

// TestForceH1EnabledDefaultsOn 守护默认值:env 空=默认强制 H1,仅显式 "false"
// 关闭。变异证伪:把 forceH1Enabled 默认改成 false(如 == "true" 判定),则空
// env 用例转红。读 runtime env,用 -count=1。
func TestForceH1EnabledDefaultsOn(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{"env未设_默认开", false, "", true},
		{"显式false_关闭", true, "false", false},
		{"显式true_开启", true, "true", true},
		{"其它值_视为开", true, "1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("HUAKAI_TRANSPORT_FORCE_H1", c.val)
			} else {
				t.Setenv("HUAKAI_TRANSPORT_FORCE_H1", "")
				// t.Setenv 设空与 unset 在 os.Getenv 下等价返回 ""。
			}
			if got := forceH1Enabled(); got != c.want {
				t.Fatalf("forceH1Enabled(val=%q)=%v,want %v", c.val, got, c.want)
			}
		})
	}
}

// TestNewUtlsDialerForceH1FromEnv 守护构造器把 env 落到 ForceH1 字段。变异证伪:
// 把 NewUtlsDialer 里 ForceH1 赋值删掉(默认零值 false),则默认开用例转红。
func TestNewUtlsDialerForceH1FromEnv(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_FORCE_H1", "")
	if d := NewUtlsDialer(AnthropicCLIMimicryV1Template()); !d.ForceH1 {
		t.Fatal("env 未设时 NewUtlsDialer.ForceH1 应为 true(默认强制 H1)")
	}
	t.Setenv("HUAKAI_TRANSPORT_FORCE_H1", "false")
	if d := NewUtlsDialer(AnthropicCLIMimicryV1Template()); d.ForceH1 {
		t.Fatal("env=false 时 NewUtlsDialer.ForceH1 应为 false")
	}
}

// TestNewRoundTripperDisablesAttemptHTTP2 守护:uTLS RoundTripper 的内层
// http.Transport 显式关 ForceAttemptHTTP2(UConn 接不住 Go 内置 h2,留 true
// 只是意图含糊)。变异证伪:把 ForceAttemptHTTP2 改回 true,断言转红。
func TestNewRoundTripperDisablesAttemptHTTP2(t *testing.T) {
	// 本测试是 package mimicry 白盒,直接取 *roundTripper 的未导出 inner 字段。
	rt, ok := NewRoundTripper(AnthropicCLIMimicryV1Template()).(*roundTripper)
	if !ok {
		t.Fatalf("NewRoundTripper 返回类型非 *roundTripper")
	}
	if rt.inner.ForceAttemptHTTP2 {
		t.Fatal("NewRoundTripper 内层 Transport.ForceAttemptHTTP2 应为 false")
	}
}

// TestProxyRoundTripperDisablesAttemptHTTP2 守护代理出口同直连出口保持一致:
// uTLS 代理路径也必须显式关闭 Go 内置 h2 尝试。变异证伪:只把 WithProxy 内层
// Transport 改回 true,直连测试仍绿,本测试转红。
func TestProxyRoundTripperDisablesAttemptHTTP2(t *testing.T) {
	base, ok := NewRoundTripper(AnthropicCLIMimicryV1Template()).(*roundTripper)
	if !ok {
		t.Fatalf("NewRoundTripper 返回类型非 *roundTripper")
	}
	proxyURL := &url.URL{Scheme: "http", Host: "127.0.0.1:8080"}
	proxied, err := base.WithProxy(proxyURL)
	if err != nil {
		t.Fatalf("WithProxy: %v", err)
	}
	rt, ok := proxied.(*roundTripper)
	if !ok {
		t.Fatalf("WithProxy 返回类型非 *roundTripper")
	}
	if rt.inner.ForceAttemptHTTP2 {
		t.Fatal("WithProxy 内层 Transport.ForceAttemptHTTP2 应为 false")
	}
}
