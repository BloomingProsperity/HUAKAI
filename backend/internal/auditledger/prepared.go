package auditledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// PreparedEntry is a sealed privacy-safe append intent. It contains only
// fields that are known before Append chooses the final ledger id, Merkle
// roots, signer fingerprint, and signature.
type PreparedEntry struct {
	requestID      string
	tenantID       int64
	createdAt      string
	tenantScopeRef string
	hopChain       []proto.HopAttestation
	modelChain     *proto.ModelChain
}

// PrepareEntry sanitizes a raw ledger entry into the explicit append intent
// accepted by Append. Unusable redaction output is converted into the
// redaction_dropped sentinel; only structural precondition failures return an
// error.
func PrepareEntry(ctx context.Context, rawEntry LedgerEntry) (PreparedEntry, error) {
	if rawEntry.RequestID == "" {
		return PreparedEntry{}, fmt.Errorf("auditledger: RequestID required for PrepareEntry")
	}
	sanitized, err := sanitizeLedgerEntry(ctx, rawEntry)
	if errors.Is(err, ErrLedgerSanitizeUnusable) {
		sanitized = ledgerEntryWithRedactionDroppedSentinel(rawEntry)
	}
	return preparedEntryFromLedgerEntry(sanitized), nil
}

func preparedEntryFromLedgerEntry(entry LedgerEntry) PreparedEntry {
	return PreparedEntry{
		requestID:      entry.RequestID,
		tenantID:       entry.TenantID,
		createdAt:      entry.Timestamp,
		tenantScopeRef: entry.TenantScopeRef,
		hopChain:       entry.HopChain,
		modelChain:     entry.ModelChain,
	}
}

// AsLedgerEntry returns a read-only value projection of the sealed append
// intent. LedgerID, Merkle roots, signer fingerprint, and signature remain
// zero-valued so Append can derive them.
func (entry PreparedEntry) AsLedgerEntry() LedgerEntry {
	return LedgerEntry{
		Timestamp:      entry.createdAt,
		RequestID:      entry.requestID,
		TenantID:       entry.tenantID,
		TenantScopeRef: entry.tenantScopeRef,
		HopChain:       clonePreparedHopChain(entry.hopChain),
		ModelChain:     clonePreparedModelChain(entry.modelChain),
	}
}

func clonePreparedHopChain(hops []proto.HopAttestation) []proto.HopAttestation {
	if hops == nil {
		return nil
	}
	out := make([]proto.HopAttestation, len(hops))
	copy(out, hops)
	for i := range out {
		out[i].Detail = append([]byte(nil), hops[i].Detail...)
		out[i].FeatureRefs = append([]string(nil), hops[i].FeatureRefs...)
	}
	return out
}

func clonePreparedModelChain(model *proto.ModelChain) *proto.ModelChain {
	if model == nil {
		return nil
	}
	out := *model
	return &out
}
