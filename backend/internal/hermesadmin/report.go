// Package hermesadmin 是受管理员门控的每日运维巡检（WAVE H5）。
//
// 它按计划运行既有的只读诊断，从经过脱敏的系统诊断数据组合出一份平台/运维全局的
// 运维报告，并将其邮件发送给已配置的管理员。它是纯增量的，且远离每条请求的热路径：
// 这里没有任何东西按请求运行。
//
// 隐私边界（关键）：报告的每一段都仅携带系统诊断的 enums / counts / ids——
// 绝不携带 prompt、completion、原始请求体、密钥、凭证字节、金额或 PII。本包消费的
// 诊断源已经过脱敏（H3 工具及底层仅 SELECT 的读取都投影为 enums/counts），而
// InspectionService 又再次投影为一组封闭的带类型字段，所以上游行中新增的自由格式
// 字段永远无法悄悄流入邮件正文。
package hermesadmin

import (
	"context"
	"sort"
	"time"
)

// Severity 对某一段的发现有多需要关注进行分级。标题由各段中最严重的级别推导而来。
type Severity string

const (
	// SeverityOK——该段没有需要处理的事项。
	SeverityOK Severity = "ok"
	// SeverityWarn——一个非致命信号，运维应当扫一眼（例如一个临近续期的凭证、
	// 一小撮 DLQ 积压）。
	SeverityWarn Severity = "warn"
	// SeverityCritical——一个正在发生的失败状况（账号宕掉、凭证失败、被投递到
	// 死信的事件），需要关注。
	SeverityCritical Severity = "critical"
)

// severityRank 为各严重级别排序，使报告标题能挑出最严重的那个。
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 2
	case SeverityWarn:
		return 1
	default:
		return 0
	}
}

// worse 返回 a 与 b 中更严重的那个级别。
func worse(a, b Severity) Severity {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

// AccountPoolSection 汇总渠道/账号健康（仅聚合状态）。
type AccountPoolSection struct {
	Total    int64            `json:"total"`
	ByState  map[string]int64 `json:"by_state"`
	Healthy  int64            `json:"healthy"`
	Degraded int64            `json:"degraded"`
	Down     int64            `json:"down"`
	Severity Severity         `json:"severity"`
}

// CredentialSection 汇总整个部署的凭证续期状态。
type CredentialSection struct {
	Total        int            `json:"total"`
	NeedRenewal  int            `json:"need_renewal"`
	Failed       int            `json:"failed"`
	ByState      map[string]int `json:"by_state"`
	TopFailClass map[string]int `json:"top_fail_class"`
	Severity     Severity       `json:"severity"`
}

// DLQSection 汇总死信队列深度 + 每条 lane 上最旧的待处理项。
type DLQSection struct {
	Total           int               `json:"total"`
	PendingTotal    int               `json:"pending_total"`
	ByLane          map[string]int    `json:"by_lane"`
	ByStatus        map[string]int    `json:"by_status"`
	OldestPendingAt map[string]string `json:"oldest_pending_at"` // lane -> RFC3339 UTC 时间
	Severity        Severity          `json:"severity"`
}

// ErrorTrendSection 汇总近期窗口内排名靠前的 error/end 分类。
type ErrorTrendSection struct {
	SampleSize  int            `json:"sample_size"`
	ByEndClass  map[string]int `json:"by_end_class"`
	TopClasses  []ClassCount   `json:"top_classes"`
	StreamTerms int            `json:"stream_terminations"`
	PendingRecn int            `json:"pending_reconcile"`
	Severity    Severity       `json:"severity"`
}

// ClassCount 是一个 (class, count) 键值对，用于排序后的 top-N 列表。
type ClassCount struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

// ModuleSection 汇总模块知识（module-knowledge）健康快照。
type ModuleSection struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
	OK       int            `json:"ok"`
	Degraded int            `json:"degraded"`
	Errored  int            `json:"errored"`
	Unknown  int            `json:"unknown"`
	Severity Severity       `json:"severity"`
}

// SourceError 记录某个诊断源读取失败。报告仍会（降级）发送，这样单次读取失败绝不
// 会让整个巡检静默；运维能看到哪一段失明了。它只携带段名 + 一个固定的错误分类
//——绝不携带底层错误文本，因为后者可能内嵌标识符。
type SourceError struct {
	Section string `json:"section"`
	Class   string `json:"class"`
}

// InspectionReport 是一次巡检运行的结构化、已脱敏的结果。每个字段都是系统诊断数据；
// 这里没有任何东西是自由格式的用户内容。
type InspectionReport struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	TenantID     int64              `json:"tenant_id"`
	AccountPool  AccountPoolSection `json:"account_pool"`
	Credentials  CredentialSection  `json:"credentials"`
	DLQ          DLQSection         `json:"dlq"`
	ErrorTrend   ErrorTrendSection  `json:"error_trend"`
	Modules      ModuleSection      `json:"modules"`
	SourceErrors []SourceError      `json:"source_errors,omitempty"`
}

// IssueCount 统计严重级别达到或高于 warn 的段数（即标题中「N issues need
// attention」那个数字）。
func (r InspectionReport) IssueCount() int {
	n := 0
	for _, s := range []Severity{
		r.AccountPool.Severity, r.Credentials.Severity, r.DLQ.Severity,
		r.ErrorTrend.Severity, r.Modules.Severity,
	} {
		if severityRank(s) >= severityRank(SeverityWarn) {
			n++
		}
	}
	return n
}

// CriticalCount 统计处于 critical 严重级别的段数。
func (r InspectionReport) CriticalCount() int {
	n := 0
	for _, s := range []Severity{
		r.AccountPool.Severity, r.Credentials.Severity, r.DLQ.Severity,
		r.ErrorTrend.Severity, r.Modules.Severity,
	} {
		if s == SeverityCritical {
			n++
		}
	}
	return n
}

// AllClear 报告本次运行是否「没发现任何需要处理的事项，且每个源都读取成功」。
// 失明的段（source error）不算 all-clear：我们不能对读不到的数据宣称健康。
func (r InspectionReport) AllClear() bool {
	return r.IssueCount() == 0 && len(r.SourceErrors) == 0
}

// Worst 返回整份报告中最高的严重级别（驱动标题）。
func (r InspectionReport) Worst() Severity {
	w := SeverityOK
	for _, s := range []Severity{
		r.AccountPool.Severity, r.Credentials.Severity, r.DLQ.Severity,
		r.ErrorTrend.Severity, r.Modules.Severity,
	} {
		w = worse(w, s)
	}
	if len(r.SourceErrors) > 0 {
		w = worse(w, SeverityWarn)
	}
	return w
}

// topN 返回 m 中计数最高的若干分类，按 count 降序、再按 name 升序排序
//（稳定、确定性的输出，便于测试 + 邮件）。
func topN(m map[string]int, n int) []ClassCount {
	out := make([]ClassCount, 0, len(m))
	for k, v := range m {
		out = append(out, ClassCount{Class: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Class < out[j].Class
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// limitContext 为一次巡检 tick 内的每次诊断读取设定边界，使单个慢源不会让每日运行
// 拖延超过一个合理预算。
const inspectionReadBudget = 30 * time.Second

func withReadBudget(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, inspectionReadBudget)
}
