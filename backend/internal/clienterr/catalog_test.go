package clienterr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestMessageForKnownCodesAndFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want string
	}{
		{CodeRegistryUnknownError, "model registry failed"},
		{CodeRouterPlanError, "route planning failed"},
		{CodeInvalidJSON, "request body is not valid JSON"},
		{CodeUpstreamDispatchError, "upstream request failed"},
		{CodeInsufficientBalance, "余额不足"},
		{CodeCanonicalResponseError, "upstream response could not be converted"},
		{CodeQueueWait, "request is queued; retry later"},
		{CodeAuditRefMissing, "Audit reference missing for money-path operation."},
		{CodeStreamForwardError, "upstream stream failed before delivery"},
		{CodeAbortFailed, "internal settlement failed"},
		{"unknown_future_code", "request failed"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()

			if got := MessageFor(tt.code); got != tt.want {
				t.Fatalf("MessageFor(%q)=%q want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestMessageForNeverReflectsCallerInput(t *testing.T) {
	t.Parallel()

	marker := "SENSITIVE_PUBLIC_ERROR_MARKER"
	if got := MessageFor(marker); strings.Contains(got, marker) {
		t.Fatalf("fallback message leaked caller input: %q", got)
	}
}

func TestLogInternalWritesRequestCodeAndRawError(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	LogInternal(context.Background(), "req-log-1", CodeReserveError, errors.New("SENSITIVE_LOG_MARKER"))

	got := buf.String()
	for _, want := range []string{"req-log-1", CodeReserveError, "SENSITIVE_LOG_MARKER"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output=%s missing %q", got, want)
		}
	}
}
