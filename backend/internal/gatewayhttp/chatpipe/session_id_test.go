package chatpipe

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestRequestClientSessionIDPromptCacheKeyAfterExplicitIDs(t *testing.T) {
	body := []byte(`{"conversation_id":"conv-1","prompt_cache_key":"cache-1","session_id":"sess-1"}`)
	got := RequestClientSessionID(nil, proto.ClientProtocolOpenAIChat, body)
	if got != "conv-1" {
		t.Fatalf("conversation_id 必须仍赢 prompt_cache_key: got=%q", got)
	}

	body = []byte(`{"prompt_cache_key":"cache-1","session_id":"sess-1"}`)
	got = RequestClientSessionID(nil, proto.ClientProtocolOpenAIChat, body)
	if got != "sess-1" {
		t.Fatalf("session_id 必须仍赢 prompt_cache_key: got=%q", got)
	}

	body = []byte(`{"prompt_cache_key":"cache-1","metadata":{"user_id":"user_x__session_11111111-2222-3333-4444-555555555555"}}`)
	got = RequestClientSessionID(nil, proto.ClientProtocolOpenAIChat, body)
	if got != "cache-1" {
		t.Fatalf("prompt_cache_key 必须赢 metadata.user_id: got=%q", got)
	}
}

func TestRequestClientSessionIDPromptCacheKeyBeatsEmptyHashPath(t *testing.T) {
	body := []byte(`{"prompt_cache_key":"tenant-a:stable-prefix","messages":[{"role":"user","content":"hi"}]}`)
	got := RequestClientSessionID(nil, proto.ClientProtocolOpenAIChat, body)
	if got != "tenant-a:stable-prefix" {
		t.Fatalf("只有 prompt_cache_key 时应收下: got=%q", got)
	}
}

func TestRequestClientSessionIDClaudeHeaderAfterGenericHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Claude-Code-Session-Id", "claude-thread")
	got := RequestClientSessionID(req, proto.ClientProtocolAnthropicMessages, nil)
	if got != "claude-thread" {
		t.Fatalf("Claude 原生入口应认官方会话头: got=%q", got)
	}

	req.Header.Set("X-Session-ID", "generic-thread")
	got = RequestClientSessionID(req, proto.ClientProtocolAnthropicMessages, nil)
	if got != "generic-thread" {
		t.Fatalf("已有 X-Session-ID 必须仍赢 Claude 头: got=%q", got)
	}
}

func TestRequestClientSessionIDUnknownHeaderDoesNotWin(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Unknown-Session", "nope")
	body := []byte(`{"prompt_cache_key":"cache-2"}`)
	got := RequestClientSessionID(req, proto.ClientProtocolOpenAIChat, body)
	if got != "cache-2" {
		t.Fatalf("未登记的头不得抢走 prompt_cache_key: got=%q", got)
	}
}
