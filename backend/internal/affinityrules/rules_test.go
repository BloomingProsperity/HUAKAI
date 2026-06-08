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
	// MUTATION: make gjson extraction always return empty; this assertion
	// must fail because the derived key would be empty or unmatched.
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
	// MUTATION: read the wrong request header; this must fail because the
	// expected key is sourced only from X-Affinity-Key.
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

	// MUTATION: make model regex checks always pass; gpt-4 would incorrectly
	// match this claude-only rule.
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
