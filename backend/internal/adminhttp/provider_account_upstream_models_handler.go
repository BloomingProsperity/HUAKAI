package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmodeldiscovery"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const upstreamModelsMutationBodyLimit = 4 << 10

type UpstreamModelsDeps struct {
	Auth      upstreamModelsAuth
	Accounts  upstreamModelsAccountStore
	Discovery upstreamModelsDiscovery
}

type upstreamModelsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type upstreamModelsAccountStore interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
}

type upstreamModelsDiscovery interface {
	Discover(context.Context, int64, int64) (accountmodeldiscovery.Result, error)
	Sync(context.Context, accountmodeldiscovery.SyncInput) (accountmodeldiscovery.SyncResult, error)
}

type upstreamModelsResponse struct {
	Models              []string                      `json:"models"`
	Items               []accountmodeldiscovery.Model `json:"items"`
	Count               int                           `json:"count"`
	Vendor              string                        `json:"vendor"`
	AuthMode            string                        `json:"auth_mode"`
	ProtocolFamily      string                        `json:"protocol_family"`
	DiscoveredAt        string                        `json:"discovered_at"`
	Changed             bool                          `json:"changed,omitempty"`
	PreviousCount       int                           `json:"previous_count,omitempty"`
	CredentialVersion   int                           `json:"credential_version,omitempty"`
	AccountCredentialID int64                         `json:"account_credential_id,omitempty"`
}

type upstreamModelsSyncRequest struct {
	Reason string `json:"reason"`
}

func MountProviderAccountUpstreamModelsRoutes(r chi.Router, d UpstreamModelsDeps) {
	r.Get("/{id}/upstream-models", newProviderAccountUpstreamModelsHandler(d, false))
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post(
		"/{id}/upstream-models/sync", newProviderAccountUpstreamModelsHandler(d, true),
	)
}

func newProviderAccountUpstreamModelsHandler(d UpstreamModelsDeps, persist bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveUpstreamModelsTenant(w, r, d)
		if !ok {
			return
		}
		accountID, ok := parseUpstreamModelsAccountID(w, r)
		if !ok || !requireUpstreamModelsAccount(w, r, d, tenantID, accountID) {
			return
		}

		if !persist {
			result, err := d.Discovery.Discover(r.Context(), tenantID, accountID)
			if err != nil {
				logUpstreamModelsFailure(r.Context(), "discover", tenantID, accountID, err)
				writeUpstreamModelsError(w, err)
				return
			}
			writeUpstreamModelsJSON(w, result, false, 0)
			return
		}

		reason, ok := decodeUpstreamModelsSyncRequest(w, r)
		if !ok {
			return
		}
		result, err := d.Discovery.Sync(r.Context(), accountmodeldiscovery.SyncInput{
			TenantID: tenantID, AccountID: accountID, ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: chimiddleware.GetReqID(r.Context()), Reason: reason,
		})
		if err != nil {
			logUpstreamModelsFailure(r.Context(), "sync", tenantID, accountID, err)
			writeUpstreamModelsError(w, err)
			return
		}
		writeUpstreamModelsJSON(w, result.Result, result.Changed, result.PreviousCount)
	}
}

func resolveUpstreamModelsTenant(w http.ResponseWriter, r *http.Request, d UpstreamModelsDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Accounts == nil || d.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "upstream models dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		tenantID, ok := resolvePlatformAdminQueryTenant(w, r, ident)
		if !ok {
			return admin.AdminIdentity{}, 0, false
		}
		return ident, tenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func parseUpstreamModelsAccountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func requireUpstreamModelsAccount(w http.ResponseWriter, r *http.Request, d UpstreamModelsDeps, tenantID, accountID int64) bool {
	_, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: tenantID})
	if err == nil {
		return true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return false
	}
	writeError(w, http.StatusServiceUnavailable, "provider_account_get_failed", "provider account lookup is unavailable")
	return false
}

func decodeUpstreamModelsSyncRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Body == nil || r.ContentLength == 0 {
		return "", true
	}
	r.Body = http.MaxBytesReader(w, r.Body, upstreamModelsMutationBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input upstreamModelsSyncRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a valid JSON object")
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return "", false
	}
	return strings.TrimSpace(input.Reason), true
}

// logUpstreamModelsFailure 给模型发现/同步失败留可辨识运维轨迹:成功与未变化
// 走管理审计日志,失败在此按 error_class 分类落系统日志。日志写入失败不掩盖
// 原始业务错误;字段全部取自 privacy 允许清单,原始上游正文/token 一概不落。
func logUpstreamModelsFailure(ctx context.Context, operation string, tenantID, accountID int64, err error) {
	var discoveryErr *accountmodeldiscovery.DiscoveryError
	vendor, authMode := "", ""
	upstreamStatus := 0
	if errors.As(err, &discoveryErr) {
		vendor, authMode = discoveryErr.Vendor, discoveryErr.AuthMode
		upstreamStatus = discoveryErr.StatusCode
	}
	_ = privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity:   privacy.SeverityError,
		Component:  "adminhttp.provider_account_upstream_models",
		RequestID:  chimiddleware.GetReqID(ctx),
		ErrorClass: logSafeUpstreamModelsClass(accountmodeldiscovery.KindOf(err)),
		Attrs: map[string]any{
			"event_class": "upstream_models_" + operation + "_failed",
			"outcome":     "failed",
			"tenant_id":   tenantID, "provider_account_id": accountID,
			"vendor": vendor, "auth_mode": logSafeAuthMode(authMode),
			"upstream_status": upstreamStatus,
		},
	})
}

// privacy 值位禁写扫描不放行含 credential/refresh_token 词根的值(整包会被
// fail-closed 拦成 privacy_guard_hit),日志侧换等义分类,辨识度不变。
func logSafeUpstreamModelsClass(kind accountmodeldiscovery.ErrorKind) string {
	switch kind {
	case accountmodeldiscovery.ErrorCredentialRejected:
		return "upstream_auth_rejected"
	case accountmodeldiscovery.ErrorCredentialChanged:
		return "auth_rotation_conflict"
	default:
		return string(kind)
	}
}

func logSafeAuthMode(mode string) string {
	if mode == credentialstore.AuthModeRefreshToken {
		return "oauth_refresh"
	}
	return mode
}

func writeUpstreamModelsError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	code := "upstream_models_failed"
	switch accountmodeldiscovery.KindOf(err) {
	case accountmodeldiscovery.ErrorNotConfigured:
		status, code = http.StatusServiceUnavailable, "gateway_not_configured"
	case accountmodeldiscovery.ErrorAccountUnavailable:
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, credentialstore.ErrCredentialNotFound) {
			status, code = http.StatusNotFound, "provider_account_not_found"
		} else {
			status, code = http.StatusServiceUnavailable, "provider_account_unavailable"
		}
	case accountmodeldiscovery.ErrorUnsupported:
		status, code = http.StatusUnprocessableEntity, "upstream_models_unsupported"
	case accountmodeldiscovery.ErrorCredentialRejected:
		status, code = http.StatusBadGateway, "upstream_credential_rejected"
	case accountmodeldiscovery.ErrorRateLimited:
		status, code = http.StatusTooManyRequests, "upstream_rate_limited"
	case accountmodeldiscovery.ErrorResponseInvalid, accountmodeldiscovery.ErrorEmptyCatalog,
		accountmodeldiscovery.ErrorCatalogTooLarge:
		status, code = http.StatusBadGateway, "upstream_models_invalid"
	case accountmodeldiscovery.ErrorCredentialChanged:
		status, code = http.StatusConflict, "credential_changed"
	case accountmodeldiscovery.ErrorPersistence:
		status, code = http.StatusServiceUnavailable, "upstream_models_persist_failed"
	}
	writeError(w, status, code, "provider account model discovery failed")
}

func writeUpstreamModelsJSON(w http.ResponseWriter, result accountmodeldiscovery.Result, changed bool, previousCount int) {
	response := upstreamModelsResponse{
		Models: result.ModelIDs(), Items: result.Models, Count: len(result.Models), Vendor: result.Vendor,
		AuthMode: result.AuthMode, ProtocolFamily: result.ProtocolFamily, DiscoveredAt: result.DiscoveredAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		Changed: changed, PreviousCount: previousCount, CredentialVersion: result.CredentialVersion,
		AccountCredentialID: result.AccountCredentialID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
