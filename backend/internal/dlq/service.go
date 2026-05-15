package dlq

import (
	"context"
	"fmt"
	"time"
)

type Handler func(context.Context, Record) error

type Service struct {
	store    *Store
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
		store:    store,
		handlers: make(map[EventKind]Handler),
		policy:   DefaultRetryPolicy(),
		now:      func() time.Time { return time.Now().UTC() },
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
		_ = s.store.MarkFailed(ctx, *rec, err.Error(), decision)
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
