package dlq

import "time"

type RetryPolicy struct {
	BaseBackoff time.Duration
	CapBackoff  time.Duration
	MaxAttempts int
	DLQAfter    time.Duration
}

type RetryDecision struct {
	Status      Status
	NextRetryAt time.Time
	Attempts    int
	Delay       time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		BaseBackoff: time.Second,
		CapBackoff:  5 * time.Minute,
		MaxAttempts: 10,
		DLQAfter:    15 * time.Minute,
	}
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.BaseBackoff <= 0 {
		p.BaseBackoff = time.Second
	}
	if p.CapBackoff <= 0 {
		p.CapBackoff = 5 * time.Minute
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 10
	}
	if p.DLQAfter <= 0 {
		p.DLQAfter = 15 * time.Minute
	}
	return p
}

func (p RetryPolicy) NextFailure(now, firstFailureAt time.Time, previousAttempts int) RetryDecision {
	p = p.normalized()
	attempts := previousAttempts + 1
	if attempts >= p.MaxAttempts || (!firstFailureAt.IsZero() && !now.Before(firstFailureAt.Add(p.DLQAfter))) {
		return RetryDecision{Status: StatusOperatorReview, Attempts: attempts}
	}
	delay := p.BaseBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.CapBackoff {
			delay = p.CapBackoff
			break
		}
	}
	if delay > p.CapBackoff {
		delay = p.CapBackoff
	}
	return RetryDecision{
		Status:      StatusPending,
		NextRetryAt: now.Add(delay),
		Attempts:    attempts,
		Delay:       delay,
	}
}
