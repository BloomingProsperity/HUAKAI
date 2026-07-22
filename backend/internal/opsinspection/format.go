package opsinspection

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

// FormattedReport 是交给邮件发送方的渲染产物：一个主题、一个 HTML 正文以及一个
// 纯文本兜底。这三者仅由报告中带类型的 enum/count/id 字段构建——没有任何路径能让
// 自由格式的用户内容流入其中。
type FormattedReport struct {
	Subject   string
	HTMLBody  string
	PlainBody string
}

// Headline 返回单行状态摘要。一切正常的运行显示为 "All clear"；否则会给出问题数，
// 并在存在 critical 时给出 critical 数，使运维仅凭主题行即可分诊。
func Headline(r InspectionReport) string {
	if r.AllClear() {
		return "All clear"
	}
	issues := r.IssueCount()
	if len(r.SourceErrors) > 0 && issues == 0 {
		return fmt.Sprintf("%d diagnostic source(s) unavailable", len(r.SourceErrors))
	}
	noun := "issue"
	if issues != 1 {
		noun = "issues"
	}
	if crit := r.CriticalCount(); crit > 0 {
		return fmt.Sprintf("%d %s need attention (%d critical)", issues, noun, crit)
	}
	return fmt.Sprintf("%d %s need attention", issues, noun)
}

// Subject 构建邮件主题：状态徽标 + 标题 + UTC 日期。
func Subject(r InspectionReport) string {
	badge := "[OK]"
	switch r.Worst() {
	case SeverityCritical:
		badge = "[CRITICAL]"
	case SeverityWarn:
		badge = "[WARN]"
	}
	return fmt.Sprintf("%s HUAKAI daily ops inspection — %s — %s",
		badge, Headline(r), r.GeneratedAt.UTC().Format("2006-01-02"))
}

// Format 把完整报告渲染为 主题 + HTML + 纯文本 三种正文。
func Format(r InspectionReport) FormattedReport {
	return FormattedReport{
		Subject:   Subject(r),
		HTMLBody:  renderHTML(r),
		PlainBody: renderPlain(r),
	}
}

func sevBadge(s Severity) string {
	switch s {
	case SeverityCritical:
		return "CRITICAL"
	case SeverityWarn:
		return "WARN"
	default:
		return "OK"
	}
}

// esc 在值进入邮件正文前对其做 HTML 转义。报告中每个动态字符串都是系统 enum/id，
// 但作为纵深防御，转义是无条件施加的，这样即便将来出现自由格式字段也无法注入标记。
func esc(v string) string { return html.EscapeString(v) }

// sortedIntCounts 将 string->int 映射渲染为按键排序的 "k=v" 键值对，以保证输出
// 确定性。
func sortedIntCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func sortedInt64Counts(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func sortedStrMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func renderHTML(r InspectionReport) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="font-family:sans-serif;color:#222">`)
	b.WriteString(`<h2>HUAKAI daily ops inspection</h2>`)
	b.WriteString(`<p><strong>` + esc(Headline(r)) + `</strong></p>`)
	b.WriteString(`<p>Generated: ` + esc(r.GeneratedAt.UTC().Format(time.RFC3339)) +
		` · tenant ` + fmt.Sprintf("%d", r.TenantID) + `</p>`)

	rowHTML := func(title string, sev Severity, body string) {
		b.WriteString(`<h3>` + esc(title) + ` <small>[` + esc(sevBadge(sev)) + `]</small></h3>`)
		b.WriteString(`<p>` + body + `</p>`)
	}

	rowHTML("Account pool health", r.AccountPool.Severity, fmt.Sprintf(
		"total=%d, healthy=%d, degraded=%d, down=%d<br>by_state: %s",
		r.AccountPool.Total, r.AccountPool.Healthy, r.AccountPool.Degraded, r.AccountPool.Down,
		esc(sortedInt64Counts(r.AccountPool.ByState))))

	rowHTML("Credential renewal", r.Credentials.Severity, fmt.Sprintf(
		"total=%d, need_renewal=%d, failed=%d<br>by_state: %s<br>top_fail_class: %s",
		r.Credentials.Total, r.Credentials.NeedRenewal, r.Credentials.Failed,
		esc(sortedIntCounts(r.Credentials.ByState)), esc(sortedIntCounts(r.Credentials.TopFailClass))))

	rowHTML("Dead-letter queue", r.DLQ.Severity, fmt.Sprintf(
		"total=%d, pending=%d<br>by_lane: %s<br>by_status: %s<br>oldest_pending: %s",
		r.DLQ.Total, r.DLQ.PendingTotal, esc(sortedIntCounts(r.DLQ.ByLane)),
		esc(sortedIntCounts(r.DLQ.ByStatus)), esc(sortedStrMap(r.DLQ.OldestPendingAt))))

	rowHTML("Error-class trend", r.ErrorTrend.Severity, fmt.Sprintf(
		"sample=%d, stream_terminations=%d, pending_reconcile=%d<br>top_classes: %s",
		r.ErrorTrend.SampleSize, r.ErrorTrend.StreamTerms, r.ErrorTrend.PendingRecn,
		esc(classList(r.ErrorTrend.TopClasses))))

	rowHTML("Module knowledge", r.Modules.Severity, fmt.Sprintf(
		"total=%d, ok=%d, degraded=%d, error=%d, unknown=%d<br>by_status: %s",
		r.Modules.Total, r.Modules.OK, r.Modules.Degraded, r.Modules.Errored, r.Modules.Unknown,
		esc(sortedIntCounts(r.Modules.ByStatus))))

	if len(r.SourceErrors) > 0 {
		b.WriteString(`<h3>Unavailable sources</h3><ul>`)
		for _, se := range r.SourceErrors {
			b.WriteString(`<li>` + esc(se.Section) + `: ` + esc(se.Class) + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<hr><p style="color:#888;font-size:12px">` +
		`This is an automated system-diagnostic summary (enums/counts/ids only). ` +
		`It contains no request content, credentials, or user data.</p>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func renderPlain(r InspectionReport) string {
	var b strings.Builder
	b.WriteString("HUAKAI daily ops inspection\n")
	b.WriteString(Headline(r) + "\n")
	b.WriteString(fmt.Sprintf("Generated: %s · tenant %d\n\n",
		r.GeneratedAt.UTC().Format(time.RFC3339), r.TenantID))

	b.WriteString(fmt.Sprintf("[%s] Account pool: total=%d healthy=%d degraded=%d down=%d (%s)\n",
		sevBadge(r.AccountPool.Severity), r.AccountPool.Total, r.AccountPool.Healthy,
		r.AccountPool.Degraded, r.AccountPool.Down, sortedInt64Counts(r.AccountPool.ByState)))
	b.WriteString(fmt.Sprintf("[%s] Credentials: total=%d need_renewal=%d failed=%d (state: %s; fail: %s)\n",
		sevBadge(r.Credentials.Severity), r.Credentials.Total, r.Credentials.NeedRenewal,
		r.Credentials.Failed, sortedIntCounts(r.Credentials.ByState), sortedIntCounts(r.Credentials.TopFailClass)))
	b.WriteString(fmt.Sprintf("[%s] DLQ: total=%d pending=%d (lane: %s; status: %s; oldest: %s)\n",
		sevBadge(r.DLQ.Severity), r.DLQ.Total, r.DLQ.PendingTotal, sortedIntCounts(r.DLQ.ByLane),
		sortedIntCounts(r.DLQ.ByStatus), sortedStrMap(r.DLQ.OldestPendingAt)))
	b.WriteString(fmt.Sprintf("[%s] Error trend: sample=%d stream_term=%d pending_recn=%d (top: %s)\n",
		sevBadge(r.ErrorTrend.Severity), r.ErrorTrend.SampleSize, r.ErrorTrend.StreamTerms,
		r.ErrorTrend.PendingRecn, classList(r.ErrorTrend.TopClasses)))
	b.WriteString(fmt.Sprintf("[%s] Modules: total=%d ok=%d degraded=%d error=%d unknown=%d\n",
		sevBadge(r.Modules.Severity), r.Modules.Total, r.Modules.OK, r.Modules.Degraded,
		r.Modules.Errored, r.Modules.Unknown))

	if len(r.SourceErrors) > 0 {
		b.WriteString("\nUnavailable sources:\n")
		for _, se := range r.SourceErrors {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", se.Section, se.Class))
		}
	}
	b.WriteString("\nAutomated system-diagnostic summary (enums/counts/ids only). No request content, credentials, or user data.\n")
	return b.String()
}

func classList(cs []ClassCount) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s=%d", c.Class, c.Count))
	}
	return strings.Join(parts, ", ")
}
