// Package accountsourcehttp 提供受授权租户使用的账号源和迁移包管理合同。
package accountsourcehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/accountbundle"
	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	"github.com/BloomingProsperity/HUAKAI/internal/accountsource/crs"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

const bodyLimit = accountbundle.MaxBundleBytes + 256<<10

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type CapabilityChecker interface {
	Require(context.Context, int64, tenantcapability.Capability) error
}

type CRSFetcher interface {
	Fetch(context.Context, crs.FetchInput) ([]accountsource.Item, map[string]any, error)
}

type SessionStore interface {
	Create(context.Context, accountsource.CreateInput) (accountsource.Session, error)
}

type SourceService interface {
	Plan(context.Context, accountsource.PlanInput) (accountsource.BatchPlan, error)
	Execute(context.Context, accountsource.ExecuteInput) (accountsource.BatchExecution, error)
}

type BundleExporter interface {
	Export(context.Context, int64, string, string, time.Duration) (accountbundle.ExportResult, error)
}

type StructureImporter interface {
	Plan(context.Context, accountbundle.StructurePlanInput) (accountbundle.StructurePlan, error)
	Execute(context.Context, accountbundle.StructureExecuteInput) (accountbundle.StructureExecution, error)
}

type AuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type Deps struct {
	Auth         AdminAuth
	Capabilities CapabilityChecker
	CRS          CRSFetcher
	Sessions     SessionStore
	Sources      SourceService
	Bundles      BundleExporter
	Structures   StructureImporter
	Audit        AuditStore
	Now          func() time.Time
}

type crsPreviewRequest struct {
	TenantID   int64                   `json:"tenant_id"`
	BaseURL    string                  `json:"base_url"`
	LoginPath  string                  `json:"login_path,omitempty"`
	ExportPath string                  `json:"export_path,omitempty"`
	Username   string                  `json:"username"`
	Password   string                  `json:"password"`
	Mappings   []accountsource.Mapping `json:"mappings"`
}

type sourceExecuteRequest struct {
	TenantID   int64                            `json:"tenant_id"`
	SessionID  string                           `json:"intake_session_id"`
	Mappings   []accountsource.Mapping          `json:"mappings"`
	Selections []accountsource.ExecuteSelection `json:"selections"`
	Reason     string                           `json:"reason"`
}

type bundleExportRequest struct {
	TenantID         int64  `json:"tenant_id"`
	Mode             string `json:"mode"`
	Passphrase       string `json:"passphrase,omitempty"`
	ExpiresInSeconds int64  `json:"expires_in_seconds,omitempty"`
	Confirmation     string `json:"confirmation,omitempty"`
	Reason           string `json:"reason"`
}

type bundlePlanRequest struct {
	TenantID     int64                   `json:"tenant_id"`
	Mode         string                  `json:"mode"`
	Bundle       json.RawMessage         `json:"bundle"`
	Passphrase   string                  `json:"passphrase,omitempty"`
	Confirmation string                  `json:"confirmation,omitempty"`
	Mappings     []accountsource.Mapping `json:"mappings"`
}

type bundleExecuteRequest struct {
	TenantID            int64                              `json:"tenant_id"`
	Mode                string                             `json:"mode"`
	Bundle              json.RawMessage                    `json:"bundle,omitempty"`
	SessionID           string                             `json:"intake_session_id,omitempty"`
	Confirmation        string                             `json:"confirmation,omitempty"`
	Mappings            []accountsource.Mapping            `json:"mappings"`
	Selections          []accountsource.ExecuteSelection   `json:"selections,omitempty"`
	StructureSelections []accountbundle.StructureSelection `json:"structure_selections,omitempty"`
	Reason              string                             `json:"reason"`
}

func Mount(r chi.Router, deps Deps) {
	r.Post("/crs-sync/preview", crsPreviewHandler(deps))
	r.Post("/crs-sync/execute", sourceExecuteHandler(deps, tenantcapability.CRSAccountSync, intake.SourceCRSSync))
	r.Post("/account-bundles/export", bundleExportHandler(deps))
	r.Post("/account-bundles/plan", bundlePlanHandler(deps))
	r.Post("/account-bundles/execute", bundleExecuteHandler(deps))
}

func crsPreviewHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request crsPreviewRequest
		if !decode(w, r, &request) || !scope(w, identity, request.TenantID) || !requireCapability(w, r, deps, request.TenantID, tenantcapability.CRSAccountSync) {
			request.Password = ""
			return
		}
		if deps.CRS == nil {
			request.Password = ""
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "CRS connector dependency unset")
			return
		}
		items, redactedContext, err := deps.CRS.Fetch(r.Context(), crs.FetchInput{
			BaseURL: request.BaseURL, LoginPath: request.LoginPath, ExportPath: request.ExportPath,
			Username: request.Username, Password: request.Password,
		})
		request.Password = ""
		if err != nil {
			writeError(w, err)
			return
		}
		defer accountsource.ZeroizeItems(items)
		session, err := deps.Sessions.Create(r.Context(), accountsource.CreateInput{
			TenantID: request.TenantID, SourceKind: "crs_sync", Items: items, RedactedContext: redactedContext,
			ActorID: identity.AuditActor(), ActorRole: identity.Role, RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		plan, err := deps.Sources.Plan(r.Context(), accountsource.PlanInput{TenantID: request.TenantID, SessionID: session.ID, Mappings: request.Mappings})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, plan)
	}
}

func sourceExecuteHandler(deps Deps, capability tenantcapability.Capability, expectedSource intake.SourceKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request sourceExecuteRequest
		if !decode(w, r, &request) || !scope(w, identity, request.TenantID) || !requireCapability(w, r, deps, request.TenantID, capability) {
			return
		}
		if len(strings.TrimSpace(request.Reason)) < 8 {
			writeJSONError(w, http.StatusBadRequest, "reason_required", "reason must contain at least 8 characters")
			return
		}
		result, err := deps.Sources.Execute(r.Context(), accountsource.ExecuteInput{
			PlanInput:      accountsource.PlanInput{TenantID: request.TenantID, SessionID: request.SessionID, Mappings: request.Mappings},
			ExpectedSource: expectedSource, Selections: request.Selections, ActorID: identity.AuditActor(), ActorRole: identity.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: request.Reason,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func bundleExportHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request bundleExportRequest
		if !decode(w, r, &request) || !scope(w, identity, request.TenantID) || !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundle) {
			request.Passphrase = ""
			return
		}
		if request.Mode == accountbundle.ModeRecovery {
			if !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundleSecret) {
				request.Passphrase = ""
				return
			}
			if !secretConfirmation(request.Confirmation) {
				request.Passphrase = ""
				writeJSONError(w, http.StatusBadRequest, "secret_confirmation_required", "explicit secret recovery confirmation is required")
				return
			}
		}
		if len(strings.TrimSpace(request.Reason)) < 8 {
			request.Passphrase = ""
			writeJSONError(w, http.StatusBadRequest, "reason_required", "reason must contain at least 8 characters")
			return
		}
		result, err := deps.Bundles.Export(r.Context(), request.TenantID, request.Mode, request.Passphrase, time.Duration(request.ExpiresInSeconds)*time.Second)
		request.Passphrase = ""
		if err != nil {
			writeError(w, err)
			return
		}
		payload, _ := json.Marshal(map[string]any{"bundle_id": result.BundleID, "mode": result.Mode, "account_count": result.AccountCount, "credential_count": result.CredentialCount})
		tenantID := request.TenantID
		_, err = deps.Audit.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: identity.AuditActor(), ActorRole: identity.Role,
			Action: "export_account_bundle", TargetType: "tenant", TargetID: &tenantID,
			RequestID: stringPointer(middleware.GetReqID(r.Context())), Reason: stringPointer(request.Reason), Payload: payload,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_failed", "bundle export audit could not be persisted")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		writeJSON(w, http.StatusOK, result)
	}
}

func bundlePlanHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request bundlePlanRequest
		if !decode(w, r, &request) || !scope(w, identity, request.TenantID) || !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundle) {
			request.Passphrase = ""
			return
		}
		now := nowTime(deps)
		if request.Mode == accountbundle.ModeStructure {
			manifest, err := accountbundle.DecodeStructure(request.Bundle, now)
			if err != nil {
				writeError(w, err)
				return
			}
			plan, err := deps.Structures.Plan(r.Context(), accountbundle.StructurePlanInput{TenantID: request.TenantID, Manifest: manifest, Mappings: request.Mappings})
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, plan)
			return
		}
		if request.Mode != accountbundle.ModeRecovery {
			request.Passphrase = ""
			writeJSONError(w, http.StatusBadRequest, "bundle_mode_invalid", "bundle mode is invalid")
			return
		}
		if !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundleSecret) {
			request.Passphrase = ""
			return
		}
		if !secretConfirmation(request.Confirmation) {
			request.Passphrase = ""
			writeJSONError(w, http.StatusBadRequest, "secret_confirmation_required", "explicit secret recovery confirmation is required")
			return
		}
		manifest, err := accountbundle.DecodeRecovery(request.Bundle, request.Passphrase, now)
		request.Bundle = nil
		request.Passphrase = ""
		if err != nil {
			writeError(w, err)
			return
		}
		defer accountbundle.ZeroizeManifest(&manifest)
		items, err := accountbundle.RecoveryItems(manifest)
		if err != nil {
			writeError(w, err)
			return
		}
		defer accountsource.ZeroizeItems(items)
		session, err := deps.Sessions.Create(r.Context(), accountsource.CreateInput{
			TenantID: request.TenantID, SourceKind: "account_bundle_recovery", Items: items,
			RedactedContext: map[string]any{"bundle_id": manifest.BundleID, "mode": manifest.Mode},
			ActorID:         identity.AuditActor(), ActorRole: identity.Role, RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		plan, err := deps.Sources.Plan(r.Context(), accountsource.PlanInput{TenantID: request.TenantID, SessionID: session.ID, Mappings: request.Mappings})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, plan)
	}
}

func bundleExecuteHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request bundleExecuteRequest
		if !decode(w, r, &request) || !scope(w, identity, request.TenantID) || !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundle) {
			return
		}
		if len(strings.TrimSpace(request.Reason)) < 8 {
			writeJSONError(w, http.StatusBadRequest, "reason_required", "reason must contain at least 8 characters")
			return
		}
		if request.Mode == accountbundle.ModeStructure {
			manifest, err := accountbundle.DecodeStructure(request.Bundle, nowTime(deps))
			if err != nil {
				writeError(w, err)
				return
			}
			result, err := deps.Structures.Execute(r.Context(), accountbundle.StructureExecuteInput{
				StructurePlanInput: accountbundle.StructurePlanInput{TenantID: request.TenantID, Manifest: manifest, Mappings: request.Mappings},
				Selections:         request.StructureSelections, ActorID: identity.AuditActor(), ActorRole: identity.Role,
				RequestID: middleware.GetReqID(r.Context()), Reason: request.Reason,
			})
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if request.Mode != accountbundle.ModeRecovery {
			writeJSONError(w, http.StatusBadRequest, "bundle_mode_invalid", "bundle mode is invalid")
			return
		}
		if !requireCapability(w, r, deps, request.TenantID, tenantcapability.AccountBundleSecret) {
			return
		}
		if !secretConfirmation(request.Confirmation) {
			writeJSONError(w, http.StatusBadRequest, "secret_confirmation_required", "explicit secret recovery confirmation is required")
			return
		}
		result, err := deps.Sources.Execute(r.Context(), accountsource.ExecuteInput{
			PlanInput:      accountsource.PlanInput{TenantID: request.TenantID, SessionID: request.SessionID, Mappings: request.Mappings},
			ExpectedSource: intake.SourceAccountRecovery, Selections: request.Selections, ActorID: identity.AuditActor(), ActorRole: identity.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: request.Reason,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolve(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Capabilities == nil || deps.Sessions == nil || deps.Sources == nil || deps.Bundles == nil || deps.Structures == nil || deps.Audit == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "account source dependency unset")
		return admin.AdminIdentity{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		return admin.AdminIdentity{}, false
	}
	if identity.Source == admin.AdminSourceSession || identity.Role != admin.RoleTenantOperator || identity.ScopeTenantID <= 0 {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "scoped tenant_operator token required")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func scope(w http.ResponseWriter, identity admin.AdminIdentity, tenantID int64) bool {
	if tenantID <= 0 || tenantID != identity.ScopeTenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope mismatch")
		return false
	}
	return true
}

func requireCapability(w http.ResponseWriter, r *http.Request, deps Deps, tenantID int64, capability tenantcapability.Capability) bool {
	if err := deps.Capabilities.Require(r.Context(), tenantID, capability); err != nil {
		if errors.Is(err, tenantcapability.ErrDenied) {
			writeJSONError(w, http.StatusForbidden, "capability_not_granted", "tenant capability is not granted")
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "tenant capability service is temporarily unavailable")
		}
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crs.ErrDisabled):
		writeJSONError(w, http.StatusForbidden, "crs_host_not_allowed", "CRS host is not enabled by the deployment administrator")
	case errors.Is(err, crs.ErrEndpointBlocked):
		writeJSONError(w, http.StatusBadRequest, "crs_endpoint_blocked", "CRS endpoint was blocked by network policy")
	case errors.Is(err, crs.ErrUpstreamRejected), errors.Is(err, crs.ErrResponseMalformed):
		writeJSONError(w, http.StatusUnprocessableEntity, "crs_upstream_invalid", "CRS login or export response was rejected")
	case errors.Is(err, accountsource.ErrSessionNotFound):
		writeJSONError(w, http.StatusNotFound, "intake_session_not_found", "account source intake session was not found")
	case errors.Is(err, accountsource.ErrSessionExpired):
		writeJSONError(w, http.StatusGone, "intake_session_expired", "account source intake session expired")
	case errors.Is(err, accountsource.ErrSessionClosed), errors.Is(err, accountsource.ErrSessionChanged), errors.Is(err, accountsource.ErrSessionSource):
		writeJSONError(w, http.StatusConflict, "intake_session_changed", "account source intake session changed")
	case errors.Is(err, accountbundle.ErrPassphraseWeak), errors.Is(err, accountbundle.ErrInvalidBundle):
		writeJSONError(w, http.StatusBadRequest, "account_bundle_invalid", "account bundle or passphrase is invalid")
	case errors.Is(err, accountbundle.ErrBundleExpired):
		writeJSONError(w, http.StatusGone, "account_bundle_expired", "account recovery bundle expired")
	case errors.Is(err, accountbundle.ErrSignatureMismatch):
		writeJSONError(w, http.StatusUnprocessableEntity, "account_bundle_signature_invalid", "account recovery bundle signature verification failed")
	case errors.Is(err, accountbundle.ErrRecoveryIncomplete):
		writeJSONError(w, http.StatusConflict, "account_bundle_recovery_incomplete", "every account must have an active credential; use a structure bundle for credential-free accounts")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "account_source_failed", "account source operation is temporarily unavailable")
	}
}

func secretConfirmation(value string) bool {
	return strings.TrimSpace(value) == "confirm_account_secret_transfer"
}

func nowTime(deps Deps) time.Time {
	if deps.Now != nil {
		return deps.Now().UTC()
	}
	return time.Now().UTC()
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
