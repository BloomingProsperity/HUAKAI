package channelhealth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func trustLedgerEntryForAudit(ev AuditEvent) (auditledger.LedgerEntry, error) {
	occurredAt := ev.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	ledgerID := uuid.NewString()
	detail, err := json.Marshal(map[string]any{
		"event_type":                ev.Type,
		"channel_id":                ev.Key.StableChannelID(),
		"vendor":                    ev.Key.Vendor,
		"previous_state":            ev.PreviousState,
		"new_state":                 ev.NewState,
		"reason_class":              ev.ReasonClass,
		"policy_version":            ev.PolicyVersion,
		"provider_account_id":       ev.Key.ProviderAccountID,
		"account_credential_id":     ev.Key.AccountCredentialID,
		"credential_version":        ev.Key.CredentialVersion,
		"source_request_id_present": ev.RequestID != "",
	})
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	return auditledger.LedgerEntry{
		LedgerID:  ledgerID,
		Timestamp: occurredAt.UTC().Format(time.RFC3339Nano),
		RequestID: fmt.Sprintf("channel-health:%s", ledgerID),
		TenantID:  ev.Key.TenantID,
		HopChain: []proto.HopAttestation{{
			Hop:           proto.HopAccount,
			Timestamp:     occurredAt.UTC().Format(time.RFC3339Nano),
			RequestID:     ev.RequestID,
			AccountIDHash: auditAccountHash(ev.Key.TenantID, ev.Key.ProviderAccountID),
			Provider:      ev.Key.Vendor,
			Detail:        detail,
		}},
	}, nil
}

func auditAccountHash(tenantID, accountID int64) string {
	if accountID <= 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", tenantID, accountID)))
	return hex.EncodeToString(sum[:])
}
