package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type ProviderAccountTestDeps struct {
	Auth     providerAccountTestAuth
	Accounts providerAccountTestAccountStore
	Tester   ProviderAccountCredentialTester
	Now      func() time.Time
}

type providerAccountTestAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountTestAccountStore interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type ProviderAccountCredentialTester interface {
	TestProviderAccountCredential(context.Context, int64, int64, time.Time, string) (credentialworker.ProviderAccountCredentialTestResult, error)
}

type providerAccountCredentialTester struct {
	store    credentialworker.ProviderAccountCredentialTestStore
	registry *credentialworker.ModeAdapterRegistry
}

func NewProviderAccountCredentialTester(store credentialworker.ProviderAccountCredentialTestStore, registry *credentialworker.ModeAdapterRegistry) ProviderAccountCredentialTester {
	return providerAccountCredentialTester{store: store, registry: registry}
}

func (t providerAccountCredentialTester) TestProviderAccountCredential(ctx context.Context, tenantID, accountID int64, now time.Time, probeModel string) (credentialworker.ProviderAccountCredentialTestResult, error) {
	return credentialworker.DryRunProviderAccountCredentialWithProbeModel(ctx, t.store, t.registry, tenantID, accountID, now, probeModel)
}

func MountProviderAccountTestRoutes(r chi.Router, d ProviderAccountTestDeps) {
	r.Post("/{id}/test", newProviderAccountTestHandler(d))
}

type providerAccountTestResponseBody struct {
	OK         bool    `json:"ok"`
	ErrorClass *string `json:"error_class"`
	Message    string  `json:"message"`
}

func newProviderAccountTestHandler(d ProviderAccountTestDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountTestTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := parseProviderAccountTestID(w, r)
		if !ok {
			return
		}
		account, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{
			ID: id, TenantID: tenantID,
		})
		if err != nil {
			writeProviderAccountTestReadError(w, err, "provider_account_get_failed")
			return
		}
		now := time.Now
		if d.Now != nil {
			now = d.Now
		}
		probeModel := ""
		if account.ProbeModel != nil {
			probeModel = strings.TrimSpace(*account.ProbeModel)
		}
		result, err := d.Tester.TestProviderAccountCredential(r.Context(), tenantID, id, now(), probeModel)
		if auditErr := writeProviderAccountTestAudit(r.Context(), r, d.Accounts, ident, tenantID, id, result, err); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_write_failed", "provider account credential test audit failed")
			return
		}
		if err != nil {
			writeProviderAccountTestReadError(w, err, "provider_account_test_failed")
			return
		}
		var errorClass *string
		if result.ErrorClass != "" {
			errorClass = &result.ErrorClass
		}
		message := result.Message
		if message == "" {
			message = "credential validation completed"
		}
		writeProviderAccountTestJSON(w, http.StatusOK, providerAccountTestResponseBody{
			OK: result.OK, ErrorClass: errorClass, Message: message,
		})
	}
}

func resolveProviderAccountTestTenant(w http.ResponseWriter, r *http.Request, d ProviderAccountTestDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Accounts == nil || d.Tester == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account test dependency unset")
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

func parseProviderAccountTestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeProviderAccountTestReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, credentialstore.ErrCredentialNotFound) {
		writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, code, "provider account credential validation is unavailable")
}

func writeProviderAccountTestAudit(ctx context.Context, r *http.Request, store providerAccountTestAccountStore, ident admin.AdminIdentity, tenantID, accountID int64, result credentialworker.ProviderAccountCredentialTestResult, testErr error) error {
	actorID := fmt.Sprintf("%d", ident.TokenID)
	reqID := middleware.GetReqID(r.Context())
	reason := "测试 provider account credential"
	errorClass := result.ErrorClass
	if testErr != nil && errorClass == "" {
		errorClass = "temporary"
	}
	payloadData := map[string]any{
		"tenant_id": tenantID,
		"operation": "provider_account_credential_test",
		"dry_run":   true,
		"ok":        result.OK && testErr == nil,
	}
	if errorClass != "" {
		payloadData["error_class"] = errorClass
	}
	if testErr != nil {
		payloadData["result"] = "unavailable"
	} else {
		payloadData["result"] = "completed"
	}
	payload, err := json.Marshal(payloadData)
	if err != nil {
		return err
	}
	_, err = store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: "test_provider_account", TargetType: "provider_account", TargetID: &accountID,
		RequestID: &reqID, Reason: &reason, Payload: payload,
	})
	return err
}

func writeProviderAccountTestJSON(w http.ResponseWriter, status int, body providerAccountTestResponseBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
