package imageshttp

import (
	"encoding/json"
	"testing"
)

func TestNormalizeGPTImageRequestRemovesRetiredB64Field(t *testing.T) {
	raw, err := normalizeGPTImageRequest(
		[]byte(`{"model":"gpt-image-1","prompt":"red dot","response_format":"b64_json","size":"1024x1024"}`),
		"gpt-image-1", "b64_json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["response_format"]; exists {
		t.Fatalf("已退役字段仍存在：%s", raw)
	}
	if fields["model"] != "gpt-image-1" || fields["prompt"] != "red dot" || fields["size"] != "1024x1024" {
		t.Fatalf("有效图片参数被改写：%s", raw)
	}
}

func TestNormalizeGPTImageRequestRejectsURLWithoutChangingDALLERoute(t *testing.T) {
	if _, err := normalizeGPTImageRequest([]byte(`{"response_format":"url"}`), "gpt-image-2", "url"); err == nil {
		t.Fatal("GPT Image 的 url 语义无法满足时必须显式拒绝")
	}
	raw := []byte(`{"model":"dall-e-3","response_format":"url"}`)
	got, err := normalizeGPTImageRequest(raw, "dall-e-3", "url")
	if err != nil || string(got) != string(raw) {
		t.Fatalf("DALL·E 协议不得被 GPT Image 兼容逻辑改写：got=%s err=%v", got, err)
	}
}
