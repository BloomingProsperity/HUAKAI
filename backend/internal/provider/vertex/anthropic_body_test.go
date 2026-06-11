package vertex

import (
	"encoding/json"
	"testing"
)

// TestReshapeAnthropicBodyStripsModelAndStreamInjectsVersion 抓的回归:reshape
// 必须剥顶层 model + stream 并注 anthropic_version=vertex-2023-10-16。
// 判别性:对比逐字段——若漏删 model（Vertex 400 duplicate model）、漏删 stream
// （上游拒）、漏注 version（Vertex 400 missing anthropic_version），断言红。
// Mutation:删 delete(raw,"model") → model 残留断言红；删 version 注入 → 断言红。
func TestReshapeAnthropicBodyStripsModelAndStreamInjectsVersion(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-1","stream":true,"max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"system":"be brief"}`)
	out, err := reshapeAnthropicBody(in)
	if err != nil {
		t.Fatalf("reshapeAnthropicBody: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not JSON object: %v\n%s", err, out)
	}
	if _, has := got["model"]; has {
		t.Errorf("model 未剥离（Vertex URL 已含 model，body 不能再带）: %s", out)
	}
	if _, has := got["stream"]; has {
		t.Errorf("stream 未剥离（流式由 URL action 决定）: %s", out)
	}
	var ver string
	if err := json.Unmarshal(got["anthropic_version"], &ver); err != nil || ver != AnthropicVersionVertex {
		t.Errorf("anthropic_version=%q want %q (err=%v)", ver, AnthropicVersionVertex, err)
	}
	// carry-over 字段必须原样保留。
	if _, has := got["max_tokens"]; !has {
		t.Errorf("max_tokens 被误删: %s", out)
	}
	if _, has := got["messages"]; !has {
		t.Errorf("messages 被误删: %s", out)
	}
	if _, has := got["system"]; !has {
		t.Errorf("system 被误删: %s", out)
	}
}

// TestReshapeAnthropicBodyRespectsCallerAnthropicVersion 抓的回归:caller 已显式
// 声明 anthropic_version 时不覆盖。
func TestReshapeAnthropicBodyRespectsCallerAnthropicVersion(t *testing.T) {
	in := []byte(`{"model":"x","anthropic_version":"vertex-custom","messages":[]}`)
	out, err := reshapeAnthropicBody(in)
	if err != nil {
		t.Fatalf("reshapeAnthropicBody: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	var ver string
	_ = json.Unmarshal(got["anthropic_version"], &ver)
	if ver != "vertex-custom" {
		t.Errorf("caller anthropic_version 被覆盖: got %q want vertex-custom", ver)
	}
}

// TestReshapeAnthropicBodyFailClosedOnBadJSON 抓的回归:坏 body 必须 fail-closed,
// 绝不把坏 body 发出去（否则上游 400 + 计费错乱）。
// 判别性:每个 case 期望 error,正确实现返回 error;若改成"解析失败就原样透传"
// 则这些 case 全部不报错、断言红。
func TestReshapeAnthropicBodyFailClosedOnBadJSON(t *testing.T) {
	cases := map[string][]byte{
		"empty":     []byte(``),
		"truncated": []byte(`{"model":"x"`),
		"null":      []byte(`null`),
		"array":     []byte(`[1,2,3]`),
		"string":    []byte(`"just a string"`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := reshapeAnthropicBody(body); err == nil {
				t.Errorf("reshapeAnthropicBody(%s) 应 fail-closed 报 error，实际返回 nil error", name)
			}
		})
	}
}
