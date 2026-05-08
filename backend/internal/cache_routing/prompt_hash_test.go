// prompt_hash_test.go — Track B 测试: stable prompt-prefix hash。
package cache_routing

import (
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
