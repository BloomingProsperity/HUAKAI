package gatewayhttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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
