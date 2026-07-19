package accountintakehttp

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/crssource"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type crsPlanRequest struct {
	TenantID     int64                                    `json:"tenant_id"`
	BaseURL      string                                   `json:"base_url"`
	Username     string                                   `json:"username"`
	Password     string                                   `json:"password"`
	Destinations map[string]accountintake.AccountDefaults `json:"destinations"`
	SyncProxies  *bool                                    `json:"sync_proxies,omitempty"`
	Reason       string                                   `json:"reason,omitempty"`
}

type crsExecuteRequest struct {
	TenantID int64 `json:"tenant_id"`
	Entries  []struct {
		FlowID        string   `json:"flow_id"`
		PlanHash      string   `json:"plan_hash"`
		Confirmations []string `json:"confirmations,omitempty"`
	} `json:"entries"`
	Reason string `json:"reason,omitempty"`
}

func newCRSPlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.CRSService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "crs_not_configured", "CRS 账号来源尚未配置")
			return
		}
		var req crsPlanRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		defer func() { req.Password = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		syncProxies := true
		if req.SyncProxies != nil {
			syncProxies = *req.SyncProxies
		}
		result, err := d.CRSService.Plan(r.Context(), accountintake.CRSPlanInput{
			TenantID: req.TenantID, BaseURL: req.BaseURL, Username: req.Username, Password: req.Password,
			Destinations: req.Destinations, SyncProxies: syncProxies,
			ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeCRSError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newCRSExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.CRSService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "crs_not_configured", "CRS 账号来源尚未配置")
			return
		}
		var req crsExecuteRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		entries := make([]accountintake.CRSExecuteEntry, 0, len(req.Entries))
		for _, entry := range req.Entries {
			entries = append(entries, accountintake.CRSExecuteEntry{
				FlowID: entry.FlowID, PlanHash: entry.PlanHash, Confirmations: entry.Confirmations,
			})
		}
		result, err := d.CRSService.Execute(r.Context(), accountintake.CRSExecuteInput{
			TenantID: req.TenantID, Entries: entries,
			ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeCRSError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func writeCRSError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crssource.ErrEndpointDenied):
		writeJSONError(w, http.StatusForbidden, "crs_endpoint_denied", "CRS 地址不在部署白名单内或解析到了禁止地址")
	case errors.Is(err, crssource.ErrAuthentication):
		writeJSONError(w, http.StatusUnprocessableEntity, "crs_authentication_failed", "CRS 登录失败，请检查来源账号")
	case errors.Is(err, crssource.ErrResponseInvalid), errors.Is(err, crssource.ErrResponseTooLarge),
		errors.Is(err, crssource.ErrUpstream):
		writeJSONError(w, http.StatusBadGateway, "crs_source_failed", "CRS 返回异常，请检查来源服务状态")
	case errors.Is(err, crssource.ErrTooManyAccounts):
		writeJSONError(w, http.StatusUnprocessableEntity, "crs_too_many_accounts", "CRS 单次账号数量超过安全上限")
	case errors.Is(err, crssource.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "crs_input_invalid", "CRS 同步参数无效")
	case errors.Is(err, crssource.ErrNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "crs_not_configured", "CRS 账号来源尚未配置")
	case errors.Is(err, accountintake.ErrStagedCredentialNotFound):
		writeJSONError(w, http.StatusNotFound, "credential_flow_not_found", "短期凭据流程不存在")
	case errors.Is(err, accountintake.ErrStagedCredentialExpired):
		writeJSONError(w, http.StatusGone, "credential_flow_expired", "短期凭据流程已过期，请重新预检")
	case errors.Is(err, accountintake.ErrStagedCredentialReplay):
		writeJSONError(w, http.StatusConflict, "credential_flow_replayed", "短期凭据流程已被领取，不可重复执行")
	default:
		writeAdminAccountIntakeError(w, err)
	}
}
