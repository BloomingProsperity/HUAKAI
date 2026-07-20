package credentialworker

import (
	"context"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type accountCredentialRefreshTxStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	SaveRefreshSuccess(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string) error
	SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error
	SetNextAttemptThrottle(context.Context, credentialstore.CredentialRecord, time.Time) error
	InsertAuditEvent(context.Context, credentialstore.AuditEvent) error
}

type accountCredentialRefreshStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	WithRefreshTransaction(context.Context, func(accountCredentialRefreshTxStore, db.DBTX) error) error
}

type shortAccountRefreshStore struct {
	store         accountCredentialRefreshStore
	pendingAudits []credentialstore.AuditEvent
}

type atomicAccountRefreshPersistence interface {
	SaveRefreshSuccessWithAudit(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string, []credentialstore.AuditEvent) error
	SaveRefreshFailureWithAudit(context.Context, credentialstore.CredentialRecord, string, time.Time, []credentialstore.AuditEvent) error
}

func (s shortAccountRefreshStore) LoadForRefresh(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	return s.store.LoadForRefresh(ctx, accountID)
}

func (s shortAccountRefreshStore) SaveRefreshSuccess(ctx context.Context, rec credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string) error {
	if atomicStore, ok := s.store.(atomicAccountRefreshPersistence); ok {
		return atomicStore.SaveRefreshSuccessWithAudit(ctx, rec, payload, expiresAt, outcome, s.pendingAudits)
	}
	return s.store.WithRefreshTransaction(ctx, func(txStore accountCredentialRefreshTxStore, _ db.DBTX) error {
		for _, event := range s.pendingAudits {
			if err := txStore.InsertAuditEvent(ctx, event); err != nil {
				return err
			}
		}
		return txStore.SaveRefreshSuccess(ctx, rec, payload, expiresAt, outcome)
	})
}

func (s shortAccountRefreshStore) SaveRefreshFailure(ctx context.Context, rec credentialstore.CredentialRecord, failureClass string, nextAttempt time.Time) error {
	if atomicStore, ok := s.store.(atomicAccountRefreshPersistence); ok {
		return atomicStore.SaveRefreshFailureWithAudit(ctx, rec, failureClass, nextAttempt, s.pendingAudits)
	}
	return s.store.WithRefreshTransaction(ctx, func(txStore accountCredentialRefreshTxStore, _ db.DBTX) error {
		for _, event := range s.pendingAudits {
			if err := txStore.InsertAuditEvent(ctx, event); err != nil {
				return err
			}
		}
		return txStore.SaveRefreshFailure(ctx, rec, failureClass, nextAttempt)
	})
}

func (s shortAccountRefreshStore) SetNextAttemptThrottle(ctx context.Context, rec credentialstore.CredentialRecord, nextAttempt time.Time) error {
	return s.store.WithRefreshTransaction(ctx, func(txStore accountCredentialRefreshTxStore, _ db.DBTX) error {
		return txStore.SetNextAttemptThrottle(ctx, rec, nextAttempt)
	})
}

func (s *shortAccountRefreshStore) InsertAuditEvent(_ context.Context, event credentialstore.AuditEvent) error {
	s.pendingAudits = append(s.pendingAudits, event)
	return nil
}

type postgresAccountCredentialRefreshStore struct {
	store *credentialstore.Store
}

func (s postgresAccountCredentialRefreshStore) WithRefreshTransaction(ctx context.Context, fn func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	if s.store == nil {
		return errors.New("credentialworker: account credential store missing")
	}
	return s.store.WithTransaction(ctx, func(txStore *credentialstore.Store, tx db.DBTX) error {
		if fn == nil {
			return nil
		}
		return fn(txStore, tx)
	})
}

func (s postgresAccountCredentialRefreshStore) LoadForRefresh(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s.store == nil {
		return credentialstore.CredentialRecord{}, errors.New("credentialworker: account credential store missing")
	}
	return s.store.LoadForRefresh(ctx, accountID)
}

func (s postgresAccountCredentialRefreshStore) SaveRefreshSuccessWithAudit(ctx context.Context, rec credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string, audits []credentialstore.AuditEvent) error {
	return s.store.SaveRefreshSuccessWithAudit(ctx, rec, payload, expiresAt, outcome, audits)
}

func (s postgresAccountCredentialRefreshStore) SaveRefreshFailureWithAudit(ctx context.Context, rec credentialstore.CredentialRecord, failureClass string, nextAttempt time.Time, audits []credentialstore.AuditEvent) error {
	return s.store.SaveRefreshFailureWithAudit(ctx, rec, failureClass, nextAttempt, audits)
}
