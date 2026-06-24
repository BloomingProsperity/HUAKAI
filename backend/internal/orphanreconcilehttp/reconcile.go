package orphanreconcilehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const maxReconcileBodyBytes = 1 << 16

// reconcileRequest 是 admin 显式对账动作的请求体。
//   - Status:目标终态(reconciled / cancelled / ignored)。默认 reconciled。
//   - BackCharge:是否追扣漏掉的费用。【默认 false=仅标记不扣钱】。追扣是 admin 的显式
//     二次确认,且仅当 Status=reconciled 时合法(cancelled/ignored 表示判定不追)。
//   - Reason:可选备注,落审计。
type reconcileRequest struct {
	Status     string `json:"status"`
	BackCharge bool   `json:"back_charge"`
	Reason     string `json:"reason"`
}

type reconcileResponse struct {
	OrphanID      int64  `json:"orphan_id"`
	Status        string `json:"status"`
	Advanced      bool   `json:"advanced"`
	BackCharged   bool   `json:"back_charged"`
	CapturedCents int64  `json:"captured_cents"`
	// BackChargeOutcome 仅追扣请求时回显:captured=真扣到;其余值=未扣到(孤儿保持 pending)。
	BackChargeOutcome string `json:"back_charge_outcome,omitempty"`
}

func newReconcileHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeError(w, http.StatusServiceUnavailable, "orphan_not_configured", "orphan reconcile dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// 任何对账动作都需 admin 角色;tenant_operator 还要进一步用 CanIssueForTenant
		// 校验该孤儿属其租户(在 store 拿到 orphan.TenantID 后,见下方 audit hook 前的检查)。
		if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
			return
		}

		orphanID, ok := pathID(w, r)
		if !ok {
			return
		}

		req := reconcileRequest{Status: "reconciled"}
		r.Body = http.MaxBytesReader(w, r.Body, maxReconcileBodyBytes)
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
				return
			}
		}
		if req.Status == "" {
			req.Status = "reconciled"
		}

		// 租户越权守卫:tenant_operator 只能对账自己租户的孤儿。把这层校验做进 audit hook,
		// 因为孤儿的 tenant_id 只有进事务读到行后才知道——在 hook 内拒绝即回滚,绝不动钱。
		reqID := middleware.GetReqID(r.Context())
		audit := buildAuditHook(ident, req, reqID)

		result, advanced, err := d.Store.ReconcileOrphan(
			r.Context(), orphanID, req.Status, req.BackCharge, nowUTC(), audit)
		if err != nil {
			writeReconcileError(w, err)
			return
		}
		// 追扣请求但未真正扣到钱(hold 已 released / 行归档 / 估算非正 / holdref 不可解析):
		// 孤儿保持 pending,返回 409 + outcome 明确告知,绝不静默 200 让 admin 误以为已追回。
		if req.BackCharge && !advanced && result.BackChargeOutcome != "" && result.BackChargeOutcome != "captured" {
			writeJSON(w, http.StatusConflict, reconcileResponse{
				OrphanID: orphanID, Status: "pending", Advanced: false,
				BackCharged: result.BackCharged, CapturedCents: result.CapturedCents,
				BackChargeOutcome: result.BackChargeOutcome,
			})
			return
		}
		writeJSON(w, http.StatusOK, reconcileResponse{
			OrphanID: orphanID, Status: req.Status, Advanced: advanced,
			BackCharged: result.BackCharged, CapturedCents: result.CapturedCents,
			BackChargeOutcome: result.BackChargeOutcome,
		})
	}
}

// errOrphanForbiddenTenant 是越权哨兵:tenant_operator 试图对账不属其租户的孤儿。
var errOrphanForbiddenTenant = errors.New("orphanreconcilehttp: orphan not in operator tenant scope")

// buildAuditHook 返回在对账事务内执行的回调:先做租户越权守卫(回滚动钱),再把一行
// admin_audit_events 与状态推进 + 追扣写在同一事务(原子)。审计行记录是否追扣 / 追扣额,
// 形成"孤儿可见→admin 处置→状态推进(+可选追扣)"的闭环留痕。
func buildAuditHook(ident admin.AdminIdentity, req reconcileRequest, reqID string) mediatask.OrphanReconcileAuditHook {
	return func(ctx context.Context, tx pgx.Tx, result mediatask.OrphanReconcileResult) error {
		if err := ident.CanIssueForTenant(result.TenantID); err != nil {
			// 越权:tenant_operator 对账他租户孤儿。回滚整笔(含任何追扣 Capture)。
			return errOrphanForbiddenTenant
		}
		payload, _ := json.Marshal(map[string]any{
			"task_id":          result.TaskID,
			"user_id":          result.UserID,
			"back_charged":     result.BackCharged,
			"captured_cents":   result.CapturedCents,
			"requested_status": req.Status,
		})
		tenantID := result.TenantID
		targetID := result.OrphanID
		var reqIDPtr *string
		if reqID != "" {
			reqIDPtr = &reqID
		}
		var reasonPtr *string
		if req.Reason != "" {
			reasonPtr = &req.Reason
		}
		_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    fmt.Sprintf("%d", ident.TokenID),
			ActorRole:  auditActorRole(ident),
			Action:     auditAction(result.Status),
			TargetType: auditTargetType,
			TargetID:   &targetID,
			RequestID:  reqIDPtr,
			Reason:     reasonPtr,
			Payload:    payload,
		})
		return err
	}
}

func nowUTC() time.Time { return time.Now().UTC() }

func auditAction(status string) string {
	switch status {
	case "reconciled":
		return "orphan_reconciled"
	case "cancelled":
		return "orphan_cancelled"
	case "ignored":
		return "orphan_ignored"
	default:
		return "orphan_reconciled"
	}
}

func auditActorRole(ident admin.AdminIdentity) string {
	if ident.Role == "" {
		return admin.RoleTenantOperator
	}
	return ident.Role
}

func writeReconcileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOrphanForbiddenTenant):
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "orphan is not in operator tenant scope")
	case errors.Is(err, mediatask.ErrInvalidOrphanStatus):
		writeError(w, http.StatusBadRequest, "invalid_orphan_status", "status must be reconciled/cancelled/ignored; back_charge only valid with reconciled")
	default:
		writeError(w, http.StatusServiceUnavailable, "orphan_backend_error", "orphan reconcile backend unavailable")
	}
}
