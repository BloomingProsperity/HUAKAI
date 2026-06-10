package bedrock

import (
	"encoding/json"
	"strings"
	"testing"
)

// 判别测试:真实 Claude Code 形态 body(必带 metadata.user_id)翻译到 Bedrock
// 后,Bedrock 不识别的顶层字段必须剥除——不剥则 Bedrock 400 ValidationException,
// 整条 Bedrock CC 闭环对真实 CC 流量坏死(delta-mine #4,参照 sub2api bf28a009)。
// Mutation guard: 去掉 delete(raw,"metadata") / 白名单过滤 → 对应断言红。
func TestTranslateAnthropicAPIToBedrock_StripsBedrockUnknownTopLevel(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus",
		"max_tokens": 100,
		"metadata": {"user_id": "cc-session-abc"},
		"anthropic_beta": ["context-management-2025-06-27", "totally-unsupported-beta"],
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	res, err := TranslateAnthropicAPIToBedrock(body)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := out["metadata"]; ok {
		t.Fatal("metadata 必须剥除(Bedrock 对未知顶层字段 400)")
	}
	betas, ok := out["anthropic_beta"]
	if !ok {
		t.Fatal("白名单内 beta token 应保留")
	}
	if !strings.Contains(string(betas), "context-management-2025-06-27") || strings.Contains(string(betas), "totally-unsupported") {
		t.Fatalf("beta 白名单过滤错: %s", betas)
	}
	if _, ok := out["messages"]; !ok {
		t.Fatal("messages 等合法字段不得误删(U7 透传语义保持)")
	}
	if _, ok := out["anthropic_version"]; !ok {
		t.Fatal("anthropic_version 注入丢失")
	}
}

// 全不支持的 beta → 字段整体删除(防泄漏 400)。
func TestTranslateAnthropicAPIToBedrock_DropsAllUnsupportedBetaField(t *testing.T) {
	body := []byte(`{"model":"claude-opus","anthropic_beta":["nope-1","nope-2"],"messages":[{"role":"user","content":"x"}]}`)
	res, err := TranslateAnthropicAPIToBedrock(body)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(res.Body, &out)
	if _, ok := out["anthropic_beta"]; ok {
		t.Fatal("全不支持的 anthropic_beta 字段必须整体删除")
	}
}
