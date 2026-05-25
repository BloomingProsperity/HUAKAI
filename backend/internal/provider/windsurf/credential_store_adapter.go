package windsurf

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func NewRefresher(store *credentialstore.Store, opts ...Option) *Refresher {
	r := &Refresher{Store: credentialStoreRefreshAdapter{store: store}}
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
		return credentialstore.CredentialRecord{}, errors.New("windsurf refresh: credential store missing")
	}
	return s.store.LoadForRefresh(ctx, accountID)
}

func (s credentialStoreRefreshAdapter) WithRefreshTransaction(ctx context.Context, fn func(RefreshTxStore, db.DBTX) error) error {
	if s.store == nil {
		return errors.New("windsurf refresh: credential store missing")
	}
	return s.store.WithTransaction(ctx, func(txStore *credentialstore.Store, tx db.DBTX) error {
		if fn == nil {
			return nil
		}
		return fn(txStore, tx)
	})
}
