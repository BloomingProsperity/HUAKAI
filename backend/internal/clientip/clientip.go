// Package clientip 在反向代理 / CDN / 负载均衡之后解析入站 HTTP 请求的真实客户端 IP,
// 并对 X-Forwarded-For 伪造采取 fail-closed 策略。
//
// 对 IP 敏感的安全路径(突发限流 keying、登录异常取证、voucher 兑付来源)此前只读
// net/http.Request.RemoteAddr。在共享入口(CDN/LB)之后,所有用户都坍缩到同一个
// RemoteAddr,导致误报的突发封禁和毫无价值的异常取证。而幼稚的修法——直接信任
// X-Forwarded-For——本身就是漏洞,因为任何客户端都能伪造该 header。本 resolver 仅当
// 直接 socket 对端(以及每一级上游 hop)都落在运维配置的可信代理 CIDR allowlist 之内时
// 才采信 forwarded header;否则返回 socket 对端。未配置任何可信代理时,它始终返回
// RemoteAddr——这是直接暴露或单租户部署下的安全默认值。
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver 把一个 HTTP 请求映射到尽力而为的真实客户端 IP。零值与 nil 指针都合法,
// 表现为「无可信代理」(只取 RemoteAddr)。
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver 从 CIDR 或裸 IP 字符串构建 Resolver(例如 "10.0.0.0/8"、
// "192.168.1.1"、"2001:db8::/32")。空白条目被跳过;裸 IP 被当作单主机 prefix。
// 无法解析的条目返回 error,这样配置错误的 allowlist 会在启动时大声失败,
// 而不是默默地谁都不信任——或者更糟,被误当成「信任所有人」。
func NewResolver(cidrs []string) (*Resolver, error) {
	var trusted []netip.Prefix
	for _, raw := range cidrs {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(s); err == nil {
			trusted = append(trusted, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("clientip: invalid trusted proxy %q: %w", raw, err)
		}
		addr = addr.Unmap()
		trusted = append(trusted, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return &Resolver{trusted: trusted}, nil
}

// ClientIP 以字符串返回解析出的客户端 IP。它对 nil 安全:nil 的 Resolver,或没有
// 配置任何可信代理的 Resolver,会返回 socket 对端(RemoteAddr 的 host),并忽略
// 每一个 forwarded header。
func (r *Resolver) ClientIP(req *http.Request) string {
	if req == nil {
		return ""
	}
	peer := remoteHost(req.RemoteAddr)
	if r == nil || len(r.trusted) == 0 {
		return peer
	}
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !r.isTrusted(peerAddr) {
		// 直接对端不是已配置的可信代理,因此它呈现的任何 forwarded header 都受攻击者控制。
		// 只信任 socket 地址。
		return peer
	}
	// 对端是可信代理:X-Forwarded-For 由可信 hop 追加。从右往左遍历,跳过可信 hop;
	// 第一个(最右侧)非可信地址,就是我们最外层可信代理所看到的真实客户端。最左侧的条目
	// 可被客户端伪造,因此绝不单独信任。
	//
	// 一个请求可能携带 X-Forwarded-For 作为多个 header field(RFC 7230 §3.2.2:
	// 重复的 field 等价于单个逗号拼接的 field,且保持顺序)。http.Header.Get 只返回第一个
	// field——它可能正是客户端伪造的那一行,而我们可信代理写入的真实值在后面的 field 里。
	// 按顺序拼接每一个 field,这样最右侧的 token 永远是我们可信代理实际追加的那个。
	xffValues := req.Header.Values("X-Forwarded-For")
	if len(xffValues) == 0 {
		return peer
	}
	parts := strings.Split(strings.Join(xffValues, ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(parts[i])
		if hop == "" {
			continue
		}
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			// 出现格式错误的 hop,意味着其左侧的内容我们都不能再信任;停在最近的可信边界,
			// 而不是返回一个可被伪造的值。
			return peer
		}
		addr = addr.Unmap()
		if r.isTrusted(addr) {
			continue
		}
		return addr.String()
	}
	// 链中每一个 hop 本身都是可信代理——没有可单独报告的客户端。
	return peer
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteHost(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
