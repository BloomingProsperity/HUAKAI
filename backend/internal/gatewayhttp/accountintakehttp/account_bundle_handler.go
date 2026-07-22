package accountintakehttp

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/accountbundle"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

const accountBundleBodyLimit = 12 << 20

type accountBundleExportPlanRequest struct {
	TenantID   int64   `json:"tenant_id"`
	AccountIDs []int64 `json:"account_ids"`
	Reason     string  `json:"reason,omitempty"`
}

type accountBundleExportExecuteRequest struct {
	accountBundleExportPlanRequest
	PlanHash     string `json:"plan_hash"`
	Password     string `json:"password"`
	Confirmation string `json:"confirmation"`
}

type accountBundleImportPlanRequest struct {
	TenantID     int64                                `json:"tenant_id"`
	Envelope     accountbundle.Envelope               `json:"envelope"`
	Password     string                               `json:"password"`
	Destinations map[string]accountbundle.Destination `json:"destinations"`
	Reason       string                               `json:"reason,omitempty"`
}

type accountBundleImportExecuteRequest struct {
	accountBundleImportPlanRequest
	BundleHash string                             `json:"bundle_hash"`
	Entries    []accountbundle.ImportExecuteEntry `json:"entries"`
}

func newAccountBundleExportPlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveBundleOperator(w, r, d)
		if !ok {
			return
		}
		var req accountBundleExportPlanRequest
		if !decodeAccountIntakeJSON(w, r, &req) || !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.BundleService.PlanExport(r.Context(), accountbundle.ExportPlanInput{
			TenantID: req.TenantID, AccountIDs: req.AccountIDs,
			ActorID: ident.AuditActor(), ActorRole: ident.Role, ActorScopeTenantID: ident.ScopeTenantID,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeAccountBundleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newAccountBundleExportExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveBundleOperator(w, r, d)
		if !ok {
			return
		}
		var req accountBundleExportExecuteRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		defer func() { req.Password = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.BundleService.ExecuteExport(r.Context(), accountbundle.ExportExecuteInput{
			ExportPlanInput: accountbundle.ExportPlanInput{
				TenantID: req.TenantID, AccountIDs: req.AccountIDs,
				ActorID: ident.AuditActor(), ActorRole: ident.Role, ActorScopeTenantID: ident.ScopeTenantID,
				RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
			},
			PlanHash: req.PlanHash, Password: req.Password, Confirmation: req.Confirmation,
		})
		if err != nil {
			writeAccountBundleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newAccountBundleImportPlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveBundleOperator(w, r, d)
		if !ok {
			return
		}
		var req accountBundleImportPlanRequest
		if !decodeAccountIntakeJSONLimit(w, r, &req, accountBundleBodyLimit) {
			return
		}
		defer func() { req.Password = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.BundleService.PlanImport(r.Context(), accountbundle.ImportPlanInput{
			TenantID: req.TenantID, Envelope: req.Envelope, Password: req.Password,
			Destinations: req.Destinations, ActorID: ident.AuditActor(), ActorRole: ident.Role, ActorScopeTenantID: ident.ScopeTenantID,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeAccountBundleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newAccountBundleImportExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveBundleOperator(w, r, d)
		if !ok {
			return
		}
		var req accountBundleImportExecuteRequest
		if !decodeAccountIntakeJSONLimit(w, r, &req, accountBundleBodyLimit) {
			return
		}
		defer func() { req.Password = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.BundleService.ExecuteImport(r.Context(), accountbundle.ImportExecuteInput{
			ImportPlanInput: accountbundle.ImportPlanInput{
				TenantID: req.TenantID, Envelope: req.Envelope, Password: req.Password,
				Destinations: req.Destinations, ActorID: ident.AuditActor(), ActorRole: ident.Role, ActorScopeTenantID: ident.ScopeTenantID,
				RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
			},
			BundleHash: req.BundleHash, Entries: req.Entries,
		})
		if err != nil {
			writeAccountBundleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolveBundleOperator(w http.ResponseWriter, r *http.Request, d Deps) (identity admin.AdminIdentity, ok bool) {
	identity, ok = resolveAdminAccountIntake(w, r, d)
	if !ok {
		return identity, false
	}
	if dependencyIsNil(d.BundleService) {
		writeJSONError(w, http.StatusServiceUnavailable, "account_bundle_not_configured", "账号迁移包依赖未配置")
		return identity, false
	}
	return identity, true
}

func dependencyIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func writeAccountBundleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accountbundle.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "account_bundle_invalid", "账号迁移包参数无效")
	case errors.Is(err, accountbundle.ErrForbidden):
		writeJSONError(w, http.StatusForbidden, "account_bundle_forbidden", "调用者无权操作该租户的账号迁移包")
	case errors.Is(err, accountbundle.ErrPassword):
		writeJSONError(w, http.StatusUnauthorized, "account_bundle_password_invalid", "迁移包密码错误")
	case errors.Is(err, accountbundle.ErrIntegrity):
		writeJSONError(w, http.StatusUnprocessableEntity, "account_bundle_integrity_failed", "迁移包已损坏、被篡改或版本不受支持")
	case errors.Is(err, accountbundle.ErrConfirmationRequired):
		writeJSONError(w, http.StatusConflict, "account_bundle_confirmation_required", "导出敏感账号材料前必须再次明确确认")
	case errors.Is(err, accountbundle.ErrPlanChanged), errors.Is(err, accountbundle.ErrConflict):
		writeJSONError(w, http.StatusConflict, "account_bundle_plan_changed", "账号、凭据、代理或目标映射已经变化，请重新预检")
	case errors.Is(err, accountbundle.ErrNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "account_bundle_not_configured", "账号迁移包依赖未配置")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "account_bundle_failed", "账号迁移包操作暂时不可用")
	}
}
