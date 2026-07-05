package dlq

import (
	"errors"
	"time"
)

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

// NextFailureForErr 在 NextFailure 的基础上加一层"结构性不可重试"短路:当 handler 返回的
// 错误经 errors.Is 命中 ErrUnretryable(payload 损坏/校验不过/事件类型不匹配等毒消息)
// 或 ErrNoHandler(当前部署没有可处理该 kind 的 handler),直接判定 quarantined 并停止重试。
// failErr 为 nil 或瞬时错误时,行为与 NextFailure 完全一致。
func (p RetryPolicy) NextFailureForErr(now, firstFailureAt time.Time, previousAttempts int, failErr error) RetryDecision {
	if failErr != nil && (errors.Is(failErr, ErrUnretryable) || errors.Is(failErr, ErrNoHandler)) {
		return RetryDecision{Status: StatusQuarantined, Attempts: previousAttempts + 1}
	}
	return p.NextFailure(now, firstFailureAt, previousAttempts)
}
