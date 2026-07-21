package rerankhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type rerankQuotaReserverSpy struct {
	calls  int
	last   quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (s *rerankQuotaReserverSpy) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	s.calls++
	s.last = req
	if s.err != nil {
		return quota.ReserveResult{}, s.err
	}
	if !s.result.Allowed && s.result.Decision.Kind == "" {
		return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}}, nil
	}
	return s.result, nil
}

// TestRerankQuotaReserveInfraErrorFailsOpenWithMetric 守同型 C-4a:quota 后端故障
// 保持 fail-open 放行,并增加 rerank 自己的观测计数。
// 变异:fail-open 分支去掉 rerankQuotaReserveFailedOpenTotal.Add → 计数差值断言红。
func TestRerankQuotaReserveInfraErrorFailsOpenWithMetric(t *testing.T) {
	env := newRerankTestEnv(t)
	env.deps.QuotaReserver = &rerankQuotaReserverSpy{err: errors.New("quota store down")}
	before := rerankQuotaReserveFailedOpenTotal.Value()

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if len(env.dispatcher.calls) != 1 {
		t.Fatalf("dispatcher calls=%d want 1 after quota fail-open", len(env.dispatcher.calls))
	}
	if after := rerankQuotaReserveFailedOpenTotal.Value(); after != before+1 {
		t.Fatalf("rerank quota failed-open metric before/after=%d/%d want +1", before, after)
	}
}

// TestRerankQuotaDenyEmitsRetryAfter 守同型 quota deny 响应:窗口拒绝必须透出
// Retry-After、window_resets_at 与 window_kind。
// 变异:deny 分支改回不带退避字段的 writeInsufficientQuotaErrorRetryable(w,0,"") → Retry-After 与 body 字段断言红。
func TestRerankQuotaDenyEmitsRetryAfter(t *testing.T) {
	env := newRerankTestEnv(t)
	env.deps.QuotaReserver = &rerankQuotaReserverSpy{result: quota.ReserveResult{
		Allowed: false,
		Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			RetryAfter: 45 * time.Second,
			WindowKind: quota.WindowFixed,
		},
	}}

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "45" {
		t.Fatalf("Retry-After=%q want 45", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"window_resets_at"`, `"window_kind":"fixed"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s want %s", body, want)
		}
	}
	if len(env.dispatcher.calls) != 0 {
		t.Fatalf("dispatcher calls=%d want 0 on quota deny", len(env.dispatcher.calls))
	}
}

func TestRerankQuotaReserveCarriesEstimatedInputTokens(t *testing.T) {
	env := newRerankTestEnv(t)
	spy := &rerankQuotaReserverSpy{result: quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}}}
	env.deps.QuotaReserver = spy

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，期望 200", rec.Code, rec.Body.String())
	}
	if spy.calls != 1 || spy.last.ReservedTokens <= 0 {
		t.Fatalf("quota calls=%d reserved_tokens=%d，期望携带正数输入估算", spy.calls, spy.last.ReservedTokens)
	}
}
