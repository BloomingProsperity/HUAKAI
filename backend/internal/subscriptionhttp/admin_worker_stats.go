// HUAKAI · iKun

package subscriptionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/workerpulse"
)

// WorkerStatsReader 读取进程内的订阅 worker 计数器。
type WorkerStatsReader interface {
	ReadWorkerStats(context.Context) WorkerStats
}

// AdminWorkerStatsDeps 持有 admin stats 端点的依赖。
type AdminWorkerStatsDeps struct {
	Auth      AdminAuth
	Reader    WorkerStatsReader
	Pulses    workerpulse.Reader
	ReplicaID string
}

// WorkerStats 是订阅通知/续费 worker 的 JSON 响应。
type WorkerStats struct {
	AnsweringReplica      string                           `json:"answering_replica"`
	Reminder              ReminderWorkerStats              `json:"reminder"`
	Expiry                ExpiryWorkerStats                `json:"expiry"`
	AutoRenew             AutoRenewWorkerStats             `json:"auto_renew"`
	PendingReconciliation PendingReconciliationWorkerStats `json:"pending_reconciliation"`
}

type ReminderWorkerStats struct {
	TickCount   uint64                 `json:"tick_count"`
	SentTotal   uint64                 `json:"sent_total"`
	FailedTicks uint64                 `json:"failed_ticks"`
	Cluster     workerpulse.ClusterView `json:"cluster"`
}

type ExpiryWorkerStats struct {
	TickCount    uint64                 `json:"tick_count"`
	ExpiredTotal uint64                 `json:"expired_total"`
	FailedTicks  uint64                 `json:"failed_ticks"`
	Cluster      workerpulse.ClusterView `json:"cluster"`
}

// AutoRenewWorkerStats 是自动续费 worker 的 money 计数器。Enabled=false 表示该 worker
// 被部署者显式停用时 Enabled=false，其余计数恒 0。
type AutoRenewWorkerStats struct {
	Enabled      bool                   `json:"enabled"`
	TickCount    uint64                 `json:"tick_count"`
	RenewedTotal uint64                 `json:"renewed_total"`
	SkippedTotal uint64                 `json:"skipped_total"`
	FailedTicks  uint64                 `json:"failed_ticks"`
	Cluster      workerpulse.ClusterView `json:"cluster"`
}

// PendingReconciliationWorkerStats 暴露 pending 且尚无对账事件的 usage_records 数量,
// 供运维用 pending_reconciliation_only=true 过滤器定位待人工核查行。
type PendingReconciliationWorkerStats struct {
	UsageRecords int64 `json:"usage_records"`
	QueryFailed  bool  `json:"query_failed"`
}

// NewAdminWorkerStatsHandler 返回本副本计数并叠加上跨副本心跳集群口径。
func NewAdminWorkerStatsHandler(d AdminWorkerStatsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Reader == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "subscription worker stats dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			if errors.Is(err, admin.ErrAdminBackend) {
				writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
			} else {
				writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
			}
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
			return
		}
		now := time.Now().UTC()
		reminderCluster, err := workerpulse.Load(r.Context(), d.Pulses, workerpulse.JobSubscriptionReminder, now)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "worker_cluster_unavailable", "subscription worker cluster status unavailable")
			return
		}
		expiryCluster, err := workerpulse.Load(r.Context(), d.Pulses, workerpulse.JobSubscriptionExpiry, now)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "worker_cluster_unavailable", "subscription worker cluster status unavailable")
			return
		}
		autoRenewCluster, err := workerpulse.Load(r.Context(), d.Pulses, workerpulse.JobSubscriptionAutoRenew, now)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "worker_cluster_unavailable", "subscription worker cluster status unavailable")
			return
		}
		stats := d.Reader.ReadWorkerStats(r.Context())
		replicaID := strings.TrimSpace(d.ReplicaID)
		if replicaID == "" {
			replicaID = workerpulse.ReplicaID()
		}
		stats.AnsweringReplica = replicaID
		stats.Reminder.Cluster = reminderCluster
		stats.Expiry.Cluster = expiryCluster
		stats.AutoRenew.Cluster = autoRenewCluster
		writeJSON(w, http.StatusOK, stats)
	}
}
