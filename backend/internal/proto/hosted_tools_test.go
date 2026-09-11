package proto

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHostedToolKindRecognizesPublishedLanes(t *testing.T) {
	for _, kind := range []string{"web_search", "file_search", "tool_search", "code_interpreter", "mcp"} {
		if !HostedToolKind(kind) {
			t.Fatalf("%s should be a hosted lane", kind)
		}
	}
	if HostedToolKind("function") || HostedToolKind("not_a_real_lane") {
		t.Fatal("function/unknown must not count as hosted")
	}
}

func TestRejectHostedToolsOnFamilyFailClosed(t *testing.T) {
	tools := []CanonicalTool{{
		Name:        "web_search",
		Kind:        "web_search",
		Declaration: json.RawMessage(`{"type":"web_search","filters":{"allowed_domains":["example.com"]}}`),
	}}
	if err := RejectHostedToolsOnFamily("openai_responses", tools); err != nil {
		t.Fatalf("responses must keep hosted tools: %v", err)
	}
	if err := RejectHostedToolsOnFamily("openai_codex", tools); err != nil {
		t.Fatalf("codex responses-shaped must keep hosted tools: %v", err)
	}
	if err := RejectHostedToolsOnFamily("openai_chat", tools); err == nil || !errors.Is(err, ErrHostedToolRouteUnsupported) {
		t.Fatalf("chat must fail-closed, got %v", err)
	}
	if err := RejectHostedToolsOnFamily("anthropic_messages", tools); err == nil {
		t.Fatal("anthropic must fail-closed")
	}
}

func TestAddHostedCallUsageOnlyCompletedPublished(t *testing.T) {
	var u CanonicalUsage
	u = AddHostedCallUsage(u, "web_search_call", "completed")
	u = AddHostedCallUsage(u, "file_search_call", "")
	u = AddHostedCallUsage(u, "image_generation_call", "failed")
	u = AddHostedCallUsage(u, "mcp_call", "completed")
	if u.WebSearchCalls != 1 || u.FileSearchCalls != 1 || u.ImageGenerationCalls != 0 {
		t.Fatalf("usage=%+v", u)
	}
}
