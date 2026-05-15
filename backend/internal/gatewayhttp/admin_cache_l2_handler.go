package gatewayhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

type AdminL2CacheAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminL2CacheDeps struct {
	Auth  AdminL2CacheAuth
	Store l2cache.Store
}

type adminL2StatsResponse struct {
	Enabled      bool                                  `json:"enabled"`
	SizeBytes    int64                                 `json:"size_bytes"`
	MaxSizeBytes int64                                 `json:"max_size_bytes"`
	TTLSeconds   int64                                 `json:"ttl_seconds"`
	Entries      []l2cache.EntryStats                  `json:"entries"`
	Metrics      map[string]cachemetrics.L2SnapshotRow `json:"metrics"`
}

func MountAdminL2CacheRoutes(r chi.Router, d AdminL2CacheDeps) {
	r.Get("/stats", newAdminL2StatsHandler(d))
	r.Delete("/{key}", newAdminL2DeleteHandler(d))
}

func newAdminL2StatsHandler(d AdminL2CacheDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminL2(w, r, d)
		if !ok {
			return
		}
		stats := l2cache.Stats{}
		if d.Store != nil {
			stats = d.Store.Stats(r.Context())
			stats.Entries = filterL2EntriesForAdmin(stats.Entries, ident)
		}
		metrics := cachemetrics.SnapshotL2()
		if ident.Role != admin.RolePlatformAdmin {
			// 现有指标没有 tenant label，租户操作员只看条目元数据，避免跨租户计数泄露。
			metrics = map[string]cachemetrics.L2SnapshotRow{}
		}
		writeAuditJSON(w, http.StatusOK, adminL2StatsResponse{
			Enabled:      stats.Enabled,
			SizeBytes:    stats.SizeBytes,
			MaxSizeBytes: stats.MaxSizeBytes,
			TTLSeconds:   stats.TTLSeconds,
			Entries:      stats.Entries,
			Metrics:      metrics,
		})
	}
}

func newAdminL2DeleteHandler(d AdminL2CacheDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminL2(w, r, d)
		if !ok {
			return
		}
		if d.Store == nil {
			writeJSONError(w, http.StatusNotFound, "cache_l2_not_found", "L2 cache is disabled")
			return
		}
		key := chi.URLParam(r, "key")
		entry, found := d.Store.Get(r.Context(), key)
		if !found {
			writeJSONError(w, http.StatusNotFound, "cache_l2_not_found", "cache entry not found")
			return
		}
		if !adminCanAccessTenant(ident, entry.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		deleted := d.Store.Delete(r.Context(), key)
		syncL2SizeMetrics(d.Store)
		writeAuditJSON(w, http.StatusOK, map[string]any{"key": key, "deleted": deleted})
	}
}

func resolveAdminL2(w http.ResponseWriter, r *http.Request, d AdminL2CacheDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin L2 cache dependency unset")
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
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func filterL2EntriesForAdmin(entries []l2cache.EntryStats, ident admin.AdminIdentity) []l2cache.EntryStats {
	if ident.Role == admin.RolePlatformAdmin {
		return entries
	}
	out := entries[:0]
	for _, entry := range entries {
		if adminCanAccessTenant(ident, entry.TenantID) {
			out = append(out, entry)
		}
	}
	return out
}

func adminCanAccessTenant(ident admin.AdminIdentity, tenantID int64) bool {
	if ident.Role == admin.RolePlatformAdmin {
		return true
	}
	return ident.Role == admin.RoleTenantOperator && ident.ScopeTenantID > 0 && ident.ScopeTenantID == tenantID
}

func syncL2SizeMetrics(store l2cache.Store) {
	if store == nil {
		cachemetrics.SyncL2SizeBytes(nil)
		return
	}
	stats := store.Stats(context.Background())
	samples := make([]cachemetrics.L2SizeSample, 0, len(stats.ByLabel))
	for _, row := range stats.ByLabel {
		samples = append(samples, cachemetrics.L2SizeSample{
			Vendor:    row.Vendor,
			Model:     row.Model,
			SizeBytes: row.SizeBytes,
		})
	}
	cachemetrics.SyncL2SizeBytes(samples)
}
