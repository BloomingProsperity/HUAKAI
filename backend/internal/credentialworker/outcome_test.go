package credentialworker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
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

// ANT-3 集成断言: 真 adapters.AnthropicRefresh 401 → ClassifyRefreshError
// 必返 OutcomeAuthExpired。锁 anthropic vendor 401 永远走 AuthExpired
// 的 invariant — cursor C1 教训, adapter 层 unit test 自己 happy 不够,
// 必须用上层 classifier wiring 真实路径验证。判别 mutation: 把 vendor
// 从 internal/auth/audit.go isKnownRefreshVendor 列表删,该 test 立刻
// 退到 OutcomeUnknown 变红 (anthropic 401 not auto-expired any more)。
// 注: tokenHTTPError body 透传是 defense-in-depth (未来 vendor 不在
// known list 时仍能通过 msg 含 invalid_grant 分类), 不是本 test 的
// discriminating fixture。
func TestClassifyRefreshErrorIntegratesAnthropicAdapter401InvalidGrant(t *testing.T) {
	client := &http.Client{Transport: outcomeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"refresh token expired"}`)),
		}, nil
	})}
	cred := []byte(`{"access_token":"old","refresh_token":"rt-old"}`)
	_, _, err := adapters.AnthropicRefresh{HTTPClient: client}.RefreshForProvider(context.Background(), 1, "anthropic", cred)
	if err == nil {
		t.Fatal("expected refresh error on upstream 401")
	}
	if got := ClassifyRefreshError(err, "anthropic", http.StatusUnauthorized); got != OutcomeAuthExpired {
		t.Fatalf("integrated outcome=%q, want %q; err=%v", got, OutcomeAuthExpired, err)
	}
}

type outcomeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f outcomeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
