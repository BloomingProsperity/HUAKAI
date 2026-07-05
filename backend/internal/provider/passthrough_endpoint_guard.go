package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

var ErrUnsafePassthroughEndpoint = errors.New("provider: unsafe upstream passthrough endpoint")

var ErrPassthroughProxyCustomEndpointIncompatible = errors.New("config_incompatible_proxy_custom_endpoint")

func safePassthroughBaseURL(raw string) (*url.URL, error) {
	if hasControlOrSpace(raw) {
		return nil, passthroughEndpointBlocked("control character or whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, passthroughEndpointBlocked("invalid URL")
	}
	if _, err := validatePassthroughEndpointURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePassthroughEndpointURL(u *url.URL) (string, error) {
	if u == nil {
		return "", passthroughEndpointBlocked("invalid URL")
	}
	if u.Scheme != "https" {
		return "", passthroughEndpointBlocked("scheme must be https")
	}
	if u.User != nil {
		return "", passthroughEndpointBlocked("userinfo is not allowed")
	}
	if u.Fragment != "" {
		return "", passthroughEndpointBlocked("fragment is not allowed")
	}
	if strings.Contains(u.Host, "%") {
		return "", passthroughEndpointBlocked("encoded host is not allowed")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", passthroughEndpointBlocked("invalid port")
	}
	if port := u.Port(); port != "" && !validEndpointPort(port) {
		return "", passthroughEndpointBlocked("invalid port")
	}
	policy, err := currentPassthroughPolicy()
	if err != nil {
		return "", err
	}
	if !policy.AllowsPort(passthroughEndpointPort(u)) {
		return "", passthroughEndpointBlocked("blocked port")
	}
	return validatePassthroughHostWithPolicy(u.Hostname(), policy)
}

func validatePassthroughHost(raw string) (string, error) {
	policy, err := currentPassthroughPolicy()
	if err != nil {
		return "", err
	}
	return validatePassthroughHostWithPolicy(raw, policy)
}

func validatePassthroughHostWithPolicy(raw string, policy ssrfpolicy.Policy) (string, error) {
	host := strings.ToLower(raw)
	if host == "" {
		return "", passthroughEndpointBlocked("empty host")
	}
	if strings.Contains(host, "%") {
		return "", passthroughEndpointBlocked("encoded host is not allowed")
	}
	if host[len(host)-1] == '.' {
		return "", passthroughEndpointBlocked("trailing-dot host")
	}
	if hasNonASCII(host) {
		return "", passthroughEndpointBlocked("non-ascii host")
	}
	if !policy.AllowsHost(host) {
		return "", passthroughEndpointBlocked("blocked host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !passthroughIPAllowedForHost(policy, host, addr) {
			return "", passthroughEndpointBlocked("blocked IP")
		}
		return host, nil
	}
	if blockedPassthroughHost(host) || numericObfuscatedHost(host) {
		return "", passthroughEndpointBlocked("blocked host")
	}
	return host, nil
}

// UsesCustomPassthroughEndpoint 报告 cred 是否携带了租户提供的上游 base URL，
// 其请求目标必须在 Do() 之前先经过守卫校验。
func UsesCustomPassthroughEndpoint(cred Credential) bool {
	if cred.Type != CredentialTypeUpstreamPassthrough {
		return false
	}
	for _, key := range []string{"base_url", "endpoint_api", "copilot_endpoint_api"} {
		if strings.TrimSpace(cred.Extra[key]) != "" {
			return true
		}
	}
	return false
}

var passthroughEndpointLookupNetIP = net.DefaultResolver.LookupNetIP

// SwapPassthroughEndpointLookupForTesting 替换 endpoint 的 DNS 查询钩子。
// 它仅为跨包的 dispatcher 测试导出；生产代码绝不能调用它。
func SwapPassthroughEndpointLookupForTesting(fn func(context.Context, string, string) ([]netip.Addr, error)) func() {
	original := passthroughEndpointLookupNetIP
	passthroughEndpointLookupNetIP = fn
	return func() { passthroughEndpointLookupNetIP = original }
}

// ValidatePassthroughEndpointTarget 在出站 I/O 之前的最后一刻解析租户提供的
// passthrough 请求目标。EndpointForCredential 保持纯函数/静态，使适配器不执行
// DNS；而 dispatcher 仍可对那些解析到 loopback、私网、link-local、metadata 或
// 其它特殊用途地址的主机名别名做到 fail-closed（拒绝放行）。
func ValidatePassthroughEndpointTarget(ctx context.Context, endpoint *url.URL) error {
	host, err := validatePassthroughEndpointURL(endpoint)
	if err != nil {
		return err
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	policy, err := currentPassthroughPolicy()
	if err != nil {
		return err
	}
	addrs, err := passthroughEndpointLookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return passthroughEndpointBlocked("host resolution failed")
	}
	for _, addr := range addrs {
		if !passthroughIPAllowedForHost(policy, host, addr) {
			return passthroughEndpointBlocked("blocked resolved IP")
		}
	}
	return nil
}

// WrapPassthroughEndpointTransport 返回一个 transport，其真正的直连拨号使用与
// ValidatePassthroughEndpointTarget 相同的公网 IP 策略。它必须在账号代理解析
// 之后应用；代理型或自定义的 RoundTripper 无法把 HUAKAI 的 DNS 决策绑定到
// 目标拨号上，因此在此处 fail-closed（拒绝放行）。
func WrapPassthroughEndpointTransport(rt http.RoundTripper) (http.RoundTripper, error) {
	if rt == nil {
		rt = http.DefaultTransport
	}
	base, ok := rt.(*http.Transport)
	if !ok {
		return nil, passthroughEndpointBlocked("unsupported transport")
	}
	if base.Proxy != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsafePassthroughEndpoint, ErrPassthroughProxyCustomEndpointIncompatible)
	}
	clone := base.Clone()
	dial := clone.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	clone.Proxy = nil
	clone.DialContext = passthroughGuardedDialContext(dial)
	clone.DialTLS = nil
	clone.DialTLSContext = nil
	return clone, nil
}

func passthroughGuardedDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialAddresses, err := resolvePassthroughDialAddresses(ctx, address)
		if err != nil {
			return nil, err
		}
		var lastDialErr error
		for _, dialAddress := range dialAddresses {
			conn, err := base(ctx, network, dialAddress.address)
			if err != nil {
				lastDialErr = err
				continue
			}
			if !passthroughNetAddrAllowedForHost(dialAddress.policy, dialAddress.host, conn.RemoteAddr()) {
				_ = conn.Close()
				return nil, passthroughEndpointBlocked("blocked remote IP")
			}
			return conn, nil
		}
		if lastDialErr != nil {
			return nil, lastDialErr
		}
		return nil, passthroughEndpointBlocked("blocked resolved IP")
	}
}

type passthroughDialAddress struct {
	address string
	host    string
	policy  ssrfpolicy.Policy
}

func resolvePassthroughDialAddresses(ctx context.Context, address string) ([]passthroughDialAddress, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, passthroughEndpointBlocked("invalid dial address")
	}
	policy, err := currentPassthroughPolicy()
	if err != nil {
		return nil, err
	}
	host, err = validatePassthroughHostWithPolicy(host, policy)
	if err != nil {
		return nil, err
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return []passthroughDialAddress{{
			address: net.JoinHostPort(addr.String(), port),
			host:    host,
			policy:  policy,
		}}, nil
	}
	addrs, err := passthroughEndpointLookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, passthroughEndpointBlocked("host resolution failed")
	}
	dialAddresses := make([]passthroughDialAddress, 0, len(addrs))
	for _, addr := range addrs {
		if !passthroughIPAllowedForHost(policy, host, addr) {
			return nil, passthroughEndpointBlocked("blocked resolved IP")
		}
		dialAddresses = append(dialAddresses, passthroughDialAddress{
			address: net.JoinHostPort(addr.String(), port),
			host:    host,
			policy:  policy,
		})
	}
	return dialAddresses, nil
}

func passthroughNetAddrAllowedForHost(policy ssrfpolicy.Policy, host string, addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	netipAddr, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	return passthroughIPAllowedForHost(policy, host, netipAddr)
}

func passthroughEndpointBlocked(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsafePassthroughEndpoint, reason)
}

func hasControlOrSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func validEndpointPort(raw string) bool {
	port, err := strconv.Atoi(raw)
	return err == nil && port > 0 && port <= 65535
}

func passthroughEndpointPort(u *url.URL) int {
	if raw := u.Port(); raw != "" {
		port, _ := strconv.Atoi(raw)
		return port
	}
	return 443
}

func currentPassthroughPolicy() (ssrfpolicy.Policy, error) {
	policy, err := ssrfpolicy.LoadFromEnv()
	if err != nil {
		return ssrfpolicy.Policy{}, passthroughEndpointBlocked("invalid SSRF policy")
	}
	return policy, nil
}

func blockedPassthroughHost(host string) bool {
	switch host {
	case "localhost",
		"localhost.localdomain",
		"metadata",
		"metadata.google.internal",
		"metadata.goog",
		"instance-data",
		"instance-data.ec2.internal",
		"169.254.169.254":
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".corp", ".intranet"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func numericObfuscatedHost(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) == 0 || len(labels) > 4 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		switch {
		case strings.HasPrefix(label, "0x") || strings.HasPrefix(label, "0X"):
			if _, err := strconv.ParseUint(label[2:], 16, 32); err != nil {
				return false
			}
		case len(label) > 1 && label[0] == '0':
			if _, err := strconv.ParseUint(label, 8, 32); err != nil {
				return false
			}
		default:
			if _, err := strconv.ParseUint(label, 10, 32); err != nil {
				return false
			}
		}
	}
	return true
}

func publicPassthroughIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range passthroughSpecialUseDenyPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func passthroughIPAllowedForHost(policy ssrfpolicy.Policy, host string, addr netip.Addr) bool {
	if publicPassthroughIP(addr) {
		return true
	}
	return policy.AllowsPrivateIPHost(host) && privatePassthroughIP(addr)
}

func privatePassthroughIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range passthroughSpecialUseDenyPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var passthroughSpecialUseDenyPrefixes = []netip.Prefix{
	mustPassthroughPrefix("0.0.0.0/8"),
	mustPassthroughPrefix("100.64.0.0/10"),
	mustPassthroughPrefix("192.0.0.0/24"),
	mustPassthroughPrefix("192.0.2.0/24"),
	mustPassthroughPrefix("192.88.99.0/24"),
	mustPassthroughPrefix("198.18.0.0/15"),
	mustPassthroughPrefix("198.51.100.0/24"),
	mustPassthroughPrefix("203.0.113.0/24"),
	mustPassthroughPrefix("240.0.0.0/4"),
	mustPassthroughPrefix("255.255.255.255/32"),
	mustPassthroughPrefix("::/96"),
	mustPassthroughPrefix("64:ff9b::/96"),
	mustPassthroughPrefix("64:ff9b:1::/48"),
	mustPassthroughPrefix("100::/64"),
	mustPassthroughPrefix("2001::/23"),
	mustPassthroughPrefix("2001:db8::/32"),
	mustPassthroughPrefix("2002::/16"),
	mustPassthroughPrefix("3fff::/20"),
	mustPassthroughPrefix("5f00::/16"),
}

func mustPassthroughPrefix(raw string) netip.Prefix {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return prefix
}
