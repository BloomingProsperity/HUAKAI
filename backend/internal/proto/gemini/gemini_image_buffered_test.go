package gemini

import (
	"context"
	"testing"
)

// 判别测试:非流式(buffered)generateContent 响应里的 inlineData 生成图必须
// 进 canonical image block——bb9d4d24 只修了流式路,这是 buffered 孪生:此前
// 这里把图记成 lossy「skipped」丢弃,客户被计 output token 却收不到图。
// Mutation guard: 改回 lossy-skip(或 Type 改非 image)→ 本测试红。
func TestGeminiBufferedResponsePreservesInlineImage(t *testing.T) {
	adapter := &Adapter{}
	raw := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1290,"totalTokenCount":1294}}`)

	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("buffered inline image 不得再被记为 loss 丢弃: %+v", losses)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatal("BufferedResponse 缺失")
	}
	foundImage := false
	for _, blk := range env.BufferedResponse.Content {
		if blk.Type == "image" {
			foundImage = true
			if len(blk.Image) == 0 {
				t.Fatal("image block 必须携带 inlineData payload")
			}
		}
	}
	if !foundImage {
		t.Fatalf("buffered 响应必须保留 image content block;got %d blocks", len(env.BufferedResponse.Content))
	}
	// 计费 usage 照常(image token 含在 candidatesTokenCount 内)
	if env.BufferedResponse.Usage.OutputTokens != 1290 {
		t.Fatalf("OutputTokens=%d want 1290", env.BufferedResponse.Usage.OutputTokens)
	}
}
