// HUAKAI · iKun

// Package controlhttp — 模型目录租户策略 admin 写面。inherit_global_catalog 此前只读(resolve 回落 +
// /v1/models discovery live JOIN 消费)却无 admin 写路径(inert gap)。platform_admin only(经 adminGate):
// 租户自行授予全局目录继承是提权风险, 该决策属平台, 与 sub2api 的 group 目录策略 platform-admin-only 先例一致。
package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// AdminTenantPolicyDeps 接线模型目录租户策略 admin 面。
type AdminTenantPolicyDeps struct {
	Store adminTenantPolicyStore
}

type adminTenantPolicyStore interface {
	GetTenantPolicy(context.Context, int64) (registry.TenantPolicy, error)
	SetTenantInheritGlobal(ctx context.Context, tenantID int64, inherit bool, actor string) (registry.TenantPolicy, error)
}

// tenantPolicySetRequest 是 PUT /v1/admin/model-registry-policy 的请求体。inherit_global_catalog 用 *bool 强制
// 显式存在: 省略会按 Go 零值 false 静默把已继承的租户改成不继承(read-omit-write footgun), 故 nil→400。
type tenantPolicySetRequest struct {
	InheritGlobalCatalog *bool `json:"inherit_global_catalog"`
}

type tenantPolicyView struct {
	TenantID             int64  `json:"tenant_id"`
	InheritGlobalCatalog bool   `json:"inherit_global_catalog"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	UpdatedByActor       string `json:"updated_by_actor,omitempty"`
}

func tenantPolicyToView(p registry.TenantPolicy) tenantPolicyView {
	v := tenantPolicyView{
		TenantID:             p.TenantID,
		InheritGlobalCatalog: p.InheritGlobalCatalog,
		UpdatedByActor:       p.UpdatedByActor,
	}
	if !p.UpdatedAt.IsZero() {
		v.UpdatedAt = p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

// NewAdminTenantPolicyGetHandler 处理 GET /v1/admin/model-registry-policy?tenant_id: 读一个租户的目录继承策略。
// 无策略行 → store 返回默认 inherit=false(对齐 resolve 语义), 故总能读到当前生效值。
func NewAdminTenantPolicyGetHandler(d AdminTenantPolicyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tenant policy dependency unset")
			return
		}
		tenantID, ok := routeAdminParsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		policy, err := d.Store.GetTenantPolicy(r.Context(), tenantID)
		if err != nil {
			writeTenantPolicyError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, map[string]any{"policy": tenantPolicyToView(policy)})
	}
}

// NewAdminTenantPolicySetHandler 处理 PUT /v1/admin/model-registry-policy?tenant_id: 翻转一个租户的
// inherit_global_catalog(并在同 Tx bump 该租户快照版本)。tenant 取自 query(目标租户); actor 取自已认证身份
// (adminGate 注入 context), 非请求体 —— 未注入时置空不回退信任 body。
func NewAdminTenantPolicySetHandler(d AdminTenantPolicyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tenant policy dependency unset")
			return
		}
		tenantID, ok := routeAdminParsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		var req tenantPolicySetRequest
		if err := dec.Decode(&req); err != nil {
			modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if req.InheritGlobalCatalog == nil {
			modelWriteError(w, http.StatusBadRequest, "invalid_tenant_policy", "inherit_global_catalog field is required")
			return
		}
		actor := ""
		if ident, ok := admin.IdentityFromContext(r.Context()); ok {
			actor = fmt.Sprintf("admin-token:%d", ident.TokenID)
		}
		policy, err := d.Store.SetTenantInheritGlobal(r.Context(), tenantID, *req.InheritGlobalCatalog, actor)
		if err != nil {
			writeTenantPolicyError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, map[string]any{"policy": tenantPolicyToView(policy)})
	}
}

func writeTenantPolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrUnknownTenant):
		modelWriteError(w, http.StatusNotFound, "tenant_not_found", "tenant not found")
	default:
		modelWriteError(w, http.StatusServiceUnavailable, "model_admin_store_failed", "model registry backend unavailable")
	}
}
