package channelhealth

import (
	"context"
	"errors"
	"sort"
	"sync"
)

type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]Record
	audits  []AuditEvent
	alerts  []Alert

	failAudit bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[string]Record{}}
}

func (s *MemoryStore) Get(_ context.Context, key ChannelKey) (Record, error) {
	if s == nil {
		return Record{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key.mapKey()]
	if !ok {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

func (s *MemoryStore) UpsertRecord(_ context.Context, rec Record) (Record, error) {
	if s == nil {
		return Record{}, ErrNotFound
	}
	if rec.Key.ChannelID == "" {
		rec.Key.ChannelID = rec.Key.StableChannelID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.Key.mapKey()] = rec
	return rec, nil
}

func (s *MemoryStore) ListChannelHealth(_ context.Context, tenantID int64, limit, offset int) ([]ChannelHealthState, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChannelHealthState, 0, len(s.records))
	for _, rec := range s.records {
		if rec.Key.TenantID == tenantID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].Key.StableChannelID() < out[j].Key.StableChannelID()
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if offset >= len(out) {
		return []ChannelHealthState{}, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return append([]ChannelHealthState(nil), out...), nil
}

func (s *MemoryStore) GetChannelHealth(_ context.Context, tenantID int64, channelID string) (ChannelHealthState, []AuditEvent, error) {
	if s == nil {
		return ChannelHealthState{}, nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		rec ChannelHealthState
		ok  bool
	)
	for _, candidate := range s.records {
		if candidate.Key.TenantID == tenantID && candidate.Key.StableChannelID() == channelID {
			rec, ok = candidate, true
			break
		}
	}
	if !ok {
		return ChannelHealthState{}, nil, ErrNotFound
	}
	events := make([]AuditEvent, 0, len(s.audits))
	for _, ev := range s.audits {
		if ev.Key.TenantID == tenantID && ev.Key.StableChannelID() == channelID {
			events = append(events, cloneAuditEvent(ev))
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.After(events[j].OccurredAt) })
	if len(events) > 50 {
		events = events[:50]
	}
	return rec, events, nil
}

func (s *MemoryStore) LatestByProviderAccount(_ context.Context, tenantID, providerAccountID int64) (Record, error) {
	if s == nil {
		return Record{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		best Record
		ok   bool
	)
	for _, rec := range s.records {
		if rec.Key.TenantID != tenantID || rec.Key.ProviderAccountID != providerAccountID {
			continue
		}
		if !ok || rec.Key.CredentialVersion > best.Key.CredentialVersion || rec.UpdatedAt.After(best.UpdatedAt) {
			best, ok = rec, true
		}
	}
	if !ok {
		return Record{}, ErrNotFound
	}
	return best, nil
}

func (s *MemoryStore) AppendAudit(_ context.Context, ev AuditEvent) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAudit {
		return errors.New("channelhealth: injected audit failure")
	}
	s.audits = append(s.audits, ev)
	return nil
}

func (s *MemoryStore) AppendAlert(_ context.Context, alert Alert) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = append(s.alerts, alert)
	return nil
}

func (s *MemoryStore) WithTx(ctx context.Context, fn func(Store) error) error {
	if s == nil {
		return ErrNotFound
	}
	s.mu.RLock()
	records := make(map[string]Record, len(s.records))
	for k, v := range s.records {
		records[k] = v
	}
	audits := append([]AuditEvent(nil), s.audits...)
	alerts := append([]Alert(nil), s.alerts...)
	s.mu.RUnlock()

	if fn != nil {
		if err := fn(s); err != nil {
			s.mu.Lock()
			s.records = records
			s.audits = audits
			s.alerts = alerts
			s.mu.Unlock()
			return err
		}
	}
	_ = ctx
	return nil
}

func (s *MemoryStore) Audits() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audits))
	copy(out, s.audits)
	return out
}

func (s *MemoryStore) Alerts() []Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Alert, len(s.alerts))
	copy(out, s.alerts)
	return out
}

func cloneAuditEvent(ev AuditEvent) AuditEvent {
	if ev.Payload != nil {
		payload := make(map[string]any, len(ev.Payload))
		for k, v := range ev.Payload {
			payload[k] = v
		}
		ev.Payload = payload
	}
	return ev
}

var _ Store = (*MemoryStore)(nil)
