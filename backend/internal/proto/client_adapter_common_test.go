package proto

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// 覆盖 P-2 D0 shared foundation：RequestMetaSeed / ClientAdapterRegistry /
// SSE emit / typed loss constructor。

func TestRequestMetaSeed_ContextRoundTrip(t *testing.T) {
	seed := RequestMetaSeed{
		RequestID:      "req_001",
		ClientProtocol: ClientProtocolOpenAIChat,
		ProtocolFamily: "openai",
		IngressPath:    "/v1/chat/completions",
		TenantID:       42,
		RouteID:        "rt_a",
		AccountID:      7,
		EvidenceLabel:  EvidenceMock,
	}
	ctx := ContextWithRequestMetaSeed(context.Background(), seed)
	got, ok := RequestMetaSeedFromContext(ctx)
	if !ok {
		t.Fatalf("expected seed in context, got ok=false")
	}
	if got.RequestID != "req_001" || got.ClientProtocol != ClientProtocolOpenAIChat {
		t.Fatalf("seed mismatch: %+v", got)
	}
	if got.IngressPath != "/v1/chat/completions" || got.TenantID != 42 {
		t.Fatalf("seed scalar mismatch: %+v", got)
	}
}

func TestRequestMetaSeed_MissingFromBaseContext(t *testing.T) {
	if _, ok := RequestMetaSeedFromContext(context.Background()); ok {
		t.Fatalf("expected ok=false for unattached context")
	}
}

func TestRequestMetaSeed_ApplyToRequestMeta_Required(t *testing.T) {
	cases := []struct {
		name  string
		seed  RequestMetaSeed
		want  string // expected substring in error；空 = 期望成功
	}{
		{
			name: "missing request id",
			seed: RequestMetaSeed{ClientProtocol: ClientProtocolOpenAIChat, ProtocolFamily: "openai", IngressPath: "/x"},
			want: "RequestID",
		},
		{
			name: "missing client protocol",
			seed: RequestMetaSeed{RequestID: "r", ProtocolFamily: "openai", IngressPath: "/x"},
			want: "ClientProtocol",
		},
		{
			name: "missing protocol family",
			seed: RequestMetaSeed{RequestID: "r", ClientProtocol: ClientProtocolOpenAIChat, IngressPath: "/x"},
			want: "ProtocolFamily",
		},
		{
			name: "missing ingress path",
			seed: RequestMetaSeed{RequestID: "r", ClientProtocol: ClientProtocolOpenAIChat, ProtocolFamily: "openai"},
			want: "IngressPath",
		},
		{
			name: "happy path",
			seed: RequestMetaSeed{RequestID: "r", ClientProtocol: ClientProtocolOpenAIChat, ProtocolFamily: "openai", IngressPath: "/x"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m RequestMeta
			err := tc.seed.ApplyToRequestMeta(&m)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected nil err, got %v", err)
				}
				if m.RequestID != tc.seed.RequestID {
					t.Fatalf("expected RequestID applied, got %q", m.RequestID)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected err containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRequestMetaSeed_ApplyNilSeedOrMeta(t *testing.T) {
	var nilSeed *RequestMetaSeed
	if err := nilSeed.ApplyToRequestMeta(&RequestMeta{}); !errors.Is(err, ErrMissingRequestMetaSeed) {
		t.Fatalf("expected ErrMissingRequestMetaSeed, got %v", err)
	}
	s := RequestMetaSeed{RequestID: "r", ClientProtocol: ClientProtocolOpenAIChat, ProtocolFamily: "openai", IngressPath: "/x"}
	if err := s.ApplyToRequestMeta(nil); err == nil {
		t.Fatalf("expected error on nil meta")
	}
}

func TestClientAdapterRegistry_RegisterLookup(t *testing.T) {
	reg := NewClientAdapterRegistry()
	a := stubClientAdapter{}
	if err := reg.Register(ClientProtocolOpenAIChat, a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Lookup(ClientProtocolOpenAIChat)
	if !ok || got != a {
		t.Fatalf("Lookup: ok=%v got=%v", ok, got)
	}
	if _, ok := reg.Lookup(ClientProtocolAnthropicMessages); ok {
		t.Fatalf("unexpected hit for unregistered protocol")
	}
}

func TestClientAdapterRegistry_DuplicateRejected(t *testing.T) {
	reg := NewClientAdapterRegistry()
	a := stubClientAdapter{}
	if err := reg.Register(ClientProtocolOpenAIChat, a); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(ClientProtocolOpenAIChat, a); !errors.Is(err, ErrClientAdapterAlreadyRegistered) {
		t.Fatalf("expected ErrClientAdapterAlreadyRegistered, got %v", err)
	}
}

func TestClientAdapterRegistry_RejectEmptyOrNil(t *testing.T) {
	reg := NewClientAdapterRegistry()
	if err := reg.Register("", stubClientAdapter{}); err == nil {
		t.Fatalf("expected error for empty protocol")
	}
	if err := reg.Register(ClientProtocolOpenAIChat, nil); err == nil {
		t.Fatalf("expected error for nil adapter")
	}
	var nilReg *ClientAdapterRegistry
	if err := nilReg.Register(ClientProtocolOpenAIChat, stubClientAdapter{}); err == nil {
		t.Fatalf("expected error on nil receiver Register")
	}
	if _, ok := nilReg.Lookup(ClientProtocolOpenAIChat); ok {
		t.Fatalf("nil receiver Lookup must return ok=false")
	}
}

func TestClientAdapterRegistry_ProtocolsSorted(t *testing.T) {
	reg := NewClientAdapterRegistry()
	for _, p := range []ClientProtocol{ClientProtocolOpenAIResponses, ClientProtocolOpenAIChat, ClientProtocolAnthropicMessages} {
		if err := reg.Register(p, stubClientAdapter{}); err != nil {
			t.Fatalf("Register %s: %v", p, err)
		}
	}
	got := reg.Protocols()
	want := []ClientProtocol{ClientProtocolAnthropicMessages, ClientProtocolOpenAIChat, ClientProtocolOpenAIResponses}
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Protocols[%d] got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestEmitSSE(t *testing.T) {
	if got := EmitSSEEvent("message_start", []byte(`{"type":"message_start"}`)); !bytes.Equal(got, []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")) {
		t.Fatalf("named event mismatch: %q", got)
	}
	if got := EmitSSEDataLine([]byte(`{"id":"x"}`)); !bytes.Equal(got, []byte("data: {\"id\":\"x\"}\n\n")) {
		t.Fatalf("data-only mismatch: %q", got)
	}
	if got := EmitSSEDone(); !bytes.Equal(got, []byte("data: [DONE]\n\n")) {
		t.Fatalf("done sentinel mismatch: %q", got)
	}
}

func TestNewClientLossEntry(t *testing.T) {
	entry, err := NewClientLossEntry(ProtocolLossWarning, "downgrade_thinking", "downgrade_thinking", CapabilityThinking, "node_th_1")
	if err != nil {
		t.Fatalf("happy path err: %v", err)
	}
	if entry.Severity != ProtocolLossWarning || entry.Reason != "downgrade_thinking" {
		t.Fatalf("unexpected entry %+v", entry)
	}
	if entry.Direction != string(DirectionClientToCanonical) {
		t.Fatalf("expected direction client_to_canonical, got %q", entry.Direction)
	}
	// 兼容 v0.4 silent-drop 守门：构造出的 entry 必须不被判 silent drop。
	if entry.IsSilentDrop() {
		t.Fatalf("constructed entry should not be silent drop")
	}
}

func TestNewClientLossEntry_Rejects(t *testing.T) {
	if _, err := NewClientLossEntry("", "r", "c", "", ""); err == nil {
		t.Fatalf("expected error for empty severity")
	}
	if _, err := NewClientLossEntry(ProtocolLossWarning, "", "", "", ""); err == nil {
		t.Fatalf("expected error for both reason/code empty")
	}
}

// stubClientAdapter 仅满足 ClientAdapter 接口，用于 registry 测试。
type stubClientAdapter struct{}

func (stubClientAdapter) RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	return nil, nil, errors.New("stub")
}
func (stubClientAdapter) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	return nil, nil, errors.New("stub")
}
func (stubClientAdapter) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error) {
	return nil, nil, errors.New("stub")
}
func (stubClientAdapter) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	return nil, errors.New("stub")
}
