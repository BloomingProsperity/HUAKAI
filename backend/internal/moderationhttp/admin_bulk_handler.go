package moderationhttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type keywordBulkCreateRequest struct {
	TenantID int64                          `json:"tenant_id"`
	Items    []keywordBulkCreateItemRequest `json:"items"`
}

type keywordBulkCreateItemRequest struct {
	Keyword    string `json:"keyword"`
	ReasonCode string `json:"reason_code"`
	Enabled    *bool  `json:"enabled"`
}

type hashBulkCreateRequest struct {
	TenantID int64                       `json:"tenant_id"`
	Items    []hashBulkCreateItemRequest `json:"items"`
}

type hashBulkCreateItemRequest struct {
	HashHex    string `json:"hash_hex"`
	ReasonCode string `json:"reason_code"`
	Enabled    *bool  `json:"enabled"`
}

func newKeywordBulkCreateHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		var body keywordBulkCreateRequest
		if !readJSONLimit(w, r, &body, 1<<20) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		if !validateBulkItemCount(w, len(body.Items)) {
			return
		}

		items := make([]moderation.BulkCreateKeywordItem, 0, len(body.Items))
		for _, item := range body.Items {
			enabled := true
			if item.Enabled != nil {
				enabled = *item.Enabled
			}
			items = append(items, moderation.BulkCreateKeywordItem{
				Keyword:    item.Keyword,
				ReasonCode: item.ReasonCode,
				Enabled:    enabled,
			})
		}

		result, err := deps.Store.BulkCreateKeywords(r.Context(), moderation.BulkCreateKeywordsRequest{
			TenantID:  body.TenantID,
			Items:     items,
			UpdatedBy: adminActorID(ident),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, normalizeBulkResult(result))
	}
}

func newHashBulkCreateHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		var body hashBulkCreateRequest
		if !readJSONLimit(w, r, &body, 1<<20) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		if !validateBulkItemCount(w, len(body.Items)) {
			return
		}

		items := make([]moderation.BulkCreateHashItem, 0, len(body.Items))
		for _, item := range body.Items {
			enabled := true
			if item.Enabled != nil {
				enabled = *item.Enabled
			}
			items = append(items, moderation.BulkCreateHashItem{
				HashHex:    item.HashHex,
				ReasonCode: item.ReasonCode,
				Enabled:    enabled,
			})
		}

		result, err := deps.Store.BulkCreateHashes(r.Context(), moderation.BulkCreateHashesRequest{
			TenantID:  body.TenantID,
			Items:     items,
			UpdatedBy: adminActorID(ident),
		})
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, normalizeBulkResult(result))
	}
}

func validateBulkItemCount(w http.ResponseWriter, count int) bool {
	if count == 0 {
		writeError(w, http.StatusBadRequest, "items_required", "items must contain at least one row")
		return false
	}
	if count > moderation.BulkImportMaxItems {
		writeError(w, http.StatusBadRequest, "bulk_import_too_large", "items must contain at most 1000 rows")
		return false
	}
	return true
}

func normalizeBulkResult(result moderation.BulkCreateResult) moderation.BulkCreateResult {
	if result.Errors == nil {
		result.Errors = []moderation.BulkItemError{}
	}
	return result
}
