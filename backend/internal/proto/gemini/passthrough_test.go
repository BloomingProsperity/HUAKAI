package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

func TestGemini_StreamingChunk_PassthroughRoundTripPreservesUnknownFields(t *testing.T) {
	chunk := `data: {"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro","responseId":"resp-gemini","modelFleet":"preview","vendorMetadata":{"region":"us-central1","trace":"abc"}}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("passthrough chunk should not emit losses: %+v", losses)
	}
	events := geminiAnyToCanonicalEvents(t, out)
	if len(events) < 3 {
		t.Fatalf("events=%d want message_start/content_block_start/content_block_delta", len(events))
	}

	var delta *proto.CanonicalEvent
	for i := range events {
		if events[i].Passthrough == nil {
			t.Fatalf("event[%d] %s missing passthrough", i, events[i].Type)
		}
		if !bytes.Equal(events[i].Passthrough.Extra["modelFleet"], json.RawMessage(`"preview"`)) {
			t.Fatalf("event[%d] modelFleet=%s", i, events[i].Passthrough.Extra["modelFleet"])
		}
		if events[i].Type == "content_block_delta" {
			delta = &events[i]
		}
	}
	if delta == nil {
		t.Fatal("content_block_delta missing")
	}

	clientTyped := map[string]any{
		"responseId":   state.MessageID,
		"modelVersion": state.Model,
		"candidates": []map[string]any{
			{"index": 0, "content": map[string]any{"parts": []map[string]string{{"text": delta.Delta.Text}}}},
		},
	}
	clientJSON, _ := json.Marshal(clientTyped)
	merged, err := proto.MergeExtrasInto(clientJSON, delta.Passthrough)
	if err != nil {
		t.Fatalf("proto.MergeExtrasInto: %v", err)
	}
	if !strings.Contains(string(merged), "modelFleet") || !strings.Contains(string(merged), "vendorMetadata") {
		t.Fatalf("merged should keep Gemini extras: %s", merged)
	}
}

func TestGeminiFinalizeObservesCacheMetrics(t *testing.T) {
	const accountID int64 = 260518991
	beforeCreation, beforeRead, beforeRequests := cachemetrics.SnapshotByAccount(accountID)

	adapter := &Adapter{}
	state := &UpstreamState{
		AccountID:  accountID,
		TenantID:   518,
		PrefixHash: "gemini-cache-prefix",
	}
	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":"cached"}]},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"cachedContentTokenCount":7,"candidatesTokenCount":1,"totalTokenCount":13}}`)
	if _, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state); err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	} else if len(losses) != 0 {
		t.Fatalf("cache metrics chunk should not emit losses: %+v", losses)
	}
	if state.AccumulatedUsage.CacheReadInputTokens != 7 || state.CachedContentTokens != 7 {
		t.Fatalf("Gemini cache usage not accumulated: usage=%+v cached=%d", state.AccumulatedUsage, state.CachedContentTokens)
	}

	if _, err := adapter.FinalizeUpstreamStream(context.Background(), state); err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	afterCreation, afterRead, afterRequests := cachemetrics.SnapshotByAccount(accountID)
	if afterCreation-beforeCreation != 0 || afterRead-beforeRead != 7 || afterRequests-beforeRequests != 1 {
		t.Fatalf("cachemetrics delta creation/read/requests=%d/%d/%d want 0/7/1",
			afterCreation-beforeCreation, afterRead-beforeRead, afterRequests-beforeRequests)
	}

	if _, err := adapter.FinalizeUpstreamStream(context.Background(), state); err != nil {
		t.Fatalf("second FinalizeUpstreamStream: %v", err)
	}
	finalCreation, finalRead, finalRequests := cachemetrics.SnapshotByAccount(accountID)
	if finalCreation != afterCreation || finalRead != afterRead || finalRequests != afterRequests {
		t.Fatalf("second finalize should not observe again: got %d/%d/%d after %d/%d/%d",
			finalCreation, finalRead, finalRequests, afterCreation, afterRead, afterRequests)
	}
}
