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

// 诊断源的读取上限。小而固定：每日巡检是面向根因的聚合，不是批量导出。
const (
	renewReadLimit = 500
	dlqReadLimit   = 500
	usageReadLimit = 500
	topClassCount  = 5
)

// ModuleSnapshotter 是巡检读取的只读模块知识探针接口。运行时的
// moduleregistry.Registry 与 H2 的 moduleSource 都满足它（Snapshot(ctx)
// []ModuleSnapshot），因此接线层会注入当前在用的那个，而本包无需 import 接线层。
type ModuleSnapshotter interface {
	Snapshot(ctx context.Context) []moduleregistry.ModuleSnapshot
}

// Sources 打包了巡检所复用的既有只读诊断函数。每个字段都是某个 WAVE H3 工具所
// 封装的同一底层仅 SELECT 读取——本包以聚合全局范围重新运行它们，并不重新实现任何
// 诊断逻辑：
//
//   - RenewStatus  == credentialstore.Store.ListRenewStatus（credential_diagnose
//     的续期读取）；以 TenantID=nil 调用以获得平台范围视图。
//   - ChannelSummary == channelhealth.Service.SummarizeChannelHealth
//     （account_health_diagnose 的聚合渠道视图）。
//   - DLQList       == dlq.Store.List（dlq_inspect 的读取；Replay 从不接线）。
//   - ListUsage     == billingQueries.ListUsageRecords（log_analyze /
//     request_diagnose 的读取）。
//   - Modules       == H2 的 moduleregistry 快照。
//
// 某字段为 nil 会把对应那一段降级为 SourceError；报告的其余部分仍会组合出来
//（软失败，绝不 panic）。
type Sources struct {
	RenewStatus    func(ctx context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error)
	ChannelSummary func(ctx context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error)
	DLQList        func(ctx context.Context, filter dlq.ListFilter) ([]dlq.Record, error)
	ListUsage      func(ctx context.Context, params dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
	Modules        ModuleSnapshotter
}

// InspectionService 从注入的诊断源组合出每日运维报告。它自身不持有任何 DB 句柄
//——每次读取都是注入进来的函数，因此在测试中极易打桩，而在生产中复用线上接线的
// 读取。
type InspectionService struct {
	src      Sources
	tenantID int64
	now      func() time.Time
}

// NewInspectionService 构建该服务。tenantID 把按租户绑定的读取（渠道健康 /
// 用量 / DLQ）限定到运维的部署租户；凭证续期读取是平台范围的（TenantID=nil）。
// 非正数的 tenantID 会被夹取为 1（单租户部署的默认值）。
func NewInspectionService(src Sources, tenantID int64, now func() time.Time) *InspectionService {
	if tenantID <= 0 {
		tenantID = 1
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &InspectionService{src: src, tenantID: tenantID, now: now}
}

// Inspect 运行每一个已接线的诊断源，并返回组合好、已脱敏的报告。它绝不会因单个源
// 失败而返回错误——那个源对应的段保持为零值（计数全为零），并追加一个 SourceError，
// 这样运维仍能看到其余全貌。它通过每个 tick 的读取预算来尊重 ctx 取消。
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

// accountPool 读取租户渠道健康汇总，并把按状态的计数投影到 healthy/degraded/down
// 三个桶。Down = disabled/manual_paused（运维必须处置的状态）；
// degraded = degraded/cooling_down/ramping。
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

// credentials 读取平台范围的续期状态（TenantID=nil），并统计其状态表明刷新失败或
// 续期临近的行。「needs renewal」信号是指该行的 refresh_before_at 已成为过去
//（以 worker 自身的时钟为准），与上游的状态字符串无关。
func (s *InspectionService) credentials(ctx context.Context, rep *InspectionReport) CredentialSection {
	sec := CredentialSection{ByState: map[string]int{}, TopFailClass: map[string]int{}, Severity: SeverityOK}
	if s.src.RenewStatus == nil {
		s.addSourceErr(rep, "credentials", "unwired")
		return sec
	}
	rows, err := s.src.RenewStatus(ctx, credentialstore.ListRenewStatusParams{
		TenantID: nil, // 平台范围
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

// dlq 读取租户 DLQ，并报告每条 lane 的深度 + 最旧的待处理失败。这里的「pending」
// 指运维可处置的积压（pending / operator_review / dlq / quarantined）——
// 已投递（delivered）的行不算问题。
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

// errorTrend 读取近期用量窗口，并聚合 end_class 计数 + 流终止数 + 待对账数
//——这些与 log_analyze 暴露的是同一批 enums，绝不涉及原始请求体。一个非成功占主导
// 的窗口会被标记为 warn。
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
	// 任何非 "ok" 的 end class 都是值得扫一眼的错误信号；较高的流终止数或待对账数
	// 同样会触发 warn。
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

// modules 读取 H2 模块知识快照并统计探针状态。探针报告 error 为 critical；
// degraded 为 warn；unknown 仅供参考。
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
