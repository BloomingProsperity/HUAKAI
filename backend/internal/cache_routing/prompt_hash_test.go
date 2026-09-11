// prompt_hash_test.go — Track B 测试: stable prompt-prefix hash。
package cache_routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestComputePromptHash_StableForSameContent(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5-sonnet",
		"system":"You are a helpful assistant",
		"tools":[{"name":"calc","description":"calculator"}],
		"messages":[{"role":"user","content":"hi"}],
		"max_tokens":100
	}`)
	h1 := ComputePromptHash(body)
	h2 := ComputePromptHash(body)
	if h1 == "" || h1 != h2 {
		t.Errorf("同 body hash 应稳定, h1=%q h2=%q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("hash 长度=%d want 64 (sha256 hex)", len(h1))
	}
}

func TestComputePromptHash_TopLevelFieldOrderIgnored(t *testing.T) {
	// 顶层字段顺序不同应 hash 同 (用 map[string]json.RawMessage 抽取)
	a := []byte(`{"system":"x","tools":[],"messages":[]}`)
	b := []byte(`{"messages":[],"tools":[],"system":"x"}`)
	c := []byte(`{"tools":[],"system":"x","messages":[]}`)
	h1, h2, h3 := ComputePromptHash(a), ComputePromptHash(b), ComputePromptHash(c)
	if h1 != h2 || h1 != h3 {
		t.Errorf("顶层字段顺序变化不应改 hash: %q %q %q", h1, h2, h3)
	}
}

func TestComputePromptHash_DifferentSystemPromptDiffersHash(t *testing.T) {
	a := []byte(`{"system":"You are helpful","tools":[]}`)
	b := []byte(`{"system":"You are evil","tools":[]}`)
	if ComputePromptHash(a) == ComputePromptHash(b) {
		t.Errorf("不同 system 应不同 hash")
	}
}

func TestComputePromptHash_DifferentToolsDiffersHash(t *testing.T) {
	a := []byte(`{"system":"x","tools":[{"name":"calc"}]}`)
	b := []byte(`{"system":"x","tools":[{"name":"weather"}]}`)
	if ComputePromptHash(a) == ComputePromptHash(b) {
		t.Errorf("不同 tools 应不同 hash")
	}
}

func TestComputePromptHash_MessagesIgnored(t *testing.T) {
	// messages 不参与 hash → 同 conversation 不同轮次稳定路由
	a := []byte(`{"system":"x","tools":[],"messages":[{"role":"user","content":"q1"}]}`)
	b := []byte(`{"system":"x","tools":[],"messages":[{"role":"user","content":"q2"},{"role":"assistant","content":"a1"},{"role":"user","content":"q3"}]}`)
	if ComputePromptHash(a) != ComputePromptHash(b) {
		t.Errorf("messages 不应参与 hash (sticky cache routing 要求): %q vs %q",
			ComputePromptHash(a), ComputePromptHash(b))
	}
}

func TestComputePromptHash_EmptyOrInvalidReturnsEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"not json", []byte(`not json`)},
		{"non object array", []byte(`[1,2,3]`)},
		{"non object scalar", []byte(`"string"`)},
		{"null literal", []byte(`null`)},
		{"object missing system+tools", []byte(`{"messages":[],"max_tokens":100}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputePromptHash(tc.in); got != PromptHashEmpty {
				t.Errorf("应空, 得 %q", got)
			}
		})
	}
}

func TestComputePromptHash_OnlySystemNoTools(t *testing.T) {
	body := []byte(`{"system":"You are helpful","messages":[]}`)
	got := ComputePromptHash(body)
	if got == PromptHashEmpty {
		t.Errorf("仅有 system 应能 hash")
	}
	if !strings.HasPrefix(got, "") || len(got) != 64 {
		t.Errorf("hash 形态错: %q", got)
	}
}

func TestComputePromptHash_OnlyToolsNoSystem(t *testing.T) {
	body := []byte(`{"tools":[{"name":"calc"}],"messages":[]}`)
	if ComputePromptHash(body) == PromptHashEmpty {
		t.Errorf("仅有 tools 应能 hash")
	}
}

// TestComputePromptHash_FieldNamePrefixPreventsCollision 验证内部用
// "system:" / "|tools:" 字段前缀防碰撞设计：
// (system="x", tools=null) 与 (system=null, tools="x") 不应共享 hash。
func TestComputePromptHash_FieldNamePrefixPreventsCollision(t *testing.T) {
	a := []byte(`{"system":"x"}`)
	b := []byte(`{"tools":"x"}`) // 罕见但不是错——tools 通常是 array
	h1, h2 := ComputePromptHash(a), ComputePromptHash(b)
	if h1 == h2 {
		t.Errorf("system='x' 与 tools='x' 应 hash 不同 (字段前缀防碰撞), 得相同 %q", h1)
	}
}

func TestComputePromptHash_NestedSystemBlocksRespected(t *testing.T) {
	// system 可以是 string 或 array of content blocks. 嵌套结构作 raw bytes 进 hash
	a := []byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}],"tools":[]}`)
	b := []byte(`{"system":[{"type":"text","text":"x"}],"tools":[]}`)
	if ComputePromptHash(a) == ComputePromptHash(b) {
		t.Errorf("不同 cache_control 标记应 hash 不同 (嵌套 raw 字节进 hash)")
	}
}

func TestComputePromptHash_TopLevelSystemKeepsLegacyDigest(t *testing.T) {
	body := []byte(`{"system":"You are helpful","tools":[{"name":"calc"}],"messages":[{"role":"system","content":"must-not-override"},{"role":"user","content":"hi"}]}`)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte("system:"))
	h.Write(top["system"])
	h.Write([]byte("|tools:"))
	h.Write(top["tools"])
	want := hex.EncodeToString(h.Sum(nil))
	if got := ComputePromptHash(body); got != want {
		t.Fatalf("顶层 system 必须保持旧 digest, 不得被 messages 改写: got %q want %q", got, want)
	}
}

func TestComputePromptHash_OpenAILeadingSystemIgnoresUserTurns(t *testing.T) {
	t.Parallel()
	a := []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"policy"},{"role":"user","content":"q1"}]}`)
	b := []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"policy"},{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"}]}`)
	loser := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"q1"}]}`)
	ha, hb := ComputePromptHash(a), ComputePromptHash(b)
	if ha == PromptHashEmpty || ha != hb {
		t.Fatalf("OpenAI leading system 应在不同用户轮次上稳定: %q vs %q", ha, hb)
	}
	if got := ComputePromptHash(loser); got != PromptHashEmpty {
		t.Fatalf("只有 user 消息的基线必须空 hash, 得 %q", got)
	}
	other := []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"other"},{"role":"user","content":"q1"}]}`)
	if ComputePromptHash(other) == ha {
		t.Fatal("不同 system 文本必须得到不同 hash")
	}
}

func TestComputePromptHash_OpenAIDeveloperCountsAsPrefix(t *testing.T) {
	t.Parallel()
	a := []byte(`{"messages":[{"role":"developer","content":"rules"},{"role":"user","content":"q1"}]}`)
	b := []byte(`{"messages":[{"role":"developer","content":"rules"},{"role":"user","content":"q2"}]}`)
	if ComputePromptHash(a) == PromptHashEmpty || ComputePromptHash(a) != ComputePromptHash(b) {
		t.Fatalf("developer 前缀应稳定: %q vs %q", ComputePromptHash(a), ComputePromptHash(b))
	}
}

func TestComputePromptHash_SystemAfterUserIsNotPrefix(t *testing.T) {
	t.Parallel()
	body := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"late"}]}`)
	if got := ComputePromptHash(body); got != PromptHashEmpty {
		t.Fatalf("user 之后的 system 不是稳定前缀, 得 %q", got)
	}
}

func TestComputePromptHash_GeminiSystemInstructionStableAcrossTurns(t *testing.T) {
	t.Parallel()
	a := []byte(`{"systemInstruction":{"parts":[{"text":"be concise"}]},"contents":[{"role":"user","parts":[{"text":"q1"}]}]}`)
	b := []byte(`{"systemInstruction":{"parts":[{"text":"be concise"}]},"contents":[{"role":"user","parts":[{"text":"q2"}]}]}`)
	ha, hb := ComputePromptHash(a), ComputePromptHash(b)
	if ha == PromptHashEmpty || ha != hb {
		t.Fatalf("Gemini systemInstruction 应跨轮稳定: %q vs %q", ha, hb)
	}
	other := []byte(`{"systemInstruction":{"parts":[{"text":"be verbose"}]},"contents":[{"role":"user","parts":[{"text":"q1"}]}]}`)
	if ComputePromptHash(other) == ha {
		t.Fatal("不同 systemInstruction 必须得到不同 hash")
	}
	if got := ComputePromptHash([]byte(`{"contents":[{"role":"user","parts":[{"text":"q1"}]}]}`)); got != PromptHashEmpty {
		t.Fatalf("无 systemInstruction 的 generateContent 必须空 hash, 得 %q", got)
	}
}

func TestComputePromptHash_ResponsesInstructionsStableAcrossInput(t *testing.T) {
	t.Parallel()
	a := []byte(`{"model":"gpt-4o","instructions":"follow policy","input":"hi"}`)
	b := []byte(`{"model":"gpt-4o","instructions":"follow policy","input":"later turn","previous_response_id":"resp_1"}`)
	ha, hb := ComputePromptHash(a), ComputePromptHash(b)
	if ha == PromptHashEmpty || ha != hb {
		t.Fatalf("Responses instructions 应跨 input 稳定: %q vs %q", ha, hb)
	}
	if got := ComputePromptHash([]byte(`{"model":"gpt-4o","input":"hi"}`)); got != PromptHashEmpty {
		t.Fatalf("无 instructions 的 Responses 必须空 hash, 得 %q", got)
	}
}

func TestComputePromptHash_TopLevelSystemBeatsOtherPrefixes(t *testing.T) {
	t.Parallel()
	withSystem := []byte(`{"system":"A","systemInstruction":{"parts":[{"text":"B"}]},"instructions":"C","messages":[{"role":"system","content":"D"}]}`)
	onlyA := []byte(`{"system":"A"}`)
	if ComputePromptHash(withSystem) != ComputePromptHash(onlyA) {
		t.Fatal("出现顶层 system 时不得回退到其它协议前缀")
	}
}

func TestComputePromptHash_ExplicitNullSystemDoesNotFallback(t *testing.T) {
	t.Parallel()
	body := []byte(`{"system":null,"messages":[{"role":"system","content":"from-messages"}]}`)
	onlyNull := []byte(`{"system":null}`)
	if ComputePromptHash(body) != ComputePromptHash(onlyNull) {
		t.Fatal("显式 system=null 不得回退到 messages 前缀")
	}
}

func TestComputePromptHash_ToolsOnlyWithoutPrefixKeepsLegacyDigest(t *testing.T) {
	t.Parallel()
	body := []byte(`{"tools":[{"name":"calc"}],"messages":[{"role":"user","content":"hi"}]}`)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte("system:"))
	h.Write([]byte("|tools:"))
	h.Write(top["tools"])
	want := hex.EncodeToString(h.Sum(nil))
	if got := ComputePromptHash(body); got != want {
		t.Fatalf("仅 tools、无 system 前缀必须保持旧 digest: got %q want %q", got, want)
	}
}

func TestComputePromptHash_StopsAtFirstNonPrefixRole(t *testing.T) {
	t.Parallel()
	a := []byte(`{"messages":[{"role":"system","content":"keep"},{"role":"user","content":"q1"}]}`)
	b := []byte(`{"messages":[{"role":"system","content":"keep"},{"role":"user","content":"q1"},{"role":"system","content":"late"}]}`)
	if ComputePromptHash(a) != ComputePromptHash(b) {
		t.Fatal("夹在对话中的后置 system 不得进入 hash")
	}
}

func TestComputePromptHash_MultipleLeadingPrefixMessages(t *testing.T) {
	t.Parallel()
	a := []byte(`{"messages":[{"role":"system","content":"a"},{"role":"developer","content":"b"},{"role":"user","content":"q1"}]}`)
	b := []byte(`{"messages":[{"role":"system","content":"a"},{"role":"developer","content":"b"},{"role":"user","content":"q2"}]}`)
	onlyFirst := []byte(`{"messages":[{"role":"system","content":"a"},{"role":"user","content":"q1"}]}`)
	ha := ComputePromptHash(a)
	if ha == PromptHashEmpty || ha != ComputePromptHash(b) {
		t.Fatalf("连续 system/developer 前缀应跨轮稳定: %q vs %q", ha, ComputePromptHash(b))
	}
	if ComputePromptHash(onlyFirst) == ha {
		t.Fatal("少一段 leading prefix 必须得到不同 hash")
	}
}
