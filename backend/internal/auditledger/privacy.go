package auditledger

import (
	"context"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type redactedLedgerPayload struct {
	TenantScopeRef string                 `json:"tenant_scope_ref,omitempty"`
	HopChain       []proto.HopAttestation `json:"hop_chain"`
	ModelChain     *proto.ModelChain      `json:"model_chain,omitempty"`
}

func sanitizeLedgerEntry(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	raw, err := privacy.DefaultRedactor().SanitizePayload(ctx, redactedLedgerPayload{
		TenantScopeRef: entry.TenantScopeRef,
		HopChain:       entry.HopChain,
		ModelChain:     entry.ModelChain,
	})
	if len(raw) == 0 {
		return entry, err
	}
	var payload redactedLedgerPayload
	if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil {
		if err != nil {
			return entry, err
		}
		return entry, unmarshalErr
	}
	entry.TenantScopeRef = payload.TenantScopeRef
	entry.HopChain = payload.HopChain
	entry.ModelChain = payload.ModelChain
	return entry, err
}

func SanitizeLedgerEntry(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	return sanitizeLedgerEntry(ctx, entry)
}
