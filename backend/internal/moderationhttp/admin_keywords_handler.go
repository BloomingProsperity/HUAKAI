package moderationhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type keywordCreateRequest struct {
	TenantID   int64  `json:"tenant_id"`
	Keyword    string `json:"keyword"`
	ReasonCode string `json:"reason_code"`
	Enabled    *bool  `json:"enabled"`
}

type keywordResponse struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	Keyword    string `json:"keyword"`
	ReasonCode string `json:"reason_code"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type keywordListResponse struct {
	Object string            `json:"object"`
	Items  []keywordResponse `json:"items"`
	Limit  int32             `json:"limit"`
	Offset int32             `json:"offset"`
}

func newKeywordCreateHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		var body keywordCreateRequest
		if !readJSON(w, r, &body) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		keyword := strings.TrimSpace(body.Keyword)
		if keyword == "" {
			writeError(w, http.StatusBadRequest, "keyword_required", "keyword is required")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		row, err := deps.Store.CreateKeyword(r.Context(), moderation.CreateKeywordRequest{
			TenantID:   body.TenantID,
			Keyword:    keyword,
			ReasonCode: strings.TrimSpace(body.ReasonCode),
			Enabled:    enabled,
			UpdatedBy:  adminActorID(ident),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, keywordFromRule(row))
	}
}

func newKeywordListHandler(deps ModerationAdminDeps) http.HandlerFunc {
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
		rows, err := deps.Store.ListKeywords(r.Context(), tenantID, limit, offset)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		items := make([]keywordResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, keywordFromRule(row))
		}
		writeJSON(w, http.StatusOK, keywordListResponse{
			Object: "moderation_keywords_list",
			Items:  items,
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newKeywordDeleteHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := positivePathID(w, chi.URLParam(r, "id"), "keyword_id")
		if !ok {
			return
		}
		if err := deps.Store.DeleteKeyword(r.Context(), tenantID, id); err != nil {
			writeModerationStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func keywordFromRule(row moderation.KeywordRule) keywordResponse {
	return keywordResponse{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Keyword:    row.Keyword,
		ReasonCode: row.ReasonCode,
		Enabled:    row.Enabled,
		CreatedAt:  formatTime(row.CreatedAt),
		UpdatedAt:  formatTime(row.UpdatedAt),
	}
}

func writeModerationStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, moderation.ErrKeywordExists):
		writeError(w, http.StatusConflict, "moderation_keyword_exists", "keyword already exists")
	case errors.Is(err, moderation.ErrHashExists):
		writeError(w, http.StatusConflict, "moderation_hash_exists", "hash already exists")
	case errors.Is(err, moderation.ErrNotFound):
		writeError(w, http.StatusNotFound, "moderation_not_found", "moderation resource not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "moderation_backend_error", err.Error())
	}
}
