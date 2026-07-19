// Package accountintakehttp 提供账号级凭据批量接入的管理端 HTTP 合同。
package accountintakehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountbundle"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

const accountIntakeBodyLimit = 2 << 20

type AdminAccountIntakeService interface {
	Plan(context.Context, accountintake.PlanInput) (accountintake.PlanResult, error)
	Execute(context.Context, accountintake.ExecuteInput) (accountintake.ExecutionResult, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Deps struct {
	Auth          AdminAuth
	Service       AdminAccountIntakeService
	CookieService interface {
		Plan(context.Context, accountintake.CookiePlanInput) (accountintake.CookiePlanResult, error)
		Execute(context.Context, accountintake.CookieExecuteInput) (accountintake.ExecutionResult, error)
	}
	CRSService interface {
		Plan(context.Context, accountintake.CRSPlanInput) (accountintake.CRSPlanResult, error)
		Execute(context.Context, accountintake.CRSExecuteInput) (accountintake.CRSExecutionResult, error)
	}
	BundleService interface {
		PlanExport(context.Context, accountbundle.ExportPlanInput) (accountbundle.ExportPlan, error)
		ExecuteExport(context.Context, accountbundle.ExportExecuteInput) (accountbundle.ExportResult, error)
		PlanImport(context.Context, accountbundle.ImportPlanInput) (accountbundle.ImportPlan, error)
		ExecuteImport(context.Context, accountbundle.ImportExecuteInput) (accountbundle.ImportExecutionResult, error)
	}
	Capabilities interface {
		Allowed(context.Context, int64, string) (bool, error)
	}
}

type accountIntakePlanRequest struct {
	TenantID        int64                         `json:"tenant_id"`
	SourceKind      intake.SourceKind             `json:"source_kind"`
	DefaultVendor   string                        `json:"default_vendor,omitempty"`
	DefaultAuthMode string                        `json:"default_auth_mode,omitempty"`
	Content         string                        `json:"content"`
	Account         accountintake.AccountDefaults `json:"account"`
}

type accountIntakeExecuteRequest struct {
	accountIntakePlanRequest
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func Mount(r chi.Router, d Deps) {
	r.Post("/account-imports/plan", newAdminAccountIntakePlanHandler(d))
	r.Post("/account-imports/execute", newAdminAccountIntakeExecuteHandler(d))
	r.Post("/account-imports/codex/plan", newCodexAccountIntakePlanHandler(d))
	r.Post("/account-imports/codex/execute", newCodexAccountIntakeExecuteHandler(d))
	r.Post("/account-imports/codex-agent/plan", newCodexAgentPlanHandler(d))
	r.Post("/account-imports/codex-agent/execute", newCodexAgentExecuteHandler(d))
	r.Post("/account-imports/claude-setup-token/plan", newClaudeSetupTokenPlanHandler(d))
	r.Post("/account-imports/claude-setup-token/execute", newClaudeSetupTokenExecuteHandler(d))
	r.Post("/account-imports/claude-cookie/plan", newClaudeCookiePlanHandler(d))
	r.Post("/account-imports/claude-cookie/execute", newClaudeCookieExecuteHandler(d))
	r.Post("/account-imports/crs/plan", newCRSPlanHandler(d))
	r.Post("/account-imports/crs/execute", newCRSExecuteHandler(d))
	r.Post("/account-bundles/export/plan", newAccountBundleExportPlanHandler(d))
	r.Post("/account-bundles/export/execute", newAccountBundleExportExecuteHandler(d))
	r.Post("/account-bundles/import/plan", newAccountBundleImportPlanHandler(d))
	r.Post("/account-bundles/import/execute", newAccountBundleImportExecuteHandler(d))
}

func newAdminAccountIntakePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		var req accountIntakePlanRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			req.Content = ""
			return
		}
		result, err := d.Service.Plan(r.Context(), req.planInput())
		req.Content = ""
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newAdminAccountIntakeExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		var req accountIntakeExecuteRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			req.Content = ""
			return
		}
		result, err := d.Service.Execute(r.Context(), accountintake.ExecuteInput{
			PlanInput: req.planInput(), PlanHash: req.PlanHash,
			Confirmations: req.Confirmations,
			ActorID:       ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		req.Content = ""
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (r accountIntakePlanRequest) planInput() accountintake.PlanInput {
	return accountintake.PlanInput{
		TenantID: r.TenantID, SourceKind: r.SourceKind,
		DefaultVendor: r.DefaultVendor, DefaultAuthMode: r.DefaultAuthMode,
		Content: r.Content, Account: r.Account,
	}
}

func resolveAdminAccountIntake(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	if d.Auth == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "account intake auth dependency unset")
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
	if ident.Source == admin.AdminSourceSession || ident.Role != admin.RoleTenantOperator || ident.ScopeTenantID <= 0 {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "scoped tenant_operator token required")
		return admin.AdminIdentity{}, false
	}
	if d.Capabilities == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tenant capability dependency unset")
		return admin.AdminIdentity{}, false
	}
	allowed, err := d.Capabilities.Allowed(r.Context(), ident.ScopeTenantID, tenantcapability.AdvancedAccountIntake)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "tenant capability lookup temporarily unavailable")
		return admin.AdminIdentity{}, false
	}
	if !allowed {
		writeJSONError(w, http.StatusForbidden, "tenant_capability_not_granted", "advanced account intake is not granted for this tenant")
		return admin.AdminIdentity{}, false
	}
	if d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "account intake service dependency unset")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func validateAccountIntakeTenant(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) bool {
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "account_intake_invalid", "tenant_id must be positive")
		return false
	}
	if tenantID != ident.ScopeTenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope mismatch")
		return false
	}
	return true
}

func decodeAccountIntakeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeAccountIntakeJSONLimit(w, r, dst, accountIntakeBodyLimit)
}

func decodeAccountIntakeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求体超过 %d MiB", limit>>20))
			return false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求体超过 %d MiB", limit>>20))
			return false
		}
		if err == nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		} else {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		}
		return false
	}
	return true
}

func writeAdminAccountIntakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accountintake.ErrInvalidInput),
		errors.Is(err, credentialacq.ErrInvalidImportBody),
		errors.Is(err, credentialstore.ErrInvalidPayload),
		errors.Is(err, credentialstore.ErrUnknownMode),
		errors.Is(err, intake.ErrTenantRequired),
		errors.Is(err, intake.ErrTooManyItems),
		errors.Is(err, intake.ErrSourceInvalid):
		writeJSONError(w, http.StatusBadRequest, "account_intake_invalid", err.Error())
	case errors.Is(err, accountintake.ErrPlanHashMissing):
		writeJSONError(w, http.StatusBadRequest, "plan_hash_required", err.Error())
	case errors.Is(err, accountintake.ErrPlanChanged):
		writeJSONError(w, http.StatusConflict, "account_intake_plan_changed", "账号或凭据状态已经变化，请重新预检")
	case errors.Is(err, pgx.ErrNoRows):
		writeJSONError(w, http.StatusBadRequest, "provider_not_found", "provider does not exist")
	case errors.Is(err, accountintake.ErrNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "account intake dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "account_intake_failed", "account intake is temporarily unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
