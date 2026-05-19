package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	defaultAdminPoolsTenantID         = int64(1)
	defaultAdminPoolsLimit            = int32(50)
	maxAdminPoolsLimit                = int32(200)
	maxAdminPoolNameRunes             = 64
	defaultAdminPoolTopKDefault       = int32(1)
	defaultAdminPoolCapabilityDefault = "exact_capability_only"
	adminPoolCapabilitySafeEquivalent = "safe_equivalent_allowed"
)

type AdminPoolsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolsStore interface {
	InsertPool(context.Context, dbbilling.InsertPoolParams) (dbbilling.PoolGroup, error)
	GetPool(context.Context, dbbilling.GetPoolParams) (dbbilling.PoolGroup, error)
	ListPools(context.Context, dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error)
	UpdatePool(context.Context, dbbilling.UpdatePoolParams) (dbbilling.PoolGroup, error)
}

type AdminPoolsDeps struct {
	Auth  AdminPoolsAuth
	Store AdminPoolsStore
}

func NewAdminPoolsHandler(d AdminPoolsDeps) http.Handler {
	r := chi.NewRouter()
	r.Get("/", newListPoolsHandler(d))
	r.Post("/", newCreatePoolHandler(d))
	r.Get("/{id}", newGetPoolHandler(d))
	r.Patch("/{id}", newUpdatePoolHandler(d))
	return r
}

type adminPoolCreateRequest struct {
	Name              string  `json:"name"`
	TopKDefault       *int32  `json:"top_k_default,omitempty"`
	CapabilityDefault *string `json:"capability_default,omitempty"`
	AllowLastResort   *bool   `json:"allow_last_resort,omitempty"`
	// 兼容本轮 body contract；当前 pool_groups schema 尚无 description 列。
	Description string `json:"description,omitempty"`
}

type adminPoolUpdateRequest struct {
	Name              *string `json:"name,omitempty"`
	TopKDefault       *int32  `json:"top_k_default,omitempty"`
	CapabilityDefault *string `json:"capability_default,omitempty"`
	AllowLastResort   *bool   `json:"allow_last_resort,omitempty"`
	// 兼容请求字段；本 slice 不改 schema，因此不落库。
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

func newListPoolsHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		limit, ok := parseAdminPoolsLimit(w, r)
		if !ok {
			return
		}
		items, err := d.Store.ListPools(r.Context(), dbbilling.ListPoolsParams{TenantID: defaultAdminPoolsTenantID, LimitCount: limit})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pool_list_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func newCreatePoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		var req adminPoolCreateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if err := validateAdminPoolName(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_pool_name", err.Error())
			return
		}
		topK, capability, allowLastResort, ok := normalizeAdminPoolCreateDefaults(w, req)
		if !ok {
			return
		}
		pool, err := d.Store.InsertPool(r.Context(), dbbilling.InsertPoolParams{
			TenantID:          defaultAdminPoolsTenantID,
			Name:              req.Name,
			TopKDefault:       topK,
			CapabilityDefault: capability,
			AllowLastResort:   allowLastResort,
		})
		if err != nil {
			writeAdminPoolMutationError(w, err, "pool_create_failed")
			return
		}
		writeAuditJSON(w, http.StatusCreated, pool)
	}
}

func newGetPoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolGroupID(w, r)
		if !ok {
			return
		}
		pool, err := d.Store.GetPool(r.Context(), dbbilling.GetPoolParams{TenantID: defaultAdminPoolsTenantID, ID: id})
		if err != nil {
			writeAdminPoolReadError(w, err, "pool_get_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, pool)
	}
}

func newUpdatePoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolGroupID(w, r)
		if !ok {
			return
		}
		var req adminPoolUpdateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			if err := validateAdminPoolName(name); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_pool_name", err.Error())
				return
			}
			req.Name = &name
		}
		if req.TopKDefault != nil {
			if err := validateAdminPoolTopK(*req.TopKDefault); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_top_k_default", err.Error())
				return
			}
		}
		if req.CapabilityDefault != nil {
			capability := strings.TrimSpace(*req.CapabilityDefault)
			if err := validateAdminPoolCapabilityDefault(capability); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_capability_default", err.Error())
				return
			}
			req.CapabilityDefault = &capability
		}
		if req.Name == nil && req.TopKDefault == nil && req.CapabilityDefault == nil && req.AllowLastResort == nil && req.Enabled == nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", "at least one supported field is required")
			return
		}
		pool, err := d.Store.UpdatePool(r.Context(), dbbilling.UpdatePoolParams{
			Name:              req.Name,
			TopKDefault:       req.TopKDefault,
			CapabilityDefault: req.CapabilityDefault,
			AllowLastResort:   req.AllowLastResort,
			Enabled:           req.Enabled,
			TenantID:          defaultAdminPoolsTenantID,
			ID:                id,
		})
		if err != nil {
			writeAdminPoolMutationError(w, err, "pool_update_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, pool)
	}
}

func resolveAdminPoolOperator(w http.ResponseWriter, r *http.Request, d AdminPoolsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin pool dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func parseAdminPoolsLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultAdminPoolsLimit, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 || n > int64(maxAdminPoolsLimit) {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return 0, false
	}
	return int32(n), true
}

func parseAdminPoolGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_pool_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func validateAdminPoolName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > maxAdminPoolNameRunes {
		return fmt.Errorf("name must be 1-64 characters")
	}
	return nil
}

func normalizeAdminPoolCreateDefaults(w http.ResponseWriter, req adminPoolCreateRequest) (int32, string, bool, bool) {
	topK := defaultAdminPoolTopKDefault
	if req.TopKDefault != nil {
		if err := validateAdminPoolTopK(*req.TopKDefault); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_top_k_default", err.Error())
			return 0, "", false, false
		}
		topK = *req.TopKDefault
	}
	capability := defaultAdminPoolCapabilityDefault
	if req.CapabilityDefault != nil {
		capability = strings.TrimSpace(*req.CapabilityDefault)
		if err := validateAdminPoolCapabilityDefault(capability); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_capability_default", err.Error())
			return 0, "", false, false
		}
	}
	allowLastResort := false
	if req.AllowLastResort != nil {
		allowLastResort = *req.AllowLastResort
	}
	return topK, capability, allowLastResort, true
}

func validateAdminPoolTopK(topK int32) error {
	if topK < 1 || topK > 10 {
		return fmt.Errorf("top_k_default must be between 1 and 10")
	}
	return nil
}

func validateAdminPoolCapabilityDefault(capability string) error {
	switch capability {
	case defaultAdminPoolCapabilityDefault, adminPoolCapabilitySafeEquivalent:
		return nil
	default:
		return fmt.Errorf("capability_default must be exact_capability_only or safe_equivalent_allowed")
	}
}

func writeAdminPoolReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "pool_not_found", "pool not found")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, code, err.Error())
}

func writeAdminPoolMutationError(w http.ResponseWriter, err error, code string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		writeJSONError(w, http.StatusConflict, "pool_name_conflict", "pool name already exists")
		return
	}
	writeAdminPoolReadError(w, err, code)
}
