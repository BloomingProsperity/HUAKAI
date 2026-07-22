package claudecodecloak

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApply_ThirdPartySystemSinksToMessages(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet-4-20250514","max_tokens":100,"system":"你是客服机器人","messages":[{"role":"user","content":"hi"}]}`)
	res := Apply(in, Options{CLIVersion: "2.1.63"})
	if !res.Applied {
		t.Fatalf("expected applied, reason=%s", res.Reason)
	}
	var root map[string]any
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatal(err)
	}
	sys, ok := root["system"].([]any)
	if !ok || len(sys) != 3 {
		t.Fatalf("system blocks want 3, got %#v", root["system"])
	}
	b0 := sys[0].(map[string]any)
	if text, _ := b0["text"].(string); !strings.HasPrefix(text, "x-anthropic-billing-header:") {
		t.Fatalf("block0 billing missing: %v", b0["text"])
	}
	b1 := sys[1].(map[string]any)
	if b1["text"] != identityPrompt {
		t.Fatalf("block1 identity: %v", b1["text"])
	}
	b2 := sys[2].(map[string]any)
	if b2["text"] != expansionPrompt {
		t.Fatalf("block2 expansion: %v", b2["text"])
	}
	if _, ok := b2["cache_control"]; !ok {
		t.Fatal("block2 must carry cache_control")
	}
	msgs := root["messages"].([]any)
	if len(msgs) < 3 {
		t.Fatalf("messages should prepend instruction pair, got %d", len(msgs))
	}
	u0 := msgs[0].(map[string]any)
	if u0["role"] != "user" {
		t.Fatalf("first msg role want user, got %v", u0["role"])
	}
	// 原 system 必须仍可达
	if !strings.Contains(string(res.Body), "你是客服机器人") {
		t.Fatal("original system text must sink into messages")
	}
	// 变异刀：若只 EnsurePrefix 而不替换，system 字符串会仍含客服句在 system 顶层
	if sysStr, ok := root["system"].(string); ok && strings.Contains(sysStr, "客服") {
		t.Fatal("original system must not remain as system string")
	}
}

func TestApply_IdempotentWhenAlreadyCloaked(t *testing.T) {
	first := Apply([]byte(`{"model":"m","messages":[],"system":"x"}`), Options{})
	second := Apply(first.Body, Options{})
	if second.Applied {
		t.Fatalf("second apply should no-op, reason=%s", second.Reason)
	}
	if second.Reason != "already_cloaked" {
		t.Fatalf("reason=%s", second.Reason)
	}
}

func TestApply_EmptySystemStillCloaks(t *testing.T) {
	in := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	res := Apply(in, Options{})
	if !res.Applied {
		t.Fatalf("want applied, got %s", res.Reason)
	}
	var root map[string]any
	_ = json.Unmarshal(res.Body, &root)
	sys := root["system"].([]any)
	if len(sys) != 3 {
		t.Fatalf("want 3 blocks")
	}
	msgs := root["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("no sink when empty system, msgs=%d", len(msgs))
	}
}

func TestEnabled_DefaultOn(t *testing.T) {
	t.Setenv(EnvBodyCloak, "")
	if !Enabled() {
		t.Fatal("default must be enabled")
	}
	t.Setenv(EnvBodyCloak, "false")
	if Enabled() {
		t.Fatal("explicit false must disable")
	}
}
