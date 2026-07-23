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

// TestComputeFingerprint_KnownVectors 用离线预计算的已知向量锁死真实 CLI 指纹算法
// (盐 + 首 user 文本第 4/7/20 字节 + cc_version → SHA256 取前 3 位)。
//
// 变异证伪(每处偏差都会让某条断言变红):
//   - 改盐 / 改索引 / 改截断长度 → "ba4" 对不上;
//   - 算法不再依赖文本(旧实现的 bug)→ vec1 == vec2;
//   - 算法不再依赖版本 → vec1 == vec3;
//   - 截成 4 字节(旧实现的 8-hex bug)→ 长度断言变红。
func TestComputeFingerprint_KnownVectors(t *testing.T) {
	const text = "Hello, this is a test message for fingerprinting."
	v1 := computeFingerprint(text, "2.1.63")
	if v1 != "ba4" {
		t.Fatalf("已知向量对不上:want ba4 got %q(盐/索引/算法被改动?)", v1)
	}
	if len(v1) != 3 {
		t.Fatalf("指纹必须是 3 位十六进制,got %d 位", len(v1))
	}
	if v2 := computeFingerprint("hi", "2.1.63"); v2 == v1 {
		t.Fatalf("指纹必须依赖首 user 文本,vec1==vec2=%q", v1)
	}
	if v3 := computeFingerprint(text, "2.1.99"); v3 == v1 {
		t.Fatalf("指纹必须依赖 cc_version,vec1==vec3=%q", v1)
	}
}

// TestApply_BillingFingerprintFromFirstUserText 端到端证明 Apply 把真实算法指纹写进
// billing 块,且用【system 下沉前】的首条 user 文本计算。
func TestApply_BillingFingerprintFromFirstUserText(t *testing.T) {
	in := []byte(`{"model":"m","system":"业务指令","messages":[{"role":"user","content":"Hello, this is a test message for fingerprinting."}]}`)
	res := Apply(in, Options{CLIVersion: "2.1.63"})
	if !res.Applied {
		t.Fatalf("want applied, reason=%s", res.Reason)
	}
	if !strings.Contains(string(res.Body), "cc_version=2.1.63.ba4; cc_entrypoint=cli;") {
		t.Fatalf("billing 块指纹不符(应为真实算法结果 ba4),body=%s", res.Body)
	}
}
