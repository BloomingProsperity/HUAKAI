package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestGeminiRequestToCanonicalAndResponseShape(t *testing.T) {
	client := &GeminiClient{}
	ctx := proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req-gemini-1",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/models/gemini-pro:generateContent",
		Model:          "gemini-pro",
	})

	raw := []byte(`{
		"contents": [
			{"role": "user", "parts": [{"text": "hello gemini"}]}
		],
		"systemInstruction": {"parts": [{"text": "be concise"}]},
		"generationConfig": {"temperature": 0.25}
	}`)

	env, losses, err := client.RequestToCanonical(ctx, raw)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("losses=%+v want none for text-only request", losses)
	}
	if env.RequestMeta.Model != "gemini-pro" {
		t.Fatalf("RequestMeta.Model=%q want gemini-pro", env.RequestMeta.Model)
	}
	if len(env.Messages) != 1 || env.Messages[0].Role != "user" || len(env.Messages[0].Content) != 1 {
		t.Fatalf("messages=%+v want one user text message", env.Messages)
	}
	if got := env.Messages[0].Content[0].Text; got != "hello gemini" {
		t.Fatalf("message text=%q want hello gemini", got)
	}
	if env.RequestControls.Temperature == nil || *env.RequestControls.Temperature != 0.25 {
		t.Fatalf("temperature=%v want 0.25", env.RequestControls.Temperature)
	}
	if env.RequestControls.SystemPrompt != "be concise" {
		t.Fatalf("system prompt=%q want be concise", env.RequestControls.SystemPrompt)
	}

	responseEnv := proto.NewEmptyEnvelope()
	responseEnv.RequestMeta.Model = "gemini-pro"
	responseEnv.BufferedResponse = &proto.CanonicalResponse{
		Model:      "gemini-pro",
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "hello back"}},
		StopReason: proto.CanonicalStopEndTurn,
		Usage:      proto.CanonicalUsage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8},
	}
	body, responseLosses, err := client.CanonicalToClientResponse(ctx, responseEnv)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(responseLosses) != 0 {
		t.Fatalf("response losses=%+v want none for text-only response", responseLosses)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("response json: %v\n%s", err, string(body))
	}
	if len(out.Candidates) != 1 || len(out.Candidates[0].Content.Parts) != 1 {
		t.Fatalf("candidates=%+v want one text part", out.Candidates)
	}
	if out.Candidates[0].Content.Parts[0].Text != "hello back" {
		t.Fatalf("response text=%q want hello back", out.Candidates[0].Content.Parts[0].Text)
	}
	if out.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("finishReason=%q want STOP", out.Candidates[0].FinishReason)
	}
	if out.UsageMetadata.PromptTokenCount != 3 || out.UsageMetadata.CandidatesTokenCount != 5 || out.UsageMetadata.TotalTokenCount != 8 {
		t.Fatalf("usage=%+v want 3/5/8", out.UsageMetadata)
	}
}

func TestGeminiPartVideoMetadataPassthrough(t *testing.T) {
	client := &GeminiClient{}
	ctx := proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req-gemini-video-part",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/models/gemini-pro:generateContent",
		Model:          "gemini-pro",
	})

	raw := []byte(`{
		"contents": [{
			"role": "user",
			"parts": [{
				"text": "summarize the clip",
				"videoMetadata": {"startOffset":"1s","endOffset":"4s"},
				"mediaResolution": "MEDIA_RESOLUTION_LOW"
			}]
		}]
	}`)

	env, losses, err := client.RequestToCanonical(ctx, raw)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if len(env.Messages) != 1 || len(env.Messages[0].Content) != 1 || env.Messages[0].Content[0].Text != "summarize the clip" {
		t.Fatalf("messages=%+v want text part still projected", env.Messages)
	}
	if env.Passthrough == nil {
		t.Fatalf("Passthrough nil; MUTATION: dropping videoMetadata leaves no canonical passthrough")
	}
	assertGeminiRawJSONEqual(t, env.Passthrough.Extra["contents[0].parts[0].videoMetadata"], `{"startOffset":"1s","endOffset":"4s"}`)
	assertGeminiRawJSONEqual(t, env.Passthrough.Extra["contents[0].parts[0].mediaResolution"], `"MEDIA_RESOLUTION_LOW"`)

	var sawVideoMetadata, sawMediaResolution bool
	for _, loss := range losses {
		if loss.Severity != proto.ProtocolLossInfo {
			continue
		}
		switch loss.Code {
		case "gemini_part_video_metadata_passthrough":
			sawVideoMetadata = true
		case "gemini_part_media_resolution_passthrough":
			sawMediaResolution = true
		}
	}
	if !sawVideoMetadata || !sawMediaResolution {
		t.Fatalf("losses=%+v want ProtocolLossInfo for passthrough-only Gemini part fields", losses)
	}
}

func assertGeminiRawJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotBuf bytes.Buffer
	if err := json.Compact(&gotBuf, got); err != nil {
		t.Fatalf("got invalid JSON %q: %v", string(got), err)
	}
	var wantBuf bytes.Buffer
	if err := json.Compact(&wantBuf, []byte(want)); err != nil {
		t.Fatalf("want invalid JSON %q: %v", want, err)
	}
	if !bytes.Equal(gotBuf.Bytes(), wantBuf.Bytes()) {
		t.Fatalf("JSON mismatch got=%s want=%s", gotBuf.String(), wantBuf.String())
	}
}
