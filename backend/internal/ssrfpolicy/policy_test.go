package ssrfpolicy

import "testing"

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

// SEC-084: the master kill-switch must force-deny a host that the per-host
// allowlist would otherwise admit, while leaving the default/enabled cases
// exactly as before. The fixture pins the SAME allowlisted host across all three
// toggle states so the toggle's effect is isolated.
// MUTATION: drop the `if p.privateIPsDisabled { return false }` guard and the
// disabled case admits the allowlisted host -> RED.
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

	// Default (unset) and explicit-true must keep the allowlist behaviour: the
	// same host is admitted. This proves the kill-switch is the only difference.
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
