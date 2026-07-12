package gemini

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestGeminiAdapterStreamInlineImageEmitsImageBlock 证明流式图片生成
//（gemini-2.5-flash-image 走 generateContent）会把 inline 图片保留为
// canonical image block 而不是丢弃。此前流式扫描器会记一条 lossy 的
// "skipped" 并丢掉图片，却仍按 output tokens 计费——客户为一张被丢弃的图片付了钱。
func TestGeminiAdapterStreamInlineImageEmitsImageBlock(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1290,"totalTokenCount":1294}}`)

	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("streaming inline image must not be dropped as a loss: %+v", losses)
	}

	events := geminiAnyToCanonicalEvents(t, out)
	var imageBlock *proto.CanonicalContentBlock
	for i := range events {
		if events[i].Type == "content_block_start" && events[i].ContentBlock != nil && events[i].ContentBlock.Type == "image" {
			imageBlock = events[i].ContentBlock
		}
	}
	if imageBlock == nil {
		t.Fatalf("streaming inlineData must emit an image content_block_start; got %s", strings.Join(geminiEventTypes(events), ","))
	}
	if len(imageBlock.Image) == 0 {
		t.Fatalf("image block must carry the inlineData payload")
	}
	// output tokens（含 image tokens）仍必须为计费而累加
	if state.AccumulatedUsage.OutputTokens != 1290 {
		t.Fatalf("output tokens should accumulate for billing: got %d want 1290", state.AccumulatedUsage.OutputTokens)
	}
}
