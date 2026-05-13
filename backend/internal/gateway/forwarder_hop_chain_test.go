package gateway

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/google/uuid"
)

func TestBuildForwardHopChainFourHopsNoDetailContent(t *testing.T) {
	req := trustChainForwardRequest()
	chain := buildForwardHopChain(req, time.Unix(1700000000, 0).UTC())

	if len(chain) != 4 {
		t.Fatalf("hop chain len=%d want 4", len(chain))
	}
	want := []proto.HopHop{proto.HopIngress, proto.HopRouter, proto.HopPool, proto.HopAccount}
	for i, hop := range chain {
		if hop.Hop != want[i] {
			t.Fatalf("hop[%d]=%q want %q", i, hop.Hop, want[i])
		}
		if len(hop.Detail) != 0 {
			t.Fatalf("hop[%d] detail must stay empty, got %s", i, hop.Detail)
		}
		if hop.RequestID != req.RequestID {
			t.Fatalf("hop[%d] request_id=%q want %q", i, hop.RequestID, req.RequestID)
		}
	}
	if chain[1].RouteID != req.RouteID {
		t.Fatalf("router route_id=%q want %q", chain[1].RouteID, req.RouteID)
	}
	if chain[2].PoolID != req.PoolID {
		t.Fatalf("pool_id=%q want %q", chain[2].PoolID, req.PoolID)
	}
	assertAccountHash(t, chain[3].AccountIDHash)
}

func TestApplyForwardRequestHopChainToEnvelope(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	ApplyForwardRequestHopChain(env, trustChainForwardRequest())

	if len(env.Accounting.HopChain) != 4 {
		t.Fatalf("envelope hop chain len=%d want 4", len(env.Accounting.HopChain))
	}
	if env.Accounting.HopChain[0].Hop != proto.HopIngress {
		t.Fatalf("first hop=%q want ingress", env.Accounting.HopChain[0].Hop)
	}
}

func TestForwardAnnotatesHCSFEventsWithHopChain(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			env,
			proto.CanonicalEvent{Type: "message_stop"},
		}},
	}

	_, err := f.Forward(context.Background(), bytes.NewReader([]byte("event: message_stop\ndata: {}\n\n")), &discardingResponseWriter{}, trustChainForwardRequest())
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if len(env.Accounting.HopChain) != 4 {
		t.Fatalf("forward did not annotate HCSF event hop chain: %+v", env.Accounting.HopChain)
	}
}

func TestFinalizeUpstreamAnnotatesHCSFEventsWithHopChain(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	adapter := &finalizeEnvelopeAdapter{final: []any{env}}
	events, err := (&StreamForwarder{}).FinalizeUpstream(context.Background(), adapter, nil, trustChainForwardRequest())
	if err != nil {
		t.Fatalf("FinalizeUpstream: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("final events len=%d want 1", len(events))
	}
	if len(env.Accounting.HopChain) != 4 {
		t.Fatalf("finalize did not annotate HCSF event hop chain: %+v", env.Accounting.HopChain)
	}
}

func trustChainForwardRequest() ForwardRequest {
	return ForwardRequest{
		TenantID:         7,
		AccountID:        42,
		AcquisitionToken: uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		RequestID:        "req_t3",
		RouteID:          "registry:7:1;router:v1:primary",
		PoolID:           "99",
		IngressPath:      "/v1/chat/completions",
		ProtocolFamily:   "openai_chat",
		ClientProtocol:   "openai_chat",
		Model:            "gpt-4o-2024-08-06",
		RequestedModel:   "gpt-4o",
		Provider:         "openai",
	}
}

func assertAccountHash(t *testing.T, got string) {
	t.Helper()
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("account hash=%q missing sha256 prefix", got)
	}
	raw := strings.TrimPrefix(got, "sha256:")
	if _, err := hex.DecodeString(raw); err != nil {
		t.Fatalf("account hash is not hex: %v", err)
	}
	if got == "42" {
		t.Fatal("account hash leaked raw account id")
	}
}

type finalizeEnvelopeAdapter struct {
	forwarderClientAdapterUpstreamStub
	final []any
}

func (a *finalizeEnvelopeAdapter) FinalizeUpstreamStream(context.Context, any) ([]any, error) {
	return a.final, nil
}

type discardingResponseWriter struct{}

func (d *discardingResponseWriter) Header() http.Header         { return http.Header{} }
func (d *discardingResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardingResponseWriter) WriteHeader(int)             {}
