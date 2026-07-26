package mimicry

import (
	"context"
	"net"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

func allowLoopbackProxy(t *testing.T) {
	t.Helper()
	restore := SwapProxyEndpointDialForTesting(
		func(ctx context.Context, proxyURL *url.URL) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", proxyHostPort(proxyURL))
		},
	)
	t.Cleanup(restore)
}

func allowPrivateProxy(t *testing.T, host string) {
	t.Helper()
	t.Setenv(ssrfpolicy.ProxyAllowPrivateIPHostsEnv, host)
	t.Setenv(ssrfpolicy.ProxyPrivateIPsEnabledEnv, "true")
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
}
