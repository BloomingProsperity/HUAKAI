// HUAKAI · iKun

package subscriptionhttp

import (
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// WorkerStatsReader 读取进程内的订阅 worker 计数器。
type WorkerStatsReader interface {
	ReadWorkerStats() WorkerStats
}

// AdminWorkerStatsDeps 持有 admin stats 端点的依赖。
type AdminWorkerStatsDeps struct {
	Auth   AdminAuth
	Reader WorkerStatsReader
}

// WorkerStats 是订阅通知 worker 的 JSON 响应。
type WorkerStats struct {
	Reminder ReminderWorkerStats `json:"reminder"`
	Expiry   ExpiryWorkerStats   `json:"expiry"`
}

type ReminderWorkerStats struct {
	TickCount   uint64 `json:"tick_count"`
	SentTotal   uint64 `json:"sent_total"`
	FailedTicks uint64 `json:"failed_ticks"`
}

type ExpiryWorkerStats struct {
	TickCount    uint64 `json:"tick_count"`
	ExpiredTotal uint64 `json:"expired_total"`
	FailedTicks  uint64 `json:"failed_ticks"`
}

// NewAdminWorkerStatsHandler 返回当前进程内的 worker 计数器。
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
		writeJSON(w, http.StatusOK, d.Reader.ReadWorkerStats())
	}
}
