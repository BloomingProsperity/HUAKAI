package completionshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type completionsQuotaReserverSpy struct {
	calls  int
	last   quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (s *completionsQuotaReserverSpy) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
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

type completionsPricingRatioSignalStub struct {
	ratio   decimal.Decimal
	pending bool
}

func (s completionsPricingRatioSignalStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s.ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return s.ratio, nil
}

func (s completionsPricingRatioSignalStub) ResolveWithSignal(context.Context, int64, int64) (decimal.Decimal, bool, error) {
	ratio := s.ratio
	if ratio.IsZero() {
		ratio = decimal.NewFromInt(1)
	}
	return ratio, s.pending, nil
}

// TestCompletionsQuotaReserveCarriesInputEstimate 守 C-5:completions 的输入 token
// 估算必须传入 quota 预检,否则 token-per-window 策略会被整体绕过。
// 变异:billing.go 的 ReserveInput.ReservedTokens 改回 0 → ReservedTokens 断言红。
func TestCompletionsQuotaReserveCarriesInputEstimate(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
	})
	quotaSpy := &completionsQuotaReserverSpy{}
	env.deps.QuotaReserver = quotaSpy

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"token window","max_tokens":4}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if quotaSpy.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaSpy.calls)
	}
	if quotaSpy.last.ReservedTokens <= 0 {
		t.Fatalf("ReservedTokens=%d want >0 from prompt input estimate", quotaSpy.last.ReservedTokens)
	}
}

// TestCompletionsPredictedCostIncludesOutputEstimate 守 C-6:balance hold 与配额预留的
// predicted cost 必须包含 max_tokens 对应的输出估算成本。
// 变异:reserve() 改回只用 input cost → PredictedCost 少掉 4 个输出 token 成本而红。
func TestCompletionsPredictedCostIncludesOutputEstimate(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
	})

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"price me","max_tokens":4}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(env.claims.reserves) != 1 {
		t.Fatalf("reserve calls=%d want 1", len(env.claims.reserves))
	}
	inputTokens := estimateInputTokens([]string{"price me"})
	want := decimal.NewFromInt(int64(inputTokens)).Mul(decimal.RequireFromString("0.001")).
		Add(decimal.NewFromInt(4).Mul(decimal.RequireFromString("0.002")))
	got := env.claims.reserves[0].req.PredictedCost
	if !got.Equal(want) {
		t.Fatalf("PredictedCost=%s want %s(input %d + output max_tokens 4)", got, want, inputTokens)
	}
}

// TestCompletionsQuotaReserveInfraErrorFailsOpenWithMetric 守 C-4a:quota 后端故障
// 保持 fail-open 放行,但必须增加可观测计数。
// 变异:fail-open 分支去掉 completionsQuotaReserveFailedOpenTotal.Add → 计数差值断言红。
func TestCompletionsQuotaReserveInfraErrorFailsOpenWithMetric(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
	})
	env.deps.QuotaReserver = &completionsQuotaReserverSpy{err: errors.New("quota store down")}
	before := completionsQuotaReserveFailedOpenTotal.Value()

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"fail open","max_tokens":4}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if env.dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1 after quota fail-open", env.dispatcher.calls)
	}
	if after := completionsQuotaReserveFailedOpenTotal.Value(); after != before+1 {
		t.Fatalf("completions quota failed-open metric before/after=%d/%d want +1", before, after)
	}
}

// TestCompletionsPricingRatioPendingSignalReachesSettle 守 C-4b:ratio resolver
// 以默认比例 fail-open 时,pending 信号必须进入结算记录和成本快照。
// 变异:groupPricingRatio 改回 Resolve 丢弃 pending → PendingReconciliation 与 marker 断言红。
func TestCompletionsPricingRatioPendingSignalReachesSettle(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   `{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
	})
	env.deps.PricingRatioResolver = completionsPricingRatioSignalStub{ratio: decimal.NewFromInt(1), pending: true}

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"ratio pending","max_tokens":4}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(env.settler.settles) != 1 {
		t.Fatalf("settle calls=%d want 1", len(env.settler.settles))
	}
	draft := env.settler.settles[0].Draft
	if !draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true for pricing ratio pending signal")
	}
	if !strings.Contains(draft.CostSnapshot, "pending_reconciliation=pricing_ratio_backend_error") {
		t.Fatalf("CostSnapshot=%q want pricing_ratio pending marker", draft.CostSnapshot)
	}
}

// TestCompletionsQuotaDenyEmitsRetryAfter 守 quota deny 的可重试窗口信息:Retry-After
// 头、window_resets_at 与 window_kind 都要写回。
// 变异:deny 分支改回不带退避字段的 writeInsufficientQuotaErrorRetryable(w,0,"") → Retry-After 与 body 字段断言红。
func TestCompletionsQuotaDenyEmitsRetryAfter(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{status: http.StatusOK, body: `{}`})
	env.deps.QuotaReserver = &completionsQuotaReserverSpy{result: quota.ReserveResult{
		Allowed: false,
		Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			RetryAfter: 2 * time.Hour,
			WindowKind: quota.WindowCalendarMonth,
		},
	}}

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"quota deny","max_tokens":4}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "7200" {
		t.Fatalf("Retry-After=%q want 7200", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"window_resets_at"`, `"window_kind":"calendar_month"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s want %s", body, want)
		}
	}
	if env.dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 on quota deny", env.dispatcher.calls)
	}
}
