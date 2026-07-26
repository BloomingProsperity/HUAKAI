package clientip

import (
	"net/http"
	"testing"
)

func req(remoteAddr, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remoteAddr, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func mustResolver(t *testing.T, cidrs ...string) *Resolver {
	t.Helper()
	r, err := NewResolver(cidrs)
	if err != nil {
		t.Fatalf("NewResolver(%v): %v", cidrs, err)
	}
	return r
}

// TestResolverClientIP 是安全核心:仅当直接对端是已配置的可信代理时才采信 forwarded
// header,且真实客户端取自最右侧的非可信 hop(我们最外层可信代理实际观测到的地址)——
// 绝不取客户端可控的最左侧条目。
//
// 变异自检(每一行会翻转):
//   - 去掉 `len(r.trusted)==0 -> peer` 这道守卫:no_trusted_config / spoof_no_config 翻转。
//   - 去掉 `!isTrusted(peer) -> peer` 这道守卫:untrusted_peer_spoof 返回伪造的客户端
//     而非 socket 对端(头号漏洞)→ 红。
//   - 把 X-Forwarded-For 从左往右遍历而非从右往左:multi_hop_spoof 返回伪造的最左侧
//     1.1.1.1 而非真实的 198.51.100.9 → 红。
func TestResolverClientIP(t *testing.T) {
	trusted := mustResolver(t, "10.0.0.0/8", "2001:db8::/32")

	cases := []struct {
		name     string
		resolver *Resolver
		remote   string
		xff      string
		want     string
	}{
		// 未配置任何可信代理:forwarded header 被完全忽略。
		{"no_trusted_config", mustResolver(t), "203.0.113.7:443", "198.51.100.9", "203.0.113.7"},
		{"spoof_no_config", mustResolver(t), "203.0.113.7:443", "1.1.1.1", "203.0.113.7"},
		{"nil_resolver", nil, "203.0.113.7:443", "1.1.1.1", "203.0.113.7"},

		// 非可信的直接对端:其 forwarded header 受攻击者控制 → 忽略。
		{"untrusted_peer_spoof", trusted, "203.0.113.7:443", "198.51.100.9", "203.0.113.7"},

		// 可信对端:采信 forwarded 链。
		{"trusted_peer_single_client", trusted, "10.1.2.3:5000", "198.51.100.9", "198.51.100.9"},
		{"trusted_peer_no_xff", trusted, "10.1.2.3:5000", "", "10.1.2.3"},
		{"all_hops_trusted", trusted, "10.1.2.3:5000", "10.9.9.9, 10.8.8.8", "10.1.2.3"},

		// 防伪造:客户端伪造了最左侧的 XFF 条目。我们代理实际追加得到的真实链是:
		// [伪造的 1.1.1.1]、真实客户端 198.51.100.9、可信 hop 10.9.9.9。
		// 「最右侧非可信」遍历返回真实客户端,绝不返回伪造值。
		{"multi_hop_spoof", trusted, "10.1.2.3:5000", "1.1.1.1, 198.51.100.9, 10.9.9.9", "198.51.100.9"},

		// 最右侧 hop(由我们可信代理追加)格式错误 → 无法信任整条链,
		// 回退到可信对端而非一个可被伪造的值。
		{"malformed_rightmost_hop", trusted, "10.1.2.3:5000", "198.51.100.9, garbage", "10.1.2.3"},

		// IPv6 可信对端 + 非可信 IPv6 客户端。
		{"ipv6_trusted_peer", trusted, "[2001:db8::1]:5000", "2606:4700::1234", "2606:4700::1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.resolver.ClientIP(req(tc.remote, tc.xff)); got != tc.want {
				t.Fatalf("ClientIP(remote=%q xff=%q)=%q want %q", tc.remote, tc.xff, got, tc.want)
			}
		})
	}
}

// TestResolverClientIPRepeatedXFFHeaders 守护重复 header 的场景:一个请求可能携带
// X-Forwarded-For 作为多个 header field(RFC 7230 §3.2.2)。客户端能伪造第一个 field;
// 我们的可信代理把真实客户端追加在后面的 field。resolver 必须先按顺序拼接所有 field,
// 再做从右往左遍历——http.Header.Get 只读第一个。
//
// 变异:退回 Header.Get("X-Forwarded-For") → 只解析并返回 "1.1.1.1" → 红。
func TestResolverClientIPRepeatedXFFHeaders(t *testing.T) {
	trusted := mustResolver(t, "10.0.0.0/8")
	r := &http.Request{RemoteAddr: "10.1.2.3:5000", Header: http.Header{}}
	r.Header.Add("X-Forwarded-For", "1.1.1.1")      // 客户端伪造(第一个 field)
	r.Header.Add("X-Forwarded-For", "198.51.100.9") // 由我们的可信代理追加(后面的 field)
	if got := trusted.ClientIP(r); got != "198.51.100.9" {
		t.Fatalf("repeated XFF: got %q want 198.51.100.9 (must join all header fields, not just the first)", got)
	}
}

// TestResolverBareIPTrust 证明裸 IP 的 allowlist 条目只信任那一台主机(/32),
// 而非其邻居。变异:放宽裸 IP 的 prefix → .4 对端会被错误地采信 XFF。
func TestResolverBareIPTrust(t *testing.T) {
	r := mustResolver(t, "10.1.2.3")
	if got := r.ClientIP(req("10.1.2.3:5000", "198.51.100.9")); got != "198.51.100.9" {
		t.Fatalf("trusted bare IP peer: got %q want forwarded client", got)
	}
	if got := r.ClientIP(req("10.1.2.4:5000", "198.51.100.9")); got != "10.1.2.4" {
		t.Fatalf("neighbour of bare IP must be untrusted: got %q want socket peer", got)
	}
}

func TestResolverTrustedPeer(t *testing.T) {
	trusted := mustResolver(t, "10.0.0.0/8")
	cases := []struct {
		name     string
		resolver *Resolver
		request  *http.Request
		want     bool
	}{
		{name: "可信代理", resolver: trusted, request: req("10.1.2.3:5000", ""), want: true},
		{name: "公网对端", resolver: trusted, request: req("203.0.113.7:5000", ""), want: false},
		{name: "畸形对端", resolver: trusted, request: req("not-an-address", ""), want: false},
		{name: "空白名单", resolver: mustResolver(t), request: req("10.1.2.3:5000", ""), want: false},
		{name: "空解析器", resolver: nil, request: req("10.1.2.3:5000", ""), want: false},
		{name: "空请求", resolver: trusted, request: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.resolver.TrustedPeer(tc.request); got != tc.want {
				t.Fatalf("TrustedPeer()=%v want %v", got, tc.want)
			}
		})
	}
}

// TestNewResolverRejectsMalformedCIDR 证明非法的 allowlist 条目会在启动时大声失败,
// 而不是被默默丢弃(那可能被理解成「谁都不信任」或与「信任所有人」混淆)。
// 变异:吞掉解析 error → 此处返回 nil err → 红。
func TestNewResolverRejectsMalformedCIDR(t *testing.T) {
	if _, err := NewResolver([]string{"10.0.0.0/8", "not-an-ip"}); err == nil {
		t.Fatal("NewResolver must reject a malformed trusted-proxy entry")
	}
	// 空白/纯空格条目被跳过,而非报错。
	if _, err := NewResolver([]string{"", "  "}); err != nil {
		t.Fatalf("blank entries must be skipped, got %v", err)
	}
}
