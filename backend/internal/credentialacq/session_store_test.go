package credentialacq

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type acqSession struct {
	ID                    string
	TenantID              int64
	ProviderAccountID     int64
	Vendor                string
	AuthMode              string
	Kind                  acqFlowKind
	Status                acqFlowStatus
	StateHash             []byte
	EncryptedPKCEVerifier []byte
	RedactedContext       map[string]any
	ResultCredentialID    int64
	ExpiresAt             time.Time
	ConsumedAt            time.Time
	CancelledAt           time.Time
}

type memorySessionStore struct {
	mu   sync.Mutex
	now  func() time.Time
	rows map[string]acqSession
}

func newMemorySessionStore(now func() time.Time) *memorySessionStore {
	return &memorySessionStore{now: now, rows: map[string]acqSession{}}
}

func (s *memorySessionStore) Create(row acqSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row.ID == "" {
		return errors.New("empty flow id")
	}
	if row.Status == "" {
		row.Status = statusStarted
	}
	if row.ExpiresAt.IsZero() {
		row.ExpiresAt = s.now().Add(10 * time.Minute)
	}
	if _, exists := s.rows[row.ID]; exists {
		return errFlowReplay
	}
	s.rows[row.ID] = cloneSession(row)
	return nil
}

func (s *memorySessionStore) Get(id string) (acqSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return acqSession{}, errFlowNotFound
	}
	return cloneSession(row), nil
}

func (s *memorySessionStore) UpdateStatus(id string, status acqFlowStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return errFlowNotFound
	}
	row.Status = status
	s.rows[id] = row
	return nil
}

func (s *memorySessionStore) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return errFlowNotFound
	}
	if row.Status == statusFinalized {
		return errFlowReplay
	}
	row.Status = statusCancelled
	row.CancelledAt = s.now()
	s.rows[id] = row
	return nil
}

func (s *memorySessionStore) Consume(id string, credentialID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return errFlowNotFound
	}
	if s.now().After(row.ExpiresAt) {
		row.Status = statusExpired
		s.rows[id] = row
		return errFlowExpired
	}
	if !row.ConsumedAt.IsZero() || row.Status == statusFinalized || row.Status == statusCancelled {
		return errFlowReplay
	}
	row.Status = statusFinalized
	row.ConsumedAt = s.now()
	row.ResultCredentialID = credentialID
	s.rows[id] = row
	return nil
}

func cloneSession(row acqSession) acqSession {
	cp := row
	if row.StateHash != nil {
		cp.StateHash = append([]byte(nil), row.StateHash...)
	}
	if row.EncryptedPKCEVerifier != nil {
		cp.EncryptedPKCEVerifier = append([]byte(nil), row.EncryptedPKCEVerifier...)
	}
	if row.RedactedContext != nil {
		cp.RedactedContext = map[string]any{}
		for k, v := range row.RedactedContext {
			cp.RedactedContext[k] = v
		}
	}
	return cp
}

func TestSessionStoreCRUD(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	row := acqSession{
		ID: "flow-1", TenantID: 1, ProviderAccountID: 2,
		Vendor: "openai", AuthMode: "chatgpt_oauth", Kind: flowKindOAuth,
		StateHash: []byte("hashed-state"), EncryptedPKCEVerifier: []byte("ciphertext"),
		RedactedContext: map[string]any{"client_identity_source": clientSourcePublicCLI},
	}
	if err := store.Create(row); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("flow-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != statusStarted {
		t.Fatalf("status=%q want %q", got.Status, statusStarted)
	}
	if got.ExpiresAt.Sub(now) != 10*time.Minute {
		t.Fatalf("ttl=%s want 10m", got.ExpiresAt.Sub(now))
	}
	if err := store.UpdateStatus("flow-1", statusValidated); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get("flow-1")
	if got.Status != statusValidated {
		t.Fatalf("updated status=%q want %q", got.Status, statusValidated)
	}
	if err := store.Cancel("flow-1"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get("flow-1")
	if got.Status != statusCancelled || got.CancelledAt.IsZero() {
		t.Fatalf("cancelled row=%+v", got)
	}
}

func TestSessionStoreConsumeRejectsExpiredAndReplay(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{ID: "fresh", ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume("fresh", 99); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume("fresh", 100); !errors.Is(err, errFlowReplay) {
		t.Fatalf("replay err=%v want %v", err, errFlowReplay)
	}
	if err := store.Create(acqSession{ID: "old", ExpiresAt: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume("old", 101); !errors.Is(err, errFlowExpired) {
		t.Fatalf("expired err=%v want %v", err, errFlowExpired)
	}
	got, _ := store.Get("old")
	if got.Status != statusExpired {
		t.Fatalf("expired status=%q want %q", got.Status, statusExpired)
	}
}
