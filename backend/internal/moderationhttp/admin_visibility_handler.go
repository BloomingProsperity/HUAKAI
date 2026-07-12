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
	PayloadHash      string `json:"payload_hash"`
	Decision         string `json:"decision"`
	ReasonCode       string `json:"reason_code"`
	MatchedKeywordID *int64 `json:"matched_keyword_id,omitempty"`
	MatchedHashID    *int64 `json:"matched_hash_id,omitempty"`
	OccurredAt       string `json:"occurred_at,omitempty"`
}

type moderationLogListResponse struct {
	Object string                  `json:"object"`
	Items  []moderationLogResponse `json:"items"`
	Limit  int32                   `json:"limit"`
	Offset int32                   `json:"offset"`
}

type bannedAPIKeyResponse struct {
	ID              int64  `json:"id"`
	TenantID        int64  `json:"tenant_id"`
	UserID          int64  `json:"user_id"`
	Name            string `json:"name"`
	KeyPrefix       string `json:"key_prefix"`
	Status          string `json:"status"`
	ViolationCount  int64  `json:"violation_count"`
	LastViolationAt string `json:"last_violation_at,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type bannedAPIKeyListResponse struct {
	Object string                 `json:"object"`
	Items  []bannedAPIKeyResponse `json:"items"`
	Limit  int32                  `json:"limit"`
	Offset int32                  `json:"offset"`
}

type unbanAPIKeyRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason"`
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
		result, err := deps.Store.UnbanAPIKey(r.Context(), moderation.UnbanAPIKeyRequest{
			TenantID: body.TenantID,
			APIKeyID: apiKeyID,
			ActorID:  adminActorID(ident),
			Reason:   strings.TrimSpace(body.Reason),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, unbanAPIKeyFromResult(result))
	}
}

func optionalAPIKeyIDFromQuery(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("api_key_id"))
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_api_key_id",
			"api_key_id must be a positive int64")
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
		PayloadHash:      row.PayloadHash,
		Decision:         string(row.Decision),
		ReasonCode:       row.ReasonCode,
		MatchedKeywordID: row.MatchedKeywordID,
		MatchedHashID:    row.MatchedHashID,
		OccurredAt:       formatTime(row.OccurredAt),
	}
}

func bannedAPIKeyFromValue(row moderation.BannedAPIKey) bannedAPIKeyResponse {
	return bannedAPIKeyResponse{
		ID:              row.ID,
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		Name:            row.Name,
		KeyPrefix:       row.KeyPrefix,
		Status:          row.Status,
		ViolationCount:  row.ViolationCount,
		LastViolationAt: formatTime(row.LastViolationAt),
		CreatedAt:       formatTime(row.CreatedAt),
		UpdatedAt:       formatTime(row.UpdatedAt),
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
