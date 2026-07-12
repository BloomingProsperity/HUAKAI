package gatewayhttp

import (
	"context"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/queuewait"
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
		return ex.queueWaitRetryableFailure(plan, result.Err, "queue_wait")
	case queuewait.StatusCancelled:
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "queue_wait_cancelled", 0, ex.protocolLoss)
		failure := terminalLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeQueueWait, clienterr.MessageFor(clienterr.CodeQueueWait), "queue_wait_cancelled", result.Err)
		failure.RetryAfterSeconds = retryAfterSecondsForWaitPlan(plan)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	default:
		return ex.queueWaitRetryableFailure(plan, result.Err, "queue_wait")
	}
}

func (ex *chatExecution) queueWaitRetryableFailure(plan *pool.WaitPlan, cause error, abortReason string) *classifiedAttemptFailure {
	abortErr := ex.abortReservation(ex.reserveRes.ClaimID, abortReason, 0, ex.protocolLoss)
	failure := retryableLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeQueueWait, clienterr.MessageFor(clienterr.CodeQueueWait), abortReason, gateway.UpstreamRateLimit, cause)
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
	ex.selRes = selRes
	ex.acquiredAccountID = selRes.AccountID
	ex.acquisitionToken = selRes.AcquisitionToken
	if ex.d.SessionCapRegistry != nil && ex.sessionHash != "" {
		ex.d.SessionCapRegistry.Register(ex.acquiredAccountID, ex.sessionHash)
	}
}
