package auditledger

import (
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type LedgerResultState int

const (
	LedgerResultStatePersisted LedgerResultState = iota + 1
	LedgerResultStateDeferred
	LedgerResultStateDisabled
)

func (s LedgerResultState) String() string {
	switch s {
	case LedgerResultStatePersisted:
		return "Persisted"
	case LedgerResultStateDeferred:
		return "Deferred"
	case LedgerResultStateDisabled:
		return "Disabled"
	default:
		return fmt.Sprintf("LedgerResultState(%d)", int(s))
	}
}

type AuditLedgerResult struct {
	State            LedgerResultState
	LedgerID         string
	DLQRef           string
	Fingerprint      string
	UpstreamProvider string `json:"upstream_provider"`
	UpstreamModel    string `json:"upstream_model"`
	RequestID        string `json:"request_id"`
}

func PersistedLedgerResult(entry LedgerEntry) AuditLedgerResult {
	return AuditLedgerResult{
		State:            LedgerResultStatePersisted,
		LedgerID:         entry.LedgerID,
		Fingerprint:      entry.PubkeyFingerprint,
		RequestID:        entry.RequestID,
		UpstreamProvider: persistedResultProvider(entry),
		UpstreamModel:    persistedResultModel(entry),
	}
}

func DeferredLedgerResult(dlqRef string) AuditLedgerResult {
	return AuditLedgerResult{
		State:  LedgerResultStateDeferred,
		DLQRef: dlqRef,
	}
}

func DisabledLedgerResult() AuditLedgerResult {
	return AuditLedgerResult{State: LedgerResultStateDisabled}
}

func (r AuditLedgerResult) Validate(production bool) error {
	switch r.State {
	case LedgerResultStatePersisted:
		if r.LedgerID == "" {
			return fmt.Errorf("auditledger: persisted result requires LedgerID")
		}
		if r.Fingerprint == "" {
			return fmt.Errorf("auditledger: persisted result requires Fingerprint")
		}
		if r.DLQRef != "" {
			return fmt.Errorf("auditledger: persisted result must not include DLQRef")
		}
	case LedgerResultStateDeferred:
		if r.UpstreamProvider != "" || r.UpstreamModel != "" || r.RequestID != "" {
			return fmt.Errorf("auditledger: deferred result must not include upstream metadata")
		}
		if r.DLQRef == "" {
			return fmt.Errorf("auditledger: deferred result requires DLQRef")
		}
		if r.LedgerID != "" {
			return fmt.Errorf("auditledger: deferred result must not include LedgerID")
		}
		if r.Fingerprint != "" {
			return fmt.Errorf("auditledger: deferred result must not include Fingerprint")
		}
	case LedgerResultStateDisabled:
		if production {
			return fmt.Errorf("auditledger: disabled result is not valid in production")
		}
		if r.UpstreamProvider != "" || r.UpstreamModel != "" || r.RequestID != "" {
			return fmt.Errorf("auditledger: disabled result must not include upstream metadata")
		}
		if r.LedgerID != "" || r.DLQRef != "" || r.Fingerprint != "" {
			return fmt.Errorf("auditledger: disabled result must not include ledger fields")
		}
	default:
		return fmt.Errorf("auditledger: unknown ledger result state %d", int(r.State))
	}
	return nil
}

func IsNoopLedger(ledger Ledger) bool {
	switch ledger.(type) {
	case NoopLedger, *NoopLedger:
		return true
	default:
		return false
	}
}

func persistedResultProvider(entry LedgerEntry) string {
	for _, hop := range entry.HopChain {
		if hop.Hop == proto.HopProvider {
			return hop.Provider
		}
	}
	return ""
}

func persistedResultModel(entry LedgerEntry) string {
	if entry.ModelChain == nil {
		return ""
	}
	if entry.ModelChain.RouteDecided != "" {
		return entry.ModelChain.RouteDecided
	}
	return entry.ModelChain.Requested
}
