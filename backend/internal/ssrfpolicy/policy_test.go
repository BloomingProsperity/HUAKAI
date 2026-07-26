package ssrfpolicy

import (
	"net/netip"
	"testing"
)

func TestParsePolicyPortRangesAndHostPatterns(t *testing.T) {
	policy, err := Parse(
		"443,8000-9000",
		"blocked.example",
		"api.openai.com,*.anthropic.com",
		"10.1.2.3,Private-Proxy.Example",
		"",
	)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	for _, port := range []int{443, 8000, 8500, 9000} {
		if !policy.AllowsPort(port) {
			t.Fatalf("port %d rejected by allowlist", port)
		}
	}
	if policy.AllowsPort(7000) {
		t.Fatal("port 7000 allowed outside configured ranges")
	}

	if !policy.AllowsHost("api.openai.com") {
		t.Fatal("exact allowlist host rejected")
	}
	if !policy.AllowsHost("x.anthropic.com") {
		t.Fatal("wildcard allowlist host rejected")
	}
	if policy.AllowsHost("anthropic.com") {
		t.Fatal("wildcard must not match the root domain")
	}
	if policy.AllowsHost("evil.example") {
		t.Fatal("non-empty allowlist allowed unmatched host")
	}
	if policy.AllowsHost("blocked.example") {
		t.Fatal("denylist host allowed")
	}

	if !policy.AllowsPrivateIPHost("10.1.2.3") {
		t.Fatal("explicit private IP host not recognized")
	}
	if !policy.AllowsPrivateIPHost("private-proxy.example") {
		t.Fatal("explicit private DNS host not normalized")
	}
	if policy.AllowsPrivateIPHost("10.1.2.4") {
		t.Fatal("unlisted private IP host recognized")
	}
}

func TestParsePolicyEmptyDefaultsPreserveAllowAll(t *testing.T) {
	policy, err := Parse("", "", "", "", "")
	if err != nil {
		t.Fatalf("Parse empty policy returned error: %v", err)
	}
	if !policy.AllowsPort(7000) {
		t.Fatal("empty port allowlist must allow all ports")
	}
	if !policy.AllowsHost("evil.example") {
		t.Fatal("empty domain policy must allow ordinary hosts")
	}
	if policy.AllowsPrivateIPHost("10.1.2.3") {
		t.Fatal("empty private IP escape hatch must not allow private hosts")
	}
}

// SEC-084:总开关必须强制拒绝一个本会被按主机 allowlist 放行的主机,
// 同时让默认/启用的各种情形与之前完全一致。该测试夹具在全部三种开关状态下都固定使用
// 同一个被 allowlist 的主机,从而隔离出开关本身的效果。
// 变异:去掉 `if p.privateIPsDisabled { return false }` 守卫,则禁用情形会放行
// 被 allowlist 的主机 -> 变红。
func TestPrivateIPsMasterKillSwitch(t *testing.T) {
	const host = "internal-proxy.example"
	const allowlist = "10.0.0.5," + host

	disabled, err := Parse("", "", "", allowlist, "false")
	if err != nil {
		t.Fatalf("Parse disabled toggle: %v", err)
	}
	if disabled.AllowsPrivateIPHost(host) {
		t.Fatal("master kill-switch off must deny an allowlisted private host")
	}
	if disabled.AllowsPrivateIPHost("10.0.0.5") {
		t.Fatal("master kill-switch off must deny every allowlisted private host")
	}

	// 默认(未设置)与显式 true 必须保持 allowlist 行为:同一个主机被放行。
	// 这证明 kill-switch 是唯一的差别。
	for _, raw := range []string{"", "true"} {
		enabled, err := Parse("", "", "", allowlist, raw)
		if err != nil {
			t.Fatalf("Parse enabled toggle %q: %v", raw, err)
		}
		if !enabled.AllowsPrivateIPHost(host) {
			t.Fatalf("toggle %q must keep allowlisted host admitted", raw)
		}
		if enabled.AllowsPrivateIPHost("10.0.0.6") {
			t.Fatalf("toggle %q must still reject an unlisted private host", raw)
		}
	}
}

func TestParsePolicyRejectsInvalidOperatorInput(t *testing.T) {
	cases := []struct {
		name                string
		portAllowlist       string
		domainDenylist      string
		domainAllowlist     string
		allowPrivateIPHosts string
		privateIPsEnabled   string
	}{
		{name: "descending port range", portAllowlist: "9000-8000"},
		{name: "invalid port", portAllowlist: "70000"},
		{name: "bare wildcard", domainAllowlist: "*"},
		{name: "wildcard explicit private host", allowPrivateIPHosts: "*.example.com"},
		{name: "non-boolean master toggle", privateIPsEnabled: "maybe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.portAllowlist, tc.domainDenylist, tc.domainAllowlist, tc.allowPrivateIPHosts, tc.privateIPsEnabled); err == nil {
				t.Fatal("Parse returned nil error for invalid policy")
			}
		})
	}
}

func TestPolicyAllowsAddressRequiresExactPrivateHost(t *testing.T) {
	policy, err := Parse("", "", "", "proxy.internal,10.0.0.9", "")
	if err != nil {
		t.Fatal(err)
	}
	if !policy.AllowsAddress("proxy.internal", netip.MustParseAddr("10.0.0.8")) {
		t.Fatal("精确授权的主机应允许解析到常规私网地址")
	}
	if policy.AllowsAddress("other.internal", netip.MustParseAddr("10.0.0.8")) {
		t.Fatal("同一私网地址不能扩散授权到其它主机")
	}
	if policy.AllowsAddress("proxy.internal", netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("loopback 即使主机获授权也必须拒绝")
	}
	if policy.AllowsAddress("proxy.internal", netip.MustParseAddr("fd00:ec2::254")) {
		t.Fatal("metadata 地址不属于私网逃生口")
	}
	if !policy.AllowsAddress("public.example", netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("普通公网地址应允许")
	}
	if policy.AllowsAddress("public.example", netip.MustParseAddr("203.0.113.1")) {
		t.Fatal("文档与特殊用途公网段必须拒绝")
	}
}
