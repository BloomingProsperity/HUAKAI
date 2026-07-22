package opsinspection

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

const testTenant = 1

func fixedNow() time.Time { return time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC) }

// healthySources 返回一组 Sources，其每次读取都报告一个干净的部署。
func healthySources() Sources {
	return Sources{
		ChannelSummary: func(_ context.Context, _ int64) (channelhealth.ChannelHealthSummary, error) {
			return channelhealth.ChannelHealthSummary{
				Total:   3,
				ByState: map[channelhealth.HealthState]int64{channelhealth.StateActive: 3},
			}, nil
		},
		RenewStatus: func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
			ok := "success"
			return []credentialstore.RenewStatusMetadata{
				{CredentialID: 1, State: "active", LastRefreshOutcome: &ok},
			}, nil
		},
		DLQList: func(_ context.Context, _ dlq.ListFilter) ([]dlq.Record, error) {
			return []dlq.Record{
				{ID: 1, Lane: dlq.LaneHigh, Status: dlq.StatusDelivered, FailureAt: fixedNow()},
			}, nil
		},
		ListUsage: func(_ context.Context, _ dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, TenantID: testTenant, EndClass: "ok"},
				{ID: 2, TenantID: testTenant, EndClass: "ok"},
			}, nil
		},
		Modules: fakeSnapshotter{snaps: []moduleregistry.ModuleSnapshot{
			{Descriptor: moduleregistry.ModuleDescriptor{ID: "a"}, Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusOK}},
		}},
	}
}

type fakeSnapshotter struct {
	snaps []moduleregistry.ModuleSnapshot
}

func (f fakeSnapshotter) Snapshot(_ context.Context) []moduleregistry.ModuleSnapshot { return f.snaps }

// TestInspectAllClear：一个完全健康的部署会产出零问题、无源错误、
// AllClear()==true，以及 OK 标题。
// 捕获的回归：若某段的严重级别逻辑把健康输入误判为 warn/critical（例如把
// "active" 渠道算成 down），AllClear 会翻转为 false，该测试随之变红。
func TestInspectAllClear(t *testing.T) {
	svc := NewInspectionService(healthySources(), testTenant, fixedNow)
	rep := svc.Inspect(context.Background())

	if len(rep.SourceErrors) != 0 {
		t.Fatalf("expected no source errors, got %v", rep.SourceErrors)
	}
	if !rep.AllClear() {
		t.Fatalf("expected all-clear, got issues=%d worst=%s", rep.IssueCount(), rep.Worst())
	}
	if got := Headline(rep); got != "All clear" {
		t.Fatalf("expected all-clear headline, got %q", got)
	}
	// 五个段都必须存在（已组合），由非 nil 的 map 来证明。
	if rep.AccountPool.ByState == nil || rep.Credentials.ByState == nil ||
		rep.DLQ.ByLane == nil || rep.ErrorTrend.ByEndClass == nil || rep.Modules.ByStatus == nil {
		t.Fatalf("a section was not composed: %+v", rep)
	}
}

func TestCredentialInspectionIsTenantScoped(t *testing.T) {
	sources := healthySources()
	sources.RenewStatus = func(_ context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
		if params.TenantID == nil || *params.TenantID != testTenant || params.Limit != renewReadLimit {
			t.Fatalf("凭据巡检范围=%+v，期望固定平台租户", params)
		}
		return nil, nil
	}
	NewInspectionService(sources, testTenant, fixedNow).Inspect(context.Background())
}

func TestInspectionServiceRejectsMissingPlatformTenant(t *testing.T) {
	if service := NewInspectionService(healthySources(), 0, fixedNow); service != nil {
		t.Fatal("缺少平台租户时不应构造巡检服务")
	}
}

// TestInspectIssuesNeedAttention：一个带有 宕掉账号 + 失败凭证 + DLQ 积压 +
// 模块错误 的部署会产出 critical 严重级别、非零的问题数、AllClear()==false，
// 以及「issues need attention」标题。
// 自证：它断言「issues 标题」不同于上面健康输入所产出的「all-clear 标题」。
// 捕获的回归：若 宕掉账号 / 失败凭证 / 待处理 DLQ 的检测被改坏，报告在一个已损坏
// 的部署上仍会显示为「All clear」。
func TestInspectIssuesNeedAttention(t *testing.T) {
	fail := "failure"
	failClass := "invalid_grant"
	src := healthySources()
	src.ChannelSummary = func(_ context.Context, _ int64) (channelhealth.ChannelHealthSummary, error) {
		return channelhealth.ChannelHealthSummary{
			Total: 3,
			ByState: map[channelhealth.HealthState]int64{
				channelhealth.StateActive:   1,
				channelhealth.StateDisabled: 2, // 2 个 down
			},
		}, nil
	}
	src.RenewStatus = func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
		return []credentialstore.RenewStatusMetadata{
			{CredentialID: 7, State: "error", LastRefreshOutcome: &fail, FailureClass: &failClass, FailureCount: 4},
		}, nil
	}
	src.DLQList = func(_ context.Context, _ dlq.ListFilter) ([]dlq.Record, error) {
		return []dlq.Record{
			{ID: 9, Lane: dlq.LaneHigh, Status: dlq.StatusDLQ, FailureAt: fixedNow().Add(-2 * time.Hour)},
		}, nil
	}
	src.Modules = fakeSnapshotter{snaps: []moduleregistry.ModuleSnapshot{
		{Descriptor: moduleregistry.ModuleDescriptor{ID: "x"}, Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusError}},
	}}

	svc := NewInspectionService(src, testTenant, fixedNow)
	rep := svc.Inspect(context.Background())

	if rep.AllClear() {
		t.Fatalf("expected NOT all-clear on a broken deployment")
	}
	if rep.Worst() != SeverityCritical {
		t.Fatalf("expected critical worst severity, got %s", rep.Worst())
	}
	if rep.AccountPool.Down != 2 {
		t.Fatalf("expected 2 down accounts, got %d", rep.AccountPool.Down)
	}
	if rep.Credentials.Failed != 1 {
		t.Fatalf("expected 1 failed credential, got %d", rep.Credentials.Failed)
	}
	if rep.DLQ.PendingTotal != 1 {
		t.Fatalf("expected 1 pending DLQ, got %d", rep.DLQ.PendingTotal)
	}
	if rep.Modules.Errored != 1 {
		t.Fatalf("expected 1 errored module, got %d", rep.Modules.Errored)
	}
	head := Headline(rep)
	if !strings.Contains(head, "need attention") || !strings.Contains(head, "critical") {
		t.Fatalf("expected issues+critical headline, got %q", head)
	}
	// 自证：两个不同的输入必须产出不同的标题。
	if head == Headline(NewInspectionService(healthySources(), testTenant, fixedNow).Inspect(context.Background())) {
		t.Fatalf("broken and healthy inputs produced the same headline %q — non-discriminating", head)
	}
}

func TestInspectIncludesObsDLQSource(t *testing.T) {
	src := healthySources()
	src.ObsDLQList = func(_ context.Context, filter obsdlq.AdminListFilter) ([]obsdlq.AdminDeadEvent, error) {
		if filter.TenantID == nil || *filter.TenantID != testTenant || filter.Limit != dlqReadLimit {
			t.Fatalf("obs dlq filter=%+v, want tenant scoped read limit", filter)
		}
		return []obsdlq.AdminDeadEvent{{
			ID:           "dead-obs-1",
			TenantID:     testTenant,
			Priority:     obsdlq.PriorityCritical,
			OutboxStatus: obsdlq.StatusFailedDead,
			DeadAt:       fixedNow().Add(-time.Hour),
		}}, nil
	}

	rep := NewInspectionService(src, testTenant, fixedNow).Inspect(context.Background())
	if rep.DLQ.PendingTotal != 1 || rep.DLQ.ByLane["OBS_CRITICAL"] != 1 || rep.DLQ.ByStatus["obs_failed_dead"] != 1 {
		t.Fatalf("obs dlq not reflected in report: %+v", rep.DLQ)
	}
}

// TestSourceErrorDegradesNotPanics：一次出错的读取会把对应那一段降级为
// SourceError，而报告其余部分仍会组合出来；AllClear()==false，因为失明的段
// 不能被宣称为健康。
// 捕获的回归：若某个源错误被吞掉（未追加 SourceError），报告会对它从未读到的
// 数据错误地显示为 all-clear。
func TestSourceErrorDegradesNotPanics(t *testing.T) {
	src := healthySources()
	src.DLQList = func(_ context.Context, _ dlq.ListFilter) ([]dlq.Record, error) {
		return nil, context.DeadlineExceeded
	}
	svc := NewInspectionService(src, testTenant, fixedNow)
	rep := svc.Inspect(context.Background())

	found := false
	for _, se := range rep.SourceErrors {
		if se.Section == "dlq" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dlq source error, got %v", rep.SourceErrors)
	}
	if rep.AllClear() {
		t.Fatalf("a report with an unreadable source must not be all-clear")
	}
}

// TestNeedRenewalDetection：一个 refresh_before_at 已成为过去的凭证会被计为
// need_renewal（warn），与刷新失败（critical）相区分。
// 捕获的回归：颠倒 Before/After 比较会漏掉临近的续期，并低报 warn 严重级别。
func TestNeedRenewalDetection(t *testing.T) {
	past := fixedNow().Add(-time.Hour)
	ok := "success"
	src := healthySources()
	src.RenewStatus = func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
		return []credentialstore.RenewStatusMetadata{
			{CredentialID: 1, State: "active", LastRefreshOutcome: &ok, RefreshBeforeAt: &past},
		}, nil
	}
	rep := NewInspectionService(src, testTenant, fixedNow).Inspect(context.Background())
	if rep.Credentials.NeedRenewal != 1 {
		t.Fatalf("expected 1 need_renewal, got %d", rep.Credentials.NeedRenewal)
	}
	if rep.Credentials.Failed != 0 {
		t.Fatalf("a not-yet-failed credential must not count as failed, got %d", rep.Credentials.Failed)
	}
	if rep.Credentials.Severity != SeverityWarn {
		t.Fatalf("need-renewal-only should be warn, got %s", rep.Credentials.Severity)
	}
}
