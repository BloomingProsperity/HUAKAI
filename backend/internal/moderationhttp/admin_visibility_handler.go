package moderationhttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type moderationLogResponse struct {
	ID               int64  `json:"id"`
	TenantID         int64  `json:"tenant_id"`
	APIKeyID         int64  `json:"api_key_id"`
	UserID           int64  `json:"user_id"`
	RequestID        string `json:"request_id,omitempty"`
	ViolationEventID *int64 `json:"violation_event_id,omitempty"`
	InputExcerpt     string `json:"input_excerpt,omitempty"`
	Decision         string `json:"decision"`
	ReasonCode       string `json:"reason_code"`
	MatchedKeywordID *int64 `json:"matched_keyword_id,omitempty"`
	MatchedHashID    *int64 `json:"matched_hash_id,omitempty"`
	ViolationCount   int64  `json:"violation_count"`
	ThresholdReached bool   `json:"threshold_reached"`
	KeyDisabled      bool   `json:"key_disabled"`
	ActorID          string `json:"actor_id,omitempty"`
	ActorRole        string `json:"actor_role,omitempty"`
	OccurredAt       string `json:"occurred_at,omitempty"`
}

type moderationLogListResponse struct {
	Object string                  `json:"object"`
	Items  []moderationLogResponse `json:"items"`
	Limit  int32                   `json:"limit"`
	Offset int32                   `json:"offset"`
}

type moderationViolationResponse struct {
	ID                       int64  `json:"id"`
	TenantID                 int64  `json:"tenant_id"`
	APIKeyID                 int64  `json:"api_key_id"`
	UserID                   int64  `json:"user_id"`
	RequestID                string `json:"request_id"`
	Decision                 string `json:"decision"`
	ReasonCode               string `json:"reason_code"`
	MatchedKeywordID         *int64 `json:"matched_keyword_id,omitempty"`
	MatchedHashID            *int64 `json:"matched_hash_id,omitempty"`
	BanThresholdSnapshot     int32  `json:"ban_threshold_snapshot"`
	BanWindowSecondsSnapshot int32  `json:"ban_window_seconds_snapshot"`
	ViolationCount           int64  `json:"violation_count"`
	ThresholdReached         bool   `json:"threshold_reached"`
	AutoDisableEnabled       bool   `json:"auto_disable_enabled"`
	DispositionSource        string `json:"disposition_source"`
	DispositionResult        string `json:"disposition_result"`
	InputExcerpt             string `json:"input_excerpt,omitempty"`
	KeyDisabled              bool   `json:"key_disabled"`
	OccurredAt               string `json:"occurred_at,omitempty"`
}

type moderationViolationListResponse struct {
	Object string                        `json:"object"`
	Items  []moderationViolationResponse `json:"items"`
	Limit  int32                         `json:"limit"`
	Offset int32                         `json:"offset"`
}

type bannedAPIKeyResponse struct {
	ID                int64  `json:"id"`
	TenantID          int64  `json:"tenant_id"`
	UserID            int64  `json:"user_id"`
	Name              string `json:"name"`
	KeyPrefix         string `json:"key_prefix"`
	Status            string `json:"status"`
	Source            string `json:"source"`
	ReasonCode        string `json:"reason_code"`
	DisableGeneration int64  `json:"disable_generation"`
	ViolationCount    int64  `json:"violation_count"`
	LastViolationAt   string `json:"last_violation_at,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type bannedAPIKeyListResponse struct {
	Object string                 `json:"object"`
	Items  []bannedAPIKeyResponse `json:"items"`
	Limit  int32                  `json:"limit"`
	Offset int32                  `json:"offset"`
}

type unbanAPIKeyRequest struct {
	TenantID       int64  `json:"tenant_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}

type disableAPIKeyRequest struct {
	TenantID         int64  `json:"tenant_id"`
	ViolationEventID int64  `json:"violation_event_id"`
	IdempotencyKey   string `json:"idempotency_key"`
	Reason           string `json:"reason"`
}

type disableAPIKeyResponse struct {
	APIKeyID  int64  `json:"api_key_id"`
	TenantID  int64  `json:"tenant_id"`
	Status    string `json:"status"`
	LogID     int64  `json:"log_id"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type unbanAPIKeyResponse struct {
	APIKeyID   int64  `json:"api_key_id"`
	TenantID   int64  `json:"tenant_id"`
	Status     string `json:"status"`
	AuditLogID int64  `json:"audit_log_id"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

func newLogListHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		apiKeyID, ok := optionalAPIKeyIDFromQuery(w, r)
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		rows, err := deps.Store.ListModerationLogs(r.Context(), tenantID, apiKeyID, limit, offset)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		items := make([]moderationLogResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, moderationLogFromValue(row))
		}
		writeJSON(w, http.StatusOK, moderationLogListResponse{
			Object: "moderation_logs_list",
			Items:  items,
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newViolationListHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		apiKeyID, ok := optionalPositiveQueryID(w, r, "api_key_id")
		if !ok {
			return
		}
		userID, ok := optionalPositiveQueryID(w, r, "user_id")
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		rows, err := deps.Store.ListModerationViolations(
			r.Context(), tenantID, apiKeyID, userID, limit, offset,
		)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		items := make([]moderationViolationResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, moderationViolationFromValue(row))
		}
		writeJSON(w, http.StatusOK, moderationViolationListResponse{
			Object: "moderation_violations_list",
			Items:  items, Limit: limit, Offset: offset,
		})
	}
}

func newBannedListHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		rows, err := deps.Store.ListBannedAPIKeys(r.Context(), tenantID, limit, offset)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		items := make([]bannedAPIKeyResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, bannedAPIKeyFromValue(row))
		}
		writeJSON(w, http.StatusOK, bannedAPIKeyListResponse{
			Object: "moderation_banned_keys_list",
			Items:  items,
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newAPIKeyUnbanHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		apiKeyID, ok := positivePathID(w, chi.URLParam(r, "id"), "api_key_id")
		if !ok {
			return
		}
		var body unbanAPIKeyRequest
		if !readJSON(w, r, &body) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		idempotencyKey, ok := requireIdempotencyKey(w, body.IdempotencyKey)
		if !ok {
			return
		}
		result, err := deps.Store.UnbanAPIKey(r.Context(), moderation.UnbanAPIKeyRequest{
			TenantID: body.TenantID, APIKeyID: apiKeyID,
			IdempotencyKey: idempotencyKey,
			ActorID:        adminActorID(ident), ActorRole: ident.Role,
			Reason: strings.TrimSpace(body.Reason),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, unbanAPIKeyFromResult(result))
	}
}

func newAPIKeyDisableHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		apiKeyID, ok := positivePathID(w, chi.URLParam(r, "id"), "api_key_id")
		if !ok {
			return
		}
		var body disableAPIKeyRequest
		if !readJSON(w, r, &body) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		if body.ViolationEventID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_violation_event_id",
				"violation_event_id must be a positive int64")
			return
		}
		idempotencyKey, ok := requireIdempotencyKey(w, body.IdempotencyKey)
		if !ok {
			return
		}
		result, err := deps.Store.DisableAPIKey(r.Context(), moderation.DisableAPIKeyRequest{
			TenantID: body.TenantID, APIKeyID: apiKeyID,
			ViolationEventID: body.ViolationEventID,
			IdempotencyKey:   idempotencyKey,
			ActorID:          adminActorID(ident), ActorRole: ident.Role,
			Reason: strings.TrimSpace(body.Reason),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, disableAPIKeyResponse{
			APIKeyID: result.APIKeyID, TenantID: result.TenantID,
			Status: result.Status, LogID: result.LogID, UpdatedAt: formatTime(result.UpdatedAt),
		})
	}
}

func requireIdempotencyKey(w http.ResponseWriter, raw string) (string, bool) {
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key",
			"idempotency_key must contain between 1 and 256 bytes")
		return "", false
	}
	return key, true
}

func optionalAPIKeyIDFromQuery(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	return optionalPositiveQueryID(w, r, "api_key_id")
}

func optionalPositiveQueryID(w http.ResponseWriter, r *http.Request, name string) (*int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_"+name,
			name+" must be a positive int64")
		return nil, false
	}
	return &id, true
}

func moderationLogFromValue(row moderation.ModerationLog) moderationLogResponse {
	return moderationLogResponse{
		ID:               row.ID,
		TenantID:         row.TenantID,
		APIKeyID:         row.APIKeyID,
		UserID:           row.UserID,
		RequestID:        row.RequestID,
		ViolationEventID: row.ViolationEventID,
		InputExcerpt:     row.InputExcerpt,
		Decision:         string(row.Decision),
		ReasonCode:       row.ReasonCode,
		MatchedKeywordID: row.MatchedKeywordID,
		MatchedHashID:    row.MatchedHashID,
		ViolationCount:   row.ViolationCount,
		ThresholdReached: row.ThresholdReached,
		KeyDisabled:      row.KeyDisabled,
		ActorID:          row.ActorID,
		ActorRole:        row.ActorRole,
		OccurredAt:       formatTime(row.OccurredAt),
	}
}

func moderationViolationFromValue(row moderation.ModerationViolation) moderationViolationResponse {
	return moderationViolationResponse{
		ID: row.ID, TenantID: row.TenantID, APIKeyID: row.APIKeyID, UserID: row.UserID,
		RequestID: row.RequestID, Decision: string(row.Decision), ReasonCode: row.ReasonCode,
		MatchedKeywordID: row.MatchedKeywordID, MatchedHashID: row.MatchedHashID,
		BanThresholdSnapshot:     row.BanThresholdSnapshot,
		BanWindowSecondsSnapshot: row.BanWindowSecondsSnapshot,
		ViolationCount:           row.ViolationCount, ThresholdReached: row.ThresholdReached,
		AutoDisableEnabled: row.AutoDisableEnabled,
		DispositionSource:  row.DispositionSource, DispositionResult: row.DispositionResult,
		InputExcerpt: row.InputExcerpt, KeyDisabled: row.KeyDisabled,
		OccurredAt: formatTime(row.OccurredAt),
	}
}

func bannedAPIKeyFromValue(row moderation.BannedAPIKey) bannedAPIKeyResponse {
	return bannedAPIKeyResponse{
		ID:                row.ID,
		TenantID:          row.TenantID,
		UserID:            row.UserID,
		Name:              row.Name,
		KeyPrefix:         row.KeyPrefix,
		Status:            row.Status,
		Source:            row.Source,
		ReasonCode:        row.ReasonCode,
		DisableGeneration: row.DisableGeneration,
		ViolationCount:    row.ViolationCount,
		LastViolationAt:   formatTime(row.LastViolationAt),
		CreatedAt:         formatTime(row.CreatedAt),
		UpdatedAt:         formatTime(row.UpdatedAt),
	}
}

func unbanAPIKeyFromResult(result moderation.UnbanAPIKeyResult) unbanAPIKeyResponse {
	return unbanAPIKeyResponse{
		APIKeyID:   result.APIKeyID,
		TenantID:   result.TenantID,
		Status:     result.Status,
		AuditLogID: result.AuditLogID,
		UpdatedAt:  formatTime(result.UpdatedAt),
	}
}
