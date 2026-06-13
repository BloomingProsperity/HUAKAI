// Package hermesadmin is the admin-gated daily ops-inspection (WAVE H5).
//
// It runs the EXISTING read-only diagnostics on a schedule, composes a
// platform/operator-wide ops report from sanitized system-diagnostic data, and
// emails it to the configured administrator. It is purely additive and off every
// request hot path: nothing here runs per-request.
//
// Privacy boundary (CRITICAL): every section of the report carries ONLY
// system-diagnostic enums / counts / ids — never prompts, completions, raw
// bodies, secrets, credential bytes, money amounts, or PII. The diagnostic
// sources this package consumes are already sanitized (the H3 tools and the
// underlying SELECT-only reads project to enums/counts), and InspectionService
// projects again into a closed set of typed fields, so a new free-form field in
// an upstream row can never silently flow into the email body.
package hermesadmin

import (
	"context"
	"sort"
	"time"
)

// Severity classifies how concerning a section's findings are. The headline is
// derived from the worst severity across sections.
type Severity string

const (
	// SeverityOK — nothing actionable in this section.
	SeverityOK Severity = "ok"
	// SeverityWarn — a non-fatal signal an operator should glance at (e.g. a
	// credential nearing renewal, a small DLQ backlog).
	SeverityWarn Severity = "warn"
	// SeverityCritical — an actively failing condition (down account, failed
	// credential, dead-lettered events) that needs attention.
	SeverityCritical Severity = "critical"
)

// severityRank orders severities so the report headline can pick the worst.
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

// worse returns the higher-severity of a and b.
func worse(a, b Severity) Severity {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

// AccountPoolSection summarizes channel/account health (aggregate states only).
type AccountPoolSection struct {
	Total    int64            `json:"total"`
	ByState  map[string]int64 `json:"by_state"`
	Healthy  int64            `json:"healthy"`
	Degraded int64            `json:"degraded"`
	Down     int64            `json:"down"`
	Severity Severity         `json:"severity"`
}

// CredentialSection summarizes credential renewal status across the deployment.
type CredentialSection struct {
	Total        int            `json:"total"`
	NeedRenewal  int            `json:"need_renewal"`
	Failed       int            `json:"failed"`
	ByState      map[string]int `json:"by_state"`
	TopFailClass map[string]int `json:"top_fail_class"`
	Severity     Severity       `json:"severity"`
}

// DLQSection summarizes dead-letter-queue depth + oldest pending per lane.
type DLQSection struct {
	Total           int               `json:"total"`
	PendingTotal    int               `json:"pending_total"`
	ByLane          map[string]int    `json:"by_lane"`
	ByStatus        map[string]int    `json:"by_status"`
	OldestPendingAt map[string]string `json:"oldest_pending_at"` // lane -> RFC3339 UTC
	Severity        Severity          `json:"severity"`
}

// ErrorTrendSection summarizes the top error/end classes over the recent window.
type ErrorTrendSection struct {
	SampleSize  int            `json:"sample_size"`
	ByEndClass  map[string]int `json:"by_end_class"`
	TopClasses  []ClassCount   `json:"top_classes"`
	StreamTerms int            `json:"stream_terminations"`
	PendingRecn int            `json:"pending_reconcile"`
	Severity    Severity       `json:"severity"`
}

// ClassCount is one (class, count) pair, used for the sorted top-N list.
type ClassCount struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

// ModuleSection summarizes the module-knowledge health snapshot.
type ModuleSection struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
	OK       int            `json:"ok"`
	Degraded int            `json:"degraded"`
	Errored  int            `json:"errored"`
	Unknown  int            `json:"unknown"`
	Severity Severity       `json:"severity"`
}

// SourceError records that one diagnostic source failed to read. The report
// still sends (degraded) so a single read failure never silences the whole
// inspection; the operator sees which section is blind. It carries only the
// section name + a fixed error class — never the underlying error text, which
// could embed identifiers.
type SourceError struct {
	Section string `json:"section"`
	Class   string `json:"class"`
}

// InspectionReport is the structured, sanitized result of one inspection run.
// Every field is system-diagnostic; nothing here is free-form user content.
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

// IssueCount counts the sections at or above warn severity (the "N issues need
// attention" number in the headline).
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

// CriticalCount counts the sections at critical severity.
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

// AllClear reports whether the run found nothing actionable AND every source
// read succeeded. A blind section (source error) is NOT all-clear: we cannot
// claim healthy on data we could not read.
func (r InspectionReport) AllClear() bool {
	return r.IssueCount() == 0 && len(r.SourceErrors) == 0
}

// Worst returns the highest severity across the report (drives the headline).
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

// topN returns the highest-count classes from m, sorted by count desc then name
// asc (stable, deterministic output for tests + email).
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

// limitContext bounds each diagnostic read inside an inspection tick so a single
// slow source cannot stall the daily run past a sane budget.
const inspectionReadBudget = 30 * time.Second

func withReadBudget(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, inspectionReadBudget)
}
