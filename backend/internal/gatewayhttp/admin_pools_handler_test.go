package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type adminPoolsStoreStub struct {
	insert    *dbbilling.InsertPoolParams
	get       *dbbilling.GetPoolParams
	list      *dbbilling.ListPoolsParams
	update    *dbbilling.UpdatePoolParams
	audits    []admindb.InsertAdminAuditEventParams
	insertErr error
	getErr    error
	updateErr error
	pool      dbbilling.PoolGroup
	items     []dbbilling.PoolGroup
}

var adminPoolsAuditAllowedActions = map[string]struct{}{
	"issue_api_key":                    {},
	"revoke_api_key":                   {},
	"list_api_keys":                    {},
	"issue_admin_token":                {},
	"revoke_admin_token":               {},
	"admin_login":                      {},
	"create_provider_account":          {},
	"disable_provider_account":         {},
	"enable_provider_account":          {},
	"delete_provider_account":          {},
	"create_account_credential":        {},
	"rotate_account_credential":        {},
	"disable_account_credential":       {},
	"delete_account_credential":        {},
	"list_account_credentials":         {},
	"credential_acquisition_started":   {},
	"credential_acquisition_completed": {},
	"credential_acquisition_failed":    {},
	"credential_acquisition_cancelled": {},
	"update_billing_settings":          {},
	"create_pool_group":                {},
	"update_pool_group":                {},
}

var adminPoolsAuditAllowedTargetTypes = map[string]struct{}{
	"api_key":            {},
	"admin_token":        {},
	"tenant":             {},
	"user":               {},
	"provider_account":   {},
	"account_credential": {},
	"billing_setting":    {},
	"pool_group":         {},
}

func (s *adminPoolsStoreStub) InsertPool(_ context.Context, arg dbbilling.InsertPoolParams) (dbbilling.PoolGroup, error) {
	s.insert = &arg
	if s.insertErr != nil {
		return dbbilling.PoolGroup{}, s.insertErr
	}
	pool := poolOrDefault(s.pool, arg.TenantID, arg.Name)
	pool.TopKDefault = arg.TopKDefault
	pool.CapabilityDefault = arg.CapabilityDefault
	pool.AllowLastResort = arg.AllowLastResort
	s.pool = pool
	return pool, nil
}

func (s *adminPoolsStoreStub) GetPool(_ context.Context, arg dbbilling.GetPoolParams) (dbbilling.PoolGroup, error) {
	s.get = &arg
	if s.getErr != nil {
		return dbbilling.PoolGroup{}, s.getErr
	}
	return poolOrDefault(s.pool, arg.TenantID, "primary"), nil
}

func (s *adminPoolsStoreStub) ListPools(_ context.Context, arg dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error) {
	s.list = &arg
	if s.items != nil {
		return s.items, nil
	}
	return []dbbilling.PoolGroup{{ID: 7, TenantID: arg.TenantID, Name: "primary", Enabled: true}}, nil
}

func (s *adminPoolsStoreStub) UpdatePool(_ context.Context, arg dbbilling.UpdatePoolParams) (dbbilling.PoolGroup, error) {
	s.update = &arg
	if s.updateErr != nil {
		return dbbilling.PoolGroup{}, s.updateErr
	}
	pool := poolOrDefault(s.pool, arg.TenantID, "primary")
	if arg.Name != nil {
		pool.Name = *arg.Name
	}
	if arg.TopKDefault != nil {
		pool.TopKDefault = *arg.TopKDefault
	}
	if arg.CapabilityDefault != nil {
		pool.CapabilityDefault = *arg.CapabilityDefault
	}
	if arg.AllowLastResort != nil {
		pool.AllowLastResort = *arg.AllowLastResort
	}
	if arg.Enabled != nil {
		pool.Enabled = *arg.Enabled
	}
	pool.ID = arg.ID
	pool.TenantID = arg.TenantID
	s.pool = pool
	return pool, nil
}

func (s *adminPoolsStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	if _, ok := adminPoolsAuditAllowedActions[arg.Action]; !ok {
		return admindb.InsertAdminAuditEventRow{}, &pgconn.PgError{
			Code:           "23514",
			ConstraintName: "admin_audit_events_action_check",
			Message:        "new row for relation \"admin_audit_events\" violates check constraint \"admin_audit_events_action_check\"",
		}
	}
	if _, ok := adminPoolsAuditAllowedTargetTypes[arg.TargetType]; !ok {
		return admindb.InsertAdminAuditEventRow{}, &pgconn.PgError{
			Code:           "23514",
			ConstraintName: "admin_audit_events_target_type_check",
			Message:        "new row for relation \"admin_audit_events\" violates check constraint \"admin_audit_events_target_type_check\"",
		}
	}
	s.audits = append(s.audits, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

func TestATS1Tenant001TenantOperatorListCreateUpdateUsesOwnTenant(t *testing.T) {
	store := &adminPoolsStoreStub{}
	auth := adminPoolsTenantOperator(7)

	listRec := invokeAdminPools(t, store, auth, http.MethodGet, "/admin/v1/pools", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if store.list == nil || store.list.TenantID != 7 {
		t.Fatalf("list did not use operator tenant scope: %+v", store.list)
	}

	createRec := invokeAdminPools(t, store, auth, http.MethodPost, "/admin/v1/pools/",
		`{"name":"tenant pool"}`)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.Name != "tenant pool" {
		t.Fatalf("create did not use operator tenant scope: %+v", store.insert)
	}

	updateRec := invokeAdminPools(t, store, auth, http.MethodPatch, "/admin/v1/pools/77",
		`{"name":"tenant pool updated"}`)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if store.update == nil || store.update.TenantID != 7 || store.update.ID != 77 {
		t.Fatalf("update did not use operator tenant scope: %+v", store.update)
	}
}

func TestATS1Tenant001TenantOperatorCrossTenantPoolDeniedOrHidden(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPatch, "/admin/v1/pools/77?tenant_id=8",
		`{"name":"wrong tenant"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update != nil {
		t.Fatalf("cross-tenant update touched store: %+v", store.update)
	}

	store = &adminPoolsStoreStub{getErr: pgx.ErrNoRows}
	rec = invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.get == nil || store.get.TenantID != 7 {
		t.Fatalf("hidden cross-tenant read did not use scoped tenant lookup: %+v", store.get)
	}
}

func TestATS1Tenant001PlatformAdminRequiresExplicitTenant(t *testing.T) {
	store := &adminPoolsStoreStub{}
	listRec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools", "")
	if listRec.Code != http.StatusBadRequest {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if store.list != nil {
		t.Fatalf("platform list without tenant touched store: %+v", store.list)
	}

	createRec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/",
		`{"name":"missing tenant"}`)
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("platform create without tenant touched store: %+v", store.insert)
	}
}

func TestATS1Tenant001PlatformAdminExplicitTenantSucceedsAndAudits(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/?tenant_id=42",
		`{"name":"platform tenant pool"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 42 {
		t.Fatalf("platform create did not use explicit tenant: %+v", store.insert)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit count=%d audits=%+v", len(store.audits), store.audits)
	}
	audit := store.audits[0]
	if audit.TenantID == nil || *audit.TenantID != 42 || audit.ActorID != "11" || audit.ActorRole != admin.RolePlatformAdmin {
		t.Fatalf("audit lost tenant or actor: %+v", audit)
	}
	if audit.Action != "create_pool_group" || audit.TargetType != "pool_group" || audit.TargetID == nil || *audit.TargetID != 77 {
		t.Fatalf("audit action/target mismatch: %+v", audit)
	}
}

func TestAdminPoolsAuditStoreMirrorsAdminAuditChecks(t *testing.T) {
	store := &adminPoolsStoreStub{}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "unknown_action",
		TargetType: "pool_group",
	}); err == nil {
		t.Fatalf("unknown action was accepted")
	}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "create_pool_group",
		TargetType: "unknown_target",
	}); err == nil {
		t.Fatalf("unknown target_type was accepted")
	}
	if len(store.audits) != 0 {
		t.Fatalf("invalid audit records persisted: %d", len(store.audits))
	}
}

func TestATS1Tenant001BodyTenantIDCannotOverrideValidatedScope(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/?tenant_id=7",
		`{"tenant_id":8,"name":"body override"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("body tenant override touched store: insert=%+v audits=%+v", store.insert, store.audits)
	}
}

func TestAdminPools_ListSuccessUsesDefaultLimit(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list == nil || store.list.TenantID != 7 || store.list.LimitCount != defaultAdminPoolsLimit {
		t.Fatalf("list params mismatch: %+v", store.list)
	}
}

func TestAdminPools_CreateSuccessInsertsTrimmedName(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":" primary ","description":"owner visible label"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.Name != "primary" {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if store.insert.TopKDefault != defaultAdminPoolTopKDefault ||
		store.insert.CapabilityDefault != defaultAdminPoolCapabilityDefault ||
		store.insert.AllowLastResort {
		t.Fatalf("insert defaults mismatch: %+v", store.insert)
	}
}

func TestAT_POOL_001_001_CreateFieldsPersistAndReadBack(t *testing.T) {
	store := &adminPoolsStoreStub{}
	createResp := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":"primary","top_k_default":4,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if store.insert == nil ||
		store.insert.TopKDefault != 4 ||
		store.insert.CapabilityDefault != "safe_equivalent_allowed" ||
		!store.insert.AllowLastResort {
		t.Fatalf("create did not pass persistence fields: %+v", store.insert)
	}

	getResp := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResp.Code, getResp.Body.String())
	}
	var got dbbilling.PoolGroup
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v body=%s", err, getResp.Body.String())
	}
	if got.TopKDefault != 4 || got.CapabilityDefault != "safe_equivalent_allowed" || !got.AllowLastResort {
		t.Fatalf("get response lost fields: %+v", got)
	}
}

func TestAdminPools_GetSuccess(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.get == nil || store.get.ID != 77 || store.get.TenantID != 7 {
		t.Fatalf("get params mismatch: %+v", store.get)
	}
}

func TestAdminPools_UpdateSuccessPatchesNameAndEnabled(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPatch, "/admin/v1/pools/77",
		`{"name":" updated ","enabled":false,"top_k_default":3,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != 7 {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if store.update.Name == nil || *store.update.Name != "updated" || store.update.Enabled == nil || *store.update.Enabled {
		t.Fatalf("update fields mismatch: %+v", store.update)
	}
	if store.update.TopKDefault == nil || *store.update.TopKDefault != 3 ||
		store.update.CapabilityDefault == nil || *store.update.CapabilityDefault != "safe_equivalent_allowed" ||
		store.update.AllowLastResort == nil || !*store.update.AllowLastResort {
		t.Fatalf("update persistence fields mismatch: %+v", store.update)
	}
}

func TestAdminPools_CreateMissingNameReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/", `{"description":"missing"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("bad request touched store: %+v", store.insert)
	}
}

func TestAdminPools_CreateDuplicateNameReturns409(t *testing.T) {
	store := &adminPoolsStoreStub{insertErr: &pgconn.PgError{Code: "23505"}}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/", `{"name":"primary"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_GetNotFoundReturns404(t *testing.T) {
	store := &adminPoolsStoreStub{getErr: pgx.ErrNoRows}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/404", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_UnauthorizedReturns401(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodGet, "/admin/v1/pools/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list != nil {
		t.Fatalf("unauthorized request touched store: %+v", store.list)
	}
}

func TestAdminPools_CreateNameTooLongReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":"`+strings.Repeat("a", maxAdminPoolNameRunes+1)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func invokeAdminPools(t *testing.T, store *adminPoolsStoreStub, auth AdminPoolsAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", NewAdminPoolsHandler(AdminPoolsDeps{Auth: auth, Store: store}))
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func poolOrDefault(pool dbbilling.PoolGroup, tenantID int64, name string) dbbilling.PoolGroup {
	if pool.ID != 0 {
		return pool
	}
	return dbbilling.PoolGroup{
		ID:                77,
		TenantID:          tenantID,
		Name:              name,
		TopKDefault:       defaultAdminPoolTopKDefault,
		CapabilityDefault: defaultAdminPoolCapabilityDefault,
		Enabled:           true,
	}
}

func adminPoolsTenantOperator(tenantID int64) adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 22, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}}
}
