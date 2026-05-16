package channelhealth

import (
	"context"
	"errors"
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

var _ Store = (*MemoryStore)(nil)
