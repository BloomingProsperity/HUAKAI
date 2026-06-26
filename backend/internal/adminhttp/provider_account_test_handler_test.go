package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderAccountTestUnauthorized(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{})
	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: testerAuthStub{err: admin.ErrAdminUnauthorized}, Accounts: accounts,
		Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 0 || len(credentials.loadKeys) != 0 {
		t.Fatalf("unauthorized request touched stores: account=%v credential=%v", accounts.getArgs, credentials.loadKeys)
	}
}

func TestProviderAccountTestCrossTenantBodyTenantIDIgnored(t *testing.T) {
	// 判别防串租户:body.tenant_id=8 必须被忽略,查询只能用 admin identity 的 tenant 7。
	// 变异:从 body 收 tenant_id 或不按 identity scope 查询,会命中 tenant 8 account 并返回 200。
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(8, 200))
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{
		ID: 55, TenantID: 8, ProviderAccountID: 200,
		Vendor: "testvendor", AuthMode: "safe_refresh",
		PlaintextPayload: []byte(`{"refresh_token":"rt-tenant-b"}`),
	})

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: testerAuthStub{ident: providerAccountTestAdmin(7)}, Accounts: accounts,
		Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/200/test", `{"tenant_id":8}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 1 || accounts.getArgs[0].TenantID != 7 || accounts.getArgs[0].ID != 200 {
		t.Fatalf("GetAdminProviderAccount args=%+v, want tenant scoped lookup tenant=7 id=200", accounts.getArgs)
	}
	if len(credentials.loadKeys) != 0 {
		t.Fatalf("cross-tenant request reached credential tester: %v", credentials.loadKeys)
	}
	if strings.Contains(rec.Body.String(), "tenant-b") || strings.Contains(rec.Body.String(), "8") {
		t.Fatalf("cross-tenant response leaked target tenant/account detail: %s", rec.Body.String())
	}
}

func TestProviderAccountTestTenantOperatorWithoutScopeForbidden(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{})
	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth:     testerAuthStub{ident: admin.AdminIdentity{TokenID: 3, Role: admin.RoleTenantOperator}},
		Accounts: accounts, Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 0 || len(credentials.loadKeys) != 0 {
		t.Fatalf("forbidden request touched stores: account=%v credential=%v", accounts.getArgs, credentials.loadKeys)
	}
}

func TestProviderAccountTestPlatformAdminCanUseDefaultTenantScope(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(defaultProviderAccountTestPlatformTenantID, 99))
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{
		ID: 59, TenantID: defaultProviderAccountTestPlatformTenantID, ProviderAccountID: 99,
		Vendor: "testvendor", AuthMode: "safe_refresh",
		PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
	})

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth:     testerAuthStub{ident: admin.AdminIdentity{TokenID: 4, Role: admin.RolePlatformAdmin}},
		Accounts: accounts, Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 1 || accounts.getArgs[0].TenantID != defaultProviderAccountTestPlatformTenantID {
		t.Fatalf("platform_admin get args=%+v, want default tenant scope", accounts.getArgs)
	}
}

func TestProviderAccountTestInvalidGrantDoesNotLeakSecretMarker(t *testing.T) {
	// 判别 secret leak:adapter 错误含上游 body + secret marker,HTTP 响应只能暴露 error_class。
	// 变异:把 raw err 或 raw upstream body 塞进 message,marker 断言会红。
	secretMarker := "sk-live-secret-marker"
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{
		ID: 56, TenantID: 7, ProviderAccountID: 99,
		Vendor: "testvendor", AuthMode: "safe_refresh",
		PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
	})
	registry.adapterErr = errors.New(`upstream body {"error":"invalid_grant","detail":"` + secretMarker + `"}`)

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts,
		Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountTestResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.OK || body.ErrorClass == nil || *body.ErrorClass != "invalid_grant" {
		t.Fatalf("response=%+v, want invalid_grant failure", body)
	}
	if strings.Contains(rec.Body.String(), secretMarker) || strings.Contains(rec.Body.String(), "upstream body") {
		t.Fatalf("response leaked raw upstream body/secret: %s", rec.Body.String())
	}
	if len(accounts.auditEvents) != 1 {
		t.Fatalf("audit events=%d want 1", len(accounts.auditEvents))
	}
	audit := accounts.auditEvents[0]
	if audit.Action == "list_account_credentials" {
		t.Fatalf("dry-run credential test must not be mislabeled as credential listing: %+v", audit)
	}
	if audit.Action != "test_provider_account" || audit.TargetID == nil || *audit.TargetID != 99 {
		t.Fatalf("audit event=%+v, want dedicated provider account test action", audit)
	}
	if strings.Contains(string(audit.Payload), secretMarker) || strings.Contains(string(audit.Payload), "upstream body") {
		t.Fatalf("audit leaked raw upstream body/secret: %s", string(audit.Payload))
	}
	if !strings.Contains(string(audit.Payload), `"error_class":"invalid_grant"`) {
		t.Fatalf("audit payload=%s, want invalid_grant class", string(audit.Payload))
	}
	if !strings.Contains(string(audit.Payload), `"operation":"provider_account_credential_test"`) {
		t.Fatalf("audit payload=%s, want provider account credential test operation", string(audit.Payload))
	}
}

func TestProviderAccountTestFailureDoesNotChangeCredentialOrAccountState(t *testing.T) {
	// DRY-RUN 核心判别:失败校验不得持久化 health/failure/next_attempt/token_version。
	// 变异:改成调用 SaveRefreshFailure 或账号健康写回,下方 before/after 会红。
	accounts := newProviderAccountTestAccountStoreStub()
	account := providerAccountTestRow(7, 99)
	account.HealthState = "healthy"
	account.CredentialState = "active"
	account.TokenVersion = 4
	accounts.put(account)
	nextAttempt := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	failureClass := "temporary"
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{
		ID: 57, TenantID: 7, ProviderAccountID: 99,
		Vendor: "testvendor", AuthMode: "safe_refresh",
		State: credentialstore.StateActive, CredentialVersion: 4,
		FailureClass: &failureClass, FailureCount: 2, NextAttemptAt: nextAttempt,
		PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
	})
	registry.adapterErr = errors.New("invalid_grant")
	beforeAccount := accounts.rows[providerAccountTestKey(7, 99)]
	beforeCredential := credentials.rec

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts,
		Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	afterAccount := accounts.rows[providerAccountTestKey(7, 99)]
	if afterAccount.HealthState != beforeAccount.HealthState ||
		afterAccount.CredentialState != beforeAccount.CredentialState ||
		afterAccount.TokenVersion != beforeAccount.TokenVersion {
		t.Fatalf("account state changed: before=%+v after=%+v", beforeAccount, afterAccount)
	}
	if credentials.rec.FailureCount != beforeCredential.FailureCount ||
		credentials.rec.NextAttemptAt != beforeCredential.NextAttemptAt ||
		credentials.rec.CredentialVersion != beforeCredential.CredentialVersion ||
		credentials.saveSuccessCalls != 0 || credentials.saveFailureCalls != 0 {
		t.Fatalf("credential state changed: before=%+v after=%+v saveSuccess=%d saveFailure=%d",
			beforeCredential, credentials.rec, credentials.saveSuccessCalls, credentials.saveFailureCalls)
	}
}

func TestProviderAccountTestSuccessReturnsOK(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	credentials, registry := newProviderAccountTestCredentialDeps(t, credentialstore.CredentialRecord{
		ID: 58, TenantID: 7, ProviderAccountID: 99,
		Vendor: "testvendor", AuthMode: "safe_refresh",
		PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
	})

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts,
		Tester: NewProviderAccountCredentialTester(credentials, registry.registry), Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountTestResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.OK || body.ErrorClass != nil {
		t.Fatalf("response=%+v, want ok true and null error_class", body)
	}
	if body.Message == "" {
		t.Fatalf("response message is empty: %+v", body)
	}
	if len(accounts.auditEvents) != 1 {
		t.Fatalf("audit events=%d want 1", len(accounts.auditEvents))
	}
	if got := string(accounts.auditEvents[0].Payload); !strings.Contains(got, `"ok":true`) || strings.Contains(got, "new-secret") {
		t.Fatalf("audit payload=%s, want ok true without credential material", got)
	}
}

func TestProviderAccountTestUsesProbeModelWhenConfigured(t *testing.T) {
	// 变异:handler 忽略 provider_accounts.probe_model 时 tester 收到空
	// probe model,无法按账号指定探测目标。
	accounts := newProviderAccountTestAccountStoreStub()
	row := providerAccountTestRow(7, 99)
	probeModel := "claude-3-5-sonnet-probe"
	row.ProbeModel = &probeModel
	accounts.put(row)
	tester := &providerAccountProbeModelTester{}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts,
		Tester: tester, Now: fixedProviderAccountTestNow,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tester.probeModel != probeModel {
		t.Fatalf("probeModel=%q want %q", tester.probeModel, probeModel)
	}
	if tester.tenantID != 7 || tester.accountID != 99 || !tester.at.Equal(fixedProviderAccountTestNow()) {
		t.Fatalf("tester args tenant=%d account=%d at=%s", tester.tenantID, tester.accountID, tester.at)
	}
}

func invokeProviderAccountTest(t *testing.T, deps ProviderAccountTestDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountTestRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func fixedProviderAccountTestNow() time.Time {
	return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
}

func providerAccountTestAuthForTenant(tenantID int64) testerAuthStub {
	return testerAuthStub{ident: providerAccountTestAdmin(tenantID)}
}

func providerAccountTestAdmin(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 10, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

type testerAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s testerAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type providerAccountTestAccountStoreStub struct {
	rows        map[string]admindb.AdminProviderAccountRow
	getArgs     []admindb.GetAdminProviderAccountParams
	auditEvents []admindb.InsertAdminAuditEventParams
	err         error
	auditErr    error
}

func newProviderAccountTestAccountStoreStub() *providerAccountTestAccountStoreStub {
	return &providerAccountTestAccountStoreStub{rows: map[string]admindb.AdminProviderAccountRow{}}
}

func (s *providerAccountTestAccountStoreStub) put(row admindb.AdminProviderAccountRow) {
	s.rows[providerAccountTestKey(row.TenantID, row.ID)] = row
}

func (s *providerAccountTestAccountStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.getArgs = append(s.getArgs, arg)
	if s.err != nil {
		return admindb.AdminProviderAccountRow{}, s.err
	}
	row, ok := s.rows[providerAccountTestKey(arg.TenantID, arg.ID)]
	if !ok {
		return admindb.AdminProviderAccountRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (s *providerAccountTestAccountStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.auditEvents = append(s.auditEvents, arg)
	if s.auditErr != nil {
		return admindb.InsertAdminAuditEventRow{}, s.auditErr
	}
	return admindb.InsertAdminAuditEventRow{}, nil
}

func providerAccountTestKey(tenantID, accountID int64) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strconv.FormatInt(accountID, 10)
}

func providerAccountTestRow(tenantID, id int64) admindb.AdminProviderAccountRow {
	return admindb.AdminProviderAccountRow{
		ID: id, TenantID: tenantID, ProviderID: 1, ChannelID: 2,
		Name: "acct", AccountType: "oauth", Enabled: true,
		HealthState: "healthy", CredentialState: credentialstore.StateActive,
		CapConcurrency: 1, Priority: 1,
	}
}

type providerAccountTestCredentialStoreStub struct {
	rec              credentialstore.CredentialRecord
	loadKeys         []string
	saveSuccessCalls int
	saveFailureCalls int
}

func newProviderAccountTestCredentialDeps(t *testing.T, rec credentialstore.CredentialRecord) (*providerAccountTestCredentialStoreStub, *providerAccountTestRegistryStub) {
	t.Helper()
	credentials := &providerAccountTestCredentialStoreStub{rec: rec}
	registry := &providerAccountTestRegistryStub{registry: credentialworker.NewModeAdapterRegistry()}
	if rec.Vendor != "" && rec.AuthMode != "" {
		if err := registry.registry.Register(rec.Vendor, rec.AuthMode, registry); err != nil {
			t.Fatalf("register adapter: %v", err)
		}
	}
	return credentials, registry
}

func (s *providerAccountTestCredentialStoreStub) LoadForProviderAccountTest(_ context.Context, tenantID, accountID int64) (credentialstore.CredentialRecord, error) {
	s.loadKeys = append(s.loadKeys, providerAccountTestKey(tenantID, accountID))
	return s.rec, nil
}

func (s *providerAccountTestCredentialStoreStub) SaveRefreshSuccess(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string) error {
	s.saveSuccessCalls++
	return nil
}

func (s *providerAccountTestCredentialStoreStub) SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error {
	s.saveFailureCalls++
	return nil
}

type providerAccountTestRegistryStub struct {
	registry   *credentialworker.ModeAdapterRegistry
	adapterErr error
}

func (s *providerAccountTestRegistryStub) RefreshCredential(context.Context, credentialworker.ModeRefreshInput) (credentialworker.ModeRefreshResult, error) {
	if s.adapterErr != nil {
		return credentialworker.ModeRefreshResult{}, s.adapterErr
	}
	return credentialworker.ModeRefreshResult{
		Payload:         []byte(`{"access_token":"new-secret"}`),
		AccessExpiresAt: fixedProviderAccountTestNow().Add(time.Hour),
		Outcome:         "refresh_succeeded",
	}, nil
}

type providerAccountProbeModelTester struct {
	tenantID   int64
	accountID  int64
	at         time.Time
	probeModel string
}

func (s *providerAccountProbeModelTester) TestProviderAccountCredential(_ context.Context, tenantID, accountID int64, now time.Time, probeModel string) (credentialworker.ProviderAccountCredentialTestResult, error) {
	s.tenantID = tenantID
	s.accountID = accountID
	s.at = now
	s.probeModel = probeModel
	return credentialworker.ProviderAccountCredentialTestResult{OK: true}, nil
}
