package gatewayhttp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/queuewait"
	"github.com/BloomingProsperity/HUAKAI/internal/warmupintercept"
)

// QueueWaiter 执行 selector 返回的 WaitPlan，并在成功时交回真实 selector 结果。
type QueueWaiter interface {
	Wait(context.Context, pool.Selector, pool.SelectionRequest, *pool.WaitPlan) queuewait.Result
}

func ensureChatQueueWaiter(d *ChatHandlerDeps) {
	if d != nil && d.QueueWaiter == nil {
		d.QueueWaiter = queuewait.NewExecutor()
	}
}

func (ex *chatExecution) handleQueueWaitPlan(w http.ResponseWriter, req pool.SelectionRequest, plan *pool.WaitPlan) *classifiedAttemptFailure {
	waiter := ex.d.QueueWaiter
	if waiter == nil {
		waiter = queuewait.NewExecutor()
	}
	budget := plan.TimeoutMS - ex.queueWaitSpentMS
	if budget <= 0 {
		return ex.queueWaitRetryableFailure(plan, nil, "queue_wait")
	}
	waitPlan := *plan
	waitPlan.TimeoutMS = budget
	startedAt := ex.currentQueueWaitTime()
	result := waiter.Wait(ex.ctx, ex.d.Selector, req, &waitPlan)
	if result.Status != queuewait.StatusOverflow {
		ex.addQueueWaitElapsed(startedAt, ex.currentQueueWaitTime())
	}
	switch result.Status {
	case queuewait.StatusAcquired:
		if result.Selection == nil || result.Selection.AccountID == 0 {
			return ex.classifyPoolSelectFailure(w, queuewait.ErrNoSelection)
		}
		ex.acceptPoolSelection(result.Selection)
		return nil
	case queuewait.StatusSelectorError:
		return ex.classifyPoolSelectFailure(w, result.Err)
	case queuewait.StatusOverflow, queuewait.StatusTimeout:
		failure := ex.queueWaitRetryableFailure(plan, result.Err, "queue_wait")
		if result.Status == queuewait.StatusOverflow {
			failure.FallbackSignal = bindingfallback.SignalQueueWaitOverflow
		} else {
			failure.FallbackSignal = bindingfallback.SignalQueueWaitTimeout
		}
		return failure
	case queuewait.StatusCancelled:
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "queue_wait_cancelled", 0, ex.protocolLoss)
		failure := terminalLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeQueueWait, clienterr.MessageFor(clienterr.CodeQueueWait), "queue_wait_cancelled", result.Err)
		failure.FallbackSignal = bindingfallback.SignalQueueWaitCancelled
		failure.RetryAfterSeconds = retryAfterSecondsForWaitPlan(plan)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	default:
		return ex.queueWaitRetryableFailure(plan, result.Err, "queue_wait")
	}
}

func (ex *chatExecution) queueWaitRetryableFailure(plan *pool.WaitPlan, cause error, abortReason string) *classifiedAttemptFailure {
	abortErr := ex.abortReservation(ex.reserveRes.ClaimID, abortReason, 0, ex.protocolLoss)
	failure := retryableLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeQueueWait, clienterr.MessageFor(clienterr.CodeQueueWait), abortReason, gateway.UpstreamRateLimit, cause)
	failure.FallbackSignal = bindingfallback.SignalQueueWaitTimeout
	failure.RetryAfterSeconds = retryAfterSecondsForWaitPlan(plan)
	return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
}

func (ex *chatExecution) currentQueueWaitTime() time.Time {
	if ex != nil && ex.queueWaitNow != nil {
		return ex.queueWaitNow()
	}
	return time.Now()
}

func (ex *chatExecution) addQueueWaitElapsed(startedAt, endedAt time.Time) {
	if ex == nil {
		return
	}
	elapsed := endedAt.Sub(startedAt)
	if elapsed <= 0 {
		return
	}
	elapsedMS := int(elapsed / time.Millisecond)
	if elapsed%time.Millisecond != 0 {
		elapsedMS++
	}
	if elapsedMS <= 0 {
		return
	}
	ex.queueWaitSpentMS += elapsedMS
}

func (ex *chatExecution) acceptPoolSelection(selRes *pool.SelectionResult) {
	if ex.classTransition != nil && bindingfallback.NormalizeClass(string(ex.attempt.FallbackClass)) != bindingfallback.ClassNormal {
		selRes.RoutingReasonJSON = routingReasonWithClassTransition(selRes.RoutingReasonJSON, *ex.classTransition)
	}
	ex.selRes = selRes
	ex.acquiredAccountID = selRes.AccountID
	ex.acquisitionToken = selRes.AcquisitionToken
	if ex.d.SessionCapRegistry != nil && ex.sessionHash != "" {
		ex.d.SessionCapRegistry.Register(ex.acquiredAccountID, ex.sessionHash)
	}
}

func routingReasonWithClassTransition(raw []byte, transition bindingClassTransition) []byte {
	return bindingfallback.AnnotateRoutingReason(raw, transition)
}

// completedAttemptResult 统一封住成功与已交付终态，确保 normal、目标类及
// 目标类 auth 子预算都不会在客户端已经收到字节后继续转移。
func completedAttemptResult(outcome attemptOutcome) (modelRunResult, bool) {
	if outcome.Success != nil {
		return modelRunResult{Success: &outcome, DeliveryStarted: outcome.DeliveryStarted}, true
	}
	if outcome.DeliveryStarted || (outcome.Failure != nil && outcome.Failure.DeliveredToClient) {
		return modelRunResult{DeliveryStarted: true}, true
	}
	return modelRunResult{}, false
}

// runSingleModel 负责单模型内的普通 attempt、auth 子预算与一次 binding class
// 转移；外层 model fallback 仍由 runWithModelFallback 管理，避免两种预算递归。
func (ex *chatExecution) runSingleModel(w http.ResponseWriter, fallbackAttempts int) modelRunResult {
	// 在计费前拦截 Claude Code 的一次性预热请求。开关默认关闭。
	if warmupInterceptEnabled(ex.ctx, ex.d.PlatformSettings) {
		isClaudeUA := warmupintercept.IsClaudeCodeUserAgent(ex.r.UserAgent())
		maxTok := 0
		if ex.req.MaxTokens != nil {
			maxTok = *ex.req.MaxTokens
		}
		if kind, ok := warmupintercept.Detect(isClaudeUA, ex.req.Model, maxTok, ex.req.Stream, ex.body); ok {
			slog.InfoContext(ex.ctx, "warmup_intercept.intercepted",
				"kind", int(kind),
				"model", ex.req.Model,
				"request_id", ex.requestID,
			)
			if ex.req.Stream {
				warmupintercept.WriteStream(w, kind, ex.req.Model)
			} else {
				warmupintercept.WriteNonStream(w, kind, ex.req.Model)
			}
			return modelRunResult{DeliveryStarted: true}
		}
	}
	if !ex.prepareRoute(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if !ex.screenModerationInput(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if !ex.reserveClaim(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if fallbackAttempts > 0 {
		setModelFallbackHeaders(w, ex.req.Model, fallbackAttempts)
	}
	if !ex.req.Stream {
		handled, proceed := ex.serveL2CacheIfAvailable(w)
		if handled || !proceed {
			return modelRunResult{DeliveryStarted: responseStarted(w)}
		}
	}
	failedAccounts := make(map[int64]struct{})
	authFailoverUsed := false
	budget := effectiveAttemptBudget(ex.plan)
	maxAttempts := len(ex.plan.Attempts)
	// auth-failover 落在普通预算最后一格时，可获得至多一个额外同类 attempt。
	attemptCap := budget
	var evidence bindingFallbackEvidence
	var lastFailure *classifiedAttemptFailure
	attemptsRun := 0
	for i := 0; i < attemptCap; i++ {
		attemptsRun = i + 1
		planIdx := i
		if planIdx >= maxAttempts {
			planIdx = maxAttempts - 1
		}
		outcome := ex.runAttempt(w, attemptInput{
			Plan:             ex.plan.Attempts[planIdx],
			AttemptSeq:       i + 1,
			ExcludedAccounts: failedAccounts,
			ReplayableBody:   true,
			FinalAttempt:     i+1 >= attemptCap,
		})
		if result, done := completedAttemptResult(outcome); done {
			return result
		}
		if outcome.AccountID != 0 && outcome.Failure != nil {
			if outcome.Failure.Decision.RefreshIntent == gateway.RefreshOAuthHotPath {
				ex.triggerCredentialHotRefresh(outcome.AccountID)
			}
			if outcome.Failure.Decision.SwitchAccount {
				failedAccounts[outcome.AccountID] = struct{}{}
			}
		}
		if outcome.Failure == nil {
			continue
		}
		lastFailure = outcome.Failure
		retry, consumeAuthBudget := shouldRetryAttemptFailure(outcome.Failure, ex.plan, true, i+1 >= attemptCap, authFailoverUsed)
		// auth 子预算只在当前 binding class 内消费。
		if retry && consumeAuthBudget {
			if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
				return modelRunResult{Failure: outcome.Failure}
			}
			authFailoverUsed = true
			if i+1 >= attemptCap {
				attemptCap++
			}
			clearRetryableAttemptFailureHeaders(w)
			ex.prepareNextAttemptAfterAbort()
			continue
		}
		if bindingfallback.IsTerminal(outcome.Failure.FallbackSignal) {
			return modelRunResult{Failure: outcome.Failure, AllowFallback: allowModelFallbackAfterClass(outcome.Failure)}
		}
		degradable := evidence.add(outcome.Failure, ex.plan)
		moreNormalAttempts := i+1 < budget
		if !moreNormalAttempts || (!retry && !degradable) {
			break
		}
		if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
			return modelRunResult{Failure: outcome.Failure}
		}
		clearRetryableAttemptFailureHeaders(w)
		ex.prepareNextAttemptAfterAbort()
	}
	if lastFailure == nil {
		return modelRunResult{}
	}
	phase, trigger, allowed := evidence.transition(
		ex.plan,
		ex.d.ModerationScreener == nil || ex.moderationScreened,
	)
	if !allowed {
		return modelRunResult{Failure: lastFailure, AllowFallback: lastFailure.Decision.RetryableBeforeDelivery}
	}
	if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
		return modelRunResult{Failure: lastFailure}
	}
	clearRetryableAttemptFailureHeaders(w)
	ex.prepareNextAttemptAfterAbort()
	ex.classTransition = &bindingClassTransition{
		From: bindingfallback.ClassNormal, To: phase.FallbackClass, Trigger: trigger,
	}
	outcome := ex.runAttempt(w, attemptInput{
		Plan:             phase.Attempts[0],
		AttemptSeq:       attemptsRun + 1,
		ExcludedAccounts: failedAccounts,
		ReplayableBody:   true,
		FinalAttempt:     true,
	})
	if result, done := completedAttemptResult(outcome); done {
		return result
	}
	if outcome.AccountID != 0 && outcome.Failure != nil && outcome.Failure.Decision.RefreshIntent == gateway.RefreshOAuthHotPath {
		ex.triggerCredentialHotRefresh(outcome.AccountID)
	}
	// 目标 class 的普通预算仍是一次；若它恰好遇到 401，可消费尚未使用的
	// 既有 auth 子预算，在同一目标 pool 内换号一次，绝不重新做 class 归约。
	if outcome.Failure != nil {
		retry, consumeAuthBudget := shouldRetryAttemptFailure(outcome.Failure, ex.plan, true, true, authFailoverUsed)
		if retry && consumeAuthBudget {
			if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
				return modelRunResult{Failure: outcome.Failure}
			}
			authFailoverUsed = true
			targetFailedAccounts := make(map[int64]struct{}, len(failedAccounts)+1)
			for accountID := range failedAccounts {
				targetFailedAccounts[accountID] = struct{}{}
			}
			if outcome.AccountID != 0 {
				targetFailedAccounts[outcome.AccountID] = struct{}{}
			}
			clearRetryableAttemptFailureHeaders(w)
			ex.prepareNextAttemptAfterAbort()
			outcome = ex.runAttempt(w, attemptInput{
				Plan:             phase.Attempts[0],
				AttemptSeq:       attemptsRun + 2,
				ExcludedAccounts: targetFailedAccounts,
				ReplayableBody:   true,
				FinalAttempt:     true,
			})
			if result, done := completedAttemptResult(outcome); done {
				return result
			}
			if outcome.AccountID != 0 && outcome.Failure != nil && outcome.Failure.Decision.RefreshIntent == gateway.RefreshOAuthHotPath {
				ex.triggerCredentialHotRefresh(outcome.AccountID)
			}
		}
	}
	if outcome.Failure == nil {
		return modelRunResult{}
	}
	return modelRunResult{Failure: outcome.Failure, AllowFallback: allowModelFallbackAfterClass(outcome.Failure)}
}

func allowModelFallbackAfterClass(failure *classifiedAttemptFailure) bool {
	if failure == nil || !failure.Decision.RetryableBeforeDelivery {
		return false
	}
	if !bindingfallback.IsTerminal(failure.FallbackSignal) {
		return true
	}
	// 静态池耗尽只禁止换 binding class；它在本能力之前就允许外层换模型。
	return failure.FallbackSignal == bindingfallback.SignalPoolStaticMismatch
}
