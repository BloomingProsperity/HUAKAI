package budget

import (
	"context"
	"sync"
	"time"
)

type CounterRequest struct {
	Scope          Scope
	Limits         LimitPair
	RPMIncrement   int64
	TPMIncrement   int64
	ClaimID        int64
	ObservedAt     time.Time
	ForceStoreTime bool
}

type CounterResult struct {
	Allowed        bool
	IdempotencyHit bool
	Minute         int64
	Counter        Counter
	Current        int64
	Limit          int64
	RetryAfter     time.Duration
}

type AdjustRequest struct {
	Scope    Scope
	Minute   int64
	ClaimID  int64
	RPMDelta int64
	TPMDelta int64
	Remove   bool
}

type Store interface {
	CheckAndIncrement(context.Context, CounterRequest) (CounterResult, error)
	Adjust(context.Context, AdjustRequest) error
}

type MemoryStore struct {
	mu       sync.Mutex
	now      func() time.Time
	counters map[memoryCounterKey]int64
	claims   map[memoryClaimKey]struct{}
	ledger   map[claimKey]StoredReservation
}

type memoryCounterKey struct {
	scope   string
	minute  int64
	counter Counter
}

type memoryClaimKey struct {
	scope  string
	minute int64
	claim  int64
}

func NewMemoryStore(clock func() time.Time) *MemoryStore {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{
		now:      clock,
		counters: make(map[memoryCounterKey]int64),
		claims:   make(map[memoryClaimKey]struct{}),
		ledger:   make(map[claimKey]StoredReservation),
	}
}

func (s *MemoryStore) CheckAndIncrement(_ context.Context, req CounterRequest) (CounterResult, error) {
	if s == nil {
		return CounterResult{}, ErrUnavailable
	}
	scope, err := EncodeScope(req.Scope)
	if err != nil {
		return CounterResult{}, err
	}
	now := s.now().UTC()
	minute := now.Unix() / 60
	limits := req.Limits.normalized()
	rpmInc := nonNegative(req.RPMIncrement)
	tpmInc := nonNegative(req.TPMIncrement)

	s.mu.Lock()
	defer s.mu.Unlock()

	claimKey := memoryClaimKey{scope: scope, minute: minute, claim: req.ClaimID}
	if _, ok := s.claims[claimKey]; ok {
		return CounterResult{Allowed: true, IdempotencyHit: true, Minute: minute}, nil
	}
	rKey := memoryCounterKey{scope: scope, minute: minute, counter: CounterRPM}
	tKey := memoryCounterKey{scope: scope, minute: minute, counter: CounterTPM}
	currentRPM := s.counters[rKey]
	currentTPM := s.counters[tKey]
	if limits.RPM > 0 && currentRPM+rpmInc > limits.RPM {
		return CounterResult{Allowed: false, Minute: minute, Counter: CounterRPM, Current: currentRPM, Limit: limits.RPM, RetryAfter: minuteRetryAfter(minute, now)}, nil
	}
	if limits.TPM > 0 && currentTPM+tpmInc > limits.TPM {
		return CounterResult{Allowed: false, Minute: minute, Counter: CounterTPM, Current: currentTPM, Limit: limits.TPM, RetryAfter: minuteRetryAfter(minute, now)}, nil
	}
	s.counters[rKey] = currentRPM + rpmInc
	s.counters[tKey] = currentTPM + tpmInc
	s.claims[claimKey] = struct{}{}
	return CounterResult{Allowed: true, Minute: minute, Current: s.counters[rKey], Limit: limits.RPM}, nil
}

func (s *MemoryStore) Adjust(_ context.Context, req AdjustRequest) error {
	if s == nil {
		return ErrUnavailable
	}
	scope, err := EncodeScope(req.Scope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.RPMDelta != 0 {
		key := memoryCounterKey{scope: scope, minute: req.Minute, counter: CounterRPM}
		s.counters[key] = clampCounter(s.counters[key] + req.RPMDelta)
	}
	if req.TPMDelta != 0 {
		key := memoryCounterKey{scope: scope, minute: req.Minute, counter: CounterTPM}
		s.counters[key] = clampCounter(s.counters[key] + req.TPMDelta)
	}
	if req.Remove {
		delete(s.claims, memoryClaimKey{scope: scope, minute: req.Minute, claim: req.ClaimID})
	}
	return nil
}

func (s *MemoryStore) CounterValue(scope Scope, minute int64, counter Counter) int64 {
	if s == nil {
		return 0
	}
	encoded, err := EncodeScope(scope)
	if err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counters[memoryCounterKey{scope: encoded, minute: minute, counter: counter}]
}

func (s *MemoryStore) LoadReservation(_ context.Context, tenantID, claimID int64) (StoredReservation, bool, error) {
	if s == nil {
		return StoredReservation{}, false, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.ledger[claimKey{tenantID: tenantID, claimID: claimID}]
	res.Scopes = append([]StoredReservedScope(nil), res.Scopes...)
	return res, ok, nil
}

func (s *MemoryStore) SaveReservation(_ context.Context, res StoredReservation) error {
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res.Scopes = append([]StoredReservedScope(nil), res.Scopes...)
	s.ledger[claimKey{tenantID: res.TenantID, claimID: res.ClaimID}] = res
	return nil
}

func (s *MemoryStore) UpdateReservation(ctx context.Context, res StoredReservation) error {
	return s.SaveReservation(ctx, res)
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func clampCounter(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
