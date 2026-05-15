package proto_test

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAnthropicRSTAfterChunkProducesPartialBillingState(t *testing.T) {
	adapter := &proto.AnthropicAdapter{}
	state := &proto.UpstreamState{}

	_, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"type":"message_start","message":{"id":"msg_partial","model":"claude-test"}}`), state)
	if err != nil {
		t.Fatalf("message_start: %v", err)
	}
	_, _, err = adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`), state)
	if err != nil {
		t.Fatalf("content_block_delta: %v", err)
	}
	if state.DeliveredChunkCount == 0 {
		t.Fatal("anthropic delivered chunk count must be >0 before simulated RST")
	}

	attempt := billing.AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.UpstreamError5xx,
		DeliveredTokenCount: state.DeliveredChunkCount,
	})
	if attempt.State != billing.StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount <= 0 {
		t.Fatalf("delivered=%d want >0", attempt.DeliveredTokenCount)
	}
}

func TestOpenAIRSTAfterChunkProducesPartialBillingState(t *testing.T) {
	adapter := &proto.OpenAIAdapter{}
	state := &proto.OpenAIUpstreamState{}

	_, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"id":"chatcmpl_partial","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":null}]}`), state)
	if err != nil {
		t.Fatalf("openai content chunk: %v", err)
	}
	if state.DeliveredChunkCount == 0 {
		t.Fatal("openai delivered chunk count must be >0 before simulated RST")
	}

	attempt := billing.AttemptFromGatewayDraft(true, gateway.UsageRecordDraft{
		EndClass:            gateway.UpstreamError5xx,
		DeliveredTokenCount: state.DeliveredChunkCount,
	})
	if attempt.State != billing.StreamStatePartial {
		t.Fatalf("state=%s want partial", attempt.State)
	}
	if attempt.DeliveredTokenCount <= 0 {
		t.Fatalf("delivered=%d want >0", attempt.DeliveredTokenCount)
	}
}
