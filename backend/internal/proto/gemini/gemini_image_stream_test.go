package gemini

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestGeminiAdapterStreamInlineImageEmitsImageBlock proves streaming image
// generation (gemini-2.5-flash-image via generateContent) preserves the inline
// image as a canonical image block instead of dropping it. Previously the
// streaming scanner recorded a lossy "skipped" and discarded the image while
// still billing the output tokens -- the customer paid for a dropped image.
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
	// output tokens (which include the image tokens) must still accumulate for billing
	if state.AccumulatedUsage.OutputTokens != 1290 {
		t.Fatalf("output tokens should accumulate for billing: got %d want 1290", state.AccumulatedUsage.OutputTokens)
	}
}
