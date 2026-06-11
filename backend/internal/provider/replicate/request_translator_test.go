package replicate

import (
	"encoding/json"
	"errors"
	"testing"
)

func translatedInput(t *testing.T, inbound string) map[string]any {
	t.Helper()
	out, err := TranslateImageRequest([]byte(inbound))
	if err != nil {
		t.Fatalf("TranslateImageRequest: %v", err)
	}
	var body struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("产物不是 JSON: %v\n%s", err, out)
	}
	if body.Input == nil {
		t.Fatalf("产物缺 input 包装: %s", out)
	}
	return body.Input
}

// TestTranslateImageRequestPromptOnlyKnownFieldsMapped 抓的回归:model/未知
// 顶层字段被原样塞进 input(Replicate input 是 model-specific,塞了未知 key
// 上游 422),或 prompt 没进 input(用户内容到不了模型)。
func TestTranslateImageRequestPromptOnlyKnownFieldsMapped(t *testing.T) {
	input := translatedInput(t, `{"model":"owner/name","prompt":"a red fox","user":"u1","style":"vivid"}`)
	if got := input["prompt"]; got != "a red fox" {
		t.Fatalf("input.prompt=%v want a red fox", got)
	}
	if len(input) != 1 {
		t.Fatalf("input 应只含 prompt,实际 %v(未知顶层字段必须丢弃)", input)
	}
}

// TestTranslateImageRequestNMapsToNumOutputs 抓的回归:n 丢失(用户付 n 张
// 的钱只生成 1 张)或字段名漂移。
func TestTranslateImageRequestNMapsToNumOutputs(t *testing.T) {
	input := translatedInput(t, `{"prompt":"p","n":3}`)
	if got := input["num_outputs"]; got != float64(3) {
		t.Fatalf("input.num_outputs=%v want 3", got)
	}
	if _, has := input["n"]; has {
		t.Fatalf("OpenAI 字段名 n 不应出现在 input: %v", input)
	}
}

// TestTranslateImageRequestStandardSizesMapToAspectRatio 抓的回归:三档标准
// 尺寸映射表漂移(方图发成横图)。
func TestTranslateImageRequestStandardSizesMapToAspectRatio(t *testing.T) {
	for size, want := range map[string]string{
		"1024x1024": "1:1",
		"1792x1024": "16:9",
		"1024x1792": "9:16",
	} {
		input := translatedInput(t, `{"prompt":"p","size":"`+size+`"}`)
		if got := input["aspect_ratio"]; got != want {
			t.Fatalf("size=%s aspect_ratio=%v want %s", size, got, want)
		}
		if _, has := input["width"]; has {
			t.Fatalf("标准档 %s 不应携带 custom width: %v", size, input)
		}
	}
}

// TestTranslateImageRequestCustomSizeSnapsAndClamps 抓的回归:snap 步进或
// clamp 边界漂移。600 是 32 步进的判别值:32 步进 snap→608,16 步进→600,
// 64 步进→576;改步进本断言必红。100/2000 钉 clamp 下/上界。
func TestTranslateImageRequestCustomSizeSnapsAndClamps(t *testing.T) {
	cases := []struct {
		size          string
		width, height float64
	}{
		{"800x600", 800, 608},
		{"100x2000", 256, 1440},
		{"512x512", 512, 512},
	}
	for _, tc := range cases {
		input := translatedInput(t, `{"prompt":"p","size":"`+tc.size+`"}`)
		if got := input["aspect_ratio"]; got != "custom" {
			t.Fatalf("size=%s aspect_ratio=%v want custom", tc.size, got)
		}
		if got := input["width"]; got != tc.width {
			t.Fatalf("size=%s width=%v want %v", tc.size, got, tc.width)
		}
		if got := input["height"]; got != tc.height {
			t.Fatalf("size=%s height=%v want %v", tc.size, got, tc.height)
		}
	}
}

// TestTranslateImageRequestUnparseableSizeFailsLoud 抓的回归:坏尺寸被静默
// 丢弃,计费档位与实际产出错位。
func TestTranslateImageRequestUnparseableSizeFailsLoud(t *testing.T) {
	for _, size := range []string{"banana", "1024", "0x512", "-2x512"} {
		if _, err := TranslateImageRequest([]byte(`{"prompt":"p","size":"` + size + `"}`)); err == nil {
			t.Fatalf("size=%q 应 fail-loud", size)
		}
	}
}

// TestTranslateImageRequestQualityMapsToPromptUpsampling 抓的回归:hd/high
// 不再触发 prompt_upsampling,或 standard 误触发。
func TestTranslateImageRequestQualityMapsToPromptUpsampling(t *testing.T) {
	for _, quality := range []string{"hd", "high", "HD"} {
		input := translatedInput(t, `{"prompt":"p","quality":"`+quality+`"}`)
		if got := input["prompt_upsampling"]; got != true {
			t.Fatalf("quality=%s prompt_upsampling=%v want true", quality, got)
		}
	}
	input := translatedInput(t, `{"prompt":"p","quality":"standard"}`)
	if _, has := input["prompt_upsampling"]; has {
		t.Fatalf("quality=standard 不应触发 prompt_upsampling: %v", input)
	}
}

// TestTranslateImageRequestClientInputNonBillingPassthrough 抓的回归:客户端
// 显式 input 的非计费轴 key(model-specific 调参 seed/guidance 等)被吞。
func TestTranslateImageRequestClientInputNonBillingPassthrough(t *testing.T) {
	input := translatedInput(t, `{"prompt":"p","n":2,"input":{"seed":42,"guidance_scale":3.5}}`)
	if got := input["seed"]; got != float64(42) {
		t.Fatalf("input.seed=%v want 42(非计费轴客户端 input 直传)", got)
	}
	if got := input["guidance_scale"]; got != 3.5 {
		t.Fatalf("input.guidance_scale=%v want 3.5", got)
	}
	if got := input["num_outputs"]; got != float64(2) {
		t.Fatalf("input.num_outputs=%v want 2(由顶层 n 决定)", got)
	}
}

// TestTranslateImageRequestClientInputCannotOverrideBillingAxis 抓的回归(S1
// 漏钱):客户端顶层 input 覆盖计费承重轴(num_outputs/width/height/
// aspect_ratio/size),使出站量与计费量脱钩——可发 n=1 + input.num_outputs=4
// 计 1 张出 4 张,绕 amount_range/quota。计费轴必须由顶层 n/size 单一掌控,
// input 里的同名 key 一律剔除。
// Mutation:删 request_translator 的 billingAxisInputKeys 剔除分支 → 本测试红。
func TestTranslateImageRequestClientInputCannotOverrideBillingAxis(t *testing.T) {
	// n=1 但 input 试图放大 num_outputs=4 → 必须保持 1(顶层 n)
	input := translatedInput(t, `{"prompt":"p","n":1,"input":{"num_outputs":4}}`)
	if got := input["num_outputs"]; got != float64(1) {
		t.Fatalf("input.num_outputs=%v want 1(客户端不得放大计费轴,否则漏钱)", got)
	}
	// size 1024x1024 → aspect_ratio 1:1;input 试图覆盖成 16:9 必须无效
	sized := translatedInput(t, `{"prompt":"p","size":"1024x1024","input":{"aspect_ratio":"16:9","width":1440,"height":1440}}`)
	if got := sized["aspect_ratio"]; got != "1:1" {
		t.Fatalf("aspect_ratio=%v want 1:1(由顶层 size 决定,input 不得覆盖档位)", got)
	}
	if _, has := sized["width"]; has {
		t.Fatalf("input.width 不得覆盖标准档尺寸: %v", sized["width"])
	}
}

// TestTranslateImageRequestB64JSONRejected 抓的回归:b64_json 被静默降级成
// url(客户端拿到与请求形态不符的响应)。
func TestTranslateImageRequestB64JSONRejected(t *testing.T) {
	_, err := TranslateImageRequest([]byte(`{"prompt":"p","response_format":"b64_json"}`))
	if !errors.Is(err, ErrB64JSONResponseFormat) {
		t.Fatalf("err=%v want ErrB64JSONResponseFormat", err)
	}
	if _, err := TranslateImageRequest([]byte(`{"prompt":"p","response_format":"url"}`)); err != nil {
		t.Fatalf("response_format=url 应放行: %v", err)
	}
}

// TestTranslateImageRequestInvalidInputsFailLoud 抓的回归:坏 JSON / 空
// prompt / 非法 n 被静默接受后发给上游。
func TestTranslateImageRequestInvalidInputsFailLoud(t *testing.T) {
	for name, body := range map[string]string{
		"坏 JSON":    `{`,
		"缺 prompt":  `{"n":1}`,
		"空 prompt":  `{"prompt":"  "}`,
		"n 为 0":     `{"prompt":"p","n":0}`,
		"input 非对象": `{"prompt":"p","input":"raw"}`,
	} {
		if _, err := TranslateImageRequest([]byte(body)); err == nil {
			t.Fatalf("%s 应 fail-loud: %s", name, body)
		}
	}
}
