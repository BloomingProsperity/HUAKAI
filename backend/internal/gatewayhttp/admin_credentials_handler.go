package gatewayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type AdminCredentialAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminCredentialStore interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
	Rotate(context.Context, credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error)
	ListByAccount(context.Context, int64, int64) ([]credentialstore.CredentialMetadata, error)
	SetState(context.Context, int64, int64, int64, string, string) error
	Delete(context.Context, int64, int64, int64, string) error
}

type AdminCredentialDeps struct {
	Auth        AdminCredentialAuth
	Credentials AdminCredentialStore
	AuditStore  AdminPoolAccountStore
}

type credentialWriteRequest struct {
	TenantID    int64           `json:"tenant_id"`
	Vendor      string          `json:"vendor"`
	AuthMode    string          `json:"auth_mode"`
	Credentials json.RawMessage `json:"credentials"`
	Reason      string          `json:"reason,omitempty"`
}

type credentialStateRequest struct {
	TenantID int64  `json:"tenant_id"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

func MountAdminCredentialRoutes(r chi.Router, d AdminCredentialDeps) {
	r.Get("/{id}/credentials", newListAccountCredentialsHandler(d))
	r.Post("/{id}/credentials", newCreateAccountCredentialHandler(d))
	r.Post("/{id}/credentials/{credentialID}/rotate", newRotateAccountCredentialHandler(d))
	r.Patch("/{id}/credentials/{credentialID}/state", newSetAccountCredentialStateHandler(d))
	r.Delete("/{id}/credentials/{credentialID}", newDeleteAccountCredentialHandler(d))
}

func newListAccountCredentialsHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, accountID, tenantID, ok := resolveCredentialAdminRequest(w, r, d, true)
		if !ok {
			return
		}
		rows, err := d.Credentials.ListByAccount(r.Context(), tenantID, accountID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "account_credentials_list_failed", err.Error())
			return
		}
		if d.AuditStore != nil {
			payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "count": len(rows)})
			_ = writeProviderAccountAudit(r.Context(), r, d.AuditStore, ident, tenantID,
				"list_account_credentials", accountID, chineseReason("", "查看 provider account credentials"), payload)
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"credentials": rows})
	}
}

func newCreateAccountCredentialHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, accountID, _, ok := resolveCredentialAdminRequest(w, r, d, false)
		if !ok {
			return
		}
		var req credentialWriteRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		meta, err := d.Credentials.Create(r.Context(), credentialstore.CreateCredentialInput{
			TenantID: req.TenantID, ProviderAccountID: accountID,
			Vendor: req.Vendor, AuthMode: req.AuthMode,
			Payload: req.Credentials, ActorID: fmt.Sprintf("%d", ident.TokenID),
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "account_credential_create_failed", err.Error())
			return
		}
		writeCredentialAdminAudit(r, d, ident, meta, "create_account_credential", req.Reason)
		writeAuditJSON(w, http.StatusCreated, meta)
	}
}

func newRotateAccountCredentialHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, accountID, _, credentialID, ok := resolveCredentialMutationRequest(w, r, d)
		if !ok {
			return
		}
		var req credentialWriteRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		meta, err := d.Credentials.Rotate(r.Context(), credentialstore.RotateCredentialInput{
			TenantID: req.TenantID, ProviderAccountID: accountID, CredentialID: credentialID,
			Payload: req.Credentials, ActorID: fmt.Sprintf("%d", ident.TokenID),
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "account_credential_rotate_failed", err.Error())
			return
		}
		writeCredentialAdminAudit(r, d, ident, meta, "rotate_account_credential", req.Reason)
		writeAuditJSON(w, http.StatusOK, meta)
	}
}

func newSetAccountCredentialStateHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, accountID, _, credentialID, ok := resolveCredentialMutationRequest(w, r, d)
		if !ok {
			return
		}
		var req credentialStateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
			return
		}
		if err := d.Credentials.SetState(r.Context(), req.TenantID, accountID, credentialID, req.State, fmt.Sprintf("%d", ident.TokenID)); err != nil {
			writeJSONError(w, http.StatusBadRequest, "account_credential_state_failed", err.Error())
			return
		}
		if d.AuditStore != nil {
			payload, _ := json.Marshal(map[string]any{"tenant_id": req.TenantID, "credential_id": credentialID, "state": credentialstore.Normalize(req.State)})
			_ = writeProviderAccountAudit(r.Context(), r, d.AuditStore, ident, req.TenantID,
				"disable_account_credential", accountID, chineseReason(req.Reason, "更新 provider account credential 状态"), payload)
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": credentialID, "state": credentialstore.Normalize(req.State)})
	}
}

func newDeleteAccountCredentialHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, accountID, _, credentialID, ok := resolveCredentialMutationRequest(w, r, d)
		if !ok {
			return
		}
		var req credentialStateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
			return
		}
		if err := d.Credentials.Delete(r.Context(), req.TenantID, accountID, credentialID, fmt.Sprintf("%d", ident.TokenID)); err != nil {
			writeJSONError(w, http.StatusBadRequest, "account_credential_delete_failed", err.Error())
			return
		}
		if d.AuditStore != nil {
			payload, _ := json.Marshal(map[string]any{"tenant_id": req.TenantID, "credential_id": credentialID, "deleted": true})
			_ = writeProviderAccountAudit(r.Context(), r, d.AuditStore, ident, req.TenantID,
				"delete_account_credential", accountID, chineseReason(req.Reason, "删除 provider account credential"), payload)
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": credentialID, "deleted": true})
	}
}

func resolveCredentialAdminRequest(w http.ResponseWriter, r *http.Request, d AdminCredentialDeps, tenantFromQuery bool) (admin.AdminIdentity, int64, int64, bool) {
	if d.Auth == nil || d.Credentials == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin credential dependency unset")
		return admin.AdminIdentity{}, 0, 0, false
	}
	ident, ok := resolvePlatformAdmin(w, r, AdminPoolAccountDeps{Auth: d.Auth, Store: d.AuditStore})
	if !ok {
		return admin.AdminIdentity{}, 0, 0, false
	}
	accountID, ok := parseAdminPoolID(w, r)
	if !ok {
		return admin.AdminIdentity{}, 0, 0, false
	}
	if !tenantFromQuery {
		return ident, accountID, 0, true
	}
	tenantID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("tenant_id")), 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return admin.AdminIdentity{}, 0, 0, false
	}
	return ident, accountID, tenantID, true
}

func resolveCredentialMutationRequest(w http.ResponseWriter, r *http.Request, d AdminCredentialDeps) (admin.AdminIdentity, int64, int64, int64, bool) {
	ident, accountID, tenantID, ok := resolveCredentialAdminRequest(w, r, d, false)
	if !ok {
		return admin.AdminIdentity{}, 0, 0, 0, false
	}
	credentialID, err := strconv.ParseInt(chi.URLParam(r, "credentialID"), 10, 64)
	if err != nil || credentialID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_credential_id", "credentialID must be a positive int64")
		return admin.AdminIdentity{}, 0, 0, 0, false
	}
	return ident, accountID, tenantID, credentialID, true
}

func writeCredentialAdminAudit(r *http.Request, d AdminCredentialDeps, ident admin.AdminIdentity, meta credentialstore.CredentialMetadata, action, reason string) {
	if d.AuditStore == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id":           meta.TenantID,
		"credential_id":       meta.ID,
		"vendor":              meta.Vendor,
		"auth_mode":           meta.AuthMode,
		"credentials_present": true,
	})
	_ = writeProviderAccountAudit(r.Context(), r, d.AuditStore, ident, meta.TenantID, action, meta.ProviderAccountID, chineseReason(reason, "更新 provider account credential"), payload)
}
