package moderationhttp

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type hashCreateRequest struct {
	TenantID   int64  `json:"tenant_id"`
	HashHex    string `json:"hash_hex"`
	ReasonCode string `json:"reason_code"`
	Enabled    *bool  `json:"enabled"`
}

type hashResponse struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	HashHex    string `json:"hash_hex"`
	ReasonCode string `json:"reason_code"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type hashListResponse struct {
	Object string         `json:"object"`
	Items  []hashResponse `json:"items"`
	Limit  int32          `json:"limit"`
	Offset int32          `json:"offset"`
}

func newHashCreateHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		var body hashCreateRequest
		if !readJSON(w, r, &body) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		hashHex := strings.ToLower(strings.TrimSpace(body.HashHex))
		if !isSHA256Hex(hashHex) {
			writeError(w, http.StatusBadRequest, "invalid_hash_hex", "hash_hex must be 64 lowercase hex characters")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		row, err := deps.Store.CreateHash(r.Context(), moderation.CreateHashRequest{
			TenantID:   body.TenantID,
			HashHex:    hashHex,
			ReasonCode: strings.TrimSpace(body.ReasonCode),
			Enabled:    enabled,
			UpdatedBy:  adminActorID(ident),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, hashFromRule(row))
	}
}

func newHashListHandler(deps ModerationAdminDeps) http.HandlerFunc {
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
		rows, err := deps.Store.ListHashes(r.Context(), tenantID, limit, offset)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		items := make([]hashResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, hashFromRule(row))
		}
		writeJSON(w, http.StatusOK, hashListResponse{
			Object: "moderation_hashes_list",
			Items:  items,
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newHashDeleteHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := positivePathID(w, chi.URLParam(r, "id"), "hash_id")
		if !ok {
			return
		}
		if err := deps.Store.DeleteHash(r.Context(), tenantID, id); err != nil {
			writeModerationStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func hashFromRule(row moderation.HashRule) hashResponse {
	return hashResponse{
		ID:         row.ID,
		TenantID:   row.TenantID,
		HashHex:    row.HashHex,
		ReasonCode: row.ReasonCode,
		Enabled:    row.Enabled,
		CreatedAt:  formatTime(row.CreatedAt),
		UpdatedAt:  formatTime(row.UpdatedAt),
	}
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
