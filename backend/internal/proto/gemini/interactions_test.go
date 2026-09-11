package gemini

import (
	"bytes"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestInteractionsInputAndOfficialResponseRoundTrip(t *testing.T) {
	t.Parallel()
	messages, ok := interactionsInputMessages([]byte(`{"model":"gemini-3.6-flash","input":"hello gateway"}`))
	if !ok || len(messages) != 1 || messages[0].Content[0].Text != "hello gateway" {
		t.Fatalf("string input 应变成一条用户文本: %+v ok=%v", messages, ok)
	}

	raw := []byte(`{"id":"v1_abc","object":"interaction","status":"completed","model":"gemini-3.6-flash","usage":{"total_tokens":197,"total_input_tokens":8,"total_output_tokens":12},"steps":[{"type":"model_output","content":[{"type":"text","text":"hi"}]}]}`)
	resp, ok := interactionCanonicalResponse(raw)
	if !ok {
		t.Fatal("官方 Interaction 响应必须被识别")
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 12 || resp.Usage.TotalTokens != 197 {
		t.Fatalf("usage 映射错误: %+v", resp.Usage)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "hi" {
		t.Fatalf("model_output 文本丢失: %+v", resp.Content)
	}
	if !bytes.Equal(officialInteractionRaw(&resp), raw) {
		t.Fatal("必须原样保留官方响应供回写客户端")
	}

	generate := []byte(`{"candidates":[{"content":{"parts":[{"text":"old"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	if _, ok := interactionCanonicalResponse(generate); ok {
		t.Fatal("不得把 generateContent 误判成 Interaction")
	}
}

func TestInteractionsPathDetection(t *testing.T) {
	t.Parallel()
	if !IsInteractionsPath("/v1beta/interactions") || !IsInteractionsPath("/v1beta/interactions/v1_abc") {
		t.Fatal("官方会话 path 识别失败")
	}
	if IsInteractionsPath("/v1beta/models/gemini-pro:generateContent") {
		t.Fatal("不得把 generateContent 认成会话协议")
	}
}

func TestGeminiClientAcceptsInteractionsInput(t *testing.T) {
	t.Parallel()
	client := &GeminiClient{}
	ctx := proto.ContextWithRequestMetaSeed(t.Context(), proto.RequestMetaSeed{
		RequestID:      "req-1",
		TenantID:       1,
		Model:          "gemini-3.6-flash",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/interactions",
	})
	env, _, err := client.RequestToCanonical(ctx, []byte(`{"model":"gemini-3.6-flash","input":"ping"}`))
	if err != nil {
		t.Fatalf("会话请求应能规范化: %v", err)
	}
	if len(env.Messages) != 1 || env.Messages[0].Content[0].Text != "ping" {
		t.Fatalf("规范化消息=%+v", env.Messages)
	}

	raw := []byte(`{"id":"v1_x","object":"interaction","status":"completed","model":"gemini-3.6-flash","usage":{"total_input_tokens":3,"total_output_tokens":4,"total_tokens":7},"steps":[{"type":"model_output","content":[{"type":"text","text":"pong"}]}]}`)
	upstream := &Adapter{}
	respEnv, _, err := upstream.ProviderResponseToCanonical(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := client.CanonicalToClientResponse(ctx, respEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(body), raw) {
		t.Fatalf("客户端必须拿到官方 Interaction JSON, got %s", body)
	}

	empty, _, err := client.RequestToCanonical(ctx, nil)
	if err != nil {
		t.Fatalf("空 body 检索必须能规范化: %v", err)
	}
	if empty.RequestMeta.Model != "gemini-3.6-flash" {
		t.Fatalf("检索 model=%q", empty.RequestMeta.Model)
	}
}
