package outboundbody

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestBuildPlan_OfficialDirectSkipAll(t *testing.T) {
	p := BuildPlan(Input{
		OfficialDirect: true,
		ProtocolFamily: "anthropic_messages",
		AccountType:    credentialstore.AuthModeClaudeAIOAuth,
	})
	if !p.SkipAll || p.SystemCloak || p.IdentityUserID {
		t.Fatalf("official direct must SkipAll only, got %+v", p)
	}
}

func TestBuildPlan_ReverseAnthropicCloak(t *testing.T) {
	t.Setenv("HUAKAI_CLAUDE_OAUTH_BODY_CLOAK", "true")
	p := BuildPlan(Input{
		OfficialDirect: false,
		ProtocolFamily: "anthropic_claude_session",
		AccountType:    credentialstore.AuthModeClaudeAIOAuth,
		AccountID:      7,
	})
	if p.SkipAll || !p.SystemCloak || !p.IdentityUserID {
		t.Fatalf("reverse anthropic want cloak+identity, got %+v", p)
	}
}

func TestBuildPlan_APIKeyNoCloak(t *testing.T) {
	t.Setenv("HUAKAI_CLAUDE_OAUTH_BODY_CLOAK", "true")
	p := BuildPlan(Input{
		ProtocolFamily: "anthropic_messages",
		AccountType:    credentialstore.AuthModeAPIKey,
	})
	if p.SystemCloak {
		t.Fatal("api_key must never system-cloak")
	}
	if !p.IdentityUserID {
		// identity still attempted but fail-open for non-reverse
		t.Fatal("identity flag still set for anthropic family; fail-open inside")
	}
}

func TestApply_SkipAll(t *testing.T) {
	in := []byte(`{"system":"x"}`)
	out := Apply(in, Plan{SkipAll: true})
	if string(out) != string(in) {
		t.Fatalf("skip all must preserve bytes")
	}
	out[0] = 'X'
	if in[0] == 'X' {
		t.Fatal("must clone")
	}
}

func TestApply_SystemCloak(t *testing.T) {
	in := []byte(`{"model":"m","system":"业务指令","messages":[{"role":"user","content":"hi"}]}`)
	out := Apply(in, Plan{SystemCloak: true, CLIVersion: "2.1.63"})
	if !strings.Contains(string(out), "x-anthropic-billing-header:") {
		t.Fatalf("want billing, got %s", out)
	}
	if !strings.Contains(string(out), "业务指令") {
		t.Fatal("want sunk system")
	}
}
