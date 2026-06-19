package pricingcataloghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
)

type AdminPricingRatioDeps struct {
	Auth     adminRatioAuth
	Store    pricingcatalog.Store
	Resolver ratioCacheInvalidator
}

type adminRatioAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type ratioCacheInvalidator interface {
	Invalidate(tenantID, poolGroupID int64)
}

const pricingRatioMaxEnv = "HUAKAI_PRICING_RATIO_MAX"

var (
	pricingRatioMin        = decimal.RequireFromString("0.01")
	defaultPricingRatioMax = decimal.NewFromInt(100)
)

type ratioRequestBody struct {
	Ratio       string `json:"ratio"`
	PublicRatio bool   `json:"public_ratio"`
}

type ratioResponseBody struct {
	Object      string `json:"object"`
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	PoolGroupID int64  `json:"pool_group_id"`
	Ratio       string `json:"ratio,omitempty"`
	PublicRatio bool   `json:"public_ratio"`
	CreatedBy   string `json:"created_by,omitempty"`
	UpdatedBy   string `json:"updated_by,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type ratioListResponseBody struct {
	Object string              `json:"object"`
	Items  []ratioResponseBody `json:"items"`
	Limit  int32               `json:"limit"`
	Offset int32               `json:"offset"`
}

func MountPricingRatioRoutes(r chi.Router, d AdminPricingRatioDeps) {
	// 这条静态的审计证明路由必须注册在 {pool_group_id} 通配段之前，
	// 否则会被按组处理的倍率处理器遮蔽。
	r.Get("/audit/verify", newRatioAuditVerifyHandler(d))
	r.Get("/", newRatioListHandler(d))
	r.Get("/{pool_group_id}", newRatioGetHandler(d))
	r.Put("/{pool_group_id}", newRatioUpsertHandler(d))
	r.Delete("/{pool_group_id}", newRatioDeleteHandler(d))
}

func newRatioListHandler(d AdminPricingRatioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, page, ok := resolveRatioPage(w, r, d)
		if !ok {
			return
		}
		_ = ident
		rows, err := d.Store.ListRatios(r.Context(), page.TenantID)
		if err != nil {
			writeRatioStoreError(w, "pricing_ratio_list_failed", err)
			return
		}
		rows = paginateRatios(rows, page)
		items := make([]ratioResponseBody, 0, len(rows))
		for _, row := range rows {
			items = append(items, ratioResponse(row))
		}
		writeJSON(w, http.StatusOK, ratioListResponseBody{
			Object: "pricing_ratio_list",
			Items:  items,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func newRatioGetHandler(d AdminPricingRatioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, page, ok := resolveRatioPage(w, r, d)
		if !ok {
			return
		}
		poolGroupID, ok := parsePoolGroupID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.GetRatio(r.Context(), page.TenantID, poolGroupID)
		if err != nil {
			writeRatioStoreError(w, "pricing_ratio_get_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, ratioResponse(row))
	}
}

func newRatioUpsertHandler(d AdminPricingRatioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, page, ok := resolveRatioPage(w, r, d)
		if !ok {
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		poolGroupID, ok := parsePoolGroupID(w, r)
		if !ok {
			return
		}
		body, ok := parseRatioBody(w, r)
		if !ok {
			return
		}
		actor := fmt.Sprintf("admin_token:%d", ident.TokenID)
		row, err := d.Store.UpsertRatio(r.Context(), pricingcatalog.UpsertRatioParams{
			TenantID:    page.TenantID,
			PoolGroupID: poolGroupID,
			Ratio:       body.ratio,
			PublicRatio: body.publicRatio,
			Actor:       actor,
			ActorRole:   ident.Role,
		})
		if err != nil {
			writeRatioStoreError(w, "pricing_ratio_upsert_failed", err)
			return
		}
		invalidateRatioResolver(d, page.TenantID, poolGroupID)
		writeJSON(w, http.StatusOK, ratioResponse(row))
	}
}

func newRatioDeleteHandler(d AdminPricingRatioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, page, ok := resolveRatioPage(w, r, d)
		if !ok {
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		poolGroupID, ok := parsePoolGroupID(w, r)
		if !ok {
			return
		}
		actor := fmt.Sprintf("admin_token:%d", ident.TokenID)
		if err := d.Store.DeleteRatio(r.Context(), pricingcatalog.DeleteRatioParams{
			TenantID:    page.TenantID,
			PoolGroupID: poolGroupID,
			Actor:       actor,
			ActorRole:   ident.Role,
		}); err != nil {
			writeRatioStoreError(w, "pricing_ratio_delete_failed", err)
			return
		}
		invalidateRatioResolver(d, page.TenantID, poolGroupID)
		writeJSON(w, http.StatusOK, map[string]any{
			"object":        "pricing_ratio_deleted",
			"tenant_id":     page.TenantID,
			"pool_group_id": poolGroupID,
		})
	}
}

type parsedRatioBody struct {
	ratio       decimal.Decimal
	publicRatio bool
}

func parseRatioBody(w http.ResponseWriter, r *http.Request) (parsedRatioBody, bool) {
	var body ratioRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return parsedRatioBody{}, false
	}
	ratio, err := decimal.NewFromString(strings.TrimSpace(body.Ratio))
	if err != nil || !ratio.IsPositive() {
		writeError(w, http.StatusBadRequest, "invalid_ratio", "ratio must be a positive decimal")
		return parsedRatioBody{}, false
	}
	maxRatio := configuredPricingRatioMax()
	if ratio.LessThan(pricingRatioMin) || ratio.GreaterThan(maxRatio) {
		writeError(w, http.StatusUnprocessableEntity, "ratio_out_of_range", fmt.Sprintf("ratio must be between %s and %s", pricingRatioMin, maxRatio))
		return parsedRatioBody{}, false
	}
	return parsedRatioBody{ratio: ratio, publicRatio: body.PublicRatio}, true
}

func configuredPricingRatioMax() decimal.Decimal {
	raw := strings.TrimSpace(os.Getenv(pricingRatioMaxEnv))
	if raw == "" {
		return defaultPricingRatioMax
	}
	maxRatio, err := decimal.NewFromString(raw)
	if err != nil || maxRatio.LessThan(pricingRatioMin) {
		return defaultPricingRatioMax
	}
	return maxRatio
}

func invalidateRatioResolver(d AdminPricingRatioDeps, tenantID, poolGroupID int64) {
	if d.Resolver == nil {
		return
	}
	// ratio 写成功后必须精确失效, 否则 billing 热路径会在 TTL 内继续使用旧倍率。
	d.Resolver.Invalidate(tenantID, poolGroupID)
}

func parsePoolGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "pool_group_id")), 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_pool_group_id", "pool_group_id must be a positive int64")
		return 0, false
	}
	return value, true
}

func resolveRatioPage(w http.ResponseWriter, r *http.Request, d AdminPricingRatioDeps) (admin.AdminIdentity, catalogPage, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "pricing ratio dependency unset")
		return admin.AdminIdentity{}, catalogPage{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, catalogPage{}, false
	}
	page, ok := parseCatalogPage(w, r, ident)
	return ident, page, ok
}

func ratioResponse(row pricingcatalog.GroupPricingRatio) ratioResponseBody {
	out := ratioResponseBody{
		Object:      "pricing_ratio",
		ID:          row.ID,
		TenantID:    row.TenantID,
		PoolGroupID: row.PoolGroupID,
		PublicRatio: row.PublicRatio,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   formatTime(row.CreatedAt),
		UpdatedAt:   formatTime(row.UpdatedAt),
	}
	if row.PublicRatio {
		out.Ratio = row.RatioString()
	}
	return out
}

func paginateRatios(rows []pricingcatalog.GroupPricingRatio, page catalogPage) []pricingcatalog.GroupPricingRatio {
	if int(page.Offset) >= len(rows) {
		return nil
	}
	end := int(page.Offset + page.Limit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[page.Offset:end]
}

func writeRatioStoreError(w http.ResponseWriter, fallback string, err error) {
	switch {
	case errors.Is(err, pricingcatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "pricing_ratio_not_found", "pricing ratio not found")
	case errors.Is(err, pricingcatalog.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "pricing_ratio_invalid", "pricing ratio input invalid")
	default:
		writeError(w, http.StatusServiceUnavailable, fallback, "pricing ratio backend unavailable")
	}
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}
