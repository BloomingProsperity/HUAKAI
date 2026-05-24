package credentialworker

import (
	"errors"
	"net/http"
	"testing"
)

func TestClassifyRefreshErrorAnthropic401InvalidGrantAuthExpired(t *testing.T) {
	err := errors.New(`anthropic refresh failed: {"error":"invalid_grant"}`)

	if got := ClassifyRefreshError(err, "anthropic", http.StatusUnauthorized); got != OutcomeAuthExpired {
		t.Fatalf("401 invalid_grant outcome=%q, want %q", got, OutcomeAuthExpired)
	}
	if got := ClassifyRefreshError(nil, "anthropic", http.StatusOK); got != OutcomeSuccess {
		t.Fatalf("200 OK outcome=%q, want %q", got, OutcomeSuccess)
	}
}

func TestClassifyRefreshError429RateLimit(t *testing.T) {
	err := errors.New("upstream token endpoint returned 429 too many requests")

	if got := ClassifyRefreshError(err, "anthropic", http.StatusTooManyRequests); got != OutcomeRateLimit {
		t.Fatalf("429 outcome=%q, want %q", got, OutcomeRateLimit)
	}
	if got := ClassifyRefreshError(errors.New("invalid_grant"), "anthropic", http.StatusUnauthorized); got != OutcomeAuthExpired {
		t.Fatalf("401 control outcome=%q, want %q", got, OutcomeAuthExpired)
	}
}

func TestClassifyRefreshError403RiskControl(t *testing.T) {
	err := errors.New(`anthropic refresh failed: 403 risk control triggered`)

	if got := ClassifyRefreshError(err, "anthropic", http.StatusForbidden); got != OutcomeRiskControl {
		t.Fatalf("403 risk outcome=%q, want %q", got, OutcomeRiskControl)
	}
	if got := ClassifyRefreshError(errors.New("forbidden by policy"), "anthropic", http.StatusForbidden); got != OutcomeUnknown {
		t.Fatalf("403 without risk outcome=%q, want %q", got, OutcomeUnknown)
	}
}

func TestClassifyRefreshErrorAccountDisabled(t *testing.T) {
	err := errors.New("copilot refresh failed: account disabled")

	if got := ClassifyRefreshError(err, "copilot", http.StatusForbidden); got != OutcomeAccountDisabled {
		t.Fatalf("disabled account outcome=%q, want %q", got, OutcomeAccountDisabled)
	}
	if got := ClassifyRefreshError(errors.New("forbidden by policy"), "copilot", http.StatusForbidden); got != OutcomeUnknown {
		t.Fatalf("403 without disabled marker outcome=%q, want %q", got, OutcomeUnknown)
	}
}

func TestClassifyRefreshError5xxTransient(t *testing.T) {
	err := errors.New("oauth token endpoint returned status 502")

	if got := ClassifyRefreshError(err, "anthropic", http.StatusBadGateway); got != OutcomeTransientError {
		t.Fatalf("5xx outcome=%q, want %q", got, OutcomeTransientError)
	}
	if got := ClassifyRefreshError(errors.New("bad request"), "anthropic", http.StatusBadRequest); got != OutcomeUnknown {
		t.Fatalf("400 control outcome=%q, want %q", got, OutcomeUnknown)
	}
}
