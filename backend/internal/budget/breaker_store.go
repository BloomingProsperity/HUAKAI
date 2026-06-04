package budget

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/circuitbreaker"
)

const breakerKey = "redis_budget"

type BreakerStore struct {
	inner   Store
	breaker *circuitbreaker.Breaker
}

func NewBreakerStore(inner Store, breaker *circuitbreaker.Breaker) Store {
	if inner == nil || breaker == nil {
		return inner
	}
	return &BreakerStore{inner: inner, breaker: breaker}
}

func (s *BreakerStore) CheckAndIncrement(ctx context.Context, req CounterRequest) (CounterResult, error) {
	decision := s.breaker.Allow(breakerKey)
	if !decision.Allowed {
		return CounterResult{}, ErrUnavailable
	}
	result, err := s.inner.CheckAndIncrement(ctx, req)
	if err != nil {
		s.breaker.RecordFailure(breakerKey)
		return CounterResult{}, err
	}
	s.breaker.RecordSuccess(breakerKey)
	return result, nil
}

func (s *BreakerStore) Adjust(ctx context.Context, req AdjustRequest) error {
	decision := s.breaker.Allow(breakerKey)
	if !decision.Allowed {
		return ErrUnavailable
	}
	if err := s.inner.Adjust(ctx, req); err != nil {
		s.breaker.RecordFailure(breakerKey)
		return err
	}
	s.breaker.RecordSuccess(breakerKey)
	return nil
}

func (s *BreakerStore) LoadReservation(ctx context.Context, tenantID, claimID int64) (StoredReservation, bool, error) {
	ledger, ok := s.inner.(ReservationLedger)
	if !ok {
		return StoredReservation{}, false, nil
	}
	decision := s.breaker.Allow(breakerKey)
	if !decision.Allowed {
		return StoredReservation{}, false, ErrUnavailable
	}
	res, found, err := ledger.LoadReservation(ctx, tenantID, claimID)
	if err != nil {
		s.breaker.RecordFailure(breakerKey)
		return StoredReservation{}, false, err
	}
	s.breaker.RecordSuccess(breakerKey)
	return res, found, nil
}

func (s *BreakerStore) SaveReservation(ctx context.Context, res StoredReservation) error {
	ledger, ok := s.inner.(ReservationLedger)
	if !ok {
		return nil
	}
	return s.withLedgerWrite(ctx, func() error { return ledger.SaveReservation(ctx, res) })
}

func (s *BreakerStore) UpdateReservation(ctx context.Context, res StoredReservation) error {
	ledger, ok := s.inner.(ReservationLedger)
	if !ok {
		return nil
	}
	return s.withLedgerWrite(ctx, func() error { return ledger.UpdateReservation(ctx, res) })
}

func (s *BreakerStore) withLedgerWrite(_ context.Context, fn func() error) error {
	decision := s.breaker.Allow(breakerKey)
	if !decision.Allowed {
		return ErrUnavailable
	}
	if err := fn(); err != nil {
		s.breaker.RecordFailure(breakerKey)
		return err
	}
	s.breaker.RecordSuccess(breakerKey)
	return nil
}
