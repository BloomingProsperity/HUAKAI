package gateway

import (
	"encoding/json"
	"testing"
)

// TestMaybeInjectStreamUsageB1 咬住 B1:官方 OpenAI 兼容族的流式出站必须带
// stream_options.include_usage=true,才能拿上游流末权威 usage,避免估算计费。
// 反转 session 族与非流式请求不注入(真实性/无需)。
// 变异:让 maybeInjectStreamUsage 恒原样返回 → openai_chat 流式用例断言红。
func TestMaybeInjectStreamUsageB1(t *testing.T) {
	hasIncludeUsageTrue := func(body []byte) bool {
		var root map[string]json.RawMessage
		if json.Unmarshal(body, &root) != nil {
			return false
		}
		var opts struct {
			IncludeUsage bool `json:"include_usage"`
		}
		_ = json.Unmarshal(root["stream_options"], &opts)
		return opts.IncludeUsage
	}

	// 官方 openai 兼容族 + 流式 + 无 include_usage → 注入 true。
	out := maybeInjectStreamUsage("openai_chat", []byte(`{"model":"gpt-4o","stream":true,"messages":[]}`))
	if !hasIncludeUsageTrue(out) {
		t.Fatalf("openai_chat 流式应注入 include_usage=true,得 %s", out)
	}
	// kimi_chat 同理。
	out = maybeInjectStreamUsage("kimi_chat", []byte(`{"model":"k2","stream":true,"messages":[]}`))
	if !hasIncludeUsageTrue(out) {
		t.Fatalf("kimi_chat 流式应注入 include_usage=true,得 %s", out)
	}
	// 已有其它 stream_options 字段 → 保留并加 include_usage。
	out = maybeInjectStreamUsage("openai_chat", []byte(`{"stream":true,"stream_options":{"foo":1},"messages":[]}`))
	if !hasIncludeUsageTrue(out) {
		t.Fatalf("应保留既有 stream_options 并加 include_usage,得 %s", out)
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(out, &root)
	var so map[string]json.RawMessage
	_ = json.Unmarshal(root["stream_options"], &so)
	if _, ok := so["foo"]; !ok {
		t.Fatalf("既有 stream_options.foo 应保留,得 %s", out)
	}

	// 非流式 → 不注入(响应本就带 usage)。
	out = maybeInjectStreamUsage("openai_chat", []byte(`{"stream":false,"messages":[]}`))
	if hasIncludeUsageTrue(out) {
		t.Fatalf("非流式不应注入,得 %s", out)
	}
	// 反转 session 族(真实性)→ 不注入。
	out = maybeInjectStreamUsage("copilot_session", []byte(`{"stream":true,"messages":[]}`))
	if hasIncludeUsageTrue(out) {
		t.Fatalf("反转 session 族不应注入(要真实性),得 %s", out)
	}
	// 非 openai 兼容族(dify)→ 不注入。
	out = maybeInjectStreamUsage("dify_chat", []byte(`{"stream":true}`))
	if hasIncludeUsageTrue(out) {
		t.Fatalf("dify_chat 不应注入,得 %s", out)
	}
}
