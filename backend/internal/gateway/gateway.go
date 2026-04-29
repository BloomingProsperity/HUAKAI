// Package gateway implements F-GW-002: streaming forwarder + usage accounting.
//
// See docs/specs/streaming-forwarder.md for the released spec.
// Phase 3 skeleton ONLY — no business logic per DR-008.
package gateway

import (
	"context"
	"net/http"
)

// Forwarder orchestrates the four-phase streaming pipeline per spec §Phase A-D
// (parse → inline event processing → end classification → Tx2 finalization).
// Drain Phase C-bis runs only on CLIENT_DISCONNECT.
type Forwarder interface {
	Forward(ctx context.Context, req ForwardRequest, w http.ResponseWriter) error
}

// ForwardRequest carries the request after F-POOL-001 Pool acquire and
// F-AUTH-005 token resolution.
type ForwardRequest struct {
	TenantID            int64
	ClaimID             int64
	AccountID           int64
	UpstreamRequestBody []byte
	ClientProtocol      string // openai_chat | openai_responses | anthropic_messages
	UpstreamProtocol    string // anthropic | openai | gemini | bedrock | antigravity
	ClientStream        bool
	IdempotentReplay    bool
	RequestStartTime    int64 // Unix nanos for first-token latency
}

// EndClass enumerates the 15 stream-end classifications per spec §Phase C.
type EndClass string

const (
	EndClassGraceful              EndClass = "stream_end_graceful"
	EndClassNoTerminalMarker      EndClass = "stream_end_no_terminal_marker"
	EndClassUpstreamError4xx      EndClass = "upstream_error_4xx"
	EndClassUpstreamError5xx      EndClass = "upstream_error_5xx"
	EndClassUpstreamRateLimit     EndClass = "upstream_rate_limit"
	EndClassUpstreamAuthFailure   EndClass = "upstream_auth_failure"
	EndClassFirstTokenTimeout     EndClass = "first_token_timeout"
	EndClassInterEventTimeout     EndClass = "inter_event_timeout"
	EndClassTotalStreamTimeout    EndClass = "total_stream_timeout"
	EndClassClientDisconnect      EndClass = "client_disconnect"
	EndClassEventSizeExceeded     EndClass = "event_size_exceeded"
	EndClassOrchestratorCancelled EndClass = "orchestrator_cancelled"
	EndClassUsageAmbiguous        EndClass = "usage_ambiguous"
	EndClassUnknownTermination    EndClass = "unknown_termination"
	EndClassNonStreaming          EndClass = "non_streaming"
)

// UsageSource enumerates the 5 trust-tier sources per spec §3.
type UsageSource string

const (
	UsageSourceReported   UsageSource = "reported"
	UsageSourceNormalized UsageSource = "normalized"
	UsageSourceInferred   UsageSource = "inferred"
	UsageSourcePartial    UsageSource = "partial"
	UsageSourceAmbiguous  UsageSource = "ambiguous"
)

// TODO(phase-4): implement Forwarder using bufio.Scanner + protocol adapter
// from pkg/adapter + Tx2 settlement via internal/billing.Settler.
