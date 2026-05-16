package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type AdminPoolAccountAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolAccountStore interface {
	InsertProviderAccount(context.Context, db.InsertProviderAccountParams) (int64, error)
	UpdateProviderAccountEnabled(context.Context, db.UpdateProviderAccountEnabledParams) error
	SoftDeleteProviderAccount(context.Context, db.SoftDeleteProviderAccountParams) error
	InsertAdminAuditEvent(context.Context, db.InsertAdminAuditEventParams) (db.InsertAdminAuditEventRow, error)
}

type AdminPoolAccountCredentialWriter interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

type AdminPoolAccountChannelHealthInitializer interface {
	EnsureDefaultActive(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type AdminPoolAccountDeps struct {
	Auth          AdminPoolAccountAuth
	Store         AdminPoolAccountStore
	Credentials   AdminPoolAccountCredentialWriter
	ChannelHealth AdminPoolAccountChannelHealthInitializer
}

func MountAdminPoolAccountRoutes(r chi.Router, d AdminPoolAccountDeps) {
	r.Post("/", newCreateProviderAccountHandler(d))
	r.Patch("/{id}/enabled", newUpdateProviderAccountEnabledHandler(d))
	r.Delete("/{id}", newDeleteProviderAccountHandler(d))
}

type createProviderAccountRequest struct {
	TenantID    int64           `json:"tenant_id"`
	ProviderID  int64           `json:"provider_id"`
	ChannelID   int64           `json:"channel_id"`
	Name        string          `json:"name"`
	AccountType string          `json:"account_type"`
	Vendor      string          `json:"vendor,omitempty"`
	AuthMode    string          `json:"auth_mode,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Credentials json.RawMessage `json:"credentials"`
	Reason      string          `json:"reason,omitempty"`
}

type mutateProviderAccountRequest struct {
	TenantID int64  `json:"tenant_id"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
}

func newCreateProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		var req createProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.AccountType = strings.TrimSpace(req.AccountType)
		req.Vendor = credentialstore.Normalize(req.Vendor)
		req.AuthMode = credentialstore.Normalize(req.AuthMode)
		if err := validateCreateProviderAccount(req, d.Credentials != nil); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		dbCredentials := []byte(req.Credentials)
		if d.Credentials != nil {
			dbCredentials = []byte(`{}`)
		}
		id, err := d.Store.InsertProviderAccount(r.Context(), db.InsertProviderAccountParams{
			TenantID: req.TenantID, ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			Name: req.Name, AccountType: req.AccountType, Enabled: req.Enabled,
			Credentials: dbCredentials, ActorID: &actorID,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_insert_failed", err.Error())
			return
		}
		var credentialID int64
		var credentialVersion int
		channelHealthInitialized := false
		if d.Credentials != nil {
			created, err := d.Credentials.Create(r.Context(), credentialstore.CreateCredentialInput{
				TenantID: req.TenantID, ProviderAccountID: id,
				Vendor: req.Vendor, AuthMode: req.AuthMode,
				Payload: req.Credentials, ActorID: actorID,
			})
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable, "account_credential_insert_failed", err.Error())
				return
			}
			credentialID = created.ID
			credentialVersion = int(created.Version)
			if d.ChannelHealth != nil {
				key := channelhealth.ChannelKey{
					TenantID: req.TenantID, Vendor: created.Vendor, ProviderAccountID: id,
					AccountCredentialID: created.ID, CredentialVersion: int(created.Version),
				}
				if _, err := d.ChannelHealth.EnsureDefaultActive(r.Context(), key); err == nil {
					channelHealthInitialized = true
				}
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"tenant_id":                  req.TenantID,
			"provider_id":                req.ProviderID,
			"channel_id":                 req.ChannelID,
			"name":                       req.Name,
			"account_type":               req.AccountType,
			"vendor":                     req.Vendor,
			"auth_mode":                  req.AuthMode,
			"credential_id":              credentialID,
			"credential_version":         credentialVersion,
			"channel_health_initialized": channelHealthInitialized,
			"credentials_present":        true,
		})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, req.TenantID,
			"create_provider_account", id, chineseReason(req.Reason, "创建 provider account"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusCreated, map[string]int64{
			"id": id, "credential_id": credentialID, "credential_version": int64(credentialVersion),
		})
	}
}

func newUpdateProviderAccountEnabledHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req mutateProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		if err := d.Store.UpdateProviderAccountEnabled(r.Context(), db.UpdateProviderAccountEnabledParams{
			Enabled: req.Enabled, ActorID: &actorID, ID: id, TenantID: req.TenantID,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_update_failed", err.Error())
			return
		}
		action, reason := "enable_provider_account", "启用 provider account"
		if !req.Enabled {
			action, reason = "disable_provider_account", "禁用 provider account"
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": req.TenantID, "enabled": req.Enabled})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, req.TenantID,
			action, id, chineseReason(req.Reason, reason), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": req.Enabled})
	}
}

func newDeleteProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req mutateProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		if err := d.Store.SoftDeleteProviderAccount(r.Context(), db.SoftDeleteProviderAccountParams{
			ActorID: &actorID, ID: id, TenantID: req.TenantID,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_delete_failed", err.Error())
			return
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": req.TenantID, "deleted": true})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, req.TenantID,
			"delete_provider_account", id, chineseReason(req.Reason, "删除 provider account"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d AdminPoolAccountDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin pool account dependency unset")
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
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func validateCreateProviderAccount(req createProviderAccountRequest, requireCredentialV2 bool) error {
	if req.TenantID <= 0 || req.ProviderID <= 0 || req.ChannelID <= 0 || req.Name == "" {
		return fmt.Errorf("tenant_id, provider_id, channel_id, and name are required")
	}
	switch req.AccountType {
	case "oauth", "api_key", "service_account", "upstream_static", "session":
	default:
		return fmt.Errorf("account_type is invalid")
	}
	var obj map[string]json.RawMessage
	if len(req.Credentials) == 0 || json.Unmarshal(req.Credentials, &obj) != nil || obj == nil {
		return fmt.Errorf("credentials must be a JSON object")
	}
	if requireCredentialV2 {
		if req.Vendor == "" || req.AuthMode == "" {
			return fmt.Errorf("vendor and auth_mode are required for account_credentials")
		}
		handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(req.Vendor, req.AuthMode)
		if err != nil {
			return err
		}
		if err := handler.ValidatePayload(req.Credentials); err != nil {
			return err
		}
	}
	return nil
}

func parseAdminPoolID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func chineseReason(got, fallback string) *string {
	if reason := strings.TrimSpace(got); reason != "" {
		return &reason
	}
	return &fallback
}

func writeProviderAccountAudit(ctx context.Context, r *http.Request, store AdminPoolAccountStore, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	actorID := fmt.Sprintf("%d", ident.TokenID)
	reqID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, db.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}
