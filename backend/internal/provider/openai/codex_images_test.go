package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCodexSessionAdapterConvertsImagesRequestToResponsesTool(t *testing.T) {
	adapter := &CodexSessionAdapter{}
	req, err := adapter.BuildRequest(context.Background(), provider.BuildInput{
		EndpointPath:    "/v1/images/generations",
		UpstreamModelID: "gpt-image-1",
		InboundBody: []byte(`{
			"model":"gpt-image-1","prompt":"画一个红色方块","size":"1024x1024",
			"quality":"high","background":"transparent","n":2
		}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "session-token",
			Extra: map[string]string{"codex_image_base_model": "gpt-5.5"},
		},
	})
	if err != nil {
		t.Fatalf("构造 Codex 图片请求: %v", err)
	}
	if req.URL.String() != defaultCodexEndpoint {
		t.Fatalf("上游 URL=%q，期望 Responses 端点 %q", req.URL.String(), defaultCodexEndpoint)
	}
	if req.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("Accept=%q，期望 text/event-stream", req.Header.Get("Accept"))
	}
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("解析出站请求: %v", err)
	}
	if body["model"] != "gpt-5.5" || body["stream"] != true || body["store"] != false {
		t.Fatalf("Responses 顶层合同错误: %s", raw)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools=%v，期望单个生图工具", body["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "image_generation" || tool["action"] != "generate" || tool["model"] != "gpt-image-1" {
		t.Fatalf("生图工具合同错误: %v", tool)
	}
	if tool["size"] != "1024x1024" || tool["quality"] != "high" || tool["background"] != "transparent" || tool["n"] != float64(2) {
		t.Fatalf("生图工具参数丢失: %v", tool)
	}
	inputs, _ := body["input"].([]any)
	message, _ := inputs[0].(map[string]any)
	content, _ := message["content"].([]any)
	text, _ := content[0].(map[string]any)
	if text["text"] != "画一个红色方块" {
		t.Fatalf("prompt 未进入 Responses input: %v", content)
	}
}

func TestTranslateCodexImagesResponseUsesCompletedOutputAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"created_at":123,"output":[{"type":"image_generation_call","result":"aGVsbG8=","revised_prompt":"clean","output_format":"webp","size":"1024x1024","quality":"high","background":"transparent"}],"tool_usage":{"image_gen":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"image_tokens":3}}}}}`,
		``,
	}, "\n")

	raw, err := TranslateCodexImagesResponse([]byte(sse), "b64_json")
	if err != nil {
		t.Fatalf("翻译 Codex 图片响应: %v", err)
	}
	var response struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		OutputFormat string `json:"output_format"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("解析公开图片响应: %v", err)
	}
	if response.Created != 123 || len(response.Data) != 1 || response.Data[0].B64JSON != "aGVsbG8=" || response.Data[0].RevisedPrompt != "clean" {
		t.Fatalf("图片结果错误: %s", raw)
	}
	if response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 11 || response.OutputFormat != "webp" {
		t.Fatalf("图片用量或格式错误: %s", raw)
	}
}

func TestTranslateCodexImagesResponseFallsBackToOutputItemDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","result":"aW1hZ2U=","output_format":"png"}}`,
		`data: {"type":"response.completed","response":{"created_at":456,"output":[],"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"image_tokens":0}}}}`,
		``,
	}, "\n")
	raw, err := TranslateCodexImagesResponse([]byte(sse), "url")
	if err != nil {
		t.Fatalf("翻译 fallback 图片响应: %v", err)
	}
	if !strings.Contains(string(raw), `"url":"data:image/png;base64,aW1hZ2U="`) {
		t.Fatalf("URL 形图片响应错误: %s", raw)
	}
}

func TestTranslateCodexImagesResponseRejectsIncompleteStream(t *testing.T) {
	_, err := TranslateCodexImagesResponse([]byte(`data: {"type":"response.created"}\n\n`), "")
	if err == nil {
		t.Fatal("缺少 response.completed 的流不应被当成成功图片")
	}
}
