package provider

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

func TestResolveProxyEndpointIPsRejectsMixedPublicAndPrivateDNS(t *testing.T) {
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
	restore := SwapProxyEndpointLookupForTesting(
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("1.1.1.1"),
				netip.MustParseAddr("127.0.0.1"),
			}, nil
		},
	)
	t.Cleanup(restore)

	proxyURL, _ := url.Parse("http://proxy.example:3128")
	_, err := ResolveProxyEndpointIPs(context.Background(), proxyURL)
	if !errors.Is(err, ErrUnsafeProxyEndpoint) {
		t.Fatalf("混合公网/本机 DNS 必须整体拒绝,got %v", err)
	}
}

func TestResolveProxyEndpointIPsAllowsOnlyExplicitPrivateHost(t *testing.T) {
	t.Setenv(ssrfpolicy.ProxyAllowPrivateIPHostsEnv, "proxy.internal")
	t.Setenv(ssrfpolicy.ProxyPrivateIPsEnabledEnv, "true")
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
	restore := SwapProxyEndpointLookupForTesting(
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
		},
	)
	t.Cleanup(restore)

	proxyURL, _ := url.Parse("socks5://proxy.internal:1080")
	addrs, err := ResolveProxyEndpointIPs(context.Background(), proxyURL)
	if err != nil {
		t.Fatalf("部署者精确授权的私网代理应通过: %v", err)
	}
	if len(addrs) != 1 || addrs[0].String() != "10.0.0.8" {
		t.Fatalf("解析结果错误: %v", addrs)
	}
}

func TestProxyGuardedDialRejectsRemoteAddressChange(t *testing.T) {
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
	restore := SwapProxyEndpointLookupForTesting(
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
		},
	)
	t.Cleanup(restore)

	proxyURL, _ := url.Parse("http://proxy.example:3128")
	base := func(context.Context, string, string) (net.Conn, error) {
		left, right := net.Pipe()
		_ = right.Close()
		return &remoteAddrConn{
			Conn:   left,
			remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3128},
		}, nil
	}
	conn, err := proxyGuardedDialContext(base, proxyURL)(
		context.Background(),
		"tcp",
		"proxy.example:3128",
	)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("远端地址变成本机时不得返回连接")
	}
	if !errors.Is(err, ErrUnsafeProxyEndpoint) {
		t.Fatalf("DNS 检查后远端地址变化必须拒绝,got %v", err)
	}
}

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *remoteAddrConn) RemoteAddr() net.Addr { return c.remote }
