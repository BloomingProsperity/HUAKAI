package accountintakehttp

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type codexAccountIntakePlanRequest struct {
	TenantID int64                         `json:"tenant_id"`
	Content  string                        `json:"content"`
	Account  accountintake.AccountDefaults `json:"account"`
}

type codexAccountIntakeExecuteRequest struct {
	codexAccountIntakePlanRequest
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func newCodexAccountIntakePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		var req codexAccountIntakePlanRequest
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

func newCodexAccountIntakeExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		var req codexAccountIntakeExecuteRequest
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

func (r codexAccountIntakePlanRequest) planInput() accountintake.PlanInput {
	return accountintake.PlanInput{
		TenantID: r.TenantID, SourceKind: intake.SourceCLI,
		DefaultVendor: credentialstore.VendorOpenAI, DefaultAuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Content: r.Content, Account: r.Account,
	}
}
