package queuewait

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

const (
	defaultInitialBackoff = 100 * time.Millisecond
	defaultMaxBackoff     = 2 * time.Second
)

var (
	ErrMissingSelector = errors.New("queuewait: selector is required")
	ErrInvalidWaitPlan = errors.New("queuewait: invalid wait plan")
	ErrWaitPlanDrift   = errors.New("queuewait: selector returned wait plan for a different account")
	ErrNoSelection     = errors.New("queuewait: selector returned no account")
)

type Status string

const (
	StatusAcquired      Status = "acquired"
	StatusOverflow      Status = "overflow"
	StatusTimeout       Status = "timeout"
	StatusCancelled     Status = "cancelled"
	StatusSelectorError Status = "selector_error"
)

type Result struct {
	Status    Status
	Selection *pool.SelectionResult
	Err       error
}

type Sleeper func(context.Context, time.Duration) error

type Executor struct {
	tracker        *Tracker
	now            func() time.Time
	sleep          Sleeper
	jitter         func(time.Duration) time.Duration
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

func NewExecutor() *Executor {
	e := &Executor{
		tracker:        NewTracker(),
		now:            time.Now,
		sleep:          sleepContext,
		jitter:         jitter20Percent,
		initialBackoff: defaultInitialBackoff,
		maxBackoff:     defaultMaxBackoff,
	}
	if e.tracker == nil {
		e.tracker = NewTracker()
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.sleep == nil {
		e.sleep = sleepContext
	}
	if e.jitter == nil {
		e.jitter = jitter20Percent
	}
	if e.initialBackoff <= 0 {
		e.initialBackoff = defaultInitialBackoff
	}
	if e.maxBackoff <= 0 {
		e.maxBackoff = defaultMaxBackoff
	}
	return e
}

func (e *Executor) Wait(ctx context.Context, selector pool.Selector, req pool.SelectionRequest, plan *pool.WaitPlan) Result {
	if e == nil {
		e = NewExecutor()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if selector == nil {
		return Result{Status: StatusSelectorError, Err: ErrMissingSelector}
	}
	if plan == nil || plan.AccountID == 0 {
		return Result{Status: StatusSelectorError, Err: ErrInvalidWaitPlan}
	}

	key := Key{TenantID: req.TenantID, PoolGroupID: req.PoolGroupID, AccountID: plan.AccountID}
	release, ok := e.tracker.TryAcquire(key, plan.MaxWaiting)
	if !ok {
		return Result{Status: StatusOverflow}
	}
	defer release()

	pinnedReq := req
	pinnedReq.PinnedAccountID = plan.AccountID
	deadline := e.now().Add(time.Duration(plan.TimeoutMS) * time.Millisecond)
	backoff := e.initialBackoff

	for {
		if err := ctx.Err(); err != nil {
			return Result{Status: StatusCancelled, Err: err}
		}
		selection, err := selector.Select(ctx, pinnedReq)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Result{Status: StatusCancelled, Err: ctxErr}
			}
			return Result{Status: StatusSelectorError, Err: err}
		}
		if selection != nil && selection.WaitPlan == nil && selection.AccountID != 0 {
			return Result{Status: StatusAcquired, Selection: selection}
		}
		if selection == nil || selection.WaitPlan == nil {
			return Result{Status: StatusSelectorError, Err: ErrNoSelection}
		}
		if selection.WaitPlan.AccountID != 0 && selection.WaitPlan.AccountID != plan.AccountID {
			return Result{Status: StatusSelectorError, Err: ErrWaitPlanDrift}
		}

		now := e.now()
		if !now.Before(deadline) {
			return Result{Status: StatusTimeout}
		}
		remaining := deadline.Sub(now)
		delay := e.jitter(backoff)
		if delay <= 0 {
			delay = backoff
		}
		if delay > remaining {
			delay = remaining
		}
		if err := e.sleep(ctx, delay); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Result{Status: StatusCancelled, Err: ctxErr}
			}
			return Result{Status: StatusCancelled, Err: err}
		}
		backoff = nextBackoff(backoff, e.maxBackoff)
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		return defaultInitialBackoff
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func jitter20Percent(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(d) * factor)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
