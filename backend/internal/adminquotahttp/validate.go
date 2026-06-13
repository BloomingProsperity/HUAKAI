package adminquotahttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

// validateRequest applies the full union validation: enum allowlists, scope_id
// bounds, numeric >=0, window_seconds required for fixed windows, and the
// valid_until > valid_from rule — all BEFORE the DB write so bad input yields a
// 400 rather than a CHECK-constraint 503.
func validateRequest(w http.ResponseWriter, req quotaPolicyRequest) (validatedPolicy, bool) {
	var vp validatedPolicy

	scopeKind := strings.TrimSpace(req.ScopeKind)
	if _, ok := validScopeKinds[scopeKind]; !ok {
		writeError(w, http.StatusBadRequest, "invalid_scope_kind",
			"scope_kind must be one of global, user, api_key, channel, pool_group, provider_account")
		return vp, false
	}
	vp.scopeKind = scopeKind

	scopeID := strings.TrimSpace(req.ScopeID)
	if scopeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_scope_id",
			"scope_id is required ('*' for global)")
		return vp, false
	}
	if len(scopeID) > maxScopeIDLen {
		writeError(w, http.StatusBadRequest, "invalid_scope_id",
			fmt.Sprintf("scope_id must be <= %d chars", maxScopeIDLen))
		return vp, false
	}
	vp.scopeID = scopeID

	metric := strings.TrimSpace(req.Metric)
	if _, ok := validMetrics[metric]; !ok {
		writeError(w, http.StatusBadRequest, "invalid_metric",
			"metric must be one of requests, tokens_estimated, cost_usd, concurrency")
		return vp, false
	}
	vp.metric = metric

	windowKind := strings.TrimSpace(req.WindowKind)
	if windowKind == "" {
		windowKind = "fixed"
	}
	if _, ok := validWindowKinds[windowKind]; !ok {
		writeError(w, http.StatusBadRequest, "invalid_window_kind",
			"window_kind must be one of none, fixed, calendar_day, calendar_week, manual")
		return vp, false
	}
	vp.windowKind = windowKind

	var windowSeconds int32
	if req.WindowSeconds != nil {
		windowSeconds = *req.WindowSeconds
	}
	if windowSeconds < 0 {
		writeError(w, http.StatusBadRequest, "invalid_window_seconds", "window_seconds must be >= 0")
		return vp, false
	}
	if windowKind == "fixed" && windowSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_window_seconds",
			"window_seconds is required (>0) when window_kind=fixed")
		return vp, false
	}
	vp.windowSeconds = windowSeconds

	limitValue, ok := parseNonNegativeDecimal(w, req.LimitValue, "limit_value", true)
	if !ok {
		return vp, false
	}
	vp.limitValue = limitValue

	burstRaw := "0"
	if req.BurstValue != nil {
		burstRaw = *req.BurstValue
	}
	burstValue, ok := parseNonNegativeDecimal(w, burstRaw, "burst_value", false)
	if !ok {
		return vp, false
	}
	vp.burstValue = burstValue

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = defaultMode
	}
	if _, ok := validModes[mode]; !ok {
		writeError(w, http.StatusBadRequest, "invalid_mode",
			"mode must be one of enforce, observe, manual_first, disabled")
		return vp, false
	}
	vp.mode = mode

	vp.priority = 100
	if req.Priority != nil {
		vp.priority = *req.Priority
	}

	vp.enabled = true
	if req.Enabled != nil {
		vp.enabled = *req.Enabled
	}

	validFrom := time.Now().UTC()
	if req.ValidFrom != nil && strings.TrimSpace(*req.ValidFrom) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ValidFrom))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_valid_from", "valid_from must be RFC3339")
			return vp, false
		}
		validFrom = parsed.UTC()
	}
	vp.validFrom = validFrom

	if req.ValidUntil != nil && strings.TrimSpace(*req.ValidUntil) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ValidUntil))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_valid_until", "valid_until must be RFC3339")
			return vp, false
		}
		until := parsed.UTC()
		if !until.After(validFrom) {
			writeError(w, http.StatusBadRequest, "invalid_validity_range",
				"valid_until must be after valid_from")
			return vp, false
		}
		vp.validUntil = &until
	}

	return vp, true
}

func buildInsertParams(w http.ResponseWriter, tenantID int64, vp validatedPolicy, actor *string) (dbquota.InsertQuotaPolicyParams, bool) {
	limit, ok := encodeNumeric(w, vp.limitValue, "limit_value")
	if !ok {
		return dbquota.InsertQuotaPolicyParams{}, false
	}
	burst, ok := encodeNumeric(w, vp.burstValue, "burst_value")
	if !ok {
		return dbquota.InsertQuotaPolicyParams{}, false
	}
	return dbquota.InsertQuotaPolicyParams{
		TenantID:            tenantID,
		ScopeKind:           vp.scopeKind,
		ScopeID:             vp.scopeID,
		Metric:              vp.metric,
		WindowKind:          vp.windowKind,
		WindowSeconds:       vp.windowSeconds,
		LimitValue:          limit,
		BurstValue:          burst,
		Mode:                vp.mode,
		Priority:            vp.priority,
		Enabled:             vp.enabled,
		ValidFrom:           timestamptzFromTime(vp.validFrom),
		ValidUntil:          timestamptzFromPtr(vp.validUntil),
		CreatedByActor:      actor,
		LastModifiedByActor: actor,
	}, true
}

func buildUpdateParams(w http.ResponseWriter, tenantID, id int64, vp validatedPolicy, actor *string) (dbquota.UpdateQuotaPolicyParams, bool) {
	limit, ok := encodeNumeric(w, vp.limitValue, "limit_value")
	if !ok {
		return dbquota.UpdateQuotaPolicyParams{}, false
	}
	burst, ok := encodeNumeric(w, vp.burstValue, "burst_value")
	if !ok {
		return dbquota.UpdateQuotaPolicyParams{}, false
	}
	return dbquota.UpdateQuotaPolicyParams{
		TenantID:            tenantID,
		ID:                  id,
		ScopeKind:           vp.scopeKind,
		ScopeID:             vp.scopeID,
		Metric:              vp.metric,
		WindowKind:          vp.windowKind,
		WindowSeconds:       vp.windowSeconds,
		LimitValue:          limit,
		BurstValue:          burst,
		Mode:                vp.mode,
		Priority:            vp.priority,
		Enabled:             vp.enabled,
		ValidFrom:           timestamptzFromTime(vp.validFrom),
		ValidUntil:          timestamptzFromPtr(vp.validUntil),
		LastModifiedByActor: actor,
	}, true
}

func buildAudit(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity, tenantID int64, action, reason string, vp validatedPolicy) (auditInput, bool) {
	payload, err := json.Marshal(map[string]any{
		"tenant_id":      tenantID,
		"scope_kind":     vp.scopeKind,
		"scope_id":       vp.scopeID,
		"metric":         vp.metric,
		"window_kind":    vp.windowKind,
		"window_seconds": vp.windowSeconds,
		"limit_value":    vp.limitValue.String(),
		"burst_value":    vp.burstValue.String(),
		"mode":           vp.mode,
		"priority":       vp.priority,
		"enabled":        vp.enabled,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
		return auditInput{}, false
	}
	return auditInput{
		TenantID:  tenantID,
		ActorID:   actorID(ident),
		ActorRole: actorRole(ident),
		Action:    action,
		RequestID: requestID(r),
		Reason:    reasonPtr(reason),
		Payload:   payload,
	}, true
}

func writeMutationError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, errQuotaPolicyConflict):
		writeError(w, http.StatusConflict, "quota_policy_conflict",
			"a live policy already exists for this scope, metric, window and priority")
	case errors.Is(err, errQuotaPolicyInUse):
		writeError(w, http.StatusConflict, "quota_policy_in_use",
			"quota policy has live windows and cannot be deleted")
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "quota_policy_not_found", "quota policy not found")
	default:
		writeError(w, http.StatusServiceUnavailable, fallbackCode, err.Error())
	}
}

// --- small helpers -----------------------------------------------------------

func filterValue(w http.ResponseWriter, r *http.Request, key string, allow map[string]struct{}, code string) (*string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, true
	}
	if _, ok := allow[raw]; !ok {
		writeError(w, http.StatusBadRequest, code, fmt.Sprintf("%s filter value is not allowed", key))
		return nil, false
	}
	return &raw, true
}

func enabledFilter(w http.ResponseWriter, r *http.Request) (*bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("enabled"))
	if raw == "" {
		return nil, true
	}
	switch raw {
	case "true":
		v := true
		return &v, true
	case "false":
		v := false
		return &v, true
	default:
		writeError(w, http.StatusBadRequest, "invalid_enabled", "enabled filter must be true or false")
		return nil, false
	}
}

func parseNonNegativeDecimal(w http.ResponseWriter, raw, field string, required bool) (decimal.Decimal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			writeError(w, http.StatusBadRequest, "invalid_"+field, field+" is required")
			return decimal.Zero, false
		}
		return decimal.Zero, true
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_"+field, field+" must be a decimal number")
		return decimal.Zero, false
	}
	if d.IsNegative() {
		writeError(w, http.StatusBadRequest, "invalid_"+field, field+" must be >= 0")
		return decimal.Zero, false
	}
	return d, true
}

func encodeNumeric(w http.ResponseWriter, d decimal.Decimal, field string) (pgtype.Numeric, bool) {
	var n pgtype.Numeric
	if err := n.Scan(d.String()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_"+field, field+" is not a valid numeric")
		return pgtype.Numeric{}, false
	}
	return n, true
}

func decimalStringFromNumeric(n pgtype.Numeric) string {
	if !n.Valid || n.Int == nil {
		return "0"
	}
	return decimal.NewFromBigInt(n.Int, n.Exp).String()
}

func timestamptzFromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timestamptzFromPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func itemFromRow(row dbquota.QuotaPolicy) quotaPolicyItem {
	item := quotaPolicyItem{
		ID:                  row.ID,
		TenantID:            row.TenantID,
		ScopeKind:           row.ScopeKind,
		ScopeID:             row.ScopeID,
		Metric:              row.Metric,
		WindowKind:          row.WindowKind,
		WindowSeconds:       row.WindowSeconds,
		LimitValue:          decimalStringFromNumeric(row.LimitValue),
		BurstValue:          decimalStringFromNumeric(row.BurstValue),
		Mode:                row.Mode,
		Priority:            row.Priority,
		Enabled:             row.Enabled,
		ValidFrom:           timestamp(row.ValidFrom),
		CreatedByActor:      row.CreatedByActor,
		LastModifiedByActor: row.LastModifiedByActor,
		CreatedAt:           timestamp(row.CreatedAt),
		UpdatedAt:           timestamp(row.UpdatedAt),
	}
	if row.ValidUntil.Valid {
		s := timestamp(row.ValidUntil)
		item.ValidUntil = &s
	}
	return item
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

// actorID is the audit ActorID (admin_audit_events.actor_id, a text column).
func actorID(ident admin.AdminIdentity) string {
	return fmt.Sprintf("%d", ident.TokenID)
}

// actorAttribution is the created_by/last_modified_by_actor column value
// (nullable text); it tracks who issued the change for ecosystem audit.
func actorAttribution(ident admin.AdminIdentity) *string {
	s := actorID(ident)
	return &s
}

func actorRole(ident admin.AdminIdentity) string {
	if ident.Role == "" {
		return admin.RoleTenantOperator
	}
	return ident.Role
}

func requestID(r *http.Request) *string {
	id := middleware.GetReqID(r.Context())
	if id == "" {
		return nil
	}
	return &id
}

func reasonPtr(reason string) *string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil
	}
	return &reason
}
