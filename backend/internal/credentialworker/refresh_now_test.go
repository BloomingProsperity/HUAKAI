package credentialworker

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// refreshNowSpy 同时提供后台列表与按账号取行两条查询面。
type refreshNowSpy struct {
	listSpy
	rows map[int64]dbbilling.GetAccountForRefreshByIDRow
}

func (s *refreshNowSpy) GetAccountForRefreshByID(_ context.Context, arg dbbilling.GetAccountForRefreshByIDParams) (dbbilling.GetAccountForRefreshByIDRow, error) {
	row, ok := s.rows[arg.ID]
	if !ok || row.TenantID != arg.TenantID {
		return dbbilling.GetAccountForRefreshByIDRow{}, pgx.ErrNoRows
	}
	return row, nil
}

// inspectingRefresher 在 refresherSpy 之上声明凭据模式是否有刷新语义。
type inspectingRefresher struct {
	refresherSpy
	static map[int64]bool
}

func (r *inspectingRefresher) HasRefreshSemantics(_ context.Context, _ int64, accountID int64) (bool, error) {
	return !r.static[accountID], nil
}

func refreshNowScheduler(t *testing.T, storm *stormSpy, ref Refresher, rows map[int64]dbbilling.GetAccountForRefreshByIDRow) *Scheduler {
	t.Helper()
	q := &refreshNowSpy{rows: rows}
	return NewScheduler(nil, nil, nil, ref, withRefreshQueries(q), withStormAcquirer(storm), withAuditWriter(&auditSpy{}), WithAuditLedger(&ledgerSpy{}))
}

func eligibleRow(id, tenant int64) dbbilling.GetAccountForRefreshByIDRow {
	return dbbilling.GetAccountForRefreshByIDRow{ID: id, TenantID: tenant, ProviderID: 3, VendorName: "openai", Enabled: true, HealthState: "healthy", Refreshable: true, ExpiresAt: pgtype.Timestamptz{}}
}

// 立刻刷新与后台调度共用同一准入链:账号槽被占 → in_progress;厂商端点 / 全局预算耗尽 → deferred
// (端点 token 退还);三 scope 放行且刷新成功 → refreshed;刷新失败 → failed 带分类。
// 判别:把 admitAndRefresh 的槽位判定改成忽略 outcome → in_progress 用例红;去掉全局拒绝时的 endpointRefund → 退还断言红。
func TestRefreshAccountNowSharesAdmissionChain(t *testing.T) {
	rows := map[int64]dbbilling.GetAccountForRefreshByIDRow{5: eligibleRow(5, 7)}

	held := &stormSpy{outcome: auth.OutcomeRefreshLockHeld}
	out, err := refreshNowScheduler(t, held, &refresherSpy{}, rows).RefreshAccountNow(context.Background(), 7, 5)
	if err != nil || out.Status != RefreshNowInProgress || out.Scope != "account" {
		t.Fatalf("槽位被占应 in_progress: %+v err=%v", out, err)
	}

	budget := &stormSpy{globalOutcome: auth.OutcomeStormBudgetExhausted}
	out, err = refreshNowScheduler(t, budget, &refresherSpy{}, rows).RefreshAccountNow(context.Background(), 7, 5)
	if err != nil || out.Status != RefreshNowDeferred || out.Scope != "global" || budget.endpointRefunds != 1 {
		t.Fatalf("全局预算耗尽应 deferred 并退还端点 token: %+v err=%v refunds=%d", out, err, budget.endpointRefunds)
	}

	ok := &stormSpy{}
	ref := &refresherSpy{}
	out, err = refreshNowScheduler(t, ok, ref, rows).RefreshAccountNow(context.Background(), 7, 5)
	if err != nil || out.Status != RefreshNowRefreshed || len(ref.calls) != 1 || ref.calls[0] != 5 || ok.released != 1 {
		t.Fatalf("放行后应刷新并释放槽位: %+v err=%v calls=%v released=%d", out, err, ref.calls, ok.released)
	}

	failing := &refresherSpy{errs: []error{nonRetryableRefreshErr{}}}
	out, err = refreshNowScheduler(t, &stormSpy{}, failing, rows).RefreshAccountNow(context.Background(), 7, 5)
	if err != nil || out.Status != RefreshNowFailed || out.Scope != "refresh" || out.Detail == "" {
		t.Fatalf("刷新失败应为业务结论 failed 而非 error: %+v err=%v", out, err)
	}
}

// 资格与模式判定在消耗任何预算之前完成:他租户 / 不存在 → not_found;禁用或已撤销 → not_applicable(eligibility);
// 静态凭据 → not_applicable(credential_mode);三者都不得触碰风暴准入器。
func TestRefreshAccountNowEligibilityBeforeAdmission(t *testing.T) {
	rows := map[int64]dbbilling.GetAccountForRefreshByIDRow{
		5: eligibleRow(5, 7),
		6: {ID: 6, TenantID: 7, ProviderID: 3, VendorName: "openai", Enabled: false, HealthState: "revoked", Refreshable: false},
	}
	storm := &stormSpy{}
	ref := &inspectingRefresher{static: map[int64]bool{5: true}}
	s := refreshNowScheduler(t, storm, ref, rows)

	if out, err := s.RefreshAccountNow(context.Background(), 8, 5); err != nil || out.Status != RefreshNowNotFound {
		t.Fatalf("他租户应 not_found: %+v err=%v", out, err)
	}
	if out, err := s.RefreshAccountNow(context.Background(), 7, 404); err != nil || out.Status != RefreshNowNotFound {
		t.Fatalf("不存在应 not_found: %+v err=%v", out, err)
	}
	if out, err := s.RefreshAccountNow(context.Background(), 7, 6); err != nil || out.Status != RefreshNowNotApplicable || out.Scope != "eligibility" || out.Detail != "revoked" {
		t.Fatalf("无刷新资格应 not_applicable(eligibility): %+v err=%v", out, err)
	}
	if out, err := s.RefreshAccountNow(context.Background(), 7, 5); err != nil || out.Status != RefreshNowNotApplicable || out.Scope != "credential_mode" {
		t.Fatalf("静态凭据应 not_applicable(credential_mode): %+v err=%v", out, err)
	}
	if storm.calls != 0 || len(ref.calls) != 0 {
		t.Fatalf("资格/模式判定阶段不得触碰准入器或刷新器: storm=%d refresh=%d", storm.calls, len(ref.calls))
	}
	if _, err := s.RefreshAccountNow(context.Background(), 0, 5); err == nil {
		t.Fatal("非法输入必须报错")
	}
	var nilScheduler *Scheduler
	if _, err := nilScheduler.RefreshAccountNow(context.Background(), 7, 5); err == nil {
		t.Fatal("未装配的调度器必须报错")
	}
}

type inspectMetaStore struct {
	vendor, mode string
	err          error
	loadCalled   *bool
}

func (s inspectMetaStore) InspectRefreshMode(context.Context, int64, int64) (string, string, error) {
	return s.vendor, s.mode, s.err
}

func (s inspectMetaStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	if s.loadCalled != nil {
		*s.loadCalled = true
	}
	return credentialstore.CredentialRecord{}, credentialstore.ErrCredentialNotFound
}

func (inspectMetaStore) WithRefreshTransaction(context.Context, func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	return nil
}

// 生产模式检查只读 vendor/auth_mode:api_key / 无行 → false;oauth → true;不得调用到期装载。
func TestHasRefreshSemanticsUsesMetadataNotDueLoad(t *testing.T) {
	loaded := false
	r := &AccountCredentialRefresher{store: inspectMetaStore{vendor: credentialstore.VendorOpenAI, mode: credentialstore.AuthModeAPIKey, loadCalled: &loaded}, registry: DefaultModeAdapterRegistry()}
	ok, err := r.HasRefreshSemantics(context.Background(), 7, 5)
	if err != nil || ok || loaded {
		t.Fatalf("api_key 必须 false 且不走 LoadForRefresh: ok=%v err=%v loaded=%v", ok, err, loaded)
	}

	loaded = false
	r.store = inspectMetaStore{err: credentialstore.ErrCredentialNotFound, loadCalled: &loaded}
	ok, err = r.HasRefreshSemantics(context.Background(), 7, 5)
	if err != nil || ok || loaded {
		t.Fatalf("无凭据必须当不适用而不是基础设施错误: ok=%v err=%v loaded=%v", ok, err, loaded)
	}

	r.store = inspectMetaStore{vendor: credentialstore.VendorAnthropic, mode: credentialstore.AuthModeClaudeAIOAuth}
	ok, err = r.HasRefreshSemantics(context.Background(), 7, 5)
	if err != nil || !ok {
		t.Fatalf("oauth 模式必须可刷新: ok=%v err=%v", ok, err)
	}
}
