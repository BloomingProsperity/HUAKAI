// Package accountfphttp 暴露 provider account 的 TLS 指纹 profile 绑定/解绑端点
// (PATCH /admin/v1/provider-accounts/{id}/fingerprint-profile)。
//
// 它接线了此前的**死缺口**:tls_fingerprint_profiles 池有完整 CRUD、resolver 也读
// provider_accounts.tls_fingerprint_profile_id,但全后端**没有任何写该 FK 的入口**——
// 账号无法绑定指纹 profile,整个指纹子系统功能上不可用(存储✓校验✓但消费端永远拿不到绑定)。
//
// 独立成包(而非塞进已达体量预算上限的 god 包 gatewayhttp)以遵守 §13;鉴权/审计范式与
// gatewayhttp 的 provider-account admin 一致:tenant_operator 用自身 scope;platform_admin 须
// ?tenant_id 显式指定并过 CanIssueForTenant。跨租户/不存在 profile 由 DB 触发器(迁移 0038)+ FK 拒绝。
package accountfphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// AdminAuth 从请求派生平台/租户运营者身份。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Store 是本端点需要的最小写面:绑定 FK + 写审计。*admindb.Queries 满足之。
type Store interface {
	UpdateProviderAccountFingerprintProfile(context.Context, admindb.UpdateProviderAccountFingerprintProfileParams) error
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type Deps struct {
	Auth  AdminAuth
	Store Store
}

// MountRoutes 在 provider-accounts 路由组内挂 PATCH /{id}/fingerprint-profile。
// 由 cmd/gateway 在挂载 /admin/v1/provider-accounts 组时一并调用(组内多 mount 合法)。
func MountRoutes(r chi.Router, d Deps) {
	r.Patch("/{id}/fingerprint-profile", newHandler(d))
}

type setFingerprintRequest struct {
	TenantID  *int64 `json:"tenant_id,omitempty"`
	ProfileID *int64 `json:"profile_id"`
	Reason    string `json:"reason,omitempty"`
}

func newHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeErr(w, http.StatusServiceUnavailable, "gateway_not_configured", "fingerprint binding dependency unset")
			return
		}
		ident, tenantID, ok := resolveAdmin(w, r, d.Auth)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req setFingerprintRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		// body 显式 tenant_id 必须与鉴权 scope 一致(防越权改他租户账号)。
		if req.TenantID != nil && *req.TenantID != tenantID {
			writeErr(w, http.StatusForbidden, "tenant_mismatch", "tenant_id does not match admin scope")
			return
		}
		if req.ProfileID != nil && *req.ProfileID <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid_profile_id", "profile_id must be a positive integer or null to unbind")
			return
		}
		actorID := ident.AuditActor()
		if err := d.Store.UpdateProviderAccountFingerprintProfile(r.Context(), admindb.UpdateProviderAccountFingerprintProfileParams{
			ProfileID: req.ProfileID, ActorID: &actorID, ID: id, TenantID: tenantID,
		}); err != nil {
			// FK 违反(profile 不存在,23503)或触发器 RAISE(跨租户,P0001)→ 400;其它瞬时错误 → 503。
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "P0001") {
				writeErr(w, http.StatusBadRequest, "invalid_fingerprint_profile", "profile does not exist or does not belong to this tenant")
				return
			}
			writeErr(w, http.StatusServiceUnavailable, "provider_account_update_failed", "could not update fingerprint profile binding")
			return
		}
		// 复用既有审计 action update_provider_account(避免改 admin_audit_events CHECK 约束=schema 迁移);
		// 绑定/解绑差异落 payload + reason。
		reason := "绑定账号 TLS 指纹 profile"
		if req.ProfileID == nil {
			reason = "解绑账号 TLS 指纹 profile(回内置默认)"
		}
		if strings.TrimSpace(req.Reason) != "" {
			reason = req.Reason
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "op": "fingerprint_profile", "tls_fingerprint_profile_id": req.ProfileID})
		reqID := middleware.GetReqID(r.Context())
		if _, err := d.Store.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
			Action: "update_provider_account", TargetType: "provider_account", TargetID: &id,
			RequestID: &reqID, Reason: &reason, Payload: payload,
		}); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "audit_write_failed", "audit write failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "tls_fingerprint_profile_id": req.ProfileID})
	}
}

// resolveAdmin 复刻 provider-account admin 的租户范式:tenant_operator 自身 scope;
// platform_admin 须 ?tenant_id 显式指定 + CanIssueForTenant。鉴权失败一律拒(防 IDOR)。
func resolveAdmin(w http.ResponseWriter, r *http.Request, auth AdminAuth) (admin.AdminIdentity, int64, bool) {
	ident, err := auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeErr(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeErr(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeErr(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
		tid, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || tid <= 0 {
			writeErr(w, http.StatusBadRequest, "tenant_id_required", "platform_admin must pass a positive ?tenant_id")
			return admin.AdminIdentity{}, 0, false
		}
		if err := ident.CanIssueForTenant(tid); err != nil {
			writeErr(w, http.StatusForbidden, "admin_forbidden", "tenant scope not permitted")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, tid, true
	default:
		writeErr(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// 与同仓 decodeAdminPoolJSON 范式对齐:限制请求体 ≤64KiB,防超大 JSON 顶层值缓冲入内存的 DoS
	// (本端点请求体只需容纳 {tenant_id,profile_id,reason} 小对象,64KiB 充裕)。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
