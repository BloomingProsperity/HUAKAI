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

var ErrUnsafeProxyEndpoint = errors.New("provider: unsafe proxy endpoint")

var proxyEndpointLookupNetIP = net.DefaultResolver.LookupNetIP

// ResolveProxyEndpointIPs 在实际出站前解析并校验代理地址。所有解析结果都必须
// 符合部署者策略，避免一个同时返回公网和内网地址的域名绕过检查。
func ResolveProxyEndpointIPs(ctx context.Context, proxyURL *url.URL) ([]netip.Addr, error) {
	host, policy, err := validateProxyEndpointURL(proxyURL)
	if err != nil {
		return nil, err
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	addrs, err := proxyEndpointLookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, unsafeProxyEndpoint("host resolution failed")
	}
	if len(addrs) > 16 {
		return nil, unsafeProxyEndpoint("too many resolved addresses")
	}
	seen := make(map[netip.Addr]struct{}, len(addrs))
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !policy.AllowsAddress(host, addr) {
			return nil, unsafeProxyEndpoint("blocked resolved IP")
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, unsafeProxyEndpoint("host resolution returned no usable address")
	}
	return out, nil
}

// ValidateProxyEndpointTarget 是业务出站、额度探测和代理健康检查共用的
// 发网前守卫。真正拨号还会再次解析并绑定地址，防止检查后 DNS 重绑定。
func ValidateProxyEndpointTarget(ctx context.Context, proxyURL *url.URL) error {
	_, err := ResolveProxyEndpointIPs(ctx, proxyURL)
	return err
}

func validateProxyEndpointURL(proxyURL *url.URL) (string, ssrfpolicy.Policy, error) {
	if proxyURL == nil || proxyURL.Host == "" || strings.Contains(proxyURL.Host, "%") {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("invalid URL")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("unsupported scheme")
	}
	if strings.HasSuffix(proxyURL.Host, ":") {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("invalid port")
	}
	port, err := proxyEndpointPort(proxyURL)
	if err != nil {
		return "", ssrfpolicy.Policy{}, err
	}
	if port <= 0 || port > 65535 {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("invalid port")
	}
	host := strings.ToLower(strings.TrimSpace(proxyURL.Hostname()))
	if host == "" || hasControlOrSpace(host) || hasNonASCII(host) ||
		strings.HasSuffix(host, ".") || blockedProxyEndpointHost(host) {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("blocked host")
	}
	policy, err := ssrfpolicy.LoadProxyFromEnv()
	if err != nil {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("invalid proxy SSRF policy")
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if !policy.AllowsAddress(host, addr) {
			return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("blocked IP")
		}
	} else if numericObfuscatedHost(host) {
		return "", ssrfpolicy.Policy{}, unsafeProxyEndpoint("blocked host")
	}
	return host, policy, nil
}

func proxyEndpointPort(proxyURL *url.URL) (int, error) {
	if raw := proxyURL.Port(); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return 0, unsafeProxyEndpoint("invalid port")
		}
		return port, nil
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "https":
		return 443, nil
	case "socks5", "socks5h":
		return 1080, nil
	default:
		return 80, nil
	}
}

func blockedProxyEndpointHost(host string) bool {
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
	default:
		return false
	}
}

func unsafeProxyEndpoint(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsafeProxyEndpoint, reason)
}

func wrapProxyEndpointTransport(base *http.Transport, proxyURL *url.URL) (*http.Transport, error) {
	if _, _, err := validateProxyEndpointURL(proxyURL); err != nil {
		return nil, err
	}
	clone := base.Clone()
	dial := clone.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	clone.Proxy = http.ProxyURL(proxyURL)
	clone.DialContext = proxyGuardedDialContext(dial, proxyURL)
	//lint:ignore SA1019 必须清空旧 TLS 拨号钩子，确保代理连接经过受保护的 DialContext。
	clone.DialTLS = nil
	clone.DialTLSContext = nil
	return clone, nil
}

func proxyGuardedDialContext(
	base func(context.Context, string, string) (net.Conn, error),
	proxyURL *url.URL,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		expectedPort, err := proxyEndpointPort(proxyURL)
		if err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(strings.Trim(host, "[]"), proxyURL.Hostname()) ||
			port != strconv.Itoa(expectedPort) {
			return nil, unsafeProxyEndpoint("unexpected dial target")
		}
		addrs, err := ResolveProxyEndpointIPs(ctx, proxyURL)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			conn, dialErr := base(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr != nil {
				lastErr = dialErr
				continue
			}
			if !proxyRemoteAddressAllowed(proxyURL.Hostname(), conn.RemoteAddr()) {
				_ = conn.Close()
				return nil, unsafeProxyEndpoint("blocked remote IP")
			}
			return conn, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, unsafeProxyEndpoint("no allowed dial target")
	}
}

func proxyRemoteAddressAllowed(host string, remote net.Addr) bool {
	tcp, ok := remote.(*net.TCPAddr)
	if !ok {
		return false
	}
	addr, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	policy, err := ssrfpolicy.LoadProxyFromEnv()
	return err == nil && policy.AllowsAddress(host, addr)
}

// SwapProxyEndpointLookupForTesting 只供判别性测试替换 DNS。
func SwapProxyEndpointLookupForTesting(fn func(context.Context, string, string) ([]netip.Addr, error)) func() {
	original := proxyEndpointLookupNetIP
	proxyEndpointLookupNetIP = fn
	return func() { proxyEndpointLookupNetIP = original }
}
