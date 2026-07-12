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
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestAdminCredentialsHandlersHappyPath(t *testing.T) {
	t.Run("list account credentials", func(t *testing.T) {
		projectRef := "project-visible"
		meta := adminCredentialMeta(201, 7, 77, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, 1)
		meta.ProjectRef = &projectRef
		store := &adminCredentialStoreStub{
			listRows: []credentialstore.CredentialMetadata{meta},
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
		if len(body.Credentials) != 1 || body.Credentials[0].ID != 201 || body.Credentials[0].ProjectRef == nil || *body.Credentials[0].ProjectRef != projectRef {
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

func TestAdminCredentialRenewStatusPlatformAdminAllTenants(t *testing.T) {
	store := &adminCredentialStoreStub{
		renewRows: []credentialstore.RenewStatusMetadata{
			adminCredentialRenewRow(301, 7, "tenant-a", 77, "acct-a"),
			adminCredentialRenewRow(302, 8, "tenant-b", 88, "acct-b"),
		},
	}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status")
	assertStatus(t, rec, http.StatusOK)
	if store.renewCalls != 1 || store.renewParams.TenantID != nil {
		t.Fatalf("renew scope mismatch: calls=%d params=%+v", store.renewCalls, store.renewParams)
	}
	var body struct {
		Items      []credentialstore.RenewStatusMetadata `json:"items"`
		NextCursor *string                               `json:"next_cursor"`
	}
	decodeAdminCredentialBody(t, rec, &body)
	if len(body.Items) != 2 || body.Items[0].CredentialID != 301 || body.Items[1].CredentialID != 302 ||
		body.Items[0].TenantName != "tenant-a" || body.Items[1].TenantName != "tenant-b" {
		t.Fatalf("renew body mismatch: %+v", body)
	}
	if len(audit.audits) != 1 || audit.audits[0].Action != "list_account_credentials" ||
		audit.audits[0].TargetType != "account_credential" || audit.audits[0].TargetID != nil ||
		audit.audits[0].TenantID != nil {
		t.Fatalf("renew audit mismatch: %+v", audit.audits)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.audits[0].Payload, &payload); err != nil {
		t.Fatalf("decode audit payload: %v", err)
	}
	if payload["scope"] != "all" || payload["count"].(float64) != 2 {
		t.Fatalf("renew audit payload mismatch: %+v", payload)
	}
}

func TestAdminCredentialRenewStatusPlatformAdminQueryTenantFiltersAndAudits(t *testing.T) {
	store := &adminCredentialStoreStub{
		renewRows: []credentialstore.RenewStatusMetadata{
			adminCredentialRenewRow(301, 7, "tenant-a", 77, "acct-a"),
			adminCredentialRenewRow(302, 8, "tenant-b", 88, "acct-b"),
		},
	}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status?tenant_id=8")
	assertStatus(t, rec, http.StatusOK)
	if store.renewCalls != 1 || store.renewParams.TenantID == nil || *store.renewParams.TenantID != 8 {
		t.Fatalf("renew tenant query scope mismatch: calls=%d params=%+v", store.renewCalls, store.renewParams)
	}
	var body struct {
		Items []credentialstore.RenewStatusMetadata `json:"items"`
	}
	decodeAdminCredentialBody(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].TenantID != 8 {
		t.Fatalf("platform admin tenant query must filter rows: %+v", body.Items)
	}
	if len(audit.audits) != 1 || audit.audits[0].TenantID == nil || *audit.audits[0].TenantID != 8 {
		t.Fatalf("tenant scoped audit mismatch: %+v", audit.audits)
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.audits[0].Payload, &payload); err != nil {
		t.Fatalf("decode audit payload: %v", err)
	}
	if payload["scope"].(float64) != 8 || payload["count"].(float64) != 1 {
		t.Fatalf("renew audit payload mismatch: %+v", payload)
	}
}

func TestAdminCredentialRenewStatusTenantOperatorSeesOnlyScope(t *testing.T) {
	store := &adminCredentialStoreStub{
		renewRows: []credentialstore.RenewStatusMetadata{
			adminCredentialRenewRow(301, 7, "tenant-a", 77, "acct-a"),
			adminCredentialRenewRow(302, 8, "tenant-b", 88, "acct-b"),
		},
	}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: providerAccountAdmin(), Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status")
	assertStatus(t, rec, http.StatusOK)
	if store.renewParams.TenantID == nil || *store.renewParams.TenantID != 7 {
		t.Fatalf("renew tenant scope mismatch: %+v", store.renewParams)
	}
	var body struct {
		Items []credentialstore.RenewStatusMetadata `json:"items"`
	}
	decodeAdminCredentialBody(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].TenantID != 7 {
		t.Fatalf("tenant operator must see only own tenant rows: %+v", body.Items)
	}
	for _, item := range body.Items {
		if item.TenantID == 8 {
			t.Fatalf("tenant operator saw another tenant row: %+v", body.Items)
		}
	}
	if len(audit.audits) != 1 || audit.audits[0].TenantID == nil || *audit.audits[0].TenantID != 7 {
		t.Fatalf("tenant scoped audit mismatch: %+v", audit.audits)
	}
}

func TestAdminCredentialRenewStatusForbiddenRoles(t *testing.T) {
	store := &adminCredentialStoreStub{}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth:        adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 12, Role: "viewer"}},
		Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status")
	assertStatus(t, rec, http.StatusForbidden)
	assertAdminCredentialStoreUntouched(t, store, audit)
}

func TestAdminCredentialRenewStatusTenantOperatorCrossTenantQueryForbidden(t *testing.T) {
	store := &adminCredentialStoreStub{}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: providerAccountAdmin(), Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status?tenant_id=8")
	assertStatus(t, rec, http.StatusForbidden)
	assertAdminCredentialStoreUntouched(t, store, audit)
}

func TestAdminCredentialRenewStatusInvalidTenantQuery(t *testing.T) {
	store := &adminCredentialStoreStub{}
	audit := &adminPoolStoreStub{}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
	}, "/admin/v1/credentials/renew-status?tenant_id=0")
	assertStatus(t, rec, http.StatusBadRequest)
	assertAdminCredentialStoreUntouched(t, store, audit)
}

func TestAdminCredentialRenewStatusCursorPagination(t *testing.T) {
	first := adminCredentialRenewRow(303, 7, "tenant-a", 77, "acct-a")
	second := adminCredentialRenewRow(302, 7, "tenant-a", 78, "acct-b")
	third := adminCredentialRenewRow(301, 7, "tenant-a", 79, "acct-c")
	second.UpdatedAt = first.UpdatedAt.Add(-time.Minute)
	third.UpdatedAt = second.UpdatedAt.Add(-time.Minute)

	store := &adminCredentialStoreStub{renewRows: []credentialstore.RenewStatusMetadata{first, second, third}}
	rec := invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: providerAccountAdmin(), Credentials: store, AuditStore: &adminPoolStoreStub{},
	}, "/admin/v1/credentials/renew-status?limit=2")
	assertStatus(t, rec, http.StatusOK)
	if store.renewParams.Limit != 3 {
		t.Fatalf("handler must request one extra row for pagination, got limit=%d", store.renewParams.Limit)
	}
	var body struct {
		Items      []credentialstore.RenewStatusMetadata `json:"items"`
		NextCursor *string                               `json:"next_cursor"`
	}
	decodeAdminCredentialBody(t, rec, &body)
	if len(body.Items) != 2 || body.NextCursor == nil {
		t.Fatalf("pagination first page mismatch: %+v", body)
	}

	nextStore := &adminCredentialStoreStub{}
	rec = invokeAdminCredentialRenewStatus(t, AdminCredentialDeps{
		Auth: providerAccountAdmin(), Credentials: nextStore, AuditStore: &adminPoolStoreStub{},
	}, "/admin/v1/credentials/renew-status?limit=2&cursor="+*body.NextCursor)
	assertStatus(t, rec, http.StatusOK)
	if nextStore.renewParams.CursorID != second.CredentialID || !nextStore.renewParams.CursorUpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("cursor params mismatch: %+v want updated_at=%s id=%d", nextStore.renewParams, second.UpdatedAt.Format(time.RFC3339), second.CredentialID)
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
	listRows  []credentialstore.CredentialMetadata
	renewRows []credentialstore.RenewStatusMetadata

	createInput *credentialstore.CreateCredentialInput
	rotateInput *credentialstore.RotateCredentialInput

	listTenantID  int64
	listAccountID int64
	listCalls     int
	renewParams   credentialstore.ListRenewStatusParams
	renewCalls    int

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

func (s *adminCredentialStoreStub) ListRenewStatus(_ context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
	s.renewCalls++
	s.renewParams = params
	if params.TenantID == nil {
		return s.renewRows, nil
	}
	out := make([]credentialstore.RenewStatusMetadata, 0, len(s.renewRows))
	for _, row := range s.renewRows {
		if row.TenantID == *params.TenantID {
			out = append(out, row)
		}
	}
	return out, nil
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

func invokeAdminCredentialRenewStatus(t *testing.T, deps AdminCredentialDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		MountAdminCredentialRenewStatusRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
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
		store.renewCalls != 0 || store.setStateCalled || store.deleteCalled || len(audit.audits) != 0 {
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

func adminCredentialRenewRow(id, tenantID int64, tenantName string, accountID int64, accountName string) credentialstore.RenewStatusMetadata {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	return credentialstore.RenewStatusMetadata{
		CredentialID: id, TenantID: tenantID, TenantName: tenantName,
		AccountID: accountID, AccountName: accountName,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
		State: credentialstore.StateActive, CredentialVersion: 1,
		FailureCount: 0, UpdatedAt: now,
	}
}

// TestCreateAccountCredentialProtocolGuard 咬住 credential-create 守卫(收敛 G1 account-first
// + R1A):给一个 anthropic_messages family 的账号加 openai/api_key 凭据(跨厂错配)必须被拒;
// 正配(anthropic/api_key)放行。变异:去掉 handler 里 ValidateProtocolCompatibility 调用 →
// 错配用例 201 而非 400,本测试红。
func TestCreateAccountCredentialProtocolGuard(t *testing.T) {
	acctRow := &admindb.AdminProviderAccountRow{ID: 77, TenantID: 7, ProviderID: 8, AccountType: "api_key"}

	t.Run("跨厂错配凭据被拒", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{get: acctRow, providerFamilies: map[int64]string{8: "anthropic_messages"}}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodPost, "/admin/v1/provider-accounts/77/credentials",
			`{"tenant_id":7,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-openai"}}`)
		assertStatus(t, rec, http.StatusBadRequest)
		if store.createInput != nil {
			t.Fatal("错配凭据不应进入 Create")
		}
	})

	t.Run("正配凭据放行", func(t *testing.T) {
		store := &adminCredentialStoreStub{}
		audit := &adminPoolStoreStub{get: acctRow, providerFamilies: map[int64]string{8: "anthropic_messages"}}
		rec := invokeAdminCredentials(t, AdminCredentialDeps{
			Auth: adminPoolAdmin(), Credentials: store, AuditStore: audit,
		}, http.MethodPost, "/admin/v1/provider-accounts/77/credentials",
			`{"tenant_id":7,"vendor":"anthropic","auth_mode":"api_key","credentials":{"api_key":"sk-ant"}}`)
		assertStatus(t, rec, http.StatusCreated)
		if store.createInput == nil {
			t.Fatal("正配凭据应进入 Create")
		}
	})
}
