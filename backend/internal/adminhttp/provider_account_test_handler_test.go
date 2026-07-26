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

	"github.com/BloomingProsperity/HUAKAI/internal/accountprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderAccountTestUnauthorized(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	tester := &providerAccountTesterStub{}
	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: testerAuthStub{err: admin.ErrAdminUnauthorized}, Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 0 || len(accounts.records) != 0 || len(tester.inputs) != 0 {
		t.Fatalf("未授权请求触达了业务依赖: get=%v records=%v probes=%v", accounts.getArgs, accounts.records, tester.inputs)
	}
}

func TestProviderAccountTestCrossTenantBodyTenantIDIgnored(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(8, 200))
	tester := &providerAccountTesterStub{result: successfulAccountProbeResult("model")}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: testerAuthStub{ident: providerAccountTestAdmin(7)}, Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/200/test", `{"tenant_id":8}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 1 || accounts.getArgs[0].TenantID != 7 || accounts.getArgs[0].ID != 200 {
		t.Fatalf("GetAdminProviderAccount args=%+v，期望 tenant=7 id=200", accounts.getArgs)
	}
	if len(tester.inputs) != 0 || len(accounts.records) != 0 {
		t.Fatalf("跨租户请求触达了探测或写入: probes=%v records=%v", tester.inputs, accounts.records)
	}
}

func TestProviderAccountTestTenantOperatorWithoutScopeForbidden(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	tester := &providerAccountTesterStub{}
	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth:     testerAuthStub{ident: admin.AdminIdentity{TokenID: 3, Role: admin.RoleTenantOperator}},
		Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 0 || len(tester.inputs) != 0 {
		t.Fatalf("无作用域租户管理员触达了业务依赖")
	}
}

func TestProviderAccountTestPlatformAdminRequiresExplicitTenant(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	tester := &providerAccountTesterStub{result: successfulAccountProbeResult("probe-model")}
	deps := ProviderAccountTestDeps{
		Auth:     testerAuthStub{ident: admin.AdminIdentity{TokenID: 4, Role: admin.RolePlatformAdmin}},
		Accounts: accounts, Tester: tester,
	}

	rec := invokeProviderAccountTest(t, deps, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("platform_admin 不带 tenant_id 应为 400，实际 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 0 {
		t.Fatalf("缺失 tenant_id 的请求不应触达 store")
	}

	rec = invokeProviderAccountTest(t, deps, http.MethodPost, "/admin/v1/provider-accounts/99/test?tenant_id=7", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("platform_admin 显式 tenant_id=7 应成功，status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.getArgs) != 1 || accounts.getArgs[0].TenantID != 7 ||
		len(tester.inputs) != 1 || tester.inputs[0].TenantID != 7 {
		t.Fatalf("显式租户作用域未贯穿查询和探测: gets=%+v probes=%+v", accounts.getArgs, tester.inputs)
	}
}

func TestProviderAccountTestSafeFailureDoesNotLeakUpstreamSecret(t *testing.T) {
	secretMarker := "sk-live-secret-marker"
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	tester := &providerAccountTesterStub{result: accountprobe.Result{
		Attempted: true, Model: "claude-probe", ProtocolFamily: "anthropic_claude_session",
		ErrorClass: "oauth_invalid_grant", Message: "上游拒绝当前账号凭据，需要重新认证或更换凭据",
		StatusCode: http.StatusUnauthorized, LatencyMS: 25, TestedAt: fixedProviderAccountTestNow(),
		HealthSignal: channelhealth.SignalAuthChallenge, HealthSignalRecorded: true,
	}}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountTestResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.OK || body.ErrorClass == nil || *body.ErrorClass != "oauth_invalid_grant" ||
		body.Attempted != true || body.HealthSignalRecorded != true {
		t.Fatalf("response=%+v", body)
	}
	if strings.Contains(rec.Body.String(), secretMarker) || strings.Contains(rec.Body.String(), "upstream body") {
		t.Fatalf("响应泄露了上游原文或秘密: %s", rec.Body.String())
	}
	if len(accounts.records) != 1 {
		t.Fatalf("records=%d want 1", len(accounts.records))
	}
	if accounts.records[0].Result.ErrorClass != "oauth_invalid_grant" ||
		accounts.records[0].Result.Model != "claude-probe" {
		t.Fatalf("record=%+v", accounts.records[0])
	}
}

func TestProviderAccountTestRecordsActiveObservationAndAuditIntent(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	account := providerAccountTestRow(7, 99)
	account.HealthState = "healthy"
	account.LastProbeLatencyMS = nil
	accounts.put(account)
	result := successfulAccountProbeResult("gpt-4o-mini")
	result.LatencyMS = 47
	tester := &providerAccountTesterStub{result: result}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.records) != 1 {
		t.Fatalf("records=%d want 1", len(accounts.records))
	}
	record := accounts.records[0]
	if !record.Result.Attempted || record.Result.LatencyMS != 47 ||
		!record.Result.TestedAt.Equal(fixedProviderAccountTestNow()) ||
		record.Identity.AuditActor() != "admin_token:10" {
		t.Fatalf("record=%+v", record)
	}
	if got := accounts.rows[providerAccountTestKey(7, 99)]; got.HealthState != "healthy" || got.LastProbeLatencyMS != nil {
		t.Fatalf("handler stub 不应伪造数据库状态，row=%+v", got)
	}
}

func TestProviderAccountTestSuccessResponseContainsOperationalProjection(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	result := successfulAccountProbeResult("gpt-4o-mini")
	result.Warnings = []string{"health_signal_not_recorded"}
	tester := &providerAccountTesterStub{result: result}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountTestResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.OK || !body.Attempted || body.Model != "gpt-4o-mini" ||
		body.ProtocolFamily != "openai_chat" || body.LatencyMS == nil || *body.LatencyMS != 31 ||
		body.TestedAt == nil || len(body.Warnings) != 1 {
		t.Fatalf("response=%+v", body)
	}
}

func TestProviderAccountTestPassesConfiguredModelAndAllowList(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	row := providerAccountTestRow(7, 99)
	probeModel := "claude-3-5-sonnet-probe"
	row.ProbeModel = &probeModel
	row.ModelAllowList = []string{"claude-a", "claude-b"}
	accounts.put(row)
	tester := &providerAccountTesterStub{result: successfulAccountProbeResult(probeModel)}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(tester.inputs) != 1 || tester.inputs[0].ProbeModel != probeModel ||
		len(tester.inputs[0].ModelAllowList) != 2 || tester.inputs[0].ModelAllowList[1] != "claude-b" {
		t.Fatalf("probe input=%+v", tester.inputs)
	}
}

func TestProviderAccountTestRecordFailureDoesNotClaimSuccess(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	accounts.recordErr = errors.New("log store unavailable")
	tester := &providerAccountTesterStub{result: successfulAccountProbeResult("gpt-4o-mini")}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "provider_account_probe_record_failed") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestProviderAccountTestRecordsAfterClientCancellation(t *testing.T) {
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	tester := &providerAccountTesterStub{result: successfulAccountProbeResult("gpt-4o-mini")}
	deps := ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}

	router := chi.NewRouter()
	router.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountTestRoutes(r, deps)
	})
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/provider-accounts/99/test", nil).WithContext(requestCtx)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(accounts.records) != 1 {
		t.Fatalf("已发生的探测必须留痕，records=%d want 1", len(accounts.records))
	}
	if accounts.recordContextErr != nil {
		t.Fatalf("探测收尾复用了已取消请求上下文: %v", accounts.recordContextErr)
	}
}

func TestProviderAccountTestServiceErrorIsRecordedWithoutRawLeak(t *testing.T) {
	secretMarker := "raw-secret-in-error"
	accounts := newProviderAccountTestAccountStoreStub()
	accounts.put(providerAccountTestRow(7, 99))
	tester := &providerAccountTesterStub{err: errors.New("dial failed " + secretMarker)}

	rec := invokeProviderAccountTest(t, ProviderAccountTestDeps{
		Auth: providerAccountTestAuthForTenant(7), Accounts: accounts, Tester: tester,
	}, http.MethodPost, "/admin/v1/provider-accounts/99/test", "")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretMarker) {
		t.Fatalf("响应泄露内部错误: %s", rec.Body.String())
	}
	if len(accounts.records) != 1 || accounts.records[0].TestError == nil {
		t.Fatalf("records=%+v", accounts.records)
	}
}

func invokeProviderAccountTest(t *testing.T, deps ProviderAccountTestDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountTestRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func fixedProviderAccountTestNow() time.Time {
	return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
}

func successfulAccountProbeResult(model string) accountprobe.Result {
	return accountprobe.Result{
		OK: true, Attempted: true, Model: model, ProtocolFamily: "openai_chat",
		StatusCode: http.StatusOK, Message: "上游模型探测成功",
		LatencyMS: 31, TestedAt: fixedProviderAccountTestNow(),
		HealthSignal: channelhealth.SignalSuccess, HealthSignalRecorded: true,
	}
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
	rows             map[string]admindb.AdminProviderAccountRow
	getArgs          []admindb.GetAdminProviderAccountParams
	records          []providerAccountTestRecordInput
	err              error
	recordErr        error
	recordContextErr error
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

func (s *providerAccountTestAccountStoreStub) RecordProviderAccountTest(ctx context.Context, in providerAccountTestRecordInput) error {
	s.recordContextErr = ctx.Err()
	s.records = append(s.records, in)
	return s.recordErr
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

type providerAccountTesterStub struct {
	inputs []accountprobe.Input
	result accountprobe.Result
	err    error
}

func (s *providerAccountTesterStub) Probe(_ context.Context, in accountprobe.Input) (accountprobe.Result, error) {
	s.inputs = append(s.inputs, in)
	return s.result, s.err
}
