package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestForwarderClientAdapterFinalizeCalledAndChunksWritten(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		eventChunks: [][]byte{[]byte("data: event\n\n")},
		finalChunks: [][]byte{[]byte("data: final\n\n")},
	}
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "message_stop"},
		}},
	}
	f.ClientAdapter = clientAdapter

	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if draft.EndClass != StreamEndGraceful {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, StreamEndGraceful)
	}
	if clientAdapter.finalizeCalls != 1 {
		t.Fatalf("FinalizeClientStream calls=%d want 1", clientAdapter.finalizeCalls)
	}
	got := rec.Body.String()
	if !strings.Contains(got, "data: event\n\n") {
		t.Fatalf("client event chunk not written; body=%q", got)
	}
	if !strings.Contains(got, "data: final\n\n") {
		t.Fatalf("finalize chunk not written; body=%q", got)
	}
}

// TestForwardAccumulatesPerEventProtocolLoss 守 S1-025-fu item 4:
// StreamForwarder 之前丢弃 ProviderEventToCanonicalEvents 与 CanonicalEventToClientChunk
// 的逐事件协议损失;现在累积进 acc 并经 finishDraft 落到 draft.StreamProtocolLoss。
func TestForwardAccumulatesPerEventProtocolLoss(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		eventChunks: [][]byte{[]byte("data: chunk\n\n")},
		finalChunks: [][]byte{[]byte("data: final\n\n")},
		eventLosses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "client_event_loss_sentinel", Reason: "client chunk conversion"}},
	}
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{
			events: []any{&proto.CanonicalEvent{Type: "message_stop"}},
			losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "provider_event_loss_sentinel", Reason: "provider event conversion"}},
		},
	}
	f.ClientAdapter = clientAdapter

	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	// MUTATION: forwarder.go 还原 provider(handleEventWithAdapter)或 client(clientChunks)损失丢弃(_)
	// → draft.StreamProtocolLoss 缺对应 sentinel → RED。
	if !forwarderLossHasCode(draft.StreamProtocolLoss, "provider_event_loss_sentinel") {
		t.Fatalf("draft.StreamProtocolLoss missing provider sentinel: %+v", draft.StreamProtocolLoss)
	}
	if !forwarderLossHasCode(draft.StreamProtocolLoss, "client_event_loss_sentinel") {
		t.Fatalf("draft.StreamProtocolLoss missing client sentinel: %+v", draft.StreamProtocolLoss)
	}
}

func forwarderLossHasCode(losses []proto.ProtocolLossEntry, code string) bool {
	for _, l := range losses {
		if l.Code == code {
			return true
		}
	}
	return false
}

// TestHandleEventWithAdapterAccumulatesLossOnErrorReturn 守 S1-025-fu review R1 finding 2:
// 部分上游 adapter 把 ProtocolLossEntry 连同 error 一起返回(anthropic/sse.go:228 未知事件
// → loss + ErrUnknownEventType)。handleEventWithAdapter 原先在 append providerLosses 之前
// 就 error 早返,证据丢失;现在累积先于 error 返回。
func TestHandleEventWithAdapterAccumulatesLossOnErrorReturn(t *testing.T) {
	adapter := &forwarderClientAdapterUpstreamStub{
		losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "provider_error_loss_sentinel", Reason: "unknown upstream event"}},
		err:    proto.ErrUnknownEventType,
	}
	f := newForwarder()
	acc := &UsageAccumulator{}
	rec := httptest.NewRecorder()

	_, _, _, err := f.handleEventWithAdapter(
		context.Background(),
		adapter,
		SSEEvent{Type: "unknown_event", Data: []byte(`{"type":"unknown_event"}`)},
		rec,
		nil,
		nil,
		acc,
		ForwardRequest{RequestID: "req-r1-provider"},
	)
	if err == nil {
		t.Fatal("expected adapter error to propagate")
	}
	// MUTATION: 把 providerLosses append 移回 error 早返之后 → acc.StreamProtocolLoss 为空 → RED。
	if !forwarderLossHasCode(acc.StreamProtocolLoss, "provider_error_loss_sentinel") {
		t.Fatalf("acc.StreamProtocolLoss missing sentinel after errored provider event: %+v", acc.StreamProtocolLoss)
	}
}

// TestDrainWithAdapterAccumulatesLossOnErrorReturn 守 finding 2 的 drain 镜像:
// drainWithAdapter 原先只在 err==nil 时 append drainLosses,drain 期未知/畸形事件
// (loss+error)证据丢失;现在累积不受 err 影响,usage 仍仅在 err==nil 时采信。
func TestDrainWithAdapterAccumulatesLossOnErrorReturn(t *testing.T) {
	adapter := &forwarderClientAdapterUpstreamStub{
		losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "drain_error_loss_sentinel", Reason: "unknown drain event"}},
		err:    proto.ErrUnknownEventType,
	}
	f := newForwarder()
	acc := &UsageAccumulator{}
	events := make(chan scanResult, 1)
	events <- scanResult{event: SSEEvent{Type: "unknown_event", Data: []byte(`{"type":"unknown_event"}`)}}
	close(events)

	f.drainWithAdapter(context.Background(), adapter, events, nil, acc)

	// MUTATION: 把 drainLosses append 移回 `if err == nil` 内 → acc.StreamProtocolLoss 为空 → RED。
	if !forwarderLossHasCode(acc.StreamProtocolLoss, "drain_error_loss_sentinel") {
		t.Fatalf("acc.StreamProtocolLoss missing drain sentinel after errored drain event: %+v", acc.StreamProtocolLoss)
	}
}

func TestForwarderClientStateInitializedByAdapterType(t *testing.T) {
	if got := (&StreamForwarder{ClientAdapter: &proto.AnthropicMessagesClient{}}).newClientState(); got == nil {
		t.Fatal("anthropic_messages client state is nil")
	} else if _, ok := got.(*proto.AnthropicMessagesStreamState); !ok {
		t.Fatalf("anthropic_messages client state type=%T", got)
	}

	if got := (&StreamForwarder{ClientAdapter: &proto.OpenAIChatClient{}}).newClientState(); got == nil {
		t.Fatal("openai_chat client state is nil")
	} else if _, ok := got.(*proto.OpenAIChatStreamState); !ok {
		t.Fatalf("openai_chat client state type=%T", got)
	}

	if got := (&StreamForwarder{ClientAdapter: &proto.OpenAIResponsesClient{}}).newClientState(); got == nil {
		t.Fatal("openai_responses client state is nil")
	} else if _, ok := got.(*proto.OpenAIResponsesStreamState); !ok {
		t.Fatalf("openai_responses client state type=%T", got)
	}

	if got := (&StreamForwarder{}).newClientState(); got != nil {
		t.Fatalf("nil ClientAdapter client state=%T want nil", got)
	}
}

func TestForwarderBufferedResponseHappyPath(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		bufferedBody: []byte(`{"ok":true}`),
	}
	f := &StreamForwarder{ClientAdapter: clientAdapter}
	canonical := proto.NewEmptyEnvelope()

	got, err := f.BufferedResponse(context.Background(), canonical)
	if err != nil {
		t.Fatalf("BufferedResponse: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("BufferedResponse body=%s", got)
	}
	if clientAdapter.bufferedCalls != 1 {
		t.Fatalf("CanonicalToClientResponse calls=%d want 1", clientAdapter.bufferedCalls)
	}
	if clientAdapter.bufferedCanonical != canonical {
		t.Fatalf("CanonicalToClientResponse received canonical=%p want %p", clientAdapter.bufferedCanonical, canonical)
	}
}

func TestForwarderNilClientAdapterFallbackRawPassthrough(t *testing.T) {
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "message_start"},
		}},
	}

	upstream := []byte("event: passthrough\ndata: {\"x\":1}\n\n")
	rec := httptest.NewRecorder()
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if got := rec.Body.String(); got != string(upstream) {
		t.Fatalf("raw passthrough body=%q want %q", got, upstream)
	}
}

func TestHandleEventWithAdapterSanitizesProtocolErrorWhenAdapterNil(t *testing.T) {
	assertProtocolErrorSanitized(t, nil)
}

func TestHandleEventWithAdapterSanitizesProtocolErrorBeforeAdapter(t *testing.T) {
	assertProtocolErrorSanitized(t, &forwarderClientAdapterUpstreamStub{
		err: errors.New("adapter must not receive protocol error"),
	})
}

// forwarderClientAdapterUpstreamStub 只负责把 scanner 事件映射成测试指定的 canonical events。
type forwarderClientAdapterUpstreamStub struct {
	events []any
	losses []proto.ProtocolLossEntry
	err    error
}

func (s *forwarderClientAdapterUpstreamStub) CanonicalToProviderRequest(context.Context, *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderResponseToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderEventToCanonicalEvents(context.Context, any, any) ([]any, []proto.ProtocolLossEntry, error) {
	return s.events, s.losses, s.err
}

func (s *forwarderClientAdapterUpstreamStub) FinalizeUpstreamStream(context.Context, any) ([]any, error) {
	return nil, nil
}

// recordingForwarderClientAdapter 记录 forwarder 是否调用 client adapter hookpoint。
type recordingForwarderClientAdapter struct {
	eventChunks       [][]byte
	finalChunks       [][]byte
	bufferedBody      []byte
	eventLosses       []proto.ProtocolLossEntry
	eventCalls        int
	finalizeCalls     int
	bufferedCalls     int
	eventState        any
	finalizeState     any
	bufferedCanonical *proto.HCSF
}

func (a *recordingForwarderClientAdapter) RequestToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (a *recordingForwarderClientAdapter) CanonicalToClientResponse(_ context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	a.bufferedCalls++
	a.bufferedCanonical = canonical
	return a.bufferedBody, nil, nil
}

func (a *recordingForwarderClientAdapter) CanonicalEventToClientChunk(_ context.Context, _ any, state any) ([][]byte, []proto.ProtocolLossEntry, error) {
	a.eventCalls++
	a.eventState = state
	return a.eventChunks, a.eventLosses, nil
}

func (a *recordingForwarderClientAdapter) FinalizeClientStream(_ context.Context, state any) ([][]byte, error) {
	a.finalizeCalls++
	a.finalizeState = state
	return a.finalChunks, nil
}

func assertProtocolErrorSanitized(t *testing.T, adapter proto.UpstreamAdapter) {
	t.Helper()

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	const marker = "SENSITIVE_BEDROCK_MARKER"
	f := newForwarder()
	rec := httptest.NewRecorder()
	terminalSeen, wrote, delivered, err := f.handleEventWithAdapter(
		context.Background(),
		adapter,
		SSEEvent{Type: "error", Data: []byte(`{"message":"` + marker + `"}`)},
		rec,
		nil,
		nil,
		&UsageAccumulator{},
		ForwardRequest{RequestID: "req-c18"},
	)
	if err != nil {
		t.Fatalf("handleEventWithAdapter returned err=%v", err)
	}
	if terminalSeen {
		t.Fatalf("protocol error frame should not masquerade as terminal model event")
	}
	if !wrote {
		t.Fatalf("sanitized error frame was not written")
	}
	if delivered != 0 {
		t.Fatalf("protocol error frame delivered chunks=%d want 0", delivered)
	}
	body := rec.Body.String()
	if strings.Contains(body, marker) {
		t.Fatalf("client SSE leaked raw protocol error payload: %q", body)
	}
	if !strings.Contains(body, `"code":"upstream_error"`) {
		t.Fatalf("client SSE missing fixed upstream_error code: %q", body)
	}
	if strings.Count(body, "event: error") != 1 {
		t.Fatalf("client SSE should contain exactly one canonical error event, got %q", body)
	}
	gotLog := logs.String()
	if !strings.Contains(gotLog, marker) {
		t.Fatalf("internal log did not retain raw protocol error payload: %s", gotLog)
	}
	if !strings.Contains(gotLog, "req-c18") || !strings.Contains(gotLog, "upstream_error") {
		t.Fatalf("internal log missing request/code context: %s", gotLog)
	}
}
