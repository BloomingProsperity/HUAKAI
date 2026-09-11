package servicetier

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestFromSSEIgnoresMidStreamEcho(t *testing.T) {
	echoOnly := []byte("data: {\"service_tier\":\"fast\"}\n\ndata: {\"service_tier\":\"priority\"}\n\ndata: [DONE]\n")
	if got := FromSSE(echoOnly); got != "" {
		t.Fatalf("echo-only FromSSE=%q want empty", got)
	}

	raw := []byte("data: {\"service_tier\":\"fast\"}\n\ndata: {\"usage\":{\"total_tokens\":3},\"service_tier\":\"default\"}\n\ndata: [DONE]\n")
	if got := FromSSE(raw); got != "default" {
		t.Fatalf("FromSSE=%q want default from usage frame", got)
	}

	completed := []byte("data: {\"type\":\"response.created\",\"service_tier\":\"fast\"}\n\ndata: {\"type\":\"response.completed\",\"service_tier\":\"default\"}\n")
	if got := FromSSE(completed); got != "default" {
		t.Fatalf("FromSSE completed=%q want default", got)
	}

	buffered := []byte(`{"id":"cmpl-1","usage":{"total_tokens":3},"service_tier":"flex"}`)
	if got := FromSSE(buffered); got != "flex" {
		t.Fatalf("buffered JSON=%q want flex", got)
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

func TestFromEnvelopePrefersBufferedThenAuthoritativeEvent(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.Passthrough = &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"service_tier": json.RawMessage(`"fast"`),
	}}
	env.StreamEvents = []proto.CanonicalEvent{
		{Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"fast"`)}}},
		{
			Type:        "message_delta",
			Usage:       &proto.CanonicalUsage{TotalTokens: 3},
			Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"default"`)}},
		},
	}
	if got := FromEnvelope(env); got != "default" {
		t.Fatalf("request passthrough must not override authoritative event: got %q", got)
	}

	echoOnly := proto.NewEmptyEnvelope()
	echoOnly.Passthrough = &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"service_tier": json.RawMessage(`"fast"`),
	}}
	echoOnly.StreamEvents = []proto.CanonicalEvent{
		{Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"fast"`)}}},
	}
	if got := FromEnvelope(echoOnly); got != "" {
		t.Fatalf("echo-only envelope=%q want empty", got)
	}

	env.BufferedResponse = &proto.CanonicalResponse{
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"ultrafast"`)}},
	}
	if got := FromEnvelope(env); got != "ultrafast" {
		t.Fatalf("buffered response=%q want ultrafast", got)
	}
}

func TestAuthoritativeFromEventIgnoresMidStream(t *testing.T) {
	mid := proto.CanonicalEvent{
		Type:        "content_block_delta",
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"fast"`)}},
	}
	if got := AuthoritativeFromEvent(mid); got != "" {
		t.Fatalf("mid-stream=%q want empty", got)
	}
	stop := proto.CanonicalEvent{
		Type:        "message_stop",
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{"service_tier": json.RawMessage(`"default"`)}},
	}
	if got := AuthoritativeFromEvent(stop); got != "default" {
		t.Fatalf("stop=%q want default", got)
	}
}
