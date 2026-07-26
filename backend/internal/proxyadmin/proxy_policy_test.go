package proxyadmin

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

func allowPrivateProxyHosts(t *testing.T, hosts string) {
	t.Helper()
	t.Setenv(ssrfpolicy.ProxyAllowPrivateIPHostsEnv, hosts)
	t.Setenv(ssrfpolicy.ProxyPrivateIPsEnabledEnv, "true")
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
}
