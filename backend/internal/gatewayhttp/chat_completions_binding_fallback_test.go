package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/queuewait"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestATBFC001NormalSuccessNeverTouchesFallbackClass(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{{poolID: 42, accountID: 101}}}
	claims := &modelFallbackClaimGate{nextClaimID: 97001}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "normal success"}}}
	deps := bfcDeps(t, selector, claims, settler, dispatcher, bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 9),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，期望 normal 直接成功", rec.Code, rec.Body.String())
	}
	if got := selector.poolIDs(); !equalInt64s(got, []int64{42}) {
		t.Fatalf("selector pools=%v，期望只访问 normal [42]", got)
	}
	if dispatcher.calls != 1 || len(claims.requests) != 1 || len(settler.calls) != 1 || len(settler.aborts) != 0 {
		t.Fatalf("dispatch/reserve/settle/abort=%d/%d/%d/%d，期望 1/1/1/0",
			dispatcher.calls, len(claims.requests), len(settler.calls), len(settler.aborts))
	}
}

func TestATBFC001SecondNormalSuccessStillNeverTouchesFallbackClass(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 43, accountID: 143},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97021}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler,
		&pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "second normal success"}}},
		bfcRoutePlan(
			[]router.AttemptPlan{
				bfcAttempt(42, 420, 1, bindingfallback.ClassNormal),
				bfcAttempt(43, 430, 1, bindingfallback.ClassNormal),
			},
			bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		),
	)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK || !equalInt64s(selector.poolIDs(), []int64{42, 43}) {
		t.Fatalf("status/pools=%d/%v body=%s，第二个 normal 成功时不得提前进入 quota",
			rec.Code, selector.poolIDs(), rec.Body.String())
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 1 {
		t.Fatalf("abort/settle=%d/%d，期望 1/1", len(settler.aborts), len(settler.calls))
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC003MissingTargetPreservesBinding429Terminal(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		clientCode string
		abort      string
	}{
		{name: "binding 并发", err: pool.ErrBindingConcurrencyLimited, clientCode: clienterr.CodeBindingConcurrencyLimited, abort: "binding_concurrency_limited"},
		{name: "binding RPM", err: pool.ErrBindingRateLimited, clientCode: clienterr.CodeKeyRateLimited, abort: "binding_rate_limited"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{{poolID: 42, err: tc.err}}}
			claims := &modelFallbackClaimGate{nextClaimID: 97051}
			settler := &recordingSettler{}
			deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, bfcRoutePlan(
				[]router.AttemptPlan{
					bfcAttempt(42, 420, 1, bindingfallback.ClassNormal),
					bfcAttempt(43, 430, 1, bindingfallback.ClassNormal),
				},
			))

			rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
			if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), tc.clientCode) {
				t.Fatalf("status/body=%d/%s，缺 quota 目标时必须保持原专用 429", rec.Code, rec.Body.String())
			}
			if got := selector.poolIDs(); !equalInt64s(got, []int64{42}) {
				t.Fatalf("selector pools=%v，缺目标时不得把原终态翻成 normal retry", got)
			}
			if len(settler.aborts) != 1 || len(settler.calls) != 0 {
				t.Fatalf("abort/settle=%d/%d，期望 1/0", len(settler.aborts), len(settler.calls))
			}
			if settler.aborts[0].reason != tc.abort {
				t.Fatalf("abort reason=%q，期望 %q", settler.aborts[0].reason, tc.abort)
			}
			assertNoHangingModelFallbackClaims(t, claims, settler)
		})
	}
}

func TestATBFC002BindingRateLimitTransitionsToQuota(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingRateLimited},
		{poolID: 52, accountID: 152},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97071}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler,
		&pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "binding RPM target success"}}},
		bfcRoutePlan(
			[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
			bfcPhase(bindingfallback.ClassQuota, 52, 520, 1),
		),
	)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK || !equalInt64s(selector.poolIDs(), []int64{42, 52}) {
		t.Fatalf("status/pools=%d/%v body=%s，binding RPM 应进入 quota", rec.Code, selector.poolIDs(), rec.Body.String())
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 1 {
		t.Fatalf("abort/settle=%d/%d，期望 1/1", len(settler.aborts), len(settler.calls))
	}
	if settler.aborts[0].reason != "binding_rate_limited" {
		t.Fatalf("abort reason=%q，期望 binding_rate_limited", settler.aborts[0].reason)
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC002And009SameClassExhaustionTransitionsOnceAndClosesMoney(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 43, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 52, accountID: 152, routingReason: []byte(`{"selector":"quota-target"}`)},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97101}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "quota target success"}}}
	deps := bfcDeps(t, selector, claims, settler, dispatcher, bfcRoutePlan(
		[]router.AttemptPlan{
			bfcAttempt(42, 420, 1, bindingfallback.ClassNormal),
			bfcAttempt(43, 430, 2, bindingfallback.ClassNormal),
		},
		bfcPhase(bindingfallback.ClassContextWindow, 53, 530, 3),
		bfcPhase(bindingfallback.ClassSafety, 54, 540, 4),
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 9),
		bfcPhase(bindingfallback.ClassManual, 55, 550, 5),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s，期望 quota 目标成功", rec.Code, rec.Body.String())
	}
	if got := selector.poolIDs(); !equalInt64s(got, []int64{42, 43, 52}) {
		t.Fatalf("selector pools=%v，期望 normal 全耗尽后恰一次 quota [42 43 52]", got)
	}
	if len(selector.requests) != 3 {
		t.Fatalf("selector calls=%d，期望 3", len(selector.requests))
	}
	targetReq := selector.requests[2]
	if targetReq.BindingID != 520 || targetReq.MaxParallelRequests != 9 || targetReq.SelectionMode != "priority_weighted" {
		t.Fatalf("目标 request binding/K/mode=%d/%d/%q，期望 520/9/priority_weighted",
			targetReq.BindingID, targetReq.MaxParallelRequests, targetReq.SelectionMode)
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 1 || len(claims.requests) != 3 {
		t.Fatalf("abort/settle/reserve=%d/%d/%d，期望 2/1/3", len(settler.aborts), len(settler.calls), len(claims.requests))
	}
	if settler.calls[0].AccountID != 152 || settler.calls[0].AttemptSeq != 3 {
		t.Fatalf("settle account/attempt=%d/%d，期望 152/3", settler.calls[0].AccountID, settler.calls[0].AttemptSeq)
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)

	var reason struct {
		Transition struct {
			From    string `json:"from"`
			To      string `json:"to"`
			Trigger string `json:"trigger"`
		} `json:"class_transition"`
	}
	if err := json.Unmarshal(settler.calls[0].Draft.RoutingReason, &reason); err != nil {
		t.Fatalf("routing reason 非法 JSON: %v body=%s", err, settler.calls[0].Draft.RoutingReason)
	}
	if reason.Transition.From != "normal" || reason.Transition.To != "quota" || reason.Transition.Trigger != "binding_concurrency_limit" {
		t.Fatalf("class transition=%+v，期望 normal→quota/binding_concurrency_limit", reason.Transition)
	}
}

func TestATBFC002MixedFailureFamiliesDoNotGuessTarget(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 43, accountID: 143},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97201}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{status: http.StatusInternalServerError, body: `{"error":{"message":"busy"}}`}}}
	deps := bfcDeps(t, selector, claims, settler, dispatcher, bfcRoutePlan(
		[]router.AttemptPlan{
			bfcAttempt(42, 420, 1, bindingfallback.ClassNormal),
			bfcAttempt(43, 430, 2, bindingfallback.ClassNormal),
		},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 9),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s，期望保留最后一个 upstream 5xx 终态", rec.Code, rec.Body.String())
	}
	if got := selector.poolIDs(); !equalInt64s(got, []int64{42, 43}) {
		t.Fatalf("selector pools=%v，即使 manual 未配置，混合 quota/manual 也不得访问目标类", got)
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 0 {
		t.Fatalf("abort/settle=%d/%d，期望 2/0", len(settler.aborts), len(settler.calls))
	}
}

func TestATBFC004TargetBindingCapIsIndependentAndTerminal(t *testing.T) {
	selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
		{poolID: 42, err: pool.ErrBindingConcurrencyLimited},
		{poolID: 52, err: pool.ErrBindingConcurrencyLimited},
	}}
	claims := &modelFallbackClaimGate{nextClaimID: 97301}
	settler := &recordingSettler{}
	deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, bfcRoutePlan(
		[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
		bfcPhase(bindingfallback.ClassQuota, 52, 520, 7),
		bfcPhase(bindingfallback.ClassManual, 55, 550, 5),
	))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), clienterr.CodeBindingConcurrencyLimited) {
		t.Fatalf("status/body=%d/%s，目标 binding 满应保持专用 429", rec.Code, rec.Body.String())
	}
	if got := selector.poolIDs(); !equalInt64s(got, []int64{42, 52}) {
		t.Fatalf("selector pools=%v，目标满后不得递归 manual", got)
	}
	if selector.requests[0].BindingID != 420 || selector.requests[1].BindingID != 520 ||
		selector.requests[0].MaxParallelRequests != 1 || selector.requests[1].MaxParallelRequests != 7 {
		t.Fatalf("binding/K 叠加错误: first=%d/%d target=%d/%d",
			selector.requests[0].BindingID, selector.requests[0].MaxParallelRequests,
			selector.requests[1].BindingID, selector.requests[1].MaxParallelRequests)
	}
	if len(settler.aborts) != 2 || len(settler.calls) != 0 {
		t.Fatalf("abort/settle=%d/%d，期望 2/0", len(settler.aborts), len(settler.calls))
	}
	assertNoHangingModelFallbackClaims(t, claims, settler)
}

func TestATBFC005QueueWaitPrecedesQuotaTransition(t *testing.T) {
	t.Run("等待后取得 normal 不转移", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
			{poolID: 42, result: &pool.SelectionResult{WaitPlan: &pool.WaitPlan{AccountID: 101, TimeoutMS: 1000, MaxWaiting: 1}}},
		}}
		claims := &modelFallbackClaimGate{nextClaimID: 97351}
		settler := &recordingSettler{}
		deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, bfcRoutePlan(
			[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
			bfcPhase(bindingfallback.ClassQuota, 52, 520, 7),
		))
		deps.QueueWaiter = fixedQueueWaiter{result: queuewait.Result{
			Status: queuewait.StatusAcquired,
			Selection: &pool.SelectionResult{
				AccountID: 101, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"selector":"wait-acquired"}`),
			},
		}}

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		if rec.Code != http.StatusOK || !equalInt64s(selector.poolIDs(), []int64{42}) {
			t.Fatalf("status/pools=%d/%v body=%s，normal 等待成功必须零 fallback", rec.Code, selector.poolIDs(), rec.Body.String())
		}
		if len(settler.aborts) != 0 || len(settler.calls) != 1 {
			t.Fatalf("abort/settle=%d/%d，期望 0/1", len(settler.aborts), len(settler.calls))
		}
	})

	for _, tc := range []struct {
		name   string
		status queuewait.Status
	}{
		{name: "timeout", status: queuewait.StatusTimeout},
		{name: "overflow", status: queuewait.StatusOverflow},
	} {
		t.Run(tc.name+" 才转移", func(t *testing.T) {
			selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
				{poolID: 42, result: &pool.SelectionResult{WaitPlan: &pool.WaitPlan{AccountID: 101, TimeoutMS: 1000, MaxWaiting: 1}}},
				{poolID: 52, accountID: 152},
			}}
			claims := &modelFallbackClaimGate{nextClaimID: 97401}
			settler := &recordingSettler{}
			deps := bfcDeps(t, selector, claims, settler, &pr5CanonicalSequenceDispatcher{}, bfcRoutePlan(
				[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
				bfcPhase(bindingfallback.ClassQuota, 52, 520, 7),
			))
			deps.QueueWaiter = fixedQueueWaiter{result: queuewait.Result{Status: tc.status}}

			rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
			if rec.Code != http.StatusOK || !equalInt64s(selector.poolIDs(), []int64{42, 52}) {
				t.Fatalf("status/pools=%d/%v body=%s，期望 wait %s 后 quota 成功", rec.Code, selector.poolIDs(), rec.Body.String(), tc.name)
			}
			if len(settler.aborts) != 1 || len(settler.calls) != 1 {
				t.Fatalf("abort/settle=%d/%d，期望 1/1", len(settler.aborts), len(settler.calls))
			}
		})
	}

	t.Run("cancel 终态", func(t *testing.T) {
		selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
			{poolID: 42, result: &pool.SelectionResult{WaitPlan: &pool.WaitPlan{AccountID: 101, TimeoutMS: 1000, MaxWaiting: 1}}},
		}}
		deps := bfcDeps(t, selector, &modelFallbackClaimGate{nextClaimID: 97451}, &recordingSettler{}, &pr5CanonicalSequenceDispatcher{}, bfcRoutePlan(
			[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
			bfcPhase(bindingfallback.ClassQuota, 52, 520, 7),
		))
		deps.QueueWaiter = fixedQueueWaiter{result: queuewait.Result{Status: queuewait.StatusCancelled, Err: context.Canceled}}

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		if rec.Code != http.StatusTooManyRequests || !equalInt64s(selector.poolIDs(), []int64{42}) {
			t.Fatalf("status/pools=%d/%v body=%s，cancel 后不得进入 quota", rec.Code, selector.poolIDs(), rec.Body.String())
		}
	})
}

func TestATBFC007FourSignalsSelectOnlyMatchingClass(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantPoolID int64
	}{
		{name: "quota", status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error"}}`, wantPoolID: 52},
		{name: "context_window", status: http.StatusBadRequest, body: `{"error":{"code":"context_length_exceeded","message":"SENSITIVE_CONTEXT_TEXT"}}`, wantPoolID: 53},
		{name: "safety", status: http.StatusForbidden, body: `{"error":{"code":"content_policy_violation","message":"SENSITIVE_POLICY_TEXT"}}`, wantPoolID: 54},
		{name: "manual", status: http.StatusInternalServerError, body: `{"error":{"message":"SENSITIVE_UPSTREAM_TEXT"}}`, wantPoolID: 55},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selector := &bfcScriptedSelector{t: t, steps: []bfcSelectorStep{
				{poolID: 42, accountID: 101},
				{poolID: tc.wantPoolID, accountID: 100 + tc.wantPoolID},
			}}
			settler := &recordingSettler{}
			deps := bfcDeps(t, selector, &modelFallbackClaimGate{nextClaimID: 97501}, settler,
				&pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
					{status: tc.status, body: tc.body},
					{successText: tc.name + " target success"},
				}}, bfcRoutePlan(
					[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
					bfcPhase(bindingfallback.ClassContextWindow, 53, 530, 3),
					bfcPhase(bindingfallback.ClassSafety, 54, 540, 4),
					bfcPhase(bindingfallback.ClassQuota, 52, 520, 7),
					bfcPhase(bindingfallback.ClassManual, 55, 550, 5),
				))

			rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s，期望对应 class 成功", rec.Code, rec.Body.String())
			}
			if got := selector.poolIDs(); !equalInt64s(got, []int64{42, tc.wantPoolID}) {
				t.Fatalf("selector pools=%v，期望 [42 %d]", got, tc.wantPoolID)
			}
			if tc.name == "quota" || tc.name == "manual" {
				if _, excluded := selector.requests[1].ExcludedAccounts[101]; !excluded {
					t.Fatalf("目标 class exclusions=%v，必须继承 normal 失败账号 101", selector.requests[1].ExcludedAccounts)
				}
			}
			if len(settler.aborts) != 1 || len(settler.calls) != 1 {
				t.Fatalf("abort/settle=%d/%d，期望 1/1", len(settler.aborts), len(settler.calls))
			}
			for _, forbidden := range []string{"SENSITIVE_CONTEXT_TEXT", "SENSITIVE_POLICY_TEXT", "SENSITIVE_UPSTREAM_TEXT"} {
				if strings.Contains(string(settler.calls[0].Draft.RoutingReason), forbidden) {
					t.Fatalf("routing reason 泄露上游原始文本 %q: %s", forbidden, settler.calls[0].Draft.RoutingReason)
				}
			}
		})
	}
}

func TestATBFC007MachineCodeClassifierRejectsBroadStatusGuessing(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   int
		body     string
		want     bindingfallback.Signal
	}{
		{name: "普通413", provider: "anthropic", status: http.StatusRequestEntityTooLarge, body: `{"error":{"type":"request_too_large"}}`, want: bindingfallback.SignalRequestBodyTooLarge},
		{name: "权限403", provider: "openai", status: http.StatusForbidden, body: `{"error":{"code":"permission_denied"}}`, want: bindingfallback.SignalPermissionDenied},
		{name: "参数400", provider: "openai", status: http.StatusBadRequest, body: `{"error":{"type":"invalid_request_error"}}`, want: bindingfallback.SignalRequestMalformed},
		{name: "上游额度429", provider: "openai", status: http.StatusTooManyRequests, body: `{"error":{"type":"insufficient_quota"}}`, want: bindingfallback.SignalUpstreamAuthFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, classification, err := gateway.ClassifyAttemptHTTPError(tc.status, nil, []byte(tc.body), tc.provider)
			if err != nil {
				t.Fatalf("ClassifyAttemptHTTPError: %v", err)
			}
			if got := bindingFallbackSignalFromUpstream(tc.status, []byte(tc.body), classification, decision); got != tc.want {
				t.Fatalf("signal=%q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestATBFCContextTargetSkipsOnlyCanonicalWindowGate(t *testing.T) {
	maxTokens := 16
	ex := &chatExecution{
		body:       []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`),
		req:        chatRequest{Model: "gpt-4o", MaxTokens: &maxTokens},
		reserveRes: &billing.ReserveResult{ClaimID: 99},
		resolved: registry.Resolved{
			ProtocolFamily: "openai_chat",
			ContextWindow:  10,
		},
		plan: bfcRoutePlan(
			[]router.AttemptPlan{bfcAttempt(42, 420, 1, bindingfallback.ClassNormal)},
			bfcPhase(bindingfallback.ClassContextWindow, 53, 530, 3),
		),
	}
	normal := ex.plan.Attempts[0]
	ex.activateRouteAttempt(normal)
	normalReq := ex.buildPoolSelectionRequest(attemptInput{Plan: normal, AttemptSeq: 1})
	contextAttempt := ex.plan.FallbackPhases[0].Attempts[0]
	ex.activateRouteAttempt(contextAttempt)
	targetReq := ex.buildPoolSelectionRequest(attemptInput{Plan: contextAttempt, AttemptSeq: 2})

	if normalReq.ModelContextWindow != 10 || normalReq.MaxOutputTokens != 16 || normalReq.EstimatedInputTokens <= 0 {
		t.Fatalf("normal context inputs=%+v，期望窗口/输出预留/估算全部接线", normalReq)
	}
	if targetReq.ModelContextWindow != 0 || targetReq.MaxOutputTokens != normalReq.MaxOutputTokens ||
		targetReq.EstimatedInputTokens != normalReq.EstimatedInputTokens {
		t.Fatalf("target context inputs=%+v，期望只清 canonical window，其余 gate 输入不变", targetReq)
	}
}

func TestATBFCAuditTransitionSanitizesNullSelectorReason(t *testing.T) {
	encoded := routingReasonWithClassTransition([]byte("null"), bindingClassTransition{
		From: bindingfallback.ClassNormal, To: bindingfallback.ClassQuota,
		Trigger: bindingfallback.SignalBindingConcurrencyLimit,
	})
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("routing reason 非法 JSON: %v", err)
	}
	if payload["routing_reason_error"] != "selector_reason_empty" || payload["class_transition"] == nil {
		t.Fatalf("sanitized routing reason=%v，期望保留 transition 且不因 null panic", payload)
	}
}

type bfcSelectorStep struct {
	poolID        int64
	accountID     int64
	result        *pool.SelectionResult
	err           error
	routingReason []byte
}

type bfcScriptedSelector struct {
	t        *testing.T
	steps    []bfcSelectorStep
	requests []pool.SelectionRequest
}

func (s *bfcScriptedSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.t.Helper()
	idx := len(s.requests)
	s.requests = append(s.requests, req)
	if idx >= len(s.steps) {
		s.t.Fatalf("selector 调用超出脚本: call=%d pool=%d", idx+1, req.PoolGroupID)
	}
	step := s.steps[idx]
	if req.PoolGroupID != step.poolID {
		s.t.Fatalf("selector call=%d pool=%d，期望 %d", idx+1, req.PoolGroupID, step.poolID)
	}
	if step.err != nil || step.result != nil {
		return step.result, step.err
	}
	reason := step.routingReason
	if len(reason) == 0 {
		reason = []byte(`{"selector":"bfc"}`)
	}
	return &pool.SelectionResult{AccountID: step.accountID, AcquisitionToken: uuid.New(), RoutingReasonJSON: reason}, nil
}

func (s *bfcScriptedSelector) poolIDs() []int64 {
	out := make([]int64, 0, len(s.requests))
	for _, req := range s.requests {
		out = append(out, req.PoolGroupID)
	}
	return out
}

func bfcDeps(t *testing.T, selector pool.Selector, claimGate billing.ClaimGate, settler billing.Settler, dispatcher HCSFDispatcher, plan router.RoutePlan) ChatHandlerDeps {
	t.Helper()
	enableHCSFDispatchForTest(t)
	deps := pr5NonStreamDeps(t, selector, claimGate, settler, dispatcher)
	deps.Registry = stubRegistry{resolved: bfcResolved()}
	deps.Router = stubRouter{plan: plan}
	deps.CredentialVault = pr5CredentialVault(t, 101, 142, 143, 152, 153, 154, 155)
	return deps
}

func bfcResolved() registry.Resolved {
	return registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_chat",
		ContextWindow:    128000,
		PoolCandidates:   []int64{42, 43, 52, 53, 54, 55},
		BindingMetadata: []registry.BindingMetadata{
			bfcBinding(420, 42, "normal", "strict_priority"),
			bfcBinding(430, 43, "normal", "strict_priority"),
			bfcBinding(520, 52, "quota", "priority_weighted"),
			bfcBinding(530, 53, "context_window", "strict_priority"),
			bfcBinding(540, 54, "safety", "strict_priority"),
			bfcBinding(550, 55, "manual", "strict_priority"),
		},
		SnapshotVersion: "registry:7:bfc",
	}
}

func bfcBinding(id, poolID int64, class, selectionMode string) registry.BindingMetadata {
	return registry.BindingMetadata{BindingID: id, PoolGroupID: poolID, FallbackClass: class, SelectionMode: selectionMode}
}

func bfcRoutePlan(normal []router.AttemptPlan, phases ...router.FallbackPhasePlan) router.RoutePlan {
	plan := pr5RoutePlan(normal...)
	plan.FallbackPhases = phases
	plan.SnapshotVersion = "registry:7:bfc;router:v0.3-binding-fallback-class"
	return plan
}

func bfcAttempt(poolID, bindingID, maxParallel int64, class bindingfallback.Class) router.AttemptPlan {
	return router.AttemptPlan{
		PoolGroupID: poolID, BindingID: bindingID, MaxParallelRequests: maxParallel,
		FallbackClass: class, UpstreamModelID: "gpt-4o", Reason: "bfc_" + string(class),
	}
}

func bfcPhase(class bindingfallback.Class, poolID, bindingID, maxParallel int64) router.FallbackPhasePlan {
	return router.FallbackPhasePlan{
		FallbackClass: class,
		Attempts:      []router.AttemptPlan{bfcAttempt(poolID, bindingID, maxParallel, class)},
		AttemptBudget: 1,
	}
}

func equalInt64s(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

var _ pool.Selector = (*bfcScriptedSelector)(nil)
