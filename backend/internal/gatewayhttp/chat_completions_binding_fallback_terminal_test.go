package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// TestATBFC006TerminalFamiliesNeverTransition 从 executor 入口锁定客户限额、
// claim/money 与本地审核三类终态；任何按 HTTP 状态粗分的实现都会访问目标池。
func TestATBFC006TerminalFamiliesNeverTransition(t *testing.T) {
	plan := bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		bfcPhase(bindingfallback.ClassSafety, 54, 540, 1),
		bfcPhase(bindingfallback.ClassManual, 55, 550, 1),
	)

	t.Run("key 限额不冒充 binding quota", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{{poolID: 42, err: pool.ErrKeyRateLimited}}}
		claims := &modelFallbackClaimGate{nextClaimID: 97601}
		settler := &recordingSettler{}
		deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, plan)

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), clienterr.CodeKeyRateLimited) {
			t.Fatalf("status/body=%d/%s，期望 key_rate_limited 终态", rec.Code, rec.Body.String())
		}
		if !equalInt64s(selector.poolIDs(), []int64{42}) || len(settler.aborts) != 1 {
			t.Fatalf("pools/aborts=%v/%d，key 限额不得进入 quota", selector.poolIDs(), len(settler.aborts))
		}
		assertNoHangingModelFallbackClaims(t, claims, settler)
	})

	t.Run("claim 冲突原地终止", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{{poolID: 42, err: pool.ErrClaimRace}}}
		claims := &modelFallbackClaimGate{nextClaimID: 97651}
		settler := &recordingSettler{}
		deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, plan)

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		if rec.Code != http.StatusConflict || !equalInt64s(selector.poolIDs(), []int64{42}) {
			t.Fatalf("status/pools=%d/%v body=%s，claim 冲突不得转移", rec.Code, selector.poolIDs(), rec.Body.String())
		}
		if len(settler.aborts) != 1 || len(settler.calls) != 0 {
			t.Fatalf("abort/settle=%d/%d，期望 1/0", len(settler.aborts), len(settler.calls))
		}
		assertNoHangingModelFallbackClaims(t, claims, settler)
	})

	t.Run("billing reserve 失败不选号", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t}
		deps := bfcDeps(t, selector, bfcFailingClaimGate{err: errors.New("billing unavailable")},
			&recordingSettler{}, &pr5CanonicalSequenceDispatcher{}, plan)

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		if rec.Code != http.StatusInternalServerError || len(selector.requests) != 0 {
			t.Fatalf("status/selector=%d/%d body=%s，reserve 失败不得发送 provider", rec.Code, len(selector.requests), rec.Body.String())
		}
	})

	t.Run("本地审核拒绝不进入 safety", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t}
		claims := &modelFallbackClaimGate{nextClaimID: 97701}
		deps := bfcDeps(t, selector, claims, &recordingSettler{}, &pr5CanonicalSequenceDispatcher{}, plan)
		deps.ModerationScreener = moderation.NewScreener(moderation.ScreenerDeps{
			Config:   chatModerationConfigStore{cfg: moderation.ModerationConfig{Enabled: true, FailClosed: true}},
			Keywords: &chatModerationKeywordStore{rules: []moderation.KeywordRule{{ID: 77, Keyword: "forbidden"}}},
		})

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"forbidden"}]}`)
		if rec.Code != http.StatusForbidden || len(selector.requests) != 0 || len(claims.requests) != 0 {
			t.Fatalf("status/selector/reserve=%d/%d/%d body=%s，本地审核必须在 class 转移前终止",
				rec.Code, len(selector.requests), len(claims.requests), rec.Body.String())
		}
	})
}

// TestATBFC010BindingClassThenModelFallback 固定顺序为当前模型 normal、一次 class、
// 下一模型 normal，并证明每次失败 claim 都已关闭且最终只 settle 一次。
func TestATBFC010BindingClassThenModelFallback(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 52, accountID: 152},
		{poolID: 43, accountID: 2202},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97801}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusInternalServerError, body: `{"error":{"message":"target busy"}}`},
		{successText: "model fallback success"},
	}}
	deps := modelFallbackDeps(t, &modelFallbackSelector{}, claims, settler, dispatcher, `{
		"enabled":true,"max_depth":2,"general":{"gpt-4o":["gpt-4o-mini"]}
	}`)
	primary := bfcResolved()
	mini := modelFallbackResolved("gpt-4o-mini", 43)
	mini.BindingMetadata = []registry.BindingMetadata{bfcBinding(4300, 43, "normal", "strict_priority")}
	deps.Registry = &modelFallbackRegistry{models: map[string]registry.Resolved{
		"gpt-4o": primary, "gpt-4o-mini": mini,
	}}
	deps.Router = &modelFallbackRouter{plans: map[string]router.RoutePlan{
		"gpt-4o": bfcRoutePlan(
			[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
			bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		),
		"gpt-4o-mini": pr5RoutePlan(router.AttemptPlan{
			PoolGroupID: 43, BindingID: 4300, MaxParallelRequests: 1,
			FallbackClass: bindingfallback.ClassNormal, UpstreamModelID: "gpt-4o-mini", Reason: "model_fallback",
		}),
	}}
	deps.Selector = selector
	deps.CredentialVault = pr5CredentialVault(t, 152, 2202)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，期望第二模型 normal 成功", rec.Code, rec.Body.String())
	}
	if got := selector.poolIDs(); !equalInt64s(got, []int64{42, 52, 43}) {
		t.Fatalf("selector pools=%v，期望 normal→quota→下一模型 normal", got)
	}
	if got := strings.Join(reserveModels(claims.requests), ","); got != "gpt-4o,gpt-4o,gpt-4o-mini" {
		t.Fatalf("reserve models=%s，期望每 attempt 独立 reserve", got)
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 1 || settler.calls[0].RequestedModel != "gpt-4o-mini" {
		t.Fatalf("abort/settle/model=%d/%d/%q，期望 2/1/gpt-4o-mini",
			len(settler.aborts), len(settler.calls), settler.calls[0].RequestedModel)
	}
	if strings.Contains(string(settler.calls[0].Draft.RoutingReason), "class_transition") {
		t.Fatalf("下一模型必须从 normal 重启，不得沿用前模型 transition: %s", settler.calls[0].Draft.RoutingReason)
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

// TestATBFC010AuthSubBudgetStaysInCurrentClass 锁定 401 子预算只能在当前 class
// 换号；第二次 401 后终止，不能把凭据失败解释成 quota/manual。
func TestATBFC010AuthSubBudgetStaysInCurrentClass(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, accountID: 101}, {poolID: 42, accountID: 142},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97901}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
	}}, bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		bfcPhase(bindingfallback.ClassManual, 55, 550, 1),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnauthorized || !equalInt64s(selector.poolIDs(), []int64{42, 42}) {
		t.Fatalf("status/pools=%d/%v body=%s，auth 子预算必须留在 normal", rec.Code, selector.poolIDs(), rec.Body.String())
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 0 {
		t.Fatalf("abort/settle=%d/%d，期望两次失败均 abort 且零 settle", len(settler.aborts), len(settler.calls))
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC010TargetAuthSubBudgetStaysInTargetClass(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 52, accountID: 152},
		{poolID: 52, accountID: 153},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97951}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
		{successText: "quota auth failover success"},
	}}, bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		bfcPhase(bindingfallback.ClassManual, 55, 550, 1),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK || !equalInt64s(selector.poolIDs(), []int64{42, 52, 52}) {
		t.Fatalf("status/pools=%d/%v body=%s，目标 401 只能在 quota 内换号一次", rec.Code, selector.poolIDs(), rec.Body.String())
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 1 || settler.calls[0].AccountID != 153 {
		t.Fatalf("abort/settle/account=%d/%d/%d，期望 2/1/153",
			len(settler.aborts), len(settler.calls), settler.calls[0].AccountID)
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC010TargetSingleAccountAuthFailureIsNotMaskedByRetryExclusion(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 52, accountID: 154},
		{poolID: 52, err: &pool.NoCapacityError{
			Cause: pool.ErrNoEligibleAccount,
			Exhaustion: pool.Exhaustion{
				Family:  pool.ExhaustionFamilyStaticMismatch,
				Reasons: map[pool.GateFailureReason]int{pool.GateFailurePerRequestExclusion: 1},
			},
		}},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97961}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
	}}, bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
	))
	deps.CredentialVault = pr5CredentialVault(t, 154)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "upstream_credential_rejected") {
		t.Fatalf("status=%d body=%s，期望保留目标池首个真实认证失败", rec.Code, rec.Body.String())
	}
	if !equalInt64s(selector.poolIDs(), []int64{42, 52, 52}) {
		t.Fatalf("pools=%v，目标认证子预算只能留在 quota 池", selector.poolIDs())
	}
	if len(settler.aborts) != 3 || len(settler.calls) != 0 {
		t.Fatalf("abort/settle=%d/%d，期望三次预扣均闭环且零结算", len(settler.aborts), len(settler.calls))
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC010TerminalAuthCannotEscapeThroughModelFallback(t *testing.T) {
	tests := []struct {
		name   string
		signal bindingfallback.Signal
		want   bool
	}{
		{name: "auth 终态", signal: bindingfallback.SignalUpstreamAuthFailure, want: false},
		{name: "瞬态 5xx", signal: bindingfallback.SignalUpstreamServerError, want: true},
		{name: "兼容既有静态池耗尽", signal: bindingfallback.SignalPoolStaticMismatch, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			failure := &classifiedAttemptFailure{
				FallbackSignal: tc.signal,
				Decision:       gateway.AttemptRetryDecision{RetryableBeforeDelivery: true},
			}
			if got := allowModelFallbackAfterClass(failure); got != tc.want {
				t.Fatalf("allowModelFallbackAfterClass(%q)=%v，期望 %v", tc.signal, got, tc.want)
			}
		})
	}
}

func TestATBFCPoolExhaustionSignalPreservesExactCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bindingfallback.Signal
	}{
		{
			name: "全渠道降级保留专用信号",
			err: &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded,
				Exhaustion: pool.Exhaustion{Family: pool.ExhaustionFamilyCapacity}},
			want: bindingfallback.SignalAllChannelsDegraded,
		},
		{
			name: "纯容量耗尽",
			err: &pool.NoCapacityError{Cause: pool.ErrNoEligibleAccount,
				Exhaustion: pool.Exhaustion{Family: pool.ExhaustionFamilyCapacity}},
			want: bindingfallback.SignalPoolCapacityExhausted,
		},
		{
			name: "纯上下文窗口",
			err: &pool.NoCapacityError{Cause: pool.ErrNoEligibleAccount,
				Exhaustion: pool.Exhaustion{Family: pool.ExhaustionFamilyContextWindow}},
			want: bindingfallback.SignalLocalContextWindow,
		},
		{
			name: "静态不匹配",
			err: &pool.NoCapacityError{Cause: pool.ErrNoEligibleAccount,
				Exhaustion: pool.Exhaustion{Family: pool.ExhaustionFamilyStaticMismatch}},
			want: bindingfallback.SignalPoolStaticMismatch,
		},
		{
			name: "混合 family 不被 cause 覆盖",
			err: &pool.NoCapacityError{Cause: pool.ErrAllChannelsDegraded,
				Exhaustion: pool.Exhaustion{Family: pool.ExhaustionFamilyMixed}},
			want: bindingfallback.SignalPoolStaticMismatch,
		},
		{name: "旧式裸槽耗尽", err: pool.ErrNoSlotAvailable, want: bindingfallback.SignalPoolCapacityExhausted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolExhaustionFallbackSignal(tc.err); got != tc.want {
				t.Fatalf("signal=%q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestATBFC006DeliveredStreamOutcomeStopsBeforeTransition 直接锁住状态机的
// 已交付入口；即使失败信号本可命中 quota，也必须把请求视为终态。
func TestATBFC006DeliveredStreamOutcomeStopsBeforeTransition(t *testing.T) {
	failure := &classifiedAttemptFailure{
		DeliveredToClient: true,
		FallbackSignal:    bindingfallback.SignalBindingConcurrencyLimit,
		Decision:          gateway.AttemptRetryDecision{RetryableBeforeDelivery: true},
	}
	result, done := completedAttemptResult(attemptOutcome{DeliveryStarted: true, Failure: failure})
	if !done || !result.DeliveryStarted || result.Success != nil || result.Failure != nil {
		t.Fatalf("done/delivered/success/failure=%v/%v/%v/%v，已交付流不得进入任何后续转移",
			done, result.DeliveryStarted, result.Success != nil, result.Failure != nil)
	}
}

type bfcFailingClaimGate struct{ err error }

func (g bfcFailingClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return nil, g.err
}
