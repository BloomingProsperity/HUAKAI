package gateway

import (
	"bytes"
	"context"
	"errors"
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

// forwarderClientAdapterUpstreamStub 只负责把 scanner 事件映射成测试指定的 canonical events。
type forwarderClientAdapterUpstreamStub struct {
	events []any
	err    error
}

func (s *forwarderClientAdapterUpstreamStub) CanonicalToProviderRequest(context.Context, *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderResponseToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderEventToCanonicalEvents(context.Context, any, any) ([]any, []proto.ProtocolLossEntry, error) {
	return s.events, nil, s.err
}

func (s *forwarderClientAdapterUpstreamStub) FinalizeUpstreamStream(context.Context, any) ([]any, error) {
	return nil, nil
}

// recordingForwarderClientAdapter 记录 forwarder 是否调用 client adapter hookpoint。
type recordingForwarderClientAdapter struct {
	eventChunks       [][]byte
	finalChunks       [][]byte
	bufferedBody      []byte
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
	return a.eventChunks, nil, nil
}

func (a *recordingForwarderClientAdapter) FinalizeClientStream(_ context.Context, state any) ([][]byte, error) {
	a.finalizeCalls++
	a.finalizeState = state
	return a.finalChunks, nil
}
