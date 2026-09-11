package servicetier

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestFromSSETakesLatestDataLine(t *testing.T) {
	raw := []byte("data: {\"service_tier\":\"flex\"}\n\ndata: {\"service_tier\":\"priority\"}\n\ndata: [DONE]\n")
	if got := FromSSE(raw); got != "priority" {
		t.Fatalf("FromSSE=%q want priority", got)
	}
}

func TestFromJSONReadsTopLevelLane(t *testing.T) {
	if got := FromJSON([]byte(`{"model":"gpt-4o","service_tier":"flex"}`)); got != "flex" {
		t.Fatalf("FromJSON=%q want flex", got)
	}
	if got := FromJSON([]byte(`{"model":"gpt-4o"}`)); got != "" {
		t.Fatalf("missing field=%q want empty", got)
	}
	if got := FromJSON([]byte(`{"service_tier":1}`)); got != "" {
		t.Fatalf("non-string=%q want empty", got)
	}
}

func TestFromEnvelopePrefersBufferedThenLatestEvent(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.Passthrough = &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"service_tier": json.RawMessage(`"default"`),
	}}
	env.StreamEvents = []proto.CanonicalEvent{
		{Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"flex"`)}}},
		{Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"priority"`)}}},
	}
	if got := FromEnvelope(env); got != "default" {
		t.Fatalf("buffered/envelope passthrough=%q want default before events", got)
	}
	env.Passthrough = nil
	if got := FromEnvelope(env); got != "priority" {
		t.Fatalf("latest event=%q want priority", got)
	}
	env.BufferedResponse = &proto.CanonicalResponse{
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"ultrafast"`)}},
	}
	if got := FromEnvelope(env); got != "ultrafast" {
		t.Fatalf("buffered response=%q want ultrafast", got)
	}
}
