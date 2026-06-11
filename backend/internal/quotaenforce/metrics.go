package quotaenforce

import (
	"expvar"
	"sync"
)

const (
	quotaMetricsMapName                   = "quotaenforce"
	quotaMetricPostCommitFinalizeFailures = "post_commit_finalize_failures_total"
)

var (
	quotaMetricsOnce sync.Once
	quotaMetrics     *expvar.Map
)

func initQuotaMetrics() {
	quotaMetricsOnce.Do(func() {
		quotaMetrics = expvar.NewMap(quotaMetricsMapName)
		quotaMetrics.Add(quotaMetricPostCommitFinalizeFailures, 0)
	})
}

func observePostCommitFinalizeFailure() {
	initQuotaMetrics()
	quotaMetrics.Add(quotaMetricPostCommitFinalizeFailures, 1)
}
