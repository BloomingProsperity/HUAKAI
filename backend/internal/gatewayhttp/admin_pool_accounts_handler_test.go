package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type adminPoolAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminPoolAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type adminPoolStoreStub struct {
	insertID   int64
	insert     *admindb.InsertProviderAccountParams
	listArg    *admindb.ListAdminProviderAccountsParams
	list       []admindb.AdminProviderAccountRow
	getArg     *admindb.GetAdminProviderAccountParams
	get        *admindb.AdminProviderAccountRow
	updateFull *admindb.UpdateAdminProviderAccountParams
	update     *admindb.UpdateProviderAccountEnabledParams
	clear      *admindb.ClearProviderAccountRateLimitParams
	delete     *admindb.SoftDeleteProviderAccountParams
	audits     []admindb.InsertAdminAuditEventParams
}

type adminPoolCredentialWriterStub struct {
	input *credentialstore.CreateCredentialInput
	id    int64
}

type adminPoolChannelHealthStub struct {
	key *channelhealth.ChannelKey
}

func (s *adminPoolChannelHealthStub) EnsureDefaultActive(_ context.Context, key channelhealth.ChannelKey) (channelhealth.Record, error) {
	s.key = &key
	return channelhealth.Record{Key: key, State: channelhealth.StateActive}, nil
}

func (s *adminPoolCredentialWriterStub) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	s.input = &in
	id := s.id
	if id == 0 {
		id = 9001
	}
	return credentialstore.CredentialMetadata{
		ID: id, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: credentialstore.Normalize(in.Vendor), AuthMode: credentialstore.Normalize(in.AuthMode),
		State: credentialstore.StateActive, Version: 1,
	}, nil
}

func (s *adminPoolStoreStub) InsertProviderAccount(_ context.Context, arg admindb.InsertProviderAccountParams) (int64, error) {
	s.insert = &arg
	if s.insertID == 0 {
		return 101, nil
	}
	return s.insertID, nil
}

func (s *adminPoolStoreStub) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	s.listArg = &arg
	return s.list, nil
}

func (s *adminPoolStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.getArg = &arg
	if s.get != nil {
		return *s.get, nil
	}
	id := arg.ID
	if id == 0 {
		id = s.insertID
	}
	if id == 0 {
		id = 101
	}
	if s.insert != nil {
		return adminProviderRowFromInsert(id, *s.insert), nil
	}
	return adminProviderRow(id, arg.TenantID), nil
}

func (s *adminPoolStoreStub) UpdateAdminProviderAccount(_ context.Context, arg admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.updateFull = &arg
	row := adminProviderRow(arg.ID, arg.TenantID)
	if arg.Enabled != nil {
		row.Enabled = *arg.Enabled
	}
	if arg.Priority != nil {
		row.Priority = *arg.Priority
	}
	if arg.CapConcurrency != nil {
		row.CapConcurrency = *arg.CapConcurrency
	}
	if arg.SetModelAllowList {
		row.ModelAllowList = arg.ModelAllowList
	}
	if arg.SetCapabilityFlags {
		row.CapabilityFlags = arg.CapabilityFlags
	}
	if arg.CustomErrorCodesEnabled != nil {
		row.CustomErrorCodesEnabled = *arg.CustomErrorCodesEnabled
	}
	if arg.SetCustomErrorCodes {
		row.CustomErrorCodes = arg.CustomErrorCodes
	}
	if arg.PoolMode != nil {
		row.PoolMode = *arg.PoolMode
	}
	if arg.TempUnschedulableEnabled != nil {
		row.TempUnschedulableEnabled = *arg.TempUnschedulableEnabled
	}
	return row, nil
}

func (s *adminPoolStoreStub) UpdateProviderAccountEnabled(_ context.Context, arg admindb.UpdateProviderAccountEnabledParams) error {
	s.update = &arg
	return nil
}

func (s *adminPoolStoreStub) ClearProviderAccountRateLimit(_ context.Context, arg admindb.ClearProviderAccountRateLimitParams) (admindb.AdminProviderAccountRow, error) {
	s.clear = &arg
	return adminProviderRow(arg.ID, arg.TenantID), nil
}

func (s *adminPoolStoreStub) SoftDeleteProviderAccount(_ context.Context, arg admindb.SoftDeleteProviderAccountParams) error {
	s.delete = &arg
	return nil
}

func (s *adminPoolStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.audits = append(s.audits, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

func TestAdminPoolAccounts_CreateHappyPathInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":" acct ","account_type":"api_key","credentials":{"api_key":"sk-live"},"cap_concurrency":3,"priority":10,"model_allow_list":[" gpt-4o "],"capability_flags":["chat"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.ProviderID != 8 || store.insert.ChannelID != 9 {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if store.insert.Name != "acct" || store.insert.AccountType != "api_key" || string(store.insert.Credentials) == "" {
		t.Fatalf("insert account fields mismatch: %+v", store.insert)
	}
	if store.insert.CapConcurrency == nil || *store.insert.CapConcurrency != 3 || store.insert.Priority == nil || *store.insert.Priority != 10 {
		t.Fatalf("insert capacity fields mismatch: %+v", store.insert)
	}
	if got := store.insert.ModelAllowList; len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("model_allow_list=%v", got)
	}
}

func TestAdminPoolAccounts_CreateSessionTypeInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 88}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"cursor-sub","account_type":"session","credentials":{"session_token":"sess-live"}}`)
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
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
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

func TestAdminPoolAccounts_CreateWithCredentialV2StoresEmptyLegacyJSON(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if string(store.insert.Credentials) != "{}" {
		t.Fatalf("legacy credentials=%s want empty object", string(store.insert.Credentials))
	}
	if credentials.input == nil || credentials.input.ProviderAccountID != 77 || credentials.input.AuthMode != "api_key" {
		t.Fatalf("credential input mismatch: %+v", credentials.input)
	}
	if strings.Contains(string(store.audits[0].Payload), "sk-live") {
		t.Fatalf("audit leaked credential: %s", string(store.audits[0].Payload))
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if response.ID != 77 {
		t.Fatalf("id=%d want 77", response.ID)
	}
}

func TestAdminPoolAccounts_CreateWithCredentialInitializesChannelHealth(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	health := &adminPoolChannelHealthStub{}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store, Credentials: credentials, ChannelHealth: health,
	}, http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if health.key == nil || health.key.TenantID != 7 || health.key.ProviderAccountID != 77 ||
		health.key.AccountCredentialID != 88 || health.key.CredentialVersion != 1 || health.key.Vendor != "openai" {
		t.Fatalf("channel health init key mismatch: %+v", health.key)
	}
	if strings.Contains(string(store.audits[0].Payload), "sk-live") {
		t.Fatalf("audit leaked credential: %s", string(store.audits[0].Payload))
	}
}

func TestAdminPoolAccounts_Unauthorized(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodPost,
		"/admin/v1/provider-accounts", `{}`)
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
		"/admin/v1/provider-accounts", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("non-admin request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_DisableUpdatesAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77/enabled",
		`{"enabled":false}`)
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
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodDelete, "/admin/v1/provider-accounts/77",
		`{}`)
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

func TestAdminPoolAccounts_ListProviderAccountsPaginated(t *testing.T) {
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{
		adminProviderRow(77, 7),
		adminProviderRow(78, 7),
	}}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts?limit=1&state_filter=active&pool_group_id=9", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.listArg == nil || store.listArg.TenantID != 7 || store.listArg.LimitCount != 2 ||
		store.listArg.StateFilter != "active" || store.listArg.PoolGroupID != 9 {
		t.Fatalf("list arg mismatch: %+v", store.listArg)
	}
	var response struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Page struct {
			HasMore    bool    `json:"has_more"`
			NextCursor *string `json:"next_cursor"`
		} `json:"page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].ID != 77 || !response.Page.HasMore || response.Page.NextCursor == nil {
		t.Fatalf("unexpected list response: %+v", response)
	}
}

func TestAdminPoolAccounts_GetProviderAccount(t *testing.T) {
	row := adminProviderRow(77, 7)
	store := &adminPoolStoreStub{get: &row}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts/77", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.getArg == nil || store.getArg.ID != 77 || store.getArg.TenantID != 7 {
		t.Fatalf("get arg mismatch: %+v", store.getArg)
	}
}

func TestAdminPoolAccounts_UpdateProviderAccountFull(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"enabled":true,"priority":5,"cap_concurrency":9,"model_allow_list":[" claude "],"capability_flags":["tool"],"custom_error_codes_enabled":true,"custom_error_codes":[429],"pool_mode":true,"temp_unschedulable_enabled":true,"temp_unschedulable_rules":[{"error_code":529,"keywords":["busy"],"duration_minutes":5}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.updateFull == nil || store.updateFull.ID != 77 || store.updateFull.TenantID != 7 ||
		store.updateFull.Priority == nil || *store.updateFull.Priority != 5 ||
		store.updateFull.CapConcurrency == nil || *store.updateFull.CapConcurrency != 9 ||
		!store.updateFull.SetModelAllowList || store.updateFull.ModelAllowList[0] != "claude" ||
		!store.updateFull.SetCustomErrorCodes || store.updateFull.CustomErrorCodes[0] != 429 ||
		!store.updateFull.SetTempUnschedulableRules {
		t.Fatalf("update arg mismatch: %+v", store.updateFull)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "update_provider_account" {
		t.Fatalf("update audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_ClearRateLimit(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts/77/clear-rate-limit", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.clear == nil || store.clear.ID != 77 || store.clear.TenantID != 7 {
		t.Fatalf("clear arg mismatch: %+v", store.clear)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "clear_provider_account_rate_limit" {
		t.Fatalf("clear audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_CrossTenantBodyRejected(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"tenant_id":8,"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("cross-tenant request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func adminPoolAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

func providerAccountAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
}

func invokeAdminPool(t *testing.T, store *adminPoolStoreStub, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	return invokeAdminPoolWithCredentialStore(t, store, nil, auth, method, target, body)
}

func invokeAdminPoolWithCredentialStore(t *testing.T, store *adminPoolStoreStub, credentials AdminPoolAccountCredentialWriter, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{Auth: auth, Store: store, Credentials: credentials}, method, target, body)
}

func invokeAdminPoolWithDeps(t *testing.T, deps AdminPoolAccountDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountAdminPoolAccountRoutes(r, deps)
	})
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountAdminPoolAccountRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func adminProviderRow(id, tenantID int64) admindb.AdminProviderAccountRow {
	return admindb.AdminProviderAccountRow{
		ID: id, TenantID: tenantID, ProviderID: 8, ChannelID: 9, Name: "acct",
		AccountType: "api_key", Enabled: true, HealthState: "operational", CredentialState: "valid",
		CapConcurrency: 4, Priority: 100, TokenVersion: 1, OAuthEndpointHealth: "operational",
		ModelAllowList: []string{}, CapabilityFlags: []string{}, CustomErrorCodes: []int32{},
	}
}

func adminProviderRowFromInsert(id int64, in admindb.InsertProviderAccountParams) admindb.AdminProviderAccountRow {
	row := adminProviderRow(id, in.TenantID)
	row.ProviderID = in.ProviderID
	row.ChannelID = in.ChannelID
	row.Name = in.Name
	row.AccountType = in.AccountType
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.CapConcurrency != nil {
		row.CapConcurrency = *in.CapConcurrency
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	row.ModelAllowList = in.ModelAllowList
	row.CapabilityFlags = in.CapabilityFlags
	return row
}
