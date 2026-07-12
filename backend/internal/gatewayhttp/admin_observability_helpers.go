package gatewayhttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/trust"
)

func identityRow[T any](r T) any { return r }

func mapUsageRow(r dbbilling.ListUsageRecordsRow) any {
	provider := valueString(r.Provider)
	upstreamModel := valueString(r.UpstreamModel)
	requestID := strings.TrimSpace(r.RequestID)
	trustStatus := string(trust.StatusMissing)
	if entry, ok := usageAuditLedgerEntry(r); ok {
		meta := trust.MetadataFromLedgerEntry(entry)
		result := auditledger.PersistedLedgerResult(entry)
		if meta.Provider != "" {
			provider = meta.Provider
		}
		if meta.Model != "" {
			upstreamModel = meta.Model
		}
		if meta.RequestID != "" {
			requestID = meta.RequestID
		}
		trustStatus = string(trust.ResponseStatus(meta, result))
	}
	return map[string]any{
		"id": r.ID, "tenant_id": r.TenantID, "claim_id": r.ClaimID,
		"api_key_id": r.APIKeyID, "user_id": r.UserID,
		"provider_account_id": r.ProviderAccountID, "provider": provider,
		"attempt_seq": r.AttemptSeq, "tokens_input": r.TokensInput,
		"tokens_output": r.TokensOutput, "cache_creation_tokens": r.CacheCreationTokens,
		"cache_creation_5m_tokens": r.CacheCreation5mTokens, "cache_creation_1h_tokens": r.CacheCreation1hTokens,
		"cache_read_tokens": r.CacheReadTokens, "actual_cost": r.ActualCost,
		"end_class": r.EndClass, "usage_source": r.UsageSource,
		"pending_reconciliation": r.PendingReconciliation,
		"stream_state":           r.StreamState, "delivered_token_count": r.DeliveredTokenCount,
		"stream_terminated_reason": r.StreamTerminatedReason,
		"requested_at":             r.RequestedAt, "created_at": r.CreatedAt,
		"requested_model": r.RequestedModel, "upstream_model": upstreamModel,
		"stream": r.Stream, "settlement_source": r.SettlementSource,
		"pool_id": r.PoolID, "request_id": requestID, "trust_status": trustStatus,
		"ip_address": r.IPAddress, "user_agent": r.UserAgent,
		"client_tool": r.ClientTool,
	}
}

func usageAuditLedgerEntry(r dbbilling.ListUsageRecordsRow) (auditledger.LedgerEntry, bool) {
	hasLedger := r.AuditLedgerID != nil || r.AuditPubkeyFingerprint != nil || len(r.AuditHopChain) > 0 || len(r.AuditModelChain) > 0
	if !hasLedger {
		return auditledger.LedgerEntry{}, false
	}
	entry := auditledger.LedgerEntry{
		RequestID: strings.TrimSpace(r.RequestID),
		TenantID:  r.TenantID,
	}
	if r.AuditLedgerID != nil {
		entry.LedgerID = strings.TrimSpace(*r.AuditLedgerID)
	}
	if r.AuditPubkeyFingerprint != nil {
		entry.PubkeyFingerprint = strings.TrimSpace(*r.AuditPubkeyFingerprint)
	}
	if len(r.AuditHopChain) > 0 {
		_ = json.Unmarshal(r.AuditHopChain, &entry.HopChain)
	}
	if len(r.AuditModelChain) > 0 {
		var model proto.ModelChain
		if err := json.Unmarshal(r.AuditModelChain, &model); err == nil {
			entry.ModelChain = &model
		}
	}
	return entry, true
}

func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func mapAuditRow(r dbbilling.ListAuditEventsRow) any {
	payload := json.RawMessage(r.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var claimID any
	if r.ClaimID != nil && *r.ClaimID > 0 {
		claimID = *r.ClaimID
	}
	return map[string]any{
		"id": r.ID, "tenant_id": r.TenantID, "event_class": r.EventClass,
		"event_type": r.EventType, "severity": r.Severity, "ledger_id": r.LedgerID,
		"claim_id": claimID, "provider_account_id": r.ProviderAccountID,
		"pool_group_id": r.PoolGroupID, "request_id": r.RequestID,
		"actor_id": r.ActorID, "actor_role": r.ActorRole, "reason": r.Reason,
		"payload": payload, "created_at": formatTS(r.CreatedAt),
	}
}

func parseQueryTime(w http.ResponseWriter, raw, name string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return nil, false
	}
	utc := t.UTC()
	return &utc, true
}

func parseIntFilter(w http.ResponseWriter, v url.Values, name string) (*int64, bool) {
	raw := trim(v, name)
	if raw == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be a positive int64")
		return nil, false
	}
	return &n, true
}

func encodeObsCursor(kind string, ts pgtype.Timestamptz, id int64) string {
	body, _ := json.Marshal(obsCursor{V: 1, K: kind, TS: formatTS(ts), ID: id})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeObsCursor(raw, kind string) (time.Time, int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	var c obsCursor
	if err := json.Unmarshal(data, &c); err != nil || c.V != 1 || c.K != kind || c.ID <= 0 {
		return time.Time{}, 0, errors.New("bad cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, c.TS)
	return ts.UTC(), c.ID, err
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339Nano)
}

func tsParam(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func trim(v url.Values, name string) string { return strings.TrimSpace(v.Get(name)) }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
