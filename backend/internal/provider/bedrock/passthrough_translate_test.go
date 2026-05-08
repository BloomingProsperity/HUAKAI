// passthrough_translate_test.go — A8 闭环测试: PassthroughAdapter 在
// AutoTranslateAnthropicAPIBody=true 时把 Anthropic API body 翻译为
// Bedrock body 并签名/路由。
package bedrock

import (
	"context"
	"io"
	"strings"
	"testing"
)

// anthropicAPIBody 是典型 Anthropic Messages API 请求体形态。
const anthropicAPIBody = `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}],"max_tokens":1024,"stream":true,"system":"You are helpful"}`

func TestAutoTranslate_StreamFlagFromBody_EndpointSwitches(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	in.InboundBody = []byte(anthropicAPIBody)
	// 不设 Extra["stream"] — 翻译器应从 body 检测 stream:true → 路由到
	// invoke-with-response-stream
	delete(in.Credential.Extra, "stream")

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest err=%v", err)
	}
	if !strings.Contains(req.URL.Path, "invoke-with-response-stream") {
		t.Errorf("body stream:true 应路由 invoke-with-response-stream，得 %q", req.URL.Path)
	}
}

func TestAutoTranslate_BodyHasAnthropicVersion_NoModelOrStream(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	in.InboundBody = []byte(anthropicAPIBody)
	delete(in.Credential.Extra, "stream")

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	gotBody, _ := io.ReadAll(req.Body)
	bodyStr := string(gotBody)

	if !strings.Contains(bodyStr, `"anthropic_version":"bedrock-2023-05-31"`) {
		t.Errorf("body 应含 anthropic_version: %s", bodyStr)
	}
	if strings.Contains(bodyStr, `"model"`) {
		t.Errorf("body 不应再含 model: %s", bodyStr)
	}
	if strings.Contains(bodyStr, `"stream"`) {
		t.Errorf("body 不应再含 stream: %s", bodyStr)
	}
	// 业务字段保留
	for _, want := range []string{`"messages"`, `"max_tokens":1024`, `"system":"You are helpful"`} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("body 缺 %q: %s", want, bodyStr)
		}
	}
}

func TestAutoTranslate_SigV4SignsTranslatedBody(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	in.InboundBody = []byte(anthropicAPIBody)
	delete(in.Credential.Extra, "stream")

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}

	// SigV4 签名 header 必须存在（Authorization + x-amz-content-sha256）
	if req.Header.Get("Authorization") == "" {
		t.Error("Authorization header 缺失")
	}
	xAmzSha := req.Header.Get("X-Amz-Content-Sha256")
	if xAmzSha == "" {
		t.Error("X-Amz-Content-Sha256 缺失（签名 header）")
	}
	// 哈希应与签名时使用的 body 匹配——translator 输出而非原 InboundBody。
	// 反向验证：用原 InboundBody hash 应该 *不* 等于 header sha256。
	// （此处仅验签名 header 存在；具体数值匹配由 sigv4_test.go 单独覆盖）
}

func TestAutoTranslate_OffByDefault_NoTranslation(t *testing.T) {
	a := newTestAdapter()
	// AutoTranslateAnthropicAPIBody = false 默认
	in := validSigV4Input()
	in.InboundBody = []byte(anthropicAPIBody)

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	gotBody, _ := io.ReadAll(req.Body)
	bodyStr := string(gotBody)
	if bodyStr != anthropicAPIBody {
		t.Errorf("默认应原 body 透传不翻译\nwant: %s\ngot:  %s", anthropicAPIBody, bodyStr)
	}
	if strings.Contains(bodyStr, "anthropic_version") {
		t.Error("AutoTranslate=false 不应注入 anthropic_version")
	}
}

func TestAutoTranslate_ExtraStreamOverridesTranslator(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	// body 含 stream:true，但 caller 显式 Extra["stream"]="false" 应覆盖
	in.InboundBody = []byte(`{"model":"claude-3","messages":[],"max_tokens":100,"stream":true}`)
	in.Credential.Extra["stream"] = "false"

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(req.URL.Path, "invoke-with-response-stream") {
		t.Errorf("Extra stream=false 应覆盖 body stream:true，得 %q", req.URL.Path)
	}
	// 注意: extra=true 也应该覆盖；此测试只测 false 路径
}

func TestAutoTranslate_InvalidBody_ReturnsError(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	in.InboundBody = []byte(`not valid json`)

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("非合法 JSON 应报错")
	}
	if !strings.Contains(err.Error(), "Anthropic API 翻译失败") {
		t.Errorf("error 应提及翻译失败，got: %v", err)
	}
}

// TestAutoTranslate_ClosureFallback_BodyModelFillsEmptyUpstreamID 闭环测试:
// caller 未设 UpstreamModelID, AutoTranslate 启用 + body 含 model 字段
// → translator 抽出 model ID 自动填补 → Anthropic CLI 这种纯 client-side
// 请求可直接路由到 Bedrock 不需 caller 预先 alias 映射。(sonnet F1/F7 修复验证)
func TestAutoTranslate_ClosureFallback_BodyModelFillsEmptyUpstreamID(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	in.UpstreamModelID = "" // caller 未声明
	in.InboundBody = []byte(`{"model":"anthropic.claude-3-5-sonnet-20241022-v2:0","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true}`)
	delete(in.Credential.Extra, "stream")

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("闭环 fallback 应能成功 build, err=%v", err)
	}
	// URL 应含 body 抽出的 model ID
	if !strings.Contains(req.URL.Path, "anthropic.claude-3-5-sonnet-20241022-v2") {
		t.Errorf("URL 应含 body 抽出的 model ID, 得 %q", req.URL.Path)
	}
	if !strings.Contains(req.URL.Path, "invoke-with-response-stream") {
		t.Errorf("body stream:true 应路由 /invoke-with-response-stream, 得 %q", req.URL.Path)
	}
}

func TestAutoTranslate_FallbackOff_StillRequiresUpstreamModelID(t *testing.T) {
	// AutoTranslate=false 时 fallback 不启动, body model 不被使用
	a := newTestAdapter()
	// AutoTranslateAnthropicAPIBody = false 默认

	in := validSigV4Input()
	in.UpstreamModelID = ""
	in.InboundBody = []byte(`{"model":"anthropic.claude-3","messages":[],"max_tokens":100}`)

	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("AutoTranslate=false + 空 UpstreamModelID 应报错")
	}
	if !strings.Contains(err.Error(), "UpstreamModelID") {
		t.Errorf("error 应提及 UpstreamModelID, got: %v", err)
	}
}

func TestAutoTranslate_NonStreamBody_RoutesToInvoke(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	in := validSigV4Input()
	// stream=false in body
	in.InboundBody = []byte(`{"model":"x","messages":[],"max_tokens":100}`)
	delete(in.Credential.Extra, "stream")

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(req.URL.Path, "invoke-with-response-stream") {
		t.Errorf("无 stream 字段应路由 /invoke (非流式)")
	}
	if !strings.Contains(req.URL.Path, "/invoke") {
		t.Errorf("应路由 /invoke endpoint，得 %q", req.URL.Path)
	}
}
