package hermesadmin

import (
	"context"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// Diagnostic-source read bounds. Small + fixed: the daily inspection is a
// root-cause aggregate, not a bulk export.
const (
	renewReadLimit = 500
	dlqReadLimit   = 500
	usageReadLimit = 500
	topClassCount  = 5
)

// ModuleSnapshotter is the read-only module-knowledge probe surface the
// inspection reads. The runtime moduleregistry.Registry and the H2 moduleSource
// both satisfy it (Snapshot(ctx) []ModuleSnapshot), so wiring injects whichever
// is live without this package importing the wiring layer.
type ModuleSnapshotter interface {
	Snapshot(ctx context.Context) []moduleregistry.ModuleSnapshot
}

// Sources bundles the EXISTING read-only diagnostic functions the inspection
// reuses. Each field is the SAME underlying SELECT-only read that a WAVE H3 tool
// wraps — this package re-runs them aggregate-wide, it does NOT reimplement any
// diagnostic:
//
//   - RenewStatus  == credentialstore.Store.ListRenewStatus (credential_diagnose's
//     renew read); called with TenantID=nil for a platform-wide view.
//   - ChannelSummary == channelhealth.Service.SummarizeChannelHealth
//     (account_health_diagnose's aggregate channel view).
//   - DLQList       == dlq.Store.List (dlq_inspect's read; Replay is never wired).
//   - ListUsage     == billingQueries.ListUsageRecords (log_analyze / request_diagnose
//     read).
//   - Modules       == the H2 moduleregistry snapshot.
//
// A nil field degrades that one section to a SourceError; the rest of the report
// still composes (fail-soft, never panic).
type Sources struct {
	RenewStatus    func(ctx context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error)
	ChannelSummary func(ctx context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error)
	DLQList        func(ctx context.Context, filter dlq.ListFilter) ([]dlq.Record, error)
	ListUsage      func(ctx context.Context, params dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
	Modules        ModuleSnapshotter
}

// InspectionService composes the daily ops report from the injected diagnostic
// sources. It holds no DB handle of its own — every read is an injected func, so
// it is trivially fakeable in tests and reuses the live wiring's reads in prod.
type InspectionService struct {
	src      Sources
	tenantID int64
	now      func() time.Time
}

// NewInspectionService builds the service. tenantID scopes the tenant-bound reads
// (channel-health / usage / DLQ) to the operator's deployment tenant; the
// credential renew read is platform-wide (TenantID=nil). A non-positive tenantID
// is clamped to 1 (the single-tenant deployment default).
func NewInspectionService(src Sources, tenantID int64, now func() time.Time) *InspectionService {
	if tenantID <= 0 {
		tenantID = 1
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &InspectionService{src: src, tenantID: tenantID, now: now}
}

// Inspect runs every wired diagnostic source and returns the composed, sanitized
// report. It never returns an error for a single source failure — that source's
// section is left at its zero (all-zero counts) and a SourceError is appended, so
// the operator still gets the rest of the picture. It honors ctx cancellation via
// a per-tick read budget.
func (s *InspectionService) Inspect(ctx context.Context) InspectionReport {
	ctx, cancel := withReadBudget(ctx)
	defer cancel()

	rep := InspectionReport{
		GeneratedAt: s.now().UTC(),
		TenantID:    s.tenantID,
	}
	rep.AccountPool = s.accountPool(ctx, &rep)
	rep.Credentials = s.credentials(ctx, &rep)
	rep.DLQ = s.dlq(ctx, &rep)
	rep.ErrorTrend = s.errorTrend(ctx, &rep)
	rep.Modules = s.modules(ctx, &rep)
	return rep
}

func (s *InspectionService) addSourceErr(rep *InspectionReport, section, class string) {
	rep.SourceErrors = append(rep.SourceErrors, SourceError{Section: section, Class: class})
}

// accountPool reads the tenant channel-health summary and projects per-state
// counts into healthy/degraded/down buckets. Down = disabled/manual_paused (the
// states an operator must act on); degraded = degraded/cooling_down/ramping.
func (s *InspectionService) accountPool(ctx context.Context, rep *InspectionReport) AccountPoolSection {
	sec := AccountPoolSection{ByState: map[string]int64{}, Severity: SeverityOK}
	if s.src.ChannelSummary == nil {
		s.addSourceErr(rep, "account_pool", "unwired")
		return sec
	}
	cs, err := s.src.ChannelSummary(ctx, s.tenantID)
	if err != nil {
		s.addSourceErr(rep, "account_pool", "read_failed")
		return sec
	}
	sec.Total = cs.Total
	for state, count := range cs.ByState {
		sec.ByState[string(state)] = count
		switch state {
		case channelhealth.StateActive:
			sec.Healthy += count
		case channelhealth.StateDegraded, channelhealth.StateCoolingDown, channelhealth.StateRamping:
			sec.Degraded += count
		case channelhealth.StateDisabled, channelhealth.StateManualPaused:
			sec.Down += count
		}
	}
	if sec.Down > 0 {
		sec.Severity = SeverityCritical
	} else if sec.Degraded > 0 {
		sec.Severity = SeverityWarn
	}
	return sec
}

// credentials reads platform-wide renew status (TenantID=nil) and counts the
// rows whose state indicates a failed refresh or an imminent renewal. The "needs
// renewal" signal is the row's refresh_before_at being in the past (the worker's
// own clock), independent of the upstream state string.
func (s *InspectionService) credentials(ctx context.Context, rep *InspectionReport) CredentialSection {
	sec := CredentialSection{ByState: map[string]int{}, TopFailClass: map[string]int{}, Severity: SeverityOK}
	if s.src.RenewStatus == nil {
		s.addSourceErr(rep, "credentials", "unwired")
		return sec
	}
	rows, err := s.src.RenewStatus(ctx, credentialstore.ListRenewStatusParams{
		TenantID: nil, // platform-wide
		Limit:    renewReadLimit,
	})
	if err != nil {
		s.addSourceErr(rep, "credentials", "read_failed")
		return sec
	}
	now := s.now().UTC()
	failClasses := map[string]int{}
	for _, r := range rows {
		sec.Total++
		sec.ByState[r.State]++
		if r.LastRefreshOutcome != nil && strings.EqualFold(*r.LastRefreshOutcome, "failure") {
			sec.Failed++
			if r.FailureClass != nil && *r.FailureClass != "" {
				failClasses[*r.FailureClass]++
			}
		}
		if r.RefreshBeforeAt != nil && r.RefreshBeforeAt.Before(now) {
			sec.NeedRenewal++
		}
	}
	for _, c := range topN(failClasses, topClassCount) {
		sec.TopFailClass[c.Class] = c.Count
	}
	if sec.Failed > 0 {
		sec.Severity = SeverityCritical
	} else if sec.NeedRenewal > 0 {
		sec.Severity = SeverityWarn
	}
	return sec
}

// dlq reads the tenant DLQ and reports depth + oldest pending failure per lane.
// "pending" here is the operator-actionable backlog (pending / operator_review /
// dlq / quarantined) — delivered rows are not a problem.
func (s *InspectionService) dlq(ctx context.Context, rep *InspectionReport) DLQSection {
	sec := DLQSection{ByLane: map[string]int{}, ByStatus: map[string]int{}, OldestPendingAt: map[string]string{}, Severity: SeverityOK}
	if s.src.DLQList == nil {
		s.addSourceErr(rep, "dlq", "unwired")
		return sec
	}
	tenant := s.tenantID
	rows, err := s.src.DLQList(ctx, dlq.ListFilter{TenantID: &tenant, Limit: dlqReadLimit})
	if err != nil {
		s.addSourceErr(rep, "dlq", "read_failed")
		return sec
	}
	oldest := map[string]time.Time{}
	for _, r := range rows {
		sec.Total++
		lane := string(r.Lane)
		sec.ByLane[lane]++
		sec.ByStatus[string(r.Status)]++
		if isPendingDLQ(r.Status) {
			sec.PendingTotal++
			if prev, ok := oldest[lane]; !ok || r.FailureAt.Before(prev) {
				oldest[lane] = r.FailureAt
			}
		}
	}
	for lane, ts := range oldest {
		sec.OldestPendingAt[lane] = ts.UTC().Format(time.RFC3339)
	}
	if sec.PendingTotal > 0 {
		sec.Severity = SeverityCritical
	}
	return sec
}

func isPendingDLQ(st dlq.Status) bool {
	switch st {
	case dlq.StatusPending, dlq.StatusInflight, dlq.StatusOperatorReview, dlq.StatusDLQ, dlq.StatusQuarantined:
		return true
	default:
		return false
	}
}

// errorTrend reads the recent usage window and aggregates end_class counts +
// stream terminations + pending reconciliations — the SAME enums log_analyze
// surfaces, never raw bodies. A non-success-dominated window is flagged warn.
func (s *InspectionService) errorTrend(ctx context.Context, rep *InspectionReport) ErrorTrendSection {
	sec := ErrorTrendSection{ByEndClass: map[string]int{}, Severity: SeverityOK}
	if s.src.ListUsage == nil {
		s.addSourceErr(rep, "error_trend", "unwired")
		return sec
	}
	tenant := s.tenantID
	rows, err := s.src.ListUsage(ctx, dbbilling.ListUsageRecordsParams{TenantID: &tenant, PageLimit: usageReadLimit})
	if err != nil {
		s.addSourceErr(rep, "error_trend", "read_failed")
		return sec
	}
	for _, u := range rows {
		sec.SampleSize++
		sec.ByEndClass[u.EndClass]++
		if u.StreamTerminatedReason != nil && *u.StreamTerminatedReason != "" {
			sec.StreamTerms++
		}
		if u.PendingReconciliation {
			sec.PendingRecn++
		}
	}
	sec.TopClasses = topN(sec.ByEndClass, topClassCount)
	// Any non-"ok" end class is an error signal worth a glance; a high stream-
	// termination or pending-reconcile count is also a warn.
	errClasses := 0
	for cls, n := range sec.ByEndClass {
		if !strings.EqualFold(cls, "ok") && !strings.EqualFold(cls, "success") {
			errClasses += n
		}
	}
	if errClasses > 0 || sec.StreamTerms > 0 || sec.PendingRecn > 0 {
		sec.Severity = SeverityWarn
	}
	return sec
}

// modules reads the H2 module-knowledge snapshot and counts probe statuses. A
// probe reporting error is critical; degraded is warn; unknown is informational.
func (s *InspectionService) modules(ctx context.Context, rep *InspectionReport) ModuleSection {
	sec := ModuleSection{ByStatus: map[string]int{}, Severity: SeverityOK}
	if s.src.Modules == nil {
		s.addSourceErr(rep, "modules", "unwired")
		return sec
	}
	snaps := s.src.Modules.Snapshot(ctx)
	for _, m := range snaps {
		sec.Total++
		st := string(m.Probe.Status)
		sec.ByStatus[st]++
		switch m.Probe.Status {
		case moduleregistry.StatusOK:
			sec.OK++
		case moduleregistry.StatusDegraded:
			sec.Degraded++
		case moduleregistry.StatusError:
			sec.Errored++
		default:
			sec.Unknown++
		}
	}
	if sec.Errored > 0 {
		sec.Severity = SeverityCritical
	} else if sec.Degraded > 0 {
		sec.Severity = SeverityWarn
	}
	return sec
}
