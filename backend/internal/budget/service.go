package budget

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"
)

type Option func(*Service)

type Service struct {
	store    Store
	fallback Store
	limitsMu sync.RWMutex
	limits   LimitsProvider
	failMode FailMode
	now      func() time.Time

	resMu        sync.Mutex
	reservations map[claimKey]*reservation
}

type claimKey struct {
	tenantID int64
	claimID  int64
}

type reservation struct {
	tenantID int64
	claimID  int64
	scopes   []reservedScope
	tokens   int64
	released bool
	settled  bool
}

type reservedScope struct {
	scope  Scope
	minute int64
	rpm    int64
	tpm    int64
}

type StoredReservation struct {
	TenantID int64
	ClaimID  int64
	Tokens   int64
	Released bool
	Settled  bool
	Scopes   []StoredReservedScope
}

type StoredReservedScope struct {
	Scope  Scope
	Minute int64
	RPM    int64
	TPM    int64
}

type ReservationLedger interface {
	LoadReservation(context.Context, int64, int64) (StoredReservation, bool, error)
	SaveReservation(context.Context, StoredReservation) error
	UpdateReservation(context.Context, StoredReservation) error
}

func NewService(store Store, limits LimitsProvider, opts ...Option) *Service {
	s := &Service{
		store:        store,
		limits:       limits,
		failMode:     FailModeMemoryFallback,
		now:          func() time.Time { return time.Now().UTC() },
		reservations: make(map[claimKey]*reservation),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.limits == nil {
		s.limits = StaticLimitsProvider{}
	}
	return s
}

func WithFailMode(mode FailMode) Option {
	return func(s *Service) {
		switch mode {
		case FailModeOpen, FailModeClosed, FailModeMemoryFallback:
			s.failMode = mode
		}
	}
}

func WithMemoryFallback(store Store) Option {
	return func(s *Service) { s.fallback = store }
}

func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.now = clock
		}
	}
}

func (s *Service) SetLimitsProvider(provider LimitsProvider) {
	if s == nil || provider == nil {
		return
	}
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	s.limits = provider
}

func (s *Service) Reserve(ctx context.Context, req ReserveRequest) (ReserveResult, error) {
	if s == nil {
		return ReserveResult{Allowed: true, FailOpen: true}, nil
	}
	req.ReservedTokens = nonNegative(req.ReservedTokens)
	if existing, ok := s.snapshot(ctx, req.TenantID, req.ClaimID); ok && !existing.released {
		return ReserveResult{Allowed: true, IdempotencyHit: true}, nil
	}
	scopes, err := s.resolveScopes(ctx, req)
	if err != nil {
		return s.handleStoreError(ctx, req, err)
	}
	if len(scopes) == 0 {
		return ReserveResult{Allowed: true}, nil
	}
	result, err := s.reserveWithStore(ctx, s.store, req, scopes)
	if err == nil || IsDenied(err) {
		return result, err
	}
	return s.handleStoreError(ctx, req, err)
}

func (s *Service) Settle(ctx context.Context, req SettleRequest) error {
	res, ok := s.snapshot(ctx, req.TenantID, req.ClaimID)
	if !ok || res.released {
		return nil
	}
	actual := nonNegative(req.ActualTokens)
	delta := actual - res.tokens
	if delta == 0 && res.settled {
		return nil
	}
	for _, item := range res.scopes {
		if item.tpm == 0 && delta == 0 {
			continue
		}
		if err := s.store.Adjust(ctx, AdjustRequest{Scope: item.scope, Minute: item.minute, ClaimID: req.ClaimID, TPMDelta: delta}); err != nil {
			slog.WarnContext(ctx, "budget settle delta failed open", slog.String("error_type", errType(err)))
			budgetFailOpenTotal.Add(1)
			return nil
		}
	}
	s.markSettled(ctx, req.TenantID, req.ClaimID, actual)
	return nil
}

func (s *Service) Release(ctx context.Context, req ReleaseRequest) error {
	res, ok := s.snapshot(ctx, req.TenantID, req.ClaimID)
	if !ok || res.released {
		return nil
	}
	for _, item := range res.scopes {
		if err := s.store.Adjust(ctx, AdjustRequest{
			Scope:    item.scope,
			Minute:   item.minute,
			ClaimID:  req.ClaimID,
			RPMDelta: -item.rpm,
			TPMDelta: -item.tpm,
			Remove:   true,
		}); err != nil {
			slog.WarnContext(ctx, "budget release failed open", slog.String("error_type", errType(err)))
			budgetFailOpenTotal.Add(1)
			return nil
		}
	}
	s.markReleased(ctx, req.TenantID, req.ClaimID)
	return nil
}

func (s *Service) reserveWithStore(ctx context.Context, store Store, req ReserveRequest, scopes []ScopeLimit) (ReserveResult, error) {
	if store == nil {
		return ReserveResult{}, ErrUnavailable
	}
	applied := make([]reservedScope, 0, len(scopes))
	for _, scopeLimit := range scopes {
		counter, err := store.CheckAndIncrement(ctx, CounterRequest{
			Scope:        scopeLimit.Scope,
			Limits:       scopeLimit.Limits,
			RPMIncrement: 1,
			TPMIncrement: req.ReservedTokens,
			ClaimID:      req.ClaimID,
			ObservedAt:   req.At,
		})
		if err != nil {
			s.refundApplied(ctx, store, req, applied)
			return ReserveResult{}, err
		}
		if !counter.Allowed {
			s.refundApplied(ctx, store, req, applied)
			decision := Decision{
				Code:       CodeLimitExceeded,
				Counter:    counter.Counter,
				Scope:      scopeLimit.Scope,
				Current:    counter.Current,
				Limit:      counter.Limit,
				RetryAfter: counter.RetryAfter,
				Reason:     "budget limit exceeded",
			}
			result := ReserveResult{Allowed: false, Decision: decision}
			return result, &DenyError{Decision: decision, Cause: ErrDenied}
		}
		if counter.IdempotencyHit {
			return ReserveResult{Allowed: true, IdempotencyHit: true}, nil
		}
		applied = append(applied, reservedScope{
			scope:  scopeLimit.Scope,
			minute: counter.Minute,
			rpm:    1,
			tpm:    req.ReservedTokens,
		})
	}
	s.remember(ctx, req, applied)
	return ReserveResult{Allowed: true}, nil
}

func (s *Service) refundApplied(ctx context.Context, store Store, req ReserveRequest, applied []reservedScope) {
	for i := len(applied) - 1; i >= 0; i-- {
		item := applied[i]
		_ = store.Adjust(ctx, AdjustRequest{
			Scope:    item.scope,
			Minute:   item.minute,
			ClaimID:  req.ClaimID,
			RPMDelta: -item.rpm,
			TPMDelta: -item.tpm,
			Remove:   true,
		})
	}
}

func (s *Service) handleStoreError(ctx context.Context, req ReserveRequest, err error) (ReserveResult, error) {
	switch s.failMode {
	case FailModeClosed:
		decision := Decision{Code: CodeBudgetUnavailable, Reason: "budget backend unavailable", RetryAfter: time.Second}
		return ReserveResult{Allowed: false, Decision: decision}, &DenyError{Decision: decision, Cause: err}
	case FailModeMemoryFallback:
		if s.fallback != nil {
			scopes, limitsErr := s.resolveScopes(ctx, req)
			if limitsErr == nil {
				return s.reserveWithStore(ctx, s.fallback, req, scopes)
			}
		}
	}
	budgetFailOpenTotal.Add(1)
	slog.WarnContext(ctx, "budget reserve failed open", slog.String("error_type", errType(err)))
	return ReserveResult{Allowed: true, FailOpen: true}, nil
}

func (s *Service) resolveScopes(ctx context.Context, req ReserveRequest) ([]ScopeLimit, error) {
	s.limitsMu.RLock()
	provider := s.limits
	s.limitsMu.RUnlock()
	return provider.Scopes(ctx, req)
}

func (s *Service) remember(ctx context.Context, req ReserveRequest, applied []reservedScope) {
	if len(applied) == 0 {
		return
	}
	res := reservation{
		tenantID: req.TenantID,
		claimID:  req.ClaimID,
		scopes:   applied,
		tokens:   req.ReservedTokens,
	}
	if ledger, ok := s.store.(ReservationLedger); ok {
		_ = ledger.SaveReservation(ctx, storedFromReservation(res))
	}
	s.resMu.Lock()
	defer s.resMu.Unlock()
	s.reservations[claimKey{tenantID: req.TenantID, claimID: req.ClaimID}] = &res
}

func (s *Service) snapshot(ctx context.Context, tenantID, claimID int64) (reservation, bool) {
	if ledger, ok := s.store.(ReservationLedger); ok {
		stored, found, err := ledger.LoadReservation(ctx, tenantID, claimID)
		if err == nil && found {
			return reservationFromStored(stored), true
		}
	}
	s.resMu.Lock()
	defer s.resMu.Unlock()
	res := s.reservations[claimKey{tenantID: tenantID, claimID: claimID}]
	if res == nil {
		return reservation{}, false
	}
	copyRes := *res
	copyRes.scopes = append([]reservedScope(nil), res.scopes...)
	return copyRes, true
}

func (s *Service) markSettled(ctx context.Context, tenantID, claimID, tokens int64) {
	s.resMu.Lock()
	if res := s.reservations[claimKey{tenantID: tenantID, claimID: claimID}]; res != nil {
		res.tokens = tokens
		res.settled = true
		stored := storedFromReservation(*res)
		s.resMu.Unlock()
		if ledger, ok := s.store.(ReservationLedger); ok {
			_ = ledger.UpdateReservation(ctx, stored)
		}
		return
	}
	s.resMu.Unlock()
	if ledger, ok := s.store.(ReservationLedger); ok {
		stored, found, err := ledger.LoadReservation(ctx, tenantID, claimID)
		if err == nil && found {
			stored.Tokens = tokens
			stored.Settled = true
			_ = ledger.UpdateReservation(ctx, stored)
		}
	}
}

func (s *Service) markReleased(ctx context.Context, tenantID, claimID int64) {
	s.resMu.Lock()
	if res := s.reservations[claimKey{tenantID: tenantID, claimID: claimID}]; res != nil {
		res.released = true
		stored := storedFromReservation(*res)
		s.resMu.Unlock()
		if ledger, ok := s.store.(ReservationLedger); ok {
			_ = ledger.UpdateReservation(ctx, stored)
		}
		return
	}
	s.resMu.Unlock()
	if ledger, ok := s.store.(ReservationLedger); ok {
		stored, found, err := ledger.LoadReservation(ctx, tenantID, claimID)
		if err == nil && found {
			stored.Released = true
			_ = ledger.UpdateReservation(ctx, stored)
		}
	}
}

func storedFromReservation(res reservation) StoredReservation {
	out := StoredReservation{
		TenantID: res.tenantID,
		ClaimID:  res.claimID,
		Tokens:   res.tokens,
		Released: res.released,
		Settled:  res.settled,
		Scopes:   make([]StoredReservedScope, 0, len(res.scopes)),
	}
	for _, item := range res.scopes {
		out.Scopes = append(out.Scopes, StoredReservedScope{
			Scope:  item.scope,
			Minute: item.minute,
			RPM:    item.rpm,
			TPM:    item.tpm,
		})
	}
	return out
}

func reservationFromStored(stored StoredReservation) reservation {
	out := reservation{
		tenantID: stored.TenantID,
		claimID:  stored.ClaimID,
		tokens:   stored.Tokens,
		released: stored.Released,
		settled:  stored.Settled,
		scopes:   make([]reservedScope, 0, len(stored.Scopes)),
	}
	for _, item := range stored.Scopes {
		out.scopes = append(out.scopes, reservedScope{
			scope:  item.Scope,
			minute: item.Minute,
			rpm:    item.RPM,
			tpm:    item.TPM,
		})
	}
	return out
}

func errType(err error) string {
	if err == nil {
		return ""
	}
	typ := reflect.TypeOf(err)
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	pkg := typ.PkgPath()
	if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
		pkg = pkg[idx+1:]
	}
	name := typ.Name()
	if pkg != "" {
		name = pkg + "_" + name
	}
	return metricLabel(name)
}

func metricLabel(in string) string {
	const maxLen = 64
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(in) {
		if b.Len() >= maxLen {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if b.Len() == 0 || lastUnderscore {
			continue
		}
		b.WriteByte('_')
		lastUnderscore = true
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown_error"
	}
	return out
}
