package accountsourcehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountbundle"
	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	"github.com/BloomingProsperity/HUAKAI/internal/accountsource/crs"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

type authStub struct{ identity admin.AdminIdentity }

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, nil
}

type capabilityStub struct {
	denied map[tenantcapability.Capability]bool
	calls  []tenantcapability.Capability
}

func (s *capabilityStub) Require(_ context.Context, _ int64, capability tenantcapability.Capability) error {
	s.calls = append(s.calls, capability)
	if s.denied[capability] {
		return tenantcapability.ErrDenied
	}
	return nil
}

type crsStub struct{ calls int }

func (s *crsStub) Fetch(context.Context, crs.FetchInput) ([]accountsource.Item, map[string]any, error) {
	s.calls++
	return nil, nil, nil
}

type sessionStub struct{}

func (sessionStub) Create(context.Context, accountsource.CreateInput) (accountsource.Session, error) {
	return accountsource.Session{ID: "00000000-0000-0000-0000-000000000001"}, nil
}

type sourceStub struct{}

func (sourceStub) Plan(context.Context, accountsource.PlanInput) (accountsource.BatchPlan, error) {
	return accountsource.BatchPlan{}, nil
}
func (sourceStub) Execute(context.Context, accountsource.ExecuteInput) (accountsource.BatchExecution, error) {
	return accountsource.BatchExecution{}, nil
}

type bundleStub struct{ calls int }

func (s *bundleStub) Export(context.Context, int64, string, string, time.Duration) (accountbundle.ExportResult, error) {
	s.calls++
	return accountbundle.ExportResult{Mode: accountbundle.ModeRecovery, BundleID: "bundle", AccountCount: 1, Bundle: json.RawMessage(`{"version":"encrypted"}`)}, nil
}

type structureStub struct{}

func (structureStub) Plan(context.Context, accountbundle.StructurePlanInput) (accountbundle.StructurePlan, error) {
	return accountbundle.StructurePlan{}, nil
}
func (structureStub) Execute(context.Context, accountbundle.StructureExecuteInput) (accountbundle.StructureExecution, error) {
	return accountbundle.StructureExecution{}, nil
}

type auditStub struct{ calls int }

func (s *auditStub) InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.calls++
	return admindb.InsertAdminAuditEventRow{}, nil
}

func TestAccountSourceRejectsPlatformAdministratorExecution(t *testing.T) {
	crsClient := &crsStub{}
	deps := completeDeps(admin.AdminIdentity{Source: admin.AdminSourceToken, TokenID: 1, Role: admin.RolePlatformAdmin})
	deps.CRS = crsClient
	recorder := serve(t, deps, "/admin/v1/credentials/crs-sync/preview", `{"tenant_id":7,"base_url":"https://relay.example.com","username":"admin","password":"secret","mappings":[]}`)
	if recorder.Code != http.StatusForbidden || crsClient.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, crsClient.calls, recorder.Body.String())
	}
}

func TestAccountSourceDeniedCapabilityStopsBeforeRemoteFetch(t *testing.T) {
	crsClient := &crsStub{}
	deps := completeDeps(tenantOperator())
	deps.CRS = crsClient
	deps.Capabilities = &capabilityStub{denied: map[tenantcapability.Capability]bool{tenantcapability.CRSAccountSync: true}}
	recorder := serve(t, deps, "/admin/v1/credentials/crs-sync/preview", `{"tenant_id":7,"base_url":"https://relay.example.com","username":"admin","password":"secret","mappings":[]}`)
	if recorder.Code != http.StatusForbidden || crsClient.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, crsClient.calls, recorder.Body.String())
	}
}

func TestRecoveryExportRequiresConfirmationAndSetsNoStore(t *testing.T) {
	bundles := &bundleStub{}
	audit := &auditStub{}
	deps := completeDeps(tenantOperator())
	deps.Bundles, deps.Audit = bundles, audit
	body := `{"tenant_id":7,"mode":"recovery","passphrase":"strong-transfer-passphrase-2026","reason":"迁移上游账号凭据"}`
	recorder := serve(t, deps, "/admin/v1/credentials/account-bundles/export", body)
	if recorder.Code != http.StatusBadRequest || bundles.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, bundles.calls, recorder.Body.String())
	}
	body = `{"tenant_id":7,"mode":"recovery","passphrase":"strong-transfer-passphrase-2026","confirmation":"confirm_account_secret_transfer","reason":"迁移上游账号凭据"}`
	recorder = serve(t, deps, "/admin/v1/credentials/account-bundles/export", body)
	if recorder.Code != http.StatusOK || bundles.calls != 1 || audit.calls != 1 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d bundle_calls=%d audit_calls=%d headers=%v body=%s", recorder.Code, bundles.calls, audit.calls, recorder.Header(), recorder.Body.String())
	}
}

func completeDeps(identity admin.AdminIdentity) Deps {
	return Deps{
		Auth: authStub{identity: identity}, Capabilities: &capabilityStub{}, CRS: &crsStub{},
		Sessions: sessionStub{}, Sources: sourceStub{}, Bundles: &bundleStub{},
		Structures: structureStub{}, Audit: &auditStub{},
	}
}

func tenantOperator() admin.AdminIdentity {
	return admin.AdminIdentity{Source: admin.AdminSourceToken, TokenID: 2, Role: admin.RoleTenantOperator, ScopeTenantID: 7}
}

func serve(t *testing.T, deps Deps, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(router chi.Router) { Mount(router, deps) })
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
