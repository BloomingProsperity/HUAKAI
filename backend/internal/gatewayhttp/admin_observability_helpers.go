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

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func identityRow[T any](r T) any { return r }

func mapAuditRow(r db.ListAuditEventsRow) any {
	payload := json.RawMessage(r.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var claimID any
	if r.ClaimID > 0 {
		claimID = r.ClaimID
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
