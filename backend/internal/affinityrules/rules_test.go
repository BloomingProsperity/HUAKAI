package affinityrules

import "testing"

func TestAffinityGjsonKeySource(t *testing.T) {
	rules := AffinityRuleSet{{
		Name: "body-key",
		KeySources: []KeySource{{
			Type: "gjson",
			Path: "prompt_cache_key",
		}},
	}}

	ruleName, affinityKey, matched := rules.Match(MatchRequest{
		Body: []byte(`{"prompt_cache_key":"abc"}`),
	})

	if !matched {
		t.Fatal("Match matched=false want true")
	}
	if ruleName != "body-key" {
		t.Fatalf("ruleName=%q want body-key", ruleName)
	}
	// 变异:让 gjson 提取永远返回空;此断言必须失败,因为派生出的 key
	// 会变成空或无法匹配。
	if affinityKey != "abc" {
		t.Fatalf("affinityKey=%q want abc", affinityKey)
	}
}

func TestAffinityHeaderKeySource(t *testing.T) {
	rules := AffinityRuleSet{{
		Name: "header-key",
		KeySources: []KeySource{{
			Type: "request_header",
			Key:  "X-Affinity-Key",
		}},
	}}

	_, affinityKey, matched := rules.Match(MatchRequest{
		Header: func(name string) string {
			if name == "X-Affinity-Key" {
				return "hdr"
			}
			return ""
		},
	})

	if !matched {
		t.Fatal("Match matched=false want true")
	}
	// 变异:读取错误的请求头;此断言必须失败,因为期望的 key 只来源于
	// X-Affinity-Key。
	if affinityKey != "hdr" {
		t.Fatalf("affinityKey=%q want hdr", affinityKey)
	}
}

func TestAffinityModelRegexGate(t *testing.T) {
	rules := AffinityRuleSet{{
		Name:       "claude-models",
		ModelRegex: []string{"^claude-"},
		KeySources: []KeySource{{
			Type: "request_header",
			Key:  "X-Affinity-Key",
		}},
	}}
	header := func(name string) string {
		if name == "X-Affinity-Key" {
			return "stable"
		}
		return ""
	}

	_, key, matched := rules.Match(MatchRequest{Model: "claude-3", Header: header})
	if !matched || key != "stable" {
		t.Fatalf("claude request matched=%v key=%q want true/stable", matched, key)
	}

	// 变异:让 model 正则检查永远通过;gpt-4 会错误地匹配上这条
	// 仅限 claude 的规则。
	_, key, matched = rules.Match(MatchRequest{Model: "gpt-4", Header: header})
	if matched || key != "" {
		t.Fatalf("gpt request matched=%v key=%q want false/empty", matched, key)
	}
}

func TestEmptyAffinityRuleSetDoesNotMatch(t *testing.T) {
	_, key, matched := AffinityRuleSet(nil).Match(MatchRequest{
		Model: "claude-3",
		Header: func(string) string {
			return "stable"
		},
	})

	if matched || key != "" {
		t.Fatalf("empty rule set matched=%v key=%q want false/empty fallback", matched, key)
	}
}

func TestAffinityValueRegexAndRuleName(t *testing.T) {
	rules := AffinityRuleSet{{
		Name:            "tenant-a",
		KeySources:      []KeySource{{Type: "gjson", Path: "metadata.cache_key"}},
		ValueRegex:      `^prefix:([a-z0-9_-]+)$`,
		IncludeRuleName: true,
	}}

	ruleName, affinityKey, matched := rules.Match(MatchRequest{
		Body: []byte(`{"metadata":{"cache_key":"prefix:user_123"}}`),
	})

	if !matched {
		t.Fatal("Match matched=false want true")
	}
	if ruleName != "tenant-a" {
		t.Fatalf("ruleName=%q want tenant-a", ruleName)
	}
	if affinityKey != "tenant-a:user_123" {
		t.Fatalf("affinityKey=%q want tenant-a:user_123", affinityKey)
	}
}
