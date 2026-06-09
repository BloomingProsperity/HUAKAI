// Package systemhealthhttp implements ADMIN-042: read-only system health
// aggregation endpoint for operators.
//
// GET /v1/admin/system/health (alias /admin/v1/system/health)
//
// The handler aggregates already-computed read-only snapshots from:
//   - channelhealth controller (SummarizeChannelHealth snapshot)
//   - DLQ pending depth (List with StatusPending)
//   - Alerting active (firing) event count (ListEvents with state=firing)
//   - DB ping
//
// No upstream paid calls; zero billing side effects.
package systemhealthhttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// ComponentStatus represents the health of a single sub-system.
type ComponentStatus string

const (
	ComponentStatusHealthy   ComponentStatus = "healthy"
	ComponentStatusDegraded  ComponentStatus = "degraded"
	ComponentStatusUnhealthy ComponentStatus = "unhealthy"
)

// TopLevelStatus is the aggregated system status derived from component statuses.
type TopLevelStatus string

const (
	TopLevelStatusHealthy   TopLevelStatus = "healthy"
	TopLevelStatusDegraded  TopLevelStatus = "degraded"
	TopLevelStatusUnhealthy TopLevelStatus = "unhealthy"
)

// Component is one named sub-system entry in the health response.
type Component struct {
	Name   string          `json:"name"`
	Status ComponentStatus `json:"status"`
	Detail string          `json:"detail,omitempty"`
}

// HealthResponse is the JSON body returned by the health endpoint.
type HealthResponse struct {
	Status     TopLevelStatus `json:"status"`
	Components []Component    `json:"components"`
}

// SystemHealthSource provides read-only already-computed snapshots for aggregation.
// Production implementations are backed by live service fields in deps.
type SystemHealthSource interface {
	// ChannelHealthSummary returns (total, unhealthyCount, err).
	// "unhealthy" = cooling_down + disabled + degraded counts.
	ChannelHealthSummary(ctx context.Context) (total int64, unhealthyCount int64, err error)
	// DLQPendingDepth returns the number of pending DLQ records.
	DLQPendingDepth(ctx context.Context) (int64, error)
	// AlertingFiringCount returns the number of currently-firing alert events.
	AlertingFiringCount(ctx context.Context) (int64, error)
	// DBPing checks database reachability.
	DBPing(ctx context.Context) error
}

// NewSystemHealthHandler returns the aggregated health handler.
// The handler is intentionally behind adminGate (caller's responsibility).
func NewSystemHealthHandler(src SystemHealthSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var components []Component

		// ── component: database ──────────────────────────────────────────────
		if src == nil {
			writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
				Status:     TopLevelStatusUnhealthy,
				Components: []Component{{Name: "system_health_source", Status: ComponentStatusUnhealthy, Detail: "source not configured"}},
			})
			return
		}

		dbStatus, dbDetail := ComponentStatusHealthy, ""
		if err := src.DBPing(ctx); err != nil {
			dbStatus = ComponentStatusUnhealthy
			dbDetail = "db ping failed"
			slog.WarnContext(ctx, "system health db ping failed", slog.String("error", err.Error()))
		}
		components = append(components, Component{Name: "database", Status: dbStatus, Detail: dbDetail})

		// ── component: channel_health ─────────────────────────────────────────
		total, unhealthy, err := src.ChannelHealthSummary(ctx)
		chStatus, chDetail := ComponentStatusHealthy, ""
		if err != nil {
			chStatus = ComponentStatusDegraded
			chDetail = "snapshot unavailable"
			slog.WarnContext(ctx, "system health channel snapshot failed", slog.String("error", err.Error()))
		} else if total > 0 && unhealthy > 0 {
			chStatus = ComponentStatusDegraded
			chDetail = formatInt64Pair("unhealthy_channels", unhealthy, "total", total)
		}
		components = append(components, Component{Name: "channel_health", Status: chStatus, Detail: chDetail})

		// ── component: dlq ───────────────────────────────────────────────────
		dlqDepth, err := src.DLQPendingDepth(ctx)
		dlqStatus, dlqDetail := ComponentStatusHealthy, ""
		if err != nil {
			dlqStatus = ComponentStatusDegraded
			dlqDetail = "depth unavailable"
			slog.WarnContext(ctx, "system health dlq depth failed", slog.String("error", err.Error()))
		} else if dlqDepth > 0 {
			dlqStatus = ComponentStatusDegraded
			dlqDetail = formatInt64("pending_records", dlqDepth)
		}
		components = append(components, Component{Name: "dlq", Status: dlqStatus, Detail: dlqDetail})

		// ── component: alerting ───────────────────────────────────────────────
		firingCount, err := src.AlertingFiringCount(ctx)
		alertStatus, alertDetail := ComponentStatusHealthy, ""
		if err != nil {
			alertStatus = ComponentStatusDegraded
			alertDetail = "count unavailable"
			slog.WarnContext(ctx, "system health alerting count failed", slog.String("error", err.Error()))
		} else if firingCount > 0 {
			alertStatus = ComponentStatusDegraded
			alertDetail = formatInt64("firing_events", firingCount)
		}
		components = append(components, Component{Name: "alerting", Status: alertStatus, Detail: alertDetail})

		// ── derive top-level status ───────────────────────────────────────────
		// MUTATION guard: if this is replaced with always-healthy the test turns red.
		top := deriveTopLevel(components)

		writeJSON(w, http.StatusOK, HealthResponse{Status: top, Components: components})
	}
}

// deriveTopLevel returns the worst component status as the system status.
func deriveTopLevel(components []Component) TopLevelStatus {
	result := TopLevelStatusHealthy
	for _, c := range components {
		switch c.Status {
		case ComponentStatusUnhealthy:
			return TopLevelStatusUnhealthy // worst possible — short circuit
		case ComponentStatusDegraded:
			result = TopLevelStatusDegraded
		}
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("system health json encode failed", slog.String("error", err.Error()))
	}
}

func formatInt64(key string, v int64) string {
	return key + "=" + int64str(v)
}

func formatInt64Pair(k1 string, v1 int64, k2 string, v2 int64) string {
	return k1 + "=" + int64str(v1) + " " + k2 + "=" + int64str(v2)
}

func int64str(v int64) string {
	buf := make([]byte, 0, 20)
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		buf = append(buf, byte('0'+v%10))
		v /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
