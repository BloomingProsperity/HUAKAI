package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type adminPoolAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminPoolAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type adminPoolStoreStub struct {
	insertID int64
	insert   *db.InsertProviderAccountParams
	update   *db.UpdateProviderAccountEnabledParams
	delete   *db.SoftDeleteProviderAccountParams
	audits   []db.InsertAdminAuditEventParams
}

func (s *adminPoolStoreStub) InsertProviderAccount(_ context.Context, arg db.InsertProviderAccountParams) (int64, error) {
	s.insert = &arg
	if s.insertID == 0 {
		return 101, nil
	}
	return s.insertID, nil
}

func (s *adminPoolStoreStub) UpdateProviderAccountEnabled(_ context.Context, arg db.UpdateProviderAccountEnabledParams) error {
	s.update = &arg
	return nil
}

func (s *adminPoolStoreStub) SoftDeleteProviderAccount(_ context.Context, arg db.SoftDeleteProviderAccountParams) error {
	s.delete = &arg
	return nil
}

func (s *adminPoolStoreStub) InsertAdminAuditEvent(_ context.Context, arg db.InsertAdminAuditEventParams) (db.InsertAdminAuditEventRow, error) {
	s.audits = append(s.audits, arg)
	return db.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

func TestAdminPoolAccounts_CreateHappyPathInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodPost, "/v1/admin/pool-accounts",
		`{"tenant_id":7,"provider_id":8,"channel_id":9,"name":" acct ","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.ProviderID != 8 || store.insert.ChannelID != 9 {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if store.insert.Name != "acct" || store.insert.AccountType != "api_key" || string(store.insert.Credentials) == "" {
		t.Fatalf("insert account fields mismatch: %+v", store.insert)
	}
}

func TestAdminPoolAccounts_CreateSessionTypeInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 88}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodPost, "/v1/admin/pool-accounts",
		`{"tenant_id":7,"provider_id":8,"channel_id":9,"name":"cursor-sub","account_type":"session","credentials":{"session_token":"sess-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil {
		t.Fatal("InsertProviderAccount was not called")
	}
	if store.insert.AccountType != "session" {
		t.Fatalf("AccountType=%q want session", store.insert.AccountType)
	}
}

func TestAdminPoolAccounts_CreateWritesAuditEventWithoutCredentialBytes(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodPost, "/v1/admin/pool-accounts",
		`{"tenant_id":7,"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit count=%d want 1", len(store.audits))
	}
	a := store.audits[0]
	if a.Action != "create_provider_account" || a.TargetType != "provider_account" || *a.TargetID != 77 {
		t.Fatalf("audit target mismatch: %+v", a)
	}
	if a.Reason == nil || *a.Reason != "创建 provider account" {
		t.Fatalf("audit reason=%v", a.Reason)
	}
	if strings.Contains(string(a.Payload), "sk-live") {
		t.Fatalf("audit payload leaked credential: %s", string(a.Payload))
	}
}

func TestAdminPoolAccounts_Unauthorized(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodPost,
		"/v1/admin/pool-accounts", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("unauthorized request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_NonAdminForbidden(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator}}, http.MethodPost,
		"/v1/admin/pool-accounts", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("non-admin request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_DisableUpdatesAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodPatch, "/v1/admin/pool-accounts/77/enabled",
		`{"tenant_id":7,"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != 7 || store.update.Enabled {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "disable_provider_account" ||
		store.audits[0].Reason == nil || *store.audits[0].Reason != "禁用 provider account" {
		t.Fatalf("disable audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_DeleteSoftDeletesAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodDelete, "/v1/admin/pool-accounts/77",
		`{"tenant_id":7}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.delete == nil || store.delete.ID != 77 || store.delete.TenantID != 7 {
		t.Fatalf("delete params mismatch: %+v", store.delete)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "delete_provider_account" ||
		store.audits[0].Reason == nil || *store.audits[0].Reason != "删除 provider account" {
		t.Fatalf("delete audit mismatch: %+v", store.audits)
	}
}

func adminPoolAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

func invokeAdminPool(t *testing.T, store *adminPoolStoreStub, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountAdminPoolAccountRoutes(r, AdminPoolAccountDeps{Auth: auth, Store: store})
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
