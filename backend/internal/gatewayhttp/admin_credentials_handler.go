package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
)

type AdminCredentialAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminCredentialStore interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
	Rotate(context.Context, credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error)
	ListByAccount(context.Context, int64, int64) ([]credentialstore.CredentialMetadata, error)
	ListRenewStatus(context.Context, credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error)
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
	// ExternalAccountID/ExternalAccountEmail 是运营者在手动创建路径中提供的上游账号
	// 身份。OAuth token 交换时的自动提取是主要来源；当没有走 OAuth 流程时，
	// 这两个字段作为手动覆盖/兜底。
	ExternalAccountID    string `json:"external_account_id,omitempty"`
	ExternalAccountEmail string `json:"external_account_email,omitempty"`
}

type credentialStateRequest struct {
	TenantID int64  `json:"tenant_id"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

type renewStatusListResponse struct {
	Items      []credentialstore.RenewStatusMetadata `json:"items"`
	NextCursor *string                               `json:"next_cursor"`
}

type renewStatusCursor struct {
	V         int       `json:"v"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        int64     `json:"id"`
}

func MountAdminCredentialRoutes(r chi.Router, d AdminCredentialDeps) {
	// 日常凭证增改放开给登录 admin(rotate=换池账号上游凭证,非 KEK);采集流/import-helper/签发/建删账号仍 token-only。
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/{id}/credentials", newListAccountCredentialsHandler(d))
	r.With(safe).Post("/{id}/credentials", newCreateAccountCredentialHandler(d))
	r.With(safe).Post("/{id}/credentials/{credentialID}/rotate", newRotateAccountCredentialHandler(d))
	r.With(safe).Patch("/{id}/credentials/{credentialID}/state", newSetAccountCredentialStateHandler(d))
	r.With(safe).Delete("/{id}/credentials/{credentialID}", newDeleteAccountCredentialHandler(d))
}

func MountAdminCredentialRenewStatusRoutes(r chi.Router, d AdminCredentialDeps) {
	r.Get("/renew-status", newListCredentialRenewStatusHandler(d))
}

func newListCredentialRenewStatusHandler(d AdminCredentialDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, _, ok := resolveCredentialRenewStatusAdmin(w, r, d)
		if !ok {
			return
		}
		limit, ok := parseCredentialRenewStatusLimit(w, r)
		if !ok {
			return
		}
		cursorUpdatedAt, cursorID, ok := parseCredentialRenewStatusCursor(w, r)
		if !ok {
			return
		}
		rows, err := d.Credentials.ListRenewStatus(r.Context(), credentialstore.ListRenewStatusParams{
			TenantID:        tenantID,
			CursorUpdatedAt: cursorUpdatedAt,
			CursorID:        cursorID,
			Limit:           limit + 1,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "credential_renew_status_list_failed", err.Error())
			return
		}
		hasMore := int32(len(rows)) > limit
		if hasMore {
			rows = rows[:limit]
		}
		var nextCursor *string
		if hasMore && len(rows) > 0 {
			next := encodeCredentialRenewStatusCursor(rows[len(rows)-1].UpdatedAt, rows[len(rows)-1].CredentialID)
			nextCursor = &next
		}
		if d.AuditStore != nil {
			payloadScope := any("all")
			if tenantID != nil {
				payloadScope = *tenantID
			}
			payload, _ := json.Marshal(map[string]any{"scope": payloadScope, "count": len(rows)})
			_ = writeAccountCredentialAudit(r.Context(), r, d.AuditStore, ident, tenantID, "list_account_credentials",
				chineseReason("", "查看 credential renew status"), payload)
		}
		writeAuditJSON(w, http.StatusOK, renewStatusListResponse{Items: rows, NextCursor: nextCursor})
	}
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
		// credential-create 守卫(收敛 G1 account-first + R1A):新凭据 vendor/auth 须与账号所属
		// provider family 兼容,防给 live 账号加错配凭据。并发 TOCTOU 由运行时兼容门兜底。
		if d.AuditStore != nil {
			if err := accountcreate.ValidateCredentialCompatibility(r.Context(), d.AuditStore, req.TenantID, accountID, req.Vendor, req.AuthMode); err != nil {
				if errors.Is(err, accountcreate.ErrProtocolIncompatible) {
					writeJSONError(w, http.StatusBadRequest, "credential_protocol_incompatible", err.Error())
				} else {
					writeJSONError(w, http.StatusServiceUnavailable, "credential_compatibility_check_failed", "provider account compatibility check unavailable")
				}
				return
			}
		}
		meta, err := d.Credentials.Create(r.Context(), credentialstore.CreateCredentialInput{
			TenantID: req.TenantID, ProviderAccountID: accountID,
			Vendor: req.Vendor, AuthMode: req.AuthMode,
			Payload: req.Credentials, ActorID: ident.AuditActor(),
			ExternalAccountID:    req.ExternalAccountID,
			ExternalAccountEmail: req.ExternalAccountEmail,
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
			Payload: req.Credentials, ActorID: ident.AuditActor(),
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
		if err := d.Credentials.SetState(r.Context(), req.TenantID, accountID, credentialID, req.State, ident.AuditActor()); err != nil {
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
		if err := d.Credentials.Delete(r.Context(), req.TenantID, accountID, credentialID, ident.AuditActor()); err != nil {
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

func resolveCredentialRenewStatusAdmin(w http.ResponseWriter, r *http.Request, d AdminCredentialDeps) (admin.AdminIdentity, *int64, bool, bool) {
	if d.Auth == nil || d.Credentials == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin credential dependency unset")
		return admin.AdminIdentity{}, nil, false, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, nil, false, false
	}
	queryTenantID, hasQueryTenant, ok := parseCredentialRenewStatusTenantID(w, r)
	if !ok {
		return admin.AdminIdentity{}, nil, false, false
	}
	switch ident.Role {
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID <= 0 {
			if hasQueryTenant {
				tenantID := queryTenantID
				return ident, &tenantID, false, true
			}
			return ident, nil, true, true
		}
		if hasQueryTenant && queryTenantID != ident.ScopeTenantID {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
			return admin.AdminIdentity{}, nil, false, false
		}
		tenantID := ident.ScopeTenantID
		return ident, &tenantID, false, true
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, nil, false, false
		}
		if hasQueryTenant && queryTenantID != ident.ScopeTenantID {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
			return admin.AdminIdentity{}, nil, false, false
		}
		tenantID := ident.ScopeTenantID
		return ident, &tenantID, false, true
	default:
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, nil, false, false
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

func parseCredentialRenewStatusLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return credentialstore.DefaultRenewStatusLimit, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 || n > int64(credentialstore.MaxRenewStatusLimit) {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
		return 0, false
	}
	return int32(n), true
}

func parseCredentialRenewStatusTenantID(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	values, present := r.URL.Query()["tenant_id"]
	if !present {
		return 0, false, true
	}
	raw := ""
	if len(values) > 0 {
		raw = strings.TrimSpace(values[0])
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_invalid", "tenant_id must be positive when provided")
		return 0, false, false
	}
	return n, true, true
}

func parseCredentialRenewStatusCursor(w http.ResponseWriter, r *http.Request) (time.Time, int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return time.Time{}, 0, true
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return time.Time{}, 0, false
	}
	var cursor renewStatusCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.V != 1 || cursor.ID <= 0 || cursor.UpdatedAt.IsZero() {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return time.Time{}, 0, false
	}
	return cursor.UpdatedAt, cursor.ID, true
}

func encodeCredentialRenewStatusCursor(updatedAt time.Time, id int64) string {
	body, _ := json.Marshal(renewStatusCursor{V: 1, UpdatedAt: updatedAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(body)
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

func writeAccountCredentialAudit(ctx context.Context, r *http.Request, store AdminPoolAccountStore, ident admin.AdminIdentity, tenantID *int64, action string, reason *string, payload []byte) error {
	actorID := ident.AuditActor()
	reqID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "account_credential", TargetID: nil,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}
