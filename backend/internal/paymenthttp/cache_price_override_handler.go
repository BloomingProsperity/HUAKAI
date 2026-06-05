package paymenthttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// CacheOverrideStore 是缓存价覆盖 admin 端点依赖的能力子集
// (由 *billing.CacheOverrideStore 实现)。
type CacheOverrideStore interface {
	List() []billing.CacheOverrideRecord
	Set(actorID string, key billing.CacheOverrideKey, multiplier decimal.Decimal) (billing.CacheOverrideRecord, error)
	Delete(actorID string, key billing.CacheOverrideKey) error
}

// CacheOverrideAdminDeps 缓存价覆盖 admin 路由依赖。
type CacheOverrideAdminDeps struct {
	Auth  AdminAuth
	Store CacheOverrideStore
}

type cacheOverrideSetRequest struct {
	Multiplier string `json:"multiplier"`
}

type cacheOverrideView struct {
	Scope      string `json:"scope"`
	Model      string `json:"model,omitempty"`
	TenantID   int64  `json:"tenant_id,omitempty"`
	Multiplier string `json:"multiplier"`
	UpdatedAt  string `json:"updated_at"`
}

// MountCacheOverrideAdminRoutes 挂载缓存价覆盖 admin 端点:
//
//	GET    /            列出当前所有覆盖(未列出的 scope = 官方价)
//	PUT    /{scope}     设置某 scope 倍率(global / model / tenant)
//	DELETE /{scope}     清除某 scope 倍率(回到官方价)
//
// model scope 需 query ?model=<name>;tenant scope 需 query ?tenant_id=<id>。
func MountCacheOverrideAdminRoutes(r chi.Router, d CacheOverrideAdminDeps) {
	r.Get("/", newCacheOverrideListHandler(d))
	r.Put("/{scope}", newCacheOverrideSetHandler(d))
	r.Delete("/{scope}", newCacheOverrideDeleteHandler(d))
}

func newCacheOverrideListHandler(d CacheOverrideAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveCacheOverrideAdmin(w, r, d); !ok {
			return
		}
		records := d.Store.List()
		views := make([]cacheOverrideView, 0, len(records))
		for _, rec := range records {
			views = append(views, cacheOverrideRecordView(rec))
		}
		writeJSON(w, http.StatusOK, map[string]any{"overrides": views})
	}
}

func newCacheOverrideSetHandler(d CacheOverrideAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCacheOverrideAdmin(w, r, d)
		if !ok {
			return
		}
		key, ok := parseCacheOverrideKey(w, r)
		if !ok {
			return
		}
		var req cacheOverrideSetRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		mult, err := decimal.NewFromString(strings.TrimSpace(req.Multiplier))
		if err != nil || !mult.IsPositive() {
			writeJSONError(w, http.StatusBadRequest, "invalid_multiplier", "multiplier must be a positive decimal string")
			return
		}
		rec, err := d.Store.Set(cacheOverrideActorID(ident), key, mult)
		if err != nil {
			writeCacheOverrideError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"override": cacheOverrideRecordView(rec)})
	}
}

func newCacheOverrideDeleteHandler(d CacheOverrideAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCacheOverrideAdmin(w, r, d)
		if !ok {
			return
		}
		key, ok := parseCacheOverrideKey(w, r)
		if !ok {
			return
		}
		if err := d.Store.Delete(cacheOverrideActorID(ident), key); err != nil {
			writeCacheOverrideError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

func resolveCacheOverrideAdmin(w http.ResponseWriter, r *http.Request, d CacheOverrideAdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "cache override admin dependency unset")
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

func parseCacheOverrideKey(w http.ResponseWriter, r *http.Request) (billing.CacheOverrideKey, bool) {
	scope := billing.CacheOverrideScope(strings.TrimSpace(chi.URLParam(r, "scope")))
	switch scope {
	case billing.CacheOverrideScopeGlobal:
		return billing.CacheOverrideKey{Scope: scope}, true
	case billing.CacheOverrideScopeModel:
		model := strings.TrimSpace(r.URL.Query().Get("model"))
		if model == "" {
			writeJSONError(w, http.StatusBadRequest, "model_required", "model scope requires a non-empty ?model= query parameter")
			return billing.CacheOverrideKey{}, false
		}
		return billing.CacheOverrideKey{Scope: scope, Model: model}, true
	case billing.CacheOverrideScopeTenant:
		raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
		tenantID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || tenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant scope requires a positive ?tenant_id= query parameter")
			return billing.CacheOverrideKey{}, false
		}
		return billing.CacheOverrideKey{Scope: scope, TenantID: tenantID}, true
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_scope", "scope must be one of global / model / tenant")
		return billing.CacheOverrideKey{}, false
	}
}

func cacheOverrideActorID(ident admin.AdminIdentity) string {
	return "admin:" + strconv.FormatInt(ident.TokenID, 10)
}

func cacheOverrideRecordView(rec billing.CacheOverrideRecord) cacheOverrideView {
	return cacheOverrideView{
		Scope:      string(rec.Key.Scope),
		Model:      rec.Key.Model,
		TenantID:   rec.Key.TenantID,
		Multiplier: rec.Multiplier.String(),
		UpdatedAt:  rec.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func writeCacheOverrideError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrCacheOverrideInvalid):
		writeJSONError(w, http.StatusBadRequest, "invalid_cache_override", "cache override request is invalid")
	case errors.Is(err, billing.ErrCacheOverrideNotFound):
		writeJSONError(w, http.StatusNotFound, "cache_override_not_found", "no cache override set for this scope")
	case errors.Is(err, billing.ErrCacheOverrideSignerMissing):
		writeJSONError(w, http.StatusServiceUnavailable, "cache_override_unavailable", "cache override audit signer unavailable")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "cache_override_backend_error", "cache override backend transient failure")
	}
}
