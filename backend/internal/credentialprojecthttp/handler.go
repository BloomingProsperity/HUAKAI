package credentialprojecthttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const auditAction = "resolve_credential_project"

type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Store interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	SaveRefreshSuccess(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string) error
}

type AuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type Deps struct {
	Auth     Auth
	Store    Store
	Enricher projectenrich.Enricher
	Audit    AuditStore
}

type resolveRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

type resolveResponse struct {
	ProjectRef string `json:"project_ref"`
}

func MountRoutes(r chi.Router, deps Deps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.With(safe).Post("/{id}/credentials/{credentialID}/resolve-project", NewResolveHandler(deps))
}

func NewResolveHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Auth == nil || deps.Store == nil || deps.Enricher == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "凭证 project 解析依赖未配置")
			return
		}
		identity, err := deps.Auth.Resolve(r.Context(), r)
		if err != nil {
			if errors.Is(err, admin.ErrAdminBackend) {
				writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "管理员认证后端暂不可用")
			} else {
				writeError(w, http.StatusUnauthorized, "admin_unauthorized", "管理员凭据缺失或无效")
			}
			return
		}
		if !identity.IsPlatformWide() {
			writeError(w, http.StatusForbidden, "admin_forbidden", "需要 platform_admin 权限")
			return
		}

		accountID, ok := positivePathID(w, r, "id", "invalid_provider_account_id")
		if !ok {
			return
		}
		credentialID, ok := positivePathID(w, r, "credentialID", "invalid_credential_id")
		if !ok {
			return
		}
		var request resolveRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "请求体必须是有效 JSON")
			return
		}
		if request.TenantID <= 0 {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id 必须为正整数")
			return
		}
		record, err := deps.Store.LoadForRefresh(r.Context(), accountID)
		if err != nil {
			if errors.Is(err, credentialstore.ErrCredentialNotFound) {
				writeError(w, http.StatusNotFound, "account_credential_not_found", "未找到可解析的凭证")
			} else {
				writeError(w, http.StatusServiceUnavailable, "account_credential_load_failed", "读取凭证失败")
			}
			return
		}
		defer privacy.Zeroize(record.PlaintextPayload)
		if record.ID != credentialID || record.ProviderAccountID != accountID || record.TenantID != request.TenantID {
			writeError(w, http.StatusNotFound, "account_credential_not_found", "未找到可解析的凭证")
			return
		}
		if credentialstore.Normalize(record.Vendor) != credentialstore.VendorAntigravity {
			writeError(w, http.StatusUnprocessableEntity, "credential_project_not_supported", "该凭证不支持 project 解析")
			return
		}

		result, err := deps.Enricher.Enrich(r.Context(), record.Vendor, record.PlaintextPayload)
		if len(result.Payload) > 0 {
			defer privacy.Zeroize(result.Payload)
		}
		if err != nil || strings.TrimSpace(result.ProjectRef) == "" || len(result.Payload) == 0 {
			writeError(w, http.StatusBadGateway, "credential_project_resolve_failed", "上游 project 标识解析失败")
			return
		}
		if err := deps.Store.SaveRefreshSuccess(r.Context(), record, result.Payload, record.AccessExpiresAt, "project_resolved"); err != nil {
			writeError(w, http.StatusServiceUnavailable, "credential_project_save_failed", "project 标识写回失败")
			return
		}

		writeAudit(r, deps.Audit, identity, record, result.ProjectRef, request.Reason)
		writeJSON(w, http.StatusOK, resolveResponse{ProjectRef: result.ProjectRef})
	}
}

func positivePathID(w http.ResponseWriter, r *http.Request, name, code string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, name)), 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, code, name+" 必须为正整数")
		return 0, false
	}
	return value, true
}

func writeAudit(r *http.Request, store AuditStore, identity admin.AdminIdentity, record credentialstore.CredentialRecord, projectRef, reason string) {
	if store == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id": record.TenantID, "provider_account_id": record.ProviderAccountID,
		"credential_id": record.ID, "project_ref": strings.TrimSpace(projectRef),
	})
	requestID := middleware.GetReqID(r.Context())
	targetID := record.ID
	tenantID := record.TenantID
	auditReason := strings.TrimSpace(reason)
	if auditReason == "" {
		auditReason = "解析凭证 project 标识"
	}
	_, err := store.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: identity.AuditActor(), ActorRole: identity.Role,
		Action: auditAction, TargetType: "account_credential", TargetID: &targetID,
		RequestID: &requestID, Reason: &auditReason, Payload: payload,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "凭证 project 解析审计写入失败",
			"action", auditAction,
			"tenant_id", record.TenantID,
			"provider_account_id", record.ProviderAccountID,
			"credential_id", record.ID,
			"request_id", requestID,
			"error", err,
		)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
