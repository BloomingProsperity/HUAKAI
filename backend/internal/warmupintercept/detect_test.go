// warmupintercept 测试 —— 全部从行为规格推导(三种 Claude Code 一次性请求
// 形状 + Anthropic Messages 应答形态),不依赖任何参照实现细节。
package warmupintercept

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func anthropicBody(t *testing.T, system []string, msgs ...[2]string) []byte {
	t.Helper()
	type text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type msg struct {
		Role    string `json:"role"`
		Content []text `json:"content"`
	}
	doc := struct {
		Messages []msg  `json:"messages"`
		System   []text `json:"system,omitempty"`
	}{}
	for _, m := range msgs {
		doc.Messages = append(doc.Messages, msg{Role: m[0], Content: []text{{Type: "text", Text: m[1]}}})
	}
	for _, s := range system {
		doc.System = append(doc.System, text{Type: "text", Text: s})
	}
	enc, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// MUTATION: Detect 任一形状判定条件松脱(丢 UA 门 / 丢末条 user 限定 / 丢
// 精确标记)→ 对应子断言红。
func TestDetectShapes(t *testing.T) {
	titleMsg := "Please write a 5-10 word title for the following conversation:\n\nhello"
	topicSys := "Analyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title."

	cases := []struct {
		name      string
		ua        bool
		model     string
		maxTokens int
		stream    bool
		body      []byte
		want      Kind
		wantHit   bool
	}{
		{"conn probe", true, "claude-haiku-4-5", 1, false, []byte(`{}`), KindConnProbe, true},
		{"conn probe needs UA", false, "claude-haiku-4-5", 1, false, []byte(`{}`), KindNone, false},
		{"conn probe needs haiku", true, "claude-opus-4-8", 1, false, []byte(`{}`), KindNone, false},
		{"conn probe needs max_tokens 1", true, "claude-haiku-4-5", 2, false, []byte(`{}`), KindNone, false},
		{"conn probe needs non-stream", true, "claude-haiku-4-5", 1, true, []byte(`{}`), KindNone, false},

		{"suggestion tail user", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", "[SUGGESTION MODE: continue]"}), KindSuggestion, true},
		{"suggestion not at tail", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", "[SUGGESTION MODE: x]"}, [2]string{"assistant", "ok"}), KindNone, false},
		{"suggestion tail must be user", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"assistant", "[SUGGESTION MODE: x]"}), KindNone, false},

		{"warmup title prompt", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", titleMsg}), KindWarmup, true},
		{"warmup exact Warmup", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", "Warmup"}), KindWarmup, true},
		{"warmup topic system", false, "m", 100, false,
			anthropicBody(t, []string{topicSys}, [2]string{"user", "hi"}), KindWarmup, true},
		{"Warmup substring not enough", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", "do a Warmup run"}), KindNone, false},
		{"normal chat with word title passes", false, "m", 100, false,
			anthropicBody(t, nil, [2]string{"user", "give my essay a title"}), KindNone, false},
		{"garbage body", false, "m", 100, false, []byte("not json [SUGGESTION MODE:"), KindNone, false},
	}
	for _, tc := range cases {
		kind, hit := Detect(tc.ua, tc.model, tc.maxTokens, tc.stream, tc.body)
		if kind != tc.want || hit != tc.wantHit {
			t.Fatalf("[%s] got (%v,%v) want (%v,%v)", tc.name, kind, hit, tc.want, tc.wantHit)
		}
	}
}

func TestIsClaudeCodeUserAgent(t *testing.T) {
	if !IsClaudeCodeUserAgent("claude-cli/2.1.0 (external)") {
		t.Fatal("claude-cli UA 应识别")
	}
	if IsClaudeCodeUserAgent("curl/8.0") {
		t.Fatal("普通 UA 不应识别")
	}
}

var msgIDPattern = regexp.MustCompile(`^msg_01[0-9A-Za-z]{16,}$`)

// MUTATION: 合成应答固定 mock ID / 错 stop_reason / 错文本 → 红。
// 客户端可见面必须拟真:随机 Anthropic 形态 ID,probe 回 "#" 且 max_tokens 截断。
func TestSyntheticNonStreamBody(t *testing.T) {
	cases := []struct {
		kind     Kind
		wantText string
		wantStop string
	}{
		{KindConnProbe, "#", "max_tokens"},
		{KindSuggestion, "", "end_turn"},
		{KindWarmup, "New Conversation", "end_turn"},
	}
	for _, tc := range cases {
		status, body := SyntheticNonStreamBody(tc.kind, "claude-haiku-4-5")
		if status != 200 {
			t.Fatalf("kind=%v status=%d", tc.kind, status)
		}
		var got struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Role    string `json:"role"`
			Model   string `json:"model"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason string `json:"stop_reason"`
			Usage      struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("kind=%v body 非法 JSON: %v\n%s", tc.kind, err, body)
		}
		if got.Type != "message" || got.Role != "assistant" || got.Model != "claude-haiku-4-5" {
			t.Fatalf("kind=%v 应答骨架错: %s", tc.kind, body)
		}
		if len(got.Content) != 1 || got.Content[0].Text != tc.wantText || got.StopReason != tc.wantStop {
			t.Fatalf("kind=%v 内容/stop 错: %s", tc.kind, body)
		}
		if !msgIDPattern.MatchString(got.ID) || strings.Contains(got.ID, "mock") {
			t.Fatalf("kind=%v 消息 ID 必须拟真随机(msg_01+base62,无 mock 指纹): %q", tc.kind, got.ID)
		}
		if got.Usage.OutputTokens <= 0 || got.Usage.InputTokens <= 0 {
			t.Fatalf("kind=%v usage 必须非零拟真: %s", tc.kind, body)
		}
	}
	// 两次生成 ID 必须不同(随机性)
	_, b1 := SyntheticNonStreamBody(KindWarmup, "m")
	_, b2 := SyntheticNonStreamBody(KindWarmup, "m")
	var m1, m2 struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b1, &m1)
	_ = json.Unmarshal(b2, &m2)
	if m1.ID == m2.ID {
		t.Fatalf("消息 ID 不应重复: %q", m1.ID)
	}
}

// MUTATION: SSE 事件序列缺段 / data 行非法 JSON / 文本拼不回 → 红。
func TestSyntheticStreamBody(t *testing.T) {
	raw := string(SyntheticStreamBody(KindWarmup, "claude-haiku-4-5"))

	wantOrder := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	pos := -1
	for _, ev := range wantOrder {
		idx := strings.Index(raw, "event: "+ev)
		if idx < 0 || idx < pos {
			t.Fatalf("SSE 事件 %s 缺失或乱序:\n%s", ev, raw)
		}
		pos = idx
	}

	var text strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("data 行非法 JSON: %v\n%s", err, payload)
		}
		if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
			text.WriteString(ev.Delta.Text)
		}
	}
	if text.String() != "New Conversation" {
		t.Fatalf("warmup 流式文本应拼出 New Conversation, got %q", text.String())
	}
}

func TestWriteStreamHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteStream(rec, KindWarmup, "m")
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type=%q", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("SSE 必须带 X-Accel-Buffering: no")
	}
	if rec.Code != 200 || rec.Body.Len() == 0 {
		t.Fatalf("code=%d bodyLen=%d", rec.Code, rec.Body.Len())
	}

	rec2 := httptest.NewRecorder()
	WriteNonStream(rec2, KindConnProbe, "m")
	if ct := rec2.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("non-stream Content-Type=%q", ct)
	}
}
