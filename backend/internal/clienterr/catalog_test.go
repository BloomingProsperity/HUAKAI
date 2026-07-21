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
		{CodeBindingConcurrencyLimited, "binding concurrency limit exceeded; retry later"},
		{CodeGroupPolicyUnavailable, "group routing policy is temporarily unavailable"},
		{CodeAuditRefMissing, "Audit reference missing for money-path operation."},
		{CodeStreamForwardError, "upstream stream failed before delivery"},
		{CodeContentPolicyViolation, "request violates content policy"},
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

func TestLogInternalWritesRequestCodeAndErrorClassWithoutRawError(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	const marker = "RAWPROMPT_SECRET_MARKER"
	const token = "sk-rawprompt-secret-marker"
	LogInternal(context.Background(), "req-log-1", CodeReserveError, errors.New("upstream raw body prompt="+marker+" authorization=Bearer "+token))

	got := buf.String()
	for _, want := range []string{"req-log-1", CodeReserveError, "error_class"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output=%s missing %q", got, want)
		}
	}
	for _, forbidden := range []string{marker, token, "raw body prompt"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("log output leaked %q: %s", forbidden, got)
		}
	}
}
