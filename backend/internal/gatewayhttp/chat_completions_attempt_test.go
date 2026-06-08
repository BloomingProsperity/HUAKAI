package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestPR4RetryKeepsGeneratedLogicalRequestIDStable(t *testing.T) {
	ex := &chatExecution{
		r:    httptest.NewRequest("POST", "/v1/chat/completions", nil),
		body: []byte(`{"model":"gpt-4.1-mini","messages":[]}`),
	}
	ex.ensureIdempotencyState()
	first := ex.logicalRequestID
	if first == "" {
		t.Fatal("first logical request id is empty")
	}

	ex.prepareNextAttemptAfterAbort()
	ex.ensureIdempotencyState()

	if ex.logicalRequestID != first {
		t.Fatalf("logical request id changed across attempts: first=%s second=%s", first, ex.logicalRequestID)
	}
}

func TestPR5EndClassFallsThroughUnknownClassificationToTransportClass(t *testing.T) {
	got := endClassFromAttemptFailure(gateway.Classification{}, gateway.AttemptRetryDecision{
		TransportClass: gateway.TransportErrorConnectTimeout,
	})
	if got != gateway.InterEventTimeout {
		t.Fatalf("EndClass=%q want %q for connect timeout", got, gateway.InterEventTimeout)
	}
}

func TestPR4PrepareNextAttemptAfterAbortClearsReservationAndAcquisition(t *testing.T) {
	token := uuid.New()
	ex := &chatExecution{
		reserveRes:        &billing.ReserveResult{ClaimID: 123},
		selRes:            &pool.SelectionResult{AccountID: 456, AcquisitionToken: token},
		acquiredAccountID: 456,
		acquisitionToken:  token,
		healthKeyOK:       true,
	}

	ex.prepareNextAttemptAfterAbort()

	if ex.reserveRes != nil {
		t.Fatalf("reserveRes still set: %+v", ex.reserveRes)
	}
	if ex.selRes != nil {
		t.Fatalf("selection still set: %+v", ex.selRes)
	}
	if ex.acquiredAccountID != 0 {
		t.Fatalf("acquiredAccountID=%d want 0", ex.acquiredAccountID)
	}
	if ex.acquisitionToken != uuid.Nil {
		t.Fatalf("acquisitionToken=%s want nil UUID", ex.acquisitionToken)
	}
	if ex.healthKeyOK {
		t.Fatal("healthKeyOK should be cleared for the next attempt")
	}
}

func TestUpstreamInboundBodyUsesResolvedModelWithoutMutatingOriginal(t *testing.T) {
	original := []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hello"}]}`)
	ex := &chatExecution{
		upstreamModelID: "fallback-model",
		body:            original,
	}

	out := ex.upstreamInboundBody(ex.body)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("outbound body is not JSON: %v", err)
	}
	if parsed["model"] != "fallback-model" {
		t.Fatalf("outbound model=%v want fallback-model body=%s", parsed["model"], string(out))
	}
	if string(ex.body) != string(original) {
		t.Fatalf("original body mutated: got %s want %s", string(ex.body), string(original))
	}
}

func TestUpstreamInboundBodyAppliesChannelBodyParamGateAfterModelRewrite(t *testing.T) {
	original := []byte(`{"model":"client-model","temperature":0.9,"service_tier":"flex","stream_options":{"include_obfuscation":true,"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`)
	ex := &chatExecution{
		upstreamModelID: "provider-model",
		body:            original,
		attempt:         router.AttemptPlan{PoolGroupID: 42},
		resolved: registry.Resolved{BindingMetadata: []registry.BindingMetadata{{
			PoolGroupID:     42,
			BodyParamStrips: []string{"service_tier", "stream_options.include_obfuscation"},
			ParamOverride: map[string]json.RawMessage{
				"temperature": json.RawMessage(`0`),
			},
		}}},
	}

	out := ex.upstreamInboundBody(ex.body)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("outbound body is not JSON: %v body=%s", err, out)
	}
	if parsed["model"] != "provider-model" {
		t.Fatalf("model=%v want provider-model body=%s", parsed["model"], out)
	}
	if parsed["temperature"] != float64(0) {
		t.Fatalf("temperature=%v want 0 body=%s", parsed["temperature"], out)
	}
	if _, ok := parsed["service_tier"]; ok {
		t.Fatalf("service_tier still present after channel strip: %s", out)
	}
	streamOptions, ok := parsed["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or non-object: %s", out)
	}
	if _, ok := streamOptions["include_obfuscation"]; ok {
		t.Fatalf("include_obfuscation still present: %s", out)
	}
	if _, ok := streamOptions["include_usage"]; !ok {
		t.Fatalf("include_usage sibling stripped: %s", out)
	}
	if string(ex.body) != string(original) {
		t.Fatalf("original body mutated: got %s want %s", string(ex.body), string(original))
	}
}

func TestDegradeFailureIfAbortFailedUsesSafeAbortReasonAndLogsErrorClass(t *testing.T) {
	const marker = "SENSITIVE_ABORT_REASON_MARKER"
	logs := captureSlogForTest(t)
	failure := terminalLocalAttemptFailure(409, "claim_race", "claim could not be completed", "claim_race", errors.New("claim race"))

	got := degradeFailureIfAbortFailed(context.Background(), "req-abort-safe", failure, errors.New(marker))
	if got == nil {
		t.Fatal("degradeFailureIfAbortFailed returned nil")
	}
	if strings.Contains(got.AbortReason, marker) || strings.Contains(got.Decision.AbortReason, marker) {
		t.Fatalf("abort reason leaked marker: failure=%+v", got)
	}
	if got.AbortReason != "claim_race;abort_failed=1" || got.Decision.AbortReason != got.AbortReason {
		t.Fatalf("abort reason=%q decision=%q want safe abort_failed marker", got.AbortReason, got.Decision.AbortReason)
	}
	assertLogContains(t, logs, "req-abort-safe", "abort_failed", "error_class")
	assertLogOmits(t, logs, marker)
}
