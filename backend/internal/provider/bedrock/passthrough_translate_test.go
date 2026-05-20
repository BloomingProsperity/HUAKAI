// passthrough_translate_test.go — A8 闭环测试: PassthroughAdapter 在
// AutoTranslateAnthropicAPIBody=true 时把 Anthropic API body 翻译为
// Bedrock body 并签名/路由。
package bedrock

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
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

// AutoTranslate=true 但 body 非 Anthropic 形态
// （包括非 JSON 垃圾数据）→ 不报错，body 原样发送，让 Bedrock server-side 拒收。
// 这与"AutoTranslate 仅对 Anthropic Messages 形态生效"语义一致。
func TestAutoTranslate_InvalidBody_PassThrough(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	garbage := []byte(`not valid json`)
	in := validSigV4Input()
	in.InboundBody = garbage

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("非 Anthropic 形态 body 不应报错（应原样透传由 server-side 拒）: %v", err)
	}
	gotBody, _ := io.ReadAll(req.Body)
	if string(gotBody) != string(garbage) {
		t.Errorf("body 应原样透传\n  原: %s\n  得: %s", garbage, gotBody)
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

// AutoTranslate=true 但 credential 是
// upstream_passthrough（caller 已签名）时不能改 body — 否则 SigV4 hash 失配。
func TestAutoTranslate_SkipsForUpstreamPassthrough(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	preSignedBody := []byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":1024,"anthropic_version":"bedrock-2023-05-31"}`)
	in := provider.BuildInput{
		UpstreamModelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		InboundBody:     preSignedBody,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "AWS4-HMAC-SHA256 Credential=...",
			Extra: map[string]string{"aws_region": "us-east-1"},
		},
	}

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	gotBody, _ := io.ReadAll(req.Body)
	if string(gotBody) != string(preSignedBody) {
		t.Errorf("upstream_passthrough 应原样发送 body，但被改了\n  原 body: %s\n  发出去: %s",
			preSignedBody, gotBody)
	}
}

// AutoTranslate=true 但 body 不是 Anthropic Messages
// 形态（如 Cohere / Llama / Titan）时不能盲翻译 — 否则会注入无关 anthropic_version
// 字段，可能破坏目标 vendor 的解析。
func TestAutoTranslate_SkipsForNonAnthropicShape(t *testing.T) {
	a := newTestAdapter()
	a.AutoTranslateAnthropicAPIBody = true

	// Llama on Bedrock 形态：用 prompt 字段，没有 messages
	llamaBody := []byte(`{"prompt":"hello world","max_gen_len":256,"temperature":0.5}`)
	in := validSigV4Input()
	in.UpstreamModelID = "meta.llama3-70b-instruct-v1:0"
	in.InboundBody = llamaBody

	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	gotBody, _ := io.ReadAll(req.Body)
	if string(gotBody) != string(llamaBody) {
		t.Errorf("非 Anthropic 形态 body 应原样发送，但被翻译了\n  原 body: %s\n  发出去: %s",
			llamaBody, gotBody)
	}
}

// IsAnthropicMessagesShape 直接单测。
func TestIsAnthropicMessagesShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"non-json", "not json", false},
		{"null", "null", false},
		{"array top-level", `[1,2,3]`, false},
		{"anthropic shape", `{"messages":[{"role":"user","content":"hi"}]}`, true},
		{"anthropic with extras", `{"model":"x","messages":[]}`, true},
		{"empty messages", `{"messages":[]}`, true},
		{"null messages", `{"messages":null}`, true},
		{"llama shape", `{"prompt":"hi","max_gen_len":100}`, false},
		{"cohere shape", `{"message":"hi","temperature":0.5}`, false},
		{"titan shape", `{"inputText":"hi","textGenerationConfig":{}}`, false},
	}
	for _, c := range cases {
		got := IsAnthropicMessagesShape([]byte(c.body))
		if got != c.want {
			t.Errorf("IsAnthropicMessagesShape(%s)=%v want %v", c.name, got, c.want)
		}
	}
}
