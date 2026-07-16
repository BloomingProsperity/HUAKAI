// Package orphanreconcilehttp 暴露 media_task_orphans(媒体任务孤儿)对账闭环的 admin 面:
// 只读列表可视化 + 管理员显式手动对账动作。孤儿 = 上游已创建任务但本平台因租约丢失未落库、
// 可能漏计费的真亏钱线索(producer 见 mediatask 包,持续入库)。
//
// 本包不堆 gatewayhttp 大包,独立成 admin http 子包。安全姿态(money,严格):
//   - Manual-First:绝无自动 / 定时扣费;追扣只在 admin 显式 POST 且 back_charge=true 时
//     由本包 handler 同步触发,无任何 worker / cron 引用。
//   - 幂等防双扣:同一孤儿重复对账 / 追扣不双扣(reconcile_status 状态门 + billing.Capture
//     的 hold.State 门双闸,见 mediatask.ReconcileOrphan)。
//   - 复用既有 settle:追扣走既有 billing.Capture,不新写任何扣费 / ledger 逻辑。
//   - RBAC:复用既有 admin 鉴权(platform_admin 跨租户;tenant_operator 限自己租户)。
//   - 审计:每次对账 / 追扣在同一事务内写一行 admin_audit_events。
package orphanreconcilehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const (
	defaultListLimit = 100
	maxListLimit     = 1000

	auditTargetType = "media_task_orphan"
)

// adminAuth 解析入站 admin 身份(生产由 *admin.AdminResolver 实现)。
type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// orphanStore 是本包依赖的对账存储能力(生产由 *mediatask.PostgresStore 实现)。
// 只读 + 单一动钱入口 ReconcileOrphan(Manual-First,详见 mediatask 包)。
type orphanStore interface {
	ListPendingOrphans(ctx context.Context, tenantID int64, limit int) ([]mediatask.OrphanRecord, error)
	ReconcileOrphan(ctx context.Context, orphanID int64, status string, backCharge bool, now time.Time, audit mediatask.OrphanReconcileAuditHook) (mediatask.OrphanReconcileResult, bool, error)
}

// Deps 是孤儿对账 admin 面的依赖集。
type Deps struct {
	Auth  adminAuth
	Store orphanStore
}

// MountRoutes 注册孤儿对账 admin 子树:
//
//	GET  /admin/v1/media-task-orphans            列出 pending 孤儿(走 idx_media_task_orphans_pending)
//	POST /admin/v1/media-task-orphans/{id}/reconcile  显式对账一个孤儿(默认仅标记,back_charge 才追扣)
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Post("/{id}/reconcile", newReconcileHandler(d))
}

// NewListHandler / NewReconcileHandler 让 gateway 可内联挂载到规范无尾斜杠路径
// (与 adminquotahttp 同款),避免 chi.Route 子树导致路径走样。
func NewListHandler(d Deps) http.HandlerFunc      { return newListHandler(d) }
func NewReconcileHandler(d Deps) http.HandlerFunc { return newReconcileHandler(d) }

type orphanItem struct {
	ID              int64  `json:"id"`
	TaskID          int64  `json:"task_id"`
	TenantID        int64  `json:"tenant_id"`
	UserID          int64  `json:"user_id"`
	Provider        string `json:"provider"`
	ProviderTaskID  string `json:"provider_task_id"`
	EstimatedCents  int64  `json:"estimated_cents"`
	ReconcileStatus string `json:"reconcile_status"`
	ObservedAt      string `json:"observed_at"`
}

type listResponse struct {
	Items []orphanItem `json:"items"`
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveListScope(w, r, d)
		if !ok {
			return
		}
		limit := parseLimit(w, r)
		if limit < 0 {
			return
		}
		recs, err := d.Store.ListPendingOrphans(r.Context(), tenantID, limit)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "orphan_backend_error", "orphan reconcile backend unavailable")
			return
		}
		_ = ident
		items := make([]orphanItem, 0, len(recs))
		for _, rec := range recs {
			items = append(items, toItem(rec))
		}
		writeJSON(w, http.StatusOK, listResponse{Items: items})
	}
}

// toItem 把存储记录投影成对外 JSON。注意:OrphanRecord 不携带 estimated_cents(它在
// media_tasks 行上),列表先以 0 占位;追扣时由 store 在事务内按 estimated_cents 结算,
// 故金额真值以追扣返回为准,列表仅作发现/分诊视图。
func toItem(rec mediatask.OrphanRecord) orphanItem {
	return orphanItem{
		ID: rec.ID, TaskID: rec.TaskID, TenantID: rec.TenantID, UserID: rec.UserID,
		Provider: rec.Provider, ProviderTaskID: rec.ProviderTaskID,
		ReconcileStatus: rec.ReconcileStatus, ObservedAt: rec.ObservedAt.UTC().Format(timeRFC3339),
	}
}

// resolveListScope 鉴权并定 scope:平台全域身份默认全局扫(tenantID=0)，
// 其他身份缺省使用自身 scope；显式目标统一经 CanActOnTenant 裁决。
func resolveListScope(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "orphan_not_configured", "orphan reconcile dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.IsPlatformWide() {
		// 仅真正的平台全域身份可用 0 表示跨租户全局扫。
		return ident, 0, true
	}
	tenantID := ident.ScopeTenantID()
	if raw != "" {
		var err error
		tenantID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
			return admin.AdminIdentity{}, 0, false
		}
	}
	if err := ident.CanActOnTenant(tenantID); err != nil {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "caller cannot act on this tenant scope")
		return admin.AdminIdentity{}, 0, false
	}
	return ident, tenantID, true
}

func parseLimit(w http.ResponseWriter, r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultListLimit
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return -1
	}
	if v > maxListLimit {
		v = maxListLimit
	}
	return v
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_orphan_id", "orphan id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin credential is not allowed for this tenant")
	default:
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
