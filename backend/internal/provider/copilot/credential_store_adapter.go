package copilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type CredentialStoreAdapter struct {
	Store *credentialstore.Store
	Now   func() time.Time

	mu      sync.Mutex
	records map[int64]credentialstore.CredentialRecord
}

func NewCredentialStoreAdapter(store *credentialstore.Store) *CredentialStoreAdapter {
	return &CredentialStoreAdapter{Store: store}
}

func (s *CredentialStoreAdapter) LoadCopilotCredential(ctx context.Context, accountID int64) ([]byte, error) {
	rec, err := s.load(ctx, accountID)
	if err != nil {
		return nil, err
	}
	payload := append([]byte(nil), rec.PlaintextPayload...)
	privacy.Zeroize(rec.PlaintextPayload)
	rec.PlaintextPayload = nil
	s.remember(accountID, rec)
	return payload, nil
}

func (s *CredentialStoreAdapter) SaveCopilotCredential(ctx context.Context, accountID int64, credential []byte, expiresAt time.Time) error {
	rec, err := s.takeOrLoad(ctx, accountID)
	if err != nil {
		return err
	}
	return s.Store.SaveRefreshSuccess(ctx, rec, credential, expiresAt, "refresh_succeeded")
}

func (s *CredentialStoreAdapter) RecordCopilotRefreshFailure(ctx context.Context, accountID int64, outcome string, _ error) error {
	rec, err := s.takeOrLoad(ctx, accountID)
	if err != nil {
		return err
	}
	if outcome == "" {
		outcome = "refresh_failed"
	}
	return s.Store.SaveRefreshFailure(ctx, rec, outcome, s.now().Add(time.Minute))
}

func (s *CredentialStoreAdapter) load(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s == nil || s.Store == nil {
		return credentialstore.CredentialRecord{}, errors.New("copilot refresh store: credential store missing")
	}
	rec, err := s.Store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return credentialstore.CredentialRecord{}, err
	}
	if credentialstore.Normalize(rec.Vendor) != credentialstore.VendorCopilot {
		privacy.Zeroize(rec.PlaintextPayload)
		return credentialstore.CredentialRecord{}, fmt.Errorf("copilot refresh store: vendor mismatch account_id=%d vendor=%s", accountID, rec.Vendor)
	}
	return rec, nil
}

func (s *CredentialStoreAdapter) remember(accountID int64, rec credentialstore.CredentialRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[int64]credentialstore.CredentialRecord)
	}
	s.records[accountID] = rec
}

func (s *CredentialStoreAdapter) takeOrLoad(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s == nil {
		return credentialstore.CredentialRecord{}, errors.New("copilot refresh store: credential store missing")
	}
	s.mu.Lock()
	rec, ok := s.records[accountID]
	if ok {
		delete(s.records, accountID)
	}
	s.mu.Unlock()
	if ok {
		return rec, nil
	}
	rec, err := s.load(ctx, accountID)
	if err != nil {
		return credentialstore.CredentialRecord{}, err
	}
	privacy.Zeroize(rec.PlaintextPayload)
	rec.PlaintextPayload = nil
	return rec, nil
}

func (s *CredentialStoreAdapter) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
