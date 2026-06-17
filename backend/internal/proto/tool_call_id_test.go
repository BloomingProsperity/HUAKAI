package proto

import (
	"strings"
	"testing"
)

// TestSynthesizeCanonicalCallIDPreservesNonPrefixedID 守 S1-1/S2-6 核心修复：
// 上游(Mistral/Qwen/GLM 等 OpenAI 兼容供应商)发出的无前缀 tool-call id 必须被保留成
// 一个非空、可反向翻译的 canonical id，而不是丢成空串。
// Mutation: 若 SynthesizeCanonicalCallID 退回返回 ""（旧 bug 行为）→ 此处断言 RED。
func TestSynthesizeCanonicalCallIDPreservesNonPrefixedID(t *testing.T) {
	// Mistral 风格 9 字符无前缀 id：应原样保留进 canonical 后缀。
	got := SynthesizeCanonicalCallID("9aBc12345")
	if got != "call_9aBc12345" {
		t.Fatalf("bare id should be preserved as call_<id>, got %q", got)
	}
	if got == "" {
		t.Fatalf("synthesized id must never be empty (the S1-1 defect)")
	}
	// 合成值必须是格式合法的 canonical id：能通过 FromCanonicalCallID 校验(后缀属 [A-Za-z0-9_-]、非空)。
	// 注意：这是"格式可反向翻译"校验，不代表真实 egress 回程——实际 tool_result 回程在 marshal 层
	// 直接透传 id、不调用 FromCanonicalCallID(见 S3 deferred：严格 OpenAI 兼容上游多轮前缀未闭合)。
	back, err := FromCanonicalCallID(got, UpstreamProtocolOpenAI)
	if err != nil {
		t.Fatalf("synthesized canonical id must be a well-formed reverse-translatable id: %v", err)
	}
	if back != "call_9aBc12345" {
		t.Fatalf("OpenAI reverse translation mismatch: %q", back)
	}
}

// TestSynthesizeCanonicalCallIDSanitizesIllegalChars 守：含非法字符的 id 被清洗到
// canonical 后缀字符集 [A-Za-z0-9_-]，结果仍可反向翻译(不会带非法字符污染下游)。
// Mutation: 若 sanitizeCallIDSuffix 改成原样透传非法字符 → FromCanonicalCallID 校验失败 RED。
func TestSynthesizeCanonicalCallIDSanitizesIllegalChars(t *testing.T) {
	got := SynthesizeCanonicalCallID("ab.cd:ef/12") // '.' ':' '/' 非法
	if got != "call_abcdef12" {
		t.Fatalf("illegal chars should be stripped, got %q", got)
	}
	if _, err := FromCanonicalCallID(got, UpstreamProtocolOpenAI); err != nil {
		t.Fatalf("sanitized id must be reverse-translatable: %v", err)
	}
}

// TestSynthesizeCanonicalCallIDHashFallbackWhenAllIllegal 守：当原 id 清洗后为空
// (全是非法字符)，回退到确定性哈希而非产出 "call_"（空后缀，会被 FromCanonicalCallID 拒）。
// Mutation: 删掉 hash 回退分支 → suffix="" → 返回 "call_" → FromCanonicalCallID RED。
func TestSynthesizeCanonicalCallIDHashFallbackWhenAllIllegal(t *testing.T) {
	got := SynthesizeCanonicalCallID("。。。") // 全非法
	if got == "call_" || got == "" {
		t.Fatalf("all-illegal id must fall back to a non-empty hashed suffix, got %q", got)
	}
	if !strings.HasPrefix(got, "call_") {
		t.Fatalf("fallback must keep canonical call_ prefix, got %q", got)
	}
	if _, err := FromCanonicalCallID(got, UpstreamProtocolOpenAI); err != nil {
		t.Fatalf("hashed fallback id must be reverse-translatable: %v", err)
	}
	// 确定性：同输入两次必须一致(供 tool_result 回程稳定关联)。
	if again := SynthesizeCanonicalCallID("。。。"); again != got {
		t.Fatalf("hash fallback must be deterministic: %q vs %q", got, again)
	}
}
