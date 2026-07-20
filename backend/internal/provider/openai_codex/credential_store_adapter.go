package openai_codex

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func NewRefresher(store *credentialstore.Store, opts ...Option) *Refresher {
	r := &Refresher{Store: credentialStoreRefreshAdapter{store: store}, requireAccountLease: true}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

type credentialStoreRefreshAdapter struct {
	store *credentialstore.Store
}

func (s credentialStoreRefreshAdapter) LoadForRefresh(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s.store == nil {
		return credentialstore.CredentialRecord{}, ErrOpenAICodexStoreMissing
	}
	return s.store.LoadForRefresh(ctx, accountID)
}

func (s credentialStoreRefreshAdapter) SaveRefreshSuccess(ctx context.Context, rec credentialstore.CredentialRecord, payload []byte, expiresAt time.Time, outcome string) error {
	if s.store == nil {
		return ErrOpenAICodexStoreMissing
	}
	return s.store.SaveRefreshSuccess(ctx, rec, payload, expiresAt, outcome)
}

func (s credentialStoreRefreshAdapter) SaveRefreshFailure(ctx context.Context, rec credentialstore.CredentialRecord, failureClass string, nextAttempt time.Time) error {
	if s.store == nil {
		return ErrOpenAICodexStoreMissing
	}
	return s.store.SaveRefreshFailure(ctx, rec, failureClass, nextAttempt)
}
