package accountintake

import (
	"context"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
)

// ModeRegistryImportRefresher 把正式导入接到生产唯一的账号模式刷新注册表。
type ModeRegistryImportRefresher struct {
	registry *credentialworker.ModeAdapterRegistry
}

func NewModeRegistryImportRefresher(registry *credentialworker.ModeAdapterRegistry) *ModeRegistryImportRefresher {
	return &ModeRegistryImportRefresher{registry: registry}
}

func (r *ModeRegistryImportRefresher) RefreshImportCredential(
	ctx context.Context,
	candidate credentialacq.CredentialCandidate,
	now time.Time,
) (credentialacq.CredentialCandidate, error) {
	if r == nil || r.registry == nil {
		return candidate, ErrImportCredentialRefreshUnavailable
	}
	adapter, ok := r.registry.Lookup(candidate.Vendor, candidate.AuthMode)
	if !ok {
		return candidate, ErrImportCredentialRefreshUnavailable
	}
	result, err := adapter.RefreshCredential(ctx, credentialworker.ModeRefreshInput{
		Vendor: candidate.Vendor, AuthMode: candidate.AuthMode,
		Payload: candidate.Payload, Now: now,
	})
	if err != nil {
		if errors.Is(err, credentialworker.ErrNoRefreshRequired) {
			return candidate, ErrImportCredentialRefreshFailed
		}
		return candidate, err
	}
	if len(result.Payload) == 0 {
		return candidate, ErrImportCredentialRefreshFailed
	}
	candidate.Payload = result.Payload
	return candidate, nil
}
