package dlq

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Handler func(context.Context, Record) error

// recordStore 抽象 Service 依赖的 DLQ 持久化操作,便于单测注入(如让 MarkFailed 写失败)以验证
// 状态持久化故障被正确上抛。生产实现是 *Store,天然满足此接口。
type recordStore interface {
	Enqueue(context.Context, Event) (int64, error)
	List(context.Context, ListFilter) ([]Record, error)
	ClaimByID(context.Context, int64, string, time.Duration) (*Record, error)
	Claim(context.Context, Lane, string, time.Duration) (*Record, error)
	MarkFailed(context.Context, Record, string, RetryDecision) error
	MarkDelivered(context.Context, Record) error
}

type Service struct {
	store    recordStore
	handlers map[EventKind]Handler
	policy   RetryPolicy
	now      func() time.Time
}

type ServiceOption func(*Service)

func WithPolicy(policy RetryPolicy) ServiceOption {
	return func(s *Service) { s.policy = policy }
}

func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func NewService(store *Store, opts ...ServiceOption) *Service {
	s := &Service{
		handlers: make(map[EventKind]Handler),
		policy:   DefaultRetryPolicy(),
		now:      func() time.Time { return time.Now().UTC() },
	}
	// 仅在非 nil 时存入接口,避免 typed-nil 让 s.store == nil 判定失真,保持既有 nil 守卫语义。
	if store != nil {
		s.store = store
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) Register(kind EventKind, h Handler) {
	if s == nil || h == nil {
		return
	}
	s.handlers[kind] = h
}

func (s *Service) Enqueue(ctx context.Context, e Event) (int64, error) {
	if s == nil || s.store == nil {
		return 0, ErrStoreNotConfigured
	}
	return s.store.Enqueue(ctx, e)
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]Record, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	return s.store.List(ctx, f)
}

func (s *Service) Replay(ctx context.Context, id int64, actorID string) (*Record, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	rec, err := s.store.ClaimByID(ctx, id, actorID, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if err := s.handle(ctx, *rec); err != nil {
		decision := s.policy.NextFailure(s.now(), rec.FailureAt, rec.ReplayAttempts)
		if markErr := s.store.MarkFailed(ctx, *rec, err.Error(), decision); markErr != nil {
			// 与 worker 路径(ProcessClaim)一致:MarkFailed 写失败是独立的状态持久化故障,必须上抛。
			// 否则 handler 失败且状态更新也失败时,行会停在 inflight(带 manual lease、陈旧 retry 计数、
			// 未转 operator_review/dlq),操作员只看到 handler 错误,而"恢复系统连自身失败状态都没落盘"
			// 这一事实被吞掉(S2-096)。errors.Join 同时保留 handler 错误与持久化错误,信息比 worker 更全。
			return rec, errors.Join(err, markErr)
		}
		return rec, err
	}
	if err := s.store.MarkDelivered(ctx, *rec); err != nil {
		return nil, err
	}
	rec.Status = StatusDelivered
	return rec, nil
}

func (s *Service) ProcessClaim(ctx context.Context, lane Lane, workerID string, leaseTTL time.Duration) (bool, error) {
	if s == nil || s.store == nil {
		return false, ErrStoreNotConfigured
	}
	rec, err := s.store.Claim(ctx, lane, workerID, leaseTTL)
	if err != nil || rec == nil {
		return false, err
	}
	if err := s.handle(ctx, *rec); err != nil {
		decision := s.policy.NextFailure(s.now(), rec.FailureAt, rec.ReplayAttempts)
		if markErr := s.store.MarkFailed(ctx, *rec, err.Error(), decision); markErr != nil {
			return true, markErr
		}
		return true, nil
	}
	if err := s.store.MarkDelivered(ctx, *rec); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) handle(ctx context.Context, rec Record) error {
	h := s.handlers[rec.EventKind]
	if h == nil {
		return fmt.Errorf("%w: %s", ErrNoHandler, rec.EventKind)
	}
	return h(ctx, rec)
}
