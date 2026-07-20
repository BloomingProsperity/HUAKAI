package credentialworker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestAgentTaskRecoveryUsesCredentialVersionToReuseWinner(t *testing.T) {
	store := &agentRecoveryStore{rec: credentialstore.CredentialRecord{
		ID: 81, TenantID: 7, ProviderAccountID: 91,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		CredentialVersion: 1, PlaintextPayload: []byte(`{"task_id":"old"}`),
	}}
	var adapterCalls atomic.Int32
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent, agentRecoveryAdapter{calls: &adapterCalls}); err != nil {
		t.Fatal(err)
	}
	refresher := &AccountCredentialRefresher{store: store, registry: registry, now: time.Now}
	if err := refresher.RecoverAgentTask(context.Background(), 7, 91, 1); err != nil {
		t.Fatal(err)
	}
	if err := refresher.RecoverAgentTask(context.Background(), 7, 91, 1); err != nil {
		t.Fatal(err)
	}
	if got := adapterCalls.Load(); got != 1 {
		t.Fatalf("重复恢复登记次数=%d want=1", got)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 1 || store.rec.CredentialVersion != 2 {
		t.Fatalf("saves=%d version=%d，期望单赢家版本前进一次", store.saves, store.rec.CredentialVersion)
	}
}

func TestAgentTaskRecoveryRejectsNonAgentCredential(t *testing.T) {
	store := &agentRecoveryStore{rec: credentialstore.CredentialRecord{
		ID: 82, TenantID: 7, ProviderAccountID: 92,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
		CredentialVersion: 1, PlaintextPayload: []byte(`{"api_key":"secret"}`),
	}}
	refresher := &AccountCredentialRefresher{store: store, registry: NewModeAdapterRegistry(), now: time.Now}
	if err := refresher.RecoverAgentTask(context.Background(), 7, 92, 1); err == nil {
		t.Fatal("非 Agent Identity 凭据不应进入任务恢复")
	}
}

type agentRecoveryAdapter struct {
	calls *atomic.Int32
}

func (a agentRecoveryAdapter) RefreshCredential(context.Context, ModeRefreshInput) (ModeRefreshResult, error) {
	a.calls.Add(1)
	return ModeRefreshResult{Payload: []byte(`{"task_id":"new"}`), Outcome: "agent_task_renewed"}, nil
}

type agentRecoveryStore struct {
	mu    sync.Mutex
	rec   credentialstore.CredentialRecord
	saves int
}

func (s *agentRecoveryStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAgentRecoveryRecord(s.rec), nil
}

func (s *agentRecoveryStore) WithRefreshTransaction(_ context.Context, fn func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &agentRecoveryTx{store: s}
	return fn(tx, tx)
}

type agentRecoveryTx struct {
	store *agentRecoveryStore
}

func (tx *agentRecoveryTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (tx *agentRecoveryTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (tx *agentRecoveryTx) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

func (tx *agentRecoveryTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	return cloneAgentRecoveryRecord(tx.store.rec), nil
}

func (tx *agentRecoveryTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, payload []byte, _ time.Time, _ string) error {
	tx.store.saves++
	tx.store.rec = cloneAgentRecoveryRecord(rec)
	tx.store.rec.CredentialVersion++
	tx.store.rec.PlaintextPayload = append([]byte(nil), payload...)
	return nil
}

func (tx *agentRecoveryTx) SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error {
	return nil
}

func (tx *agentRecoveryTx) SetNextAttemptThrottle(context.Context, credentialstore.CredentialRecord, time.Time) error {
	return nil
}

func (tx *agentRecoveryTx) InsertAuditEvent(context.Context, credentialstore.AuditEvent) error {
	return nil
}

func cloneAgentRecoveryRecord(rec credentialstore.CredentialRecord) credentialstore.CredentialRecord {
	rec.PlaintextPayload = append([]byte(nil), rec.PlaintextPayload...)
	return rec
}
