package auditledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// PreparedEntry 是一个已封箱的、隐私安全的 append 意图。它只包含在
// Append 选定最终 ledger id、Merkle root、signer fingerprint 与 signature
// 之前就已知的字段。
type PreparedEntry struct {
	requestID      string
	tenantID       int64
	createdAt      string
	tenantScopeRef string
	hopChain       []proto.HopAttestation
	modelChain     *proto.ModelChain
}

// PrepareEntry 把一条原始 ledger entry 脱敏成 Append 所接受的显式 append
// 意图。不可用的脱敏结果会被转换为 redaction_dropped 哨兵；只有结构性
// 前置条件失败才会返回错误。
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

// AsLedgerEntry 返回这个已封箱 append 意图的一个只读值投影。LedgerID、
// Merkle root、signer fingerprint 与 signature 保持零值，以便 Append 推导
// 它们。
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
