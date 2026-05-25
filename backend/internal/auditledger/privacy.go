package auditledger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const redactionDroppedSentinel = "redaction_dropped"

type ledgerPayloadRedactor interface {
	SanitizePayload(context.Context, any) ([]byte, error)
}

var ledgerRedactor = func() ledgerPayloadRedactor {
	return privacy.DefaultRedactor()
}

type redactedLedgerPayload struct {
	TenantScopeRef string                 `json:"tenant_scope_ref,omitempty"`
	HopChain       []proto.HopAttestation `json:"hop_chain"`
	ModelChain     *proto.ModelChain      `json:"model_chain,omitempty"`
}

func sanitizeLedgerEntry(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	raw, err := ledgerRedactor().SanitizePayload(ctx, redactedLedgerPayload{
		TenantScopeRef: entry.TenantScopeRef,
		HopChain:       entry.HopChain,
		ModelChain:     entry.ModelChain,
	})
	if len(raw) == 0 {
		if err != nil {
			return entry, fmt.Errorf("%w: empty sanitized payload: %w", ErrLedgerSanitizeUnusable, err)
		}
		return entry, fmt.Errorf("%w: empty sanitized payload", ErrLedgerSanitizeUnusable)
	}
	var payload redactedLedgerPayload
	if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil {
		return entry, fmt.Errorf("%w: invalid sanitized payload JSON: %w", ErrLedgerSanitizeUnusable, unmarshalErr)
	}
	entry.TenantScopeRef = payload.TenantScopeRef
	entry.HopChain = payload.HopChain
	entry.ModelChain = payload.ModelChain
	return entry, err
}

func ledgerEntryWithRedactionDroppedSentinel(entry LedgerEntry) LedgerEntry {
	entry.HopChain = []proto.HopAttestation{{
		SchemaVersion: "trust.ledger.redaction_sentinel.v1",
		HopKind:       redactionDroppedSentinel,
		Actor:         "auditledger",
		DecisionRef:   redactionDroppedSentinel,
	}}
	entry.ModelChain = nil
	entry.TenantScopeRef = ""
	return entry
}

func SanitizeLedgerEntry(ctx context.Context, entry LedgerEntry) (LedgerEntry, error) {
	return sanitizeLedgerEntry(ctx, entry)
}
