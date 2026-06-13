package hermesadmin

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
)

const testTenant = 1

func fixedNow() time.Time { return time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC) }

// healthySources returns sources whose every read reports a clean deployment.
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

// TestInspectAllClear: a fully-healthy deployment yields zero issues, no source
// errors, AllClear()==true, and the OK headline.
// Regression caught: if a section's severity logic mis-flags a healthy input as
// warn/critical (e.g. counts "active" channels as down), AllClear flips false and
// this test goes red.
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
	// All five sections must be present (composed), proven by non-nil maps.
	if rep.AccountPool.ByState == nil || rep.Credentials.ByState == nil ||
		rep.DLQ.ByLane == nil || rep.ErrorTrend.ByEndClass == nil || rep.Modules.ByStatus == nil {
		t.Fatalf("a section was not composed: %+v", rep)
	}
}

// TestInspectIssuesNeedAttention: a deployment with a down account + failed
// credential + DLQ backlog + module error yields critical severity, a non-zero
// issue count, AllClear()==false, and the "issues need attention" headline.
// Self-proving: it asserts the issues-headline DIFFERS from the all-clear
// headline produced by the healthy input above.
// Regression caught: if down-account / failed-credential / pending-DLQ detection
// is neutered, the report would still read "All clear" on a broken deployment.
func TestInspectIssuesNeedAttention(t *testing.T) {
	fail := "failure"
	failClass := "invalid_grant"
	src := healthySources()
	src.ChannelSummary = func(_ context.Context, _ int64) (channelhealth.ChannelHealthSummary, error) {
		return channelhealth.ChannelHealthSummary{
			Total: 3,
			ByState: map[channelhealth.HealthState]int64{
				channelhealth.StateActive:   1,
				channelhealth.StateDisabled: 2, // 2 down
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
	// Self-proving: the two distinct inputs MUST produce distinct headlines.
	if head == Headline(NewInspectionService(healthySources(), testTenant, fixedNow).Inspect(context.Background())) {
		t.Fatalf("broken and healthy inputs produced the same headline %q — non-discriminating", head)
	}
}

// TestSourceErrorDegradesNotPanics: a read that errors degrades that one section
// to a SourceError and the rest of the report still composes; AllClear()==false
// because a blind section cannot be claimed healthy.
// Regression caught: if a source error were swallowed (no SourceError appended),
// the report would falsely read all-clear on data it never read.
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

// TestNeedRenewalDetection: a credential whose refresh_before_at is in the past
// is counted as need_renewal (warn), distinct from a failed refresh (critical).
// Regression caught: flipping the Before/After comparison would miss imminent
// renewals and under-report the warn severity.
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
