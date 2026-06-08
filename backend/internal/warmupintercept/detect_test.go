package warmupintercept

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- helper to build a raw request body ---

type testMsg struct {
	Role    string        `json:"role"`
	Content []testContent `json:"content"`
}

type testContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func buildBody(messages []testMsg, system []string) []byte {
	type body struct {
		Messages []testMsg           `json:"messages"`
		System   []map[string]string `json:"system,omitempty"`
	}
	b := body{Messages: messages}
	for _, s := range system {
		b.System = append(b.System, map[string]string{"type": "text", "text": s})
	}
	enc, _ := json.Marshal(b)
	return enc
}

func buildBodyMaxTokens(model string, maxTokens int, stream bool, messages []testMsg) []byte {
	type body struct {
		Model     string    `json:"model"`
		MaxTokens int       `json:"max_tokens"`
		Stream    bool      `json:"stream"`
		Messages  []testMsg `json:"messages"`
	}
	b := body{Model: model, MaxTokens: maxTokens, Stream: stream, Messages: messages}
	enc, _ := json.Marshal(b)
	return enc
}

// ===== SHAPE 1: Connectivity probe =====

func TestDetect_ConnProbe_OK(t *testing.T) {
	body := buildBodyMaxTokens("claude-3-haiku-20240307", 1, false, []testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Hi"}}},
	})
	kind, ok := Detect(true, "claude-3-haiku-20240307", 1, false, body)
	if !ok || kind != KindConnProbe {
		t.Fatalf("want KindConnProbe/true, got kind=%v ok=%v", kind, ok)
	}
}

func TestDetect_ConnProbe_NoUA_NotIntercepted(t *testing.T) {
	body := buildBodyMaxTokens("claude-3-haiku-20240307", 1, false, []testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Hi"}}},
	})
	_, ok := Detect(false, "claude-3-haiku-20240307", 1, false, body)
	if ok {
		t.Fatal("non-ClaudeCode UA conn-probe shape must not be intercepted")
	}
}

// Near-miss: max_tokens=1 but non-haiku model must NOT be intercepted.

func TestDetect_ConnProbe_NearMiss_NonHaikuModel(t *testing.T) {
	body := buildBodyMaxTokens("claude-opus-4-5", 1, false, []testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Hello world"}}},
	})
	_, ok := Detect(true, "claude-opus-4-5", 1, false, body)
	if ok {
		t.Fatal("max_tokens=1 + non-haiku model must NOT be intercepted (near-miss)")
	}
}

// ===== SHAPE 2: Suggestion mode =====

func TestDetect_SuggestionMode_OK(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "[SUGGESTION MODE: autocomplete] write me a function"}}},
	}, nil)
	kind, ok := Detect(false, "claude-sonnet-4-5", 1000, false, body)
	if !ok || kind != KindSuggestion {
		t.Fatalf("want KindSuggestion/true, got kind=%v ok=%v", kind, ok)
	}
}

func TestDetect_SuggestionMode_NearMiss_WrongPrefix(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "[SUGGESTION give me ideas"}}},
	}, nil)
	_, ok := Detect(false, "claude-sonnet-4-5", 1000, false, body)
	if ok {
		t.Fatal("[SUGGESTION without MODE: suffix must NOT be intercepted (near-miss)")
	}
}

// ===== SHAPE 3: Warmup / title-generation =====

func TestDetect_Warmup_TitlePromptInMessage(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Please write a 5-10 word title for the following conversation: user: hello"}}},
	}, nil)
	kind, ok := Detect(false, "claude-haiku-3-5", 100, false, body)
	if !ok || kind != KindWarmup {
		t.Fatalf("want KindWarmup/true, got kind=%v ok=%v", kind, ok)
	}
}

func TestDetect_Warmup_ExactWarmupMessage(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Warmup"}}},
	}, nil)
	kind, ok := Detect(false, "claude-sonnet-4-5", 500, false, body)
	if !ok || kind != KindWarmup {
		t.Fatalf("want KindWarmup/true, got kind=%v ok=%v", kind, ok)
	}
}

func TestDetect_Warmup_SystemTopicExtraction(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "What is Go?"}}},
	}, []string{
		"Analyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title",
	})
	kind, ok := Detect(false, "claude-sonnet-4-5", 100, false, body)
	if !ok || kind != KindWarmup {
		t.Fatalf("want KindWarmup/true, got kind=%v ok=%v", kind, ok)
	}
}

// ===== NO FALSE POSITIVES: normal request must NEVER be intercepted =====

func TestDetect_NormalRequest_NotIntercepted(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "What is the capital of France?"}}},
	}, nil)
	_, ok := Detect(true, "claude-sonnet-4-5", 1000, false, body)
	if ok {
		t.Fatal("normal request must NOT be intercepted")
	}
}

func TestDetect_NormalRequest_Stream_NotIntercepted(t *testing.T) {
	body := buildBody([]testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Write me a poem"}}},
	}, nil)
	_, ok := Detect(true, "claude-haiku-3-5", 4096, true, body)
	if ok {
		t.Fatal("normal streaming request must NOT be intercepted")
	}
}

func TestDetect_MaxTokensOneHaiku_Streaming_NotIntercepted(t *testing.T) {
	// max_tokens=1 + haiku + stream=true is NOT a conn probe (stream disqualifies it)
	body := buildBodyMaxTokens("claude-3-haiku-20240307", 1, true, []testMsg{
		{Role: "user", Content: []testContent{{Type: "text", Text: "Hi"}}},
	})
	_, ok := Detect(true, "claude-3-haiku-20240307", 1, true, body)
	if ok {
		t.Fatal("streaming haiku max_tokens=1 must NOT be intercepted")
	}
}

// ===== SyntheticNonStreamBody =====

func TestSyntheticNonStreamBody_ConnProbe(t *testing.T) {
	status, body := SyntheticNonStreamBody(KindConnProbe, "claude-3-haiku-20240307")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, _ := resp["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("content must be non-empty")
	}
	block, _ := content[0].(map[string]interface{})
	if block["text"] != "#" {
		t.Fatalf("conn probe synthetic text must be '#', got %q", block["text"])
	}
	if resp["stop_reason"] != "max_tokens" {
		t.Fatalf("conn probe stop_reason must be 'max_tokens', got %q", resp["stop_reason"])
	}
}

func TestSyntheticNonStreamBody_Suggestion(t *testing.T) {
	_, body := SyntheticNonStreamBody(KindSuggestion, "claude-sonnet-4-5")
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, _ := resp["content"].([]interface{})
	block, _ := content[0].(map[string]interface{})
	if block["text"] != "" {
		t.Fatalf("suggestion synthetic text must be empty, got %q", block["text"])
	}
}

func TestSyntheticNonStreamBody_Warmup(t *testing.T) {
	_, body := SyntheticNonStreamBody(KindWarmup, "claude-sonnet-4-5")
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, _ := resp["content"].([]interface{})
	block, _ := content[0].(map[string]interface{})
	if block["text"] != "New Conversation" {
		t.Fatalf("warmup synthetic text must be 'New Conversation', got %q", block["text"])
	}
}

// ===== SyntheticStreamBody =====

func TestSyntheticStreamBody_Warmup(t *testing.T) {
	body := SyntheticStreamBody(KindWarmup, "claude-sonnet-4-5")
	s := string(body)
	if !strings.Contains(s, "message_start") {
		t.Error("stream must contain message_start event")
	}
	if !strings.Contains(s, "message_stop") {
		t.Error("stream must contain message_stop event")
	}
	if !strings.Contains(s, "New") {
		t.Error("warmup stream must contain 'New' text delta")
	}
}

func TestSyntheticStreamBody_Suggestion(t *testing.T) {
	body := SyntheticStreamBody(KindSuggestion, "claude-sonnet-4-5")
	s := string(body)
	if !strings.Contains(s, "msg_mock_suggestion") {
		t.Error("suggestion stream must use msg_mock_suggestion id")
	}
}

// ===== WriteNonStream / WriteStream HTTP helpers =====

func TestWriteNonStream(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteNonStream(rec, KindWarmup, "claude-sonnet-4-5")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("want application/json, got %q", ct)
	}
}

func TestWriteStream(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteStream(rec, KindWarmup, "claude-sonnet-4-5")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("want text/event-stream, got %q", ct)
	}
}

// ===== IsClaudeCodeUserAgent =====

func TestIsClaudeCodeUserAgent(t *testing.T) {
	cases := []struct {
		ua   string
		want bool
	}{
		{"claude-cli/2.1.22 (external, cli)", true},
		{"claude-cli/1.0.62 (darwin; arm64)", true},
		{"Claude-Cli/2.0.0", true},
		{"curl/7.88.1", false},
		{"openai-node/4.0.0", false},
		{"", false},
	}
	for _, tc := range cases {
		got := IsClaudeCodeUserAgent(tc.ua)
		if got != tc.want {
			t.Errorf("IsClaudeCodeUserAgent(%q) = %v, want %v", tc.ua, got, tc.want)
		}
	}
}
