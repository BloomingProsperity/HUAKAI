package auditledger

import (
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type preparedEntryJSON struct {
	RequestID      string                 `json:"request_id"`
	TenantID       int64                  `json:"tenant_id"`
	CreatedAt      string                 `json:"created_at"`
	TenantScopeRef string                 `json:"tenant_scope_ref"`
	HopChain       []proto.HopAttestation `json:"hop_chain"`
	ModelChain     *proto.ModelChain      `json:"model_chain"`
}

func (entry PreparedEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(preparedEntryJSON{
		RequestID:      entry.requestID,
		TenantID:       entry.tenantID,
		CreatedAt:      entry.createdAt,
		TenantScopeRef: entry.tenantScopeRef,
		HopChain:       entry.hopChain,
		ModelChain:     entry.modelChain,
	})
}

func decodeLedgerEntryFromDLQPayload(raw []byte) (LedgerEntry, error) {
	var payload preparedEntryJSON
	if err := json.Unmarshal(raw, &payload); err != nil {
		return LedgerEntry{}, err
	}
	return LedgerEntry{
		RequestID:      payload.RequestID,
		TenantID:       payload.TenantID,
		Timestamp:      payload.CreatedAt,
		TenantScopeRef: payload.TenantScopeRef,
		HopChain:       payload.HopChain,
		ModelChain:     payload.ModelChain,
	}, nil
}
