package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestAdminCredentialsHandlersHappyPath(t *testing.T) {
	t.Run("list account credentials", func(t *testing.T) {
		store := &adminCredentialStoreStub{
			listRows: []credentialstore.CredentialMetadata{
				adminCredentialMeta(201, 7, 77, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, 1),
			},
		}
		audit := &adminPoolStoreStub{}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodGet, "/admin/v1/provider-accounts/77/credentials?tenant_id=7", "")
		assertStatus(t, rec, http.StatusOK)
		if store.listTenantID != 7 || store.listAccountID != 77 {
			t.Fatalf("list scope mismatch: tenant=%d account=%d", store.listTenantID, store.listAccountID)
		}
		if len(audit.audits) != 1 || audit.audits[0].Action != "list_account_credentials" {
			t.Fatalf("list audit mismatch: %+v", audit.audits)
		}
		var body struct {
			Credentials []credentialstore.CredentialMetadata `json:"credentials"`
		}
		decodeAdminCredentialBody(t, rec, &body)
		if len(body.Credentials) != 1 || body.Credentials[0].ID != 201 {
			t.Fatalf("list body mismatch: %+v", body)
		}
	})

	t.Run("create account credential", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodPost, "/admin/v1/provider-accounts/77/credentials",
			`{"tenant_id":7,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"},"reason":"initial load"}`)
		assertStatus(t, rec, http.StatusCreated)
		if store.createInput == nil || store.createInput.TenantID != 7 || store.createInput.ProviderAccountID != 77 ||
			store.createInput.Vendor != credentialstore.VendorOpenAI || store.createInput.AuthMode != credentialstore.AuthModeAPIKey {
			t.Fatalf("create input mismatch: %+v", store.createInput)
		}
		if len(audit.audits) != 1 || audit.audits[0].Action != "create_account_credential" {
			t.Fatalf("create audit mismatch: %+v", audit.audits)
		}
		if strings.Contains(string(audit.audits[0].Payload), "sk-live") {
			t.Fatalf("create audit leaked credential: %s", string(audit.audits[0].Payload))
		}
	})

	t.Run("rotate account credential", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodPost, "/admin/v1/provider-accounts/77/credentials/201/rotate",
			`{"tenant_id":7,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-rotated"},"reason":"rotation"}`)
		assertStatus(t, rec, http.StatusOK)
		if store.rotateInput == nil || store.rotateInput.TenantID != 7 || store.rotateInput.ProviderAccountID != 77 ||
			store.rotateInput.CredentialID != 201 {
			t.Fatalf("rotate input mismatch: %+v", store.rotateInput)
		}
		if len(audit.audits) != 1 || audit.audits[0].Action != "rotate_account_credential" {
			t.Fatalf("rotate audit mismatch: %+v", audit.audits)
		}
		if strings.Contains(string(audit.audits[0].Payload), "sk-rotated") {
			t.Fatalf("rotate audit leaked credential: %s", string(audit.audits[0].Payload))
		}
	})

	t.Run("set account credential state", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodPatch, "/admin/v1/provider-accounts/77/credentials/201/state",
			`{"tenant_id":7,"state":"revoked","reason":"disable compromised key"}`)
		assertStatus(t, rec, http.StatusOK)
		if !store.setStateCalled || store.setTenantID != 7 || store.setAccountID != 77 ||
			store.setCredentialID != 201 || store.setState != credentialstore.StateRevoked {
			t.Fatalf("state input mismatch: %+v", store)
		}
		if len(audit.audits) != 1 || audit.audits[0].Action != "disable_account_credential" {
			t.Fatalf("state audit mismatch: %+v", audit.audits)
		}
	})

	t.Run("delete account credential", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodDelete, "/admin/v1/provider-accounts/77/credentials/201",
			`{"tenant_id":7,"reason":"operator cleanup"}`)
		assertStatus(t, rec, http.StatusOK)
		if !store.deleteCalled || store.deleteTenantID != 7 || store.deleteAccountID != 77 || store.deleteCredentialID != 201 {
			t.Fatalf("delete input mismatch: %+v", store)
		}
		if len(audit.audits) != 1 || audit.audits[0].Action != "delete_account_credential" {
			t.Fatalf("delete audit mismatch: %+v", audit.audits)
		}
	})
}

func TestAdminCredentialsHandlersUnauthorized(t *testing.T) {
	for _, tc := range adminCredentialHandlerCases() {
		t.Run(tc.name, func(t *testing.T) {
			store := &adminCredentialStoreStub{}
			audit := &adminPoolStoreStub{}
			rec := invokeAdminCredentials(t, AdminCredentialDeps{
				Auth: adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, Credentials: store, AuditStore: audit,
			}, tc.method, tc.target, tc.body)
			assertStatus(t, rec, http.StatusUnauthorized)
			assertAdminCredentialStoreUntouched(t, store, audit)
		})
	}
}

func TestAdminCredentialsHandlersForbidden(t *testing.T) {
	for _, tc := range adminCredentialHandlerCases() {
		t.Run(tc.name, func(t *testing.T) {
			store := &adminCredentialStoreStub{}
			audit := &adminPoolStoreStub{}
			rec := invokeAdminCredentials(t, AdminCredentialDeps{
				Auth: providerAccountAdmin(), Credentials: store, AuditStore: audit,
			}, tc.method, tc.target, tc.body)
			assertStatus(t, rec, http.StatusForbidden)
			assertAdminCredentialStoreUntouched(t, store, audit)
		})
	}
}

func TestAdminCredentialsHandlersInvalidRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "list invalid account id", method: http.MethodGet, target: "/admin/v1/provider-accounts/not-int/credentials?tenant_id=7"},
		{name: "create invalid json", method: http.MethodPost, target: "/admin/v1/provider-accounts/77/credentials", body: `{"tenant_id":`},
		{name: "rotate invalid credential id", method: http.MethodPost, target: "/admin/v1/provider-accounts/77/credentials/nope/rotate", body: `{"tenant_id":7}`},
		{name: "state missing tenant", method: http.MethodPatch, target: "/admin/v1/provider-accounts/77/credentials/201/state", body: `{"state":"revoked"}`},
		{name: "delete missing tenant", method: http.MethodDelete, target: "/admin/v1/provider-accounts/77/credentials/201", body: `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &adminCredentialStoreStub{}
			audit := &adminPoolStoreStub{}
			rec := invokeAdminCredentials(t, AdminCredentialDeps{
				Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
			}, tc.method, tc.target, tc.body)
			assertStatus(t, rec, http.StatusBadRequest)
			assertAdminCredentialStoreUntouched(t, store, audit)
		})
	}
}

type adminCredentialRouteCase struct {
	name   string
	method string
	target string
	body   string
}

func adminCredentialHandlerCases() []adminCredentialRouteCase {
	return []adminCredentialRouteCase{
		{name: "list", method: http.MethodGet, target: "/admin/v1/provider-accounts/77/credentials?tenant_id=7"},
		{name: "create", method: http.MethodPost, target: "/admin/v1/provider-accounts/77/credentials", body: `{"tenant_id":7,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`},
		{name: "rotate", method: http.MethodPost, target: "/admin/v1/provider-accounts/77/credentials/201/rotate", body: `{"tenant_id":7,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`},
		{name: "state", method: http.MethodPatch, target: "/admin/v1/provider-accounts/77/credentials/201/state", body: `{"tenant_id":7,"state":"revoked"}`},
		{name: "delete", method: http.MethodDelete, target: "/admin/v1/provider-accounts/77/credentials/201", body: `{"tenant_id":7}`},
	}
}

type adminCredentialStoreStub struct {
	listRows []credentialstore.CredentialMetadata

	createInput *credentialstore.CreateCredentialInput
	rotateInput *credentialstore.RotateCredentialInput

	listTenantID  int64
	listAccountID int64
	listCalls     int

	setStateCalled  bool
	setTenantID     int64
	setAccountID    int64
	setCredentialID int64
	setState        string
	setActorID      string

	deleteCalled       bool
	deleteTenantID     int64
	deleteAccountID    int64
	deleteCredentialID int64
	deleteActorID      string
}

func (s *adminCredentialStoreStub) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	cp := in
	cp.Payload = append([]byte(nil), in.Payload...)
	s.createInput = &cp
	return adminCredentialMeta(301, in.TenantID, in.ProviderAccountID, in.Vendor, in.AuthMode, 1), nil
}

func (s *adminCredentialStoreStub) Rotate(_ context.Context, in credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error) {
	cp := in
	cp.Payload = append([]byte(nil), in.Payload...)
	s.rotateInput = &cp
	return adminCredentialMeta(in.CredentialID, in.TenantID, in.ProviderAccountID, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, 2), nil
}

func (s *adminCredentialStoreStub) ListByAccount(_ context.Context, tenantID, accountID int64) ([]credentialstore.CredentialMetadata, error) {
	s.listCalls++
	s.listTenantID = tenantID
	s.listAccountID = accountID
	return s.listRows, nil
}

func (s *adminCredentialStoreStub) SetState(_ context.Context, tenantID, accountID, credentialID int64, state, actorID string) error {
	s.setStateCalled = true
	s.setTenantID = tenantID
	s.setAccountID = accountID
	s.setCredentialID = credentialID
	s.setState = state
	s.setActorID = actorID
	return nil
}

func (s *adminCredentialStoreStub) Delete(_ context.Context, tenantID, accountID, credentialID int64, actorID string) error {
	s.deleteCalled = true
	s.deleteTenantID = tenantID
	s.deleteAccountID = accountID
	s.deleteCredentialID = credentialID
	s.deleteActorID = actorID
	return nil
}

func invokeAdminCredentials(t *testing.T, deps AdminCredentialDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountAdminCredentialRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeAdminCredentialBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertAdminCredentialStoreUntouched(t *testing.T, store *adminCredentialStoreStub, audit *adminPoolStoreStub) {
	t.Helper()
	if store.createInput != nil || store.rotateInput != nil || store.listCalls != 0 ||
		store.setStateCalled || store.deleteCalled || len(audit.audits) != 0 {
		t.Fatalf("request touched store: store=%+v audits=%+v", store, audit.audits)
	}
}

func adminCredentialMeta(id, tenantID, accountID int64, vendor, authMode string, version int32) credentialstore.CredentialMetadata {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	return credentialstore.CredentialMetadata{
		ID: id, TenantID: tenantID, ProviderAccountID: accountID,
		Vendor: credentialstore.Normalize(vendor), AuthMode: credentialstore.Normalize(authMode),
		State: credentialstore.StateActive, Version: version,
		CreatedAt: now, UpdatedAt: now,
	}
}
