package modelratehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/modelrate"
)

type Store interface {
	List(context.Context) ([]modelrate.Override, error)
	Get(context.Context, string, string) (modelrate.Override, error)
	Upsert(context.Context, modelrate.UpsertParams) (modelrate.Override, error)
	Delete(context.Context, modelrate.DeleteParams) error
	VerifyChain(context.Context) (modelrate.VerifyResult, error)
}

type OfficialTable interface {
	GetOfficialRateTable(context.Context, string) (billing.RateTable, error)
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Deps struct {
	Auth                 adminAuth
	Store                Store
	Official             OfficialTable
	BillingPolicyVersion string
}

type upsertRequest struct {
	InputUSDPerMillion           *string `json:"input_usd_per_million"`
	OutputUSDPerMillion          *string `json:"output_usd_per_million"`
	CacheReadUSDPerMillion       *string `json:"cache_read_usd_per_million"`
	CacheCreationUSDPerMillion   *string `json:"cache_creation_usd_per_million"`
	CacheCreation5mUSDPerMillion *string `json:"cache_creation_5m_usd_per_million"`
	CacheCreation1hUSDPerMillion *string `json:"cache_creation_1h_usd_per_million"`
}

type rateView struct {
	Input           string `json:"input_usd_per_million,omitempty"`
	Output          string `json:"output_usd_per_million,omitempty"`
	CacheRead       string `json:"cache_read_usd_per_million,omitempty"`
	CacheCreation   string `json:"cache_creation_usd_per_million,omitempty"`
	CacheCreation5m string `json:"cache_creation_5m_usd_per_million,omitempty"`
	CacheCreation1h string `json:"cache_creation_1h_usd_per_million,omitempty"`
}

type catalogItemView struct {
	Object     string   `json:"object"`
	Vendor     string   `json:"vendor"`
	Model      string   `json:"model"`
	Currency   string   `json:"currency"`
	Unit       string   `json:"unit"`
	Official   rateView `json:"official"`
	Effective  rateView `json:"effective"`
	Overridden bool     `json:"overridden"`
	Source     string   `json:"source"`
}

type catalogListView struct {
	Object   string            `json:"object"`
	Currency string            `json:"currency"`
	Unit     string            `json:"unit"`
	Items    []catalogItemView `json:"items"`
	Limit    int32             `json:"limit"`
	Offset   int32             `json:"offset"`
}

func MountRoutes(r chi.Router, d Deps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/audit/verify", newAuditVerifyHandler(d))
	r.Get("/", newListHandler(d))
	r.Get("/{vendor}/{model}", newGetHandler(d))
	r.With(safe).Put("/{vendor}/{model}", newUpsertHandler(d))
	r.With(safe).Delete("/{vendor}/{model}", newDeleteHandler(d))
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveReader(w, r, d); !ok {
			return
		}
		limit, offset, ok := parseLimitOffset(w, r)
		if !ok {
			return
		}
		items, ok := loadCatalog(w, r, d, r.URL.Query().Get("vendor"))
		if !ok {
			return
		}
		page := paginate(items, limit, offset)
		views := make([]catalogItemView, 0, len(page))
		for _, item := range page {
			views = append(views, catalogView(item))
		}
		writeJSON(w, http.StatusOK, catalogListView{
			Object:   "model_rate_catalog",
			Currency: "USD",
			Unit:     "usd_per_million_tokens",
			Items:    views,
			Limit:    limit,
			Offset:   offset,
		})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveReader(w, r, d); !ok {
			return
		}
		vendor, model, ok := parseVendorModel(w, r)
		if !ok {
			return
		}
		items, ok := loadCatalog(w, r, d, vendor)
		if !ok {
			return
		}
		item, found := modelrate.FindCatalogItem(items, vendor, model)
		if !found {
			writeError(w, http.StatusNotFound, "model_rate_not_found", "no official price or override for this vendor/model")
			return
		}
		writeJSON(w, http.StatusOK, catalogView(item))
	}
}

func newUpsertHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveWriter(w, r, d)
		if !ok {
			return
		}
		vendor, model, ok := parseVendorModel(w, r)
		if !ok {
			return
		}
		rates, ok := parseUpsertBody(w, r)
		if !ok {
			return
		}
		params := modelrate.UpsertParams{
			Vendor:    vendor,
			Model:     model,
			Rates:     rates,
			Actor:     ident.AuditActor(),
			ActorRole: ident.Role,
		}
		if err := modelrate.ValidateUpsert(params); err != nil {
			writeStoreError(w, "model_rate_upsert_failed", err)
			return
		}
		row, err := d.Store.Upsert(r.Context(), params)
		if err != nil {
			writeStoreError(w, "model_rate_upsert_failed", err)
			return
		}
		items, ok := loadCatalog(w, r, d, vendor)
		if !ok {
			return
		}
		item, found := modelrate.FindCatalogItem(items, row.Vendor, row.Model)
		if !found {
			item = modelrate.CatalogItem{
				Vendor:     row.Vendor,
				Model:      row.Model,
				Effective:  row.Rates,
				Overridden: true,
				Source:     modelrate.SourceOverride,
			}
		}
		writeJSON(w, http.StatusOK, catalogView(item))
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveWriter(w, r, d)
		if !ok {
			return
		}
		vendor, model, ok := parseVendorModel(w, r)
		if !ok {
			return
		}
		if err := d.Store.Delete(r.Context(), modelrate.DeleteParams{
			Vendor:    vendor,
			Model:     model,
			Actor:     ident.AuditActor(),
			ActorRole: ident.Role,
		}); err != nil {
			writeStoreError(w, "model_rate_delete_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "model_rate_override_deleted",
			"vendor": vendor,
			"model":  model,
		})
	}
}

func newAuditVerifyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveReader(w, r, d)
		if !ok {
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		result, err := d.Store.VerifyChain(r.Context())
		if err != nil {
			writeStoreError(w, "model_rate_audit_verify_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "model_rate_audit_verification",
			"ok":     result.OK,
			"row_id": result.RowID,
			"reason": result.Reason,
		})
	}
}

func resolveReader(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil || d.Official == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model rate dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, false
	}
	switch ident.Role {
	case admin.RolePlatformAdmin, admin.RoleTenantOperator:
		return ident, true
	default:
		writeAdminError(w, admin.ErrAdminForbidden)
		return admin.AdminIdentity{}, false
	}
}

func resolveWriter(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	ident, ok := resolveReader(w, r, d)
	if !ok {
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeAdminError(w, admin.ErrAdminForbidden)
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func parseVendorModel(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	vendor := strings.TrimSpace(chi.URLParam(r, "vendor"))
	model := strings.TrimSpace(chi.URLParam(r, "model"))
	if vendor == "" || model == "" {
		writeError(w, http.StatusBadRequest, "invalid_vendor_model", "vendor and model are required")
		return "", "", false
	}
	return vendor, model, true
}

func parseUpsertBody(w http.ResponseWriter, r *http.Request) (modelrate.RateBuckets, bool) {
	var body upsertRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be JSON")
		return modelrate.RateBuckets{}, false
	}
	rates := modelrate.RateBuckets{}
	var ok bool
	if rates.Input, ok = parseOptionalUSD(w, body.InputUSDPerMillion, "input_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if rates.Output, ok = parseOptionalUSD(w, body.OutputUSDPerMillion, "output_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if rates.CacheRead, ok = parseOptionalUSD(w, body.CacheReadUSDPerMillion, "cache_read_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if rates.CacheCreation, ok = parseOptionalUSD(w, body.CacheCreationUSDPerMillion, "cache_creation_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if rates.CacheCreation5m, ok = parseOptionalUSD(w, body.CacheCreation5mUSDPerMillion, "cache_creation_5m_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if rates.CacheCreation1h, ok = parseOptionalUSD(w, body.CacheCreation1hUSDPerMillion, "cache_creation_1h_usd_per_million"); !ok {
		return modelrate.RateBuckets{}, false
	}
	if !rates.HasAny() {
		writeError(w, http.StatusBadRequest, "invalid_model_rate", "at least one usd_per_million field is required")
		return modelrate.RateBuckets{}, false
	}
	return rates, true
}

func parseOptionalUSD(w http.ResponseWriter, raw *string, field string) (*decimal.Decimal, bool) {
	if raw == nil {
		return nil, true
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		return nil, true
	}
	value, err := decimal.NewFromString(text)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model_rate", field+" must be a decimal string")
		return nil, false
	}
	if value.IsNegative() {
		writeError(w, http.StatusBadRequest, "invalid_model_rate", field+" must not be negative")
		return nil, false
	}
	maxRate := decimal.RequireFromString(modelrate.MaxUSDPerMillion)
	if value.GreaterThan(maxRate) {
		writeError(w, http.StatusUnprocessableEntity, "model_rate_out_of_range", field+" exceeds "+modelrate.MaxUSDPerMillion)
		return nil, false
	}
	return &value, true
}

func loadCatalog(w http.ResponseWriter, r *http.Request, d Deps, vendor string) ([]modelrate.CatalogItem, bool) {
	version := strings.TrimSpace(d.BillingPolicyVersion)
	if version == "" {
		writeError(w, http.StatusServiceUnavailable, "billing_policy_version_empty", "billing policy version empty")
		return nil, false
	}
	table, err := d.Official.GetOfficialRateTable(r.Context(), version)
	if err != nil {
		if errors.Is(err, billing.ErrRateTableNotFound) {
			writeError(w, http.StatusServiceUnavailable, "rate_table_not_found", "official rate table not found")
			return nil, false
		}
		writeError(w, http.StatusServiceUnavailable, "rate_table_read_failed", "official rate table read failed")
		return nil, false
	}
	overrides, err := d.Store.List(r.Context())
	if err != nil {
		writeStoreError(w, "model_rate_list_failed", err)
		return nil, false
	}
	items, err := modelrate.BuildCatalog(table.PricingData, overrides, vendor)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "model_rate_catalog_invalid", "official catalog could not be parsed")
		return nil, false
	}
	return items, true
}

func catalogView(item modelrate.CatalogItem) catalogItemView {
	return catalogItemView{
		Object:     "model_rate",
		Vendor:     item.Vendor,
		Model:      item.Model,
		Currency:   "USD",
		Unit:       "usd_per_million_tokens",
		Official:   rateViewFromBuckets(item.Official),
		Effective:  rateViewFromBuckets(item.Effective),
		Overridden: item.Overridden,
		Source:     item.Source,
	}
}

func rateViewFromBuckets(b modelrate.RateBuckets) rateView {
	return rateView{
		Input:           decimalString(b.Input),
		Output:          decimalString(b.Output),
		CacheRead:       decimalString(b.CacheRead),
		CacheCreation:   decimalString(b.CacheCreation),
		CacheCreation5m: decimalString(b.CacheCreation5m),
		CacheCreation1h: decimalString(b.CacheCreation1h),
	}
}

func decimalString(value *decimal.Decimal) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func writeStoreError(w http.ResponseWriter, fallback string, err error) {
	switch {
	case errors.Is(err, modelrate.ErrNotFound):
		writeError(w, http.StatusNotFound, "model_rate_not_found", "model rate override not found")
	case errors.Is(err, modelrate.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_model_rate", "model rate input invalid")
	case errors.Is(err, modelrate.ErrOutOfRange):
		writeError(w, http.StatusUnprocessableEntity, "model_rate_out_of_range", "model rate exceeds allowed range")
	case errors.Is(err, modelrate.ErrAuditSignerMissing):
		writeError(w, http.StatusServiceUnavailable, "model_rate_unavailable", "model rate audit signer unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, fallback, "model rate backend unavailable")
	}
}
