// Package systemhealthhttp 实现 ADMIN-042:面向运维的只读系统健康
// 聚合端点。
//
// GET /v1/admin/system/health(别名 /admin/v1/system/health)
//
// 该 handler 聚合以下来源的、已经算好的只读快照:
//   - channelhealth controller(SummarizeChannelHealth 快照)
//   - DLQ pending 深度(List 配 StatusPending)
//   - Alerting 活跃(firing)事件计数(ListEvents 配 state=firing)
//   - DB ping
//
// 不会发起任何上游付费调用;零计费副作用。
package systemhealthhttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"
)

// processStart 近似为进程启动时刻:本包被 gateway main 包导入,
// 因此其 init 在启动时运行。uptime 据此推算。
var processStart = time.Now()

// ComponentStatus 表示单个子系统的健康状态。
type ComponentStatus string

const (
	ComponentStatusHealthy   ComponentStatus = "healthy"
	ComponentStatusDegraded  ComponentStatus = "degraded"
	ComponentStatusUnhealthy ComponentStatus = "unhealthy"
)

// TopLevelStatus 是由各组件状态推导出的聚合系统状态。
type TopLevelStatus string

const (
	TopLevelStatusHealthy   TopLevelStatus = "healthy"
	TopLevelStatusDegraded  TopLevelStatus = "degraded"
	TopLevelStatusUnhealthy TopLevelStatus = "unhealthy"
)

// Component 是健康响应中一个具名的子系统条目。
type Component struct {
	Name   string          `json:"name"`
	Status ComponentStatus `json:"status"`
	Detail string          `json:"detail,omitempty"`
}

// RuntimeInfo 是 gateway 进程自身资源占用的实时快照,在请求时直接从
// Go runtime 读取(无后台采集器,无存储)。所有值都是诊断信息 —— heap/goroutine/GC
// 仪表盘外加 uptime、Go 工具链版本和二进制大小 —— 绝不含机密
// (二进制的 PATH 不会暴露,只暴露其大小)。
type RuntimeInfo struct {
	GoVersion       string `json:"go_version"`
	NumGoroutine    int    `json:"num_goroutine"`
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes    uint64 `json:"heap_sys_bytes"`
	NumGC           uint32 `json:"num_gc"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	BinarySizeBytes int64  `json:"binary_size_bytes,omitempty"`
}

// HealthResponse 是健康端点返回的 JSON body。
type HealthResponse struct {
	Status     TopLevelStatus `json:"status"`
	Components []Component    `json:"components"`
	Runtime    RuntimeInfo    `json:"runtime"`
}

// collectRuntimeInfo 读取一份实时的进程运行时快照。纯 runtime/os 读取;
// 无依赖、无 ctx、无副作用。当无法解析/stat 当前可执行文件时,
// BinarySizeBytes 被省略(为 0)。
func collectRuntimeInfo() RuntimeInfo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	info := RuntimeInfo{
		GoVersion:      runtime.Version(),
		NumGoroutine:   runtime.NumGoroutine(),
		HeapAllocBytes: ms.HeapAlloc,
		HeapSysBytes:   ms.HeapSys,
		NumGC:          ms.NumGC,
		UptimeSeconds:  int64(time.Since(processStart).Seconds()),
	}
	if exe, err := os.Executable(); err == nil {
		if fi, statErr := os.Stat(exe); statErr == nil {
			info.BinarySizeBytes = fi.Size()
		}
	}
	return info
}

// SystemHealthSource 为聚合提供只读的、已经算好的快照。
// 生产实现由 deps 中的实时 service 字段支撑。
type SystemHealthSource interface {
	// ChannelHealthSummary 返回 (total, unhealthyCount, err)。
	// "unhealthy" = cooling_down + disabled + degraded 的计数之和。
	ChannelHealthSummary(ctx context.Context) (total int64, unhealthyCount int64, err error)
	// DLQPendingDepth 返回 pending 状态的 DLQ 记录数。
	DLQPendingDepth(ctx context.Context) (int64, error)
	// AlertingFiringCount 返回当前处于 firing 状态的告警事件数。
	AlertingFiringCount(ctx context.Context) (int64, error)
	// ServerMonitorSummary 返回实例监测是否启用，以及节点总数、离线数和降级数。
	ServerMonitorSummary(ctx context.Context) (enabled bool, total int64, offline int64, degraded int64, err error)
	// DBPing 检查数据库可达性。
	DBPing(ctx context.Context) error
}

// NewSystemHealthHandler 返回聚合健康 handler。
// 该 handler 有意置于 adminGate 之后(由调用方负责)。
func NewSystemHealthHandler(src SystemHealthSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var components []Component

		// ── 组件:database ──────────────────────────────────────────────
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

		// ── 组件:channel_health ─────────────────────────────────────────
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

		// ── 组件:dlq ───────────────────────────────────────────────────
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

		// ── 组件:alerting ───────────────────────────────────────────────
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

		// ── 组件:server_monitor ───────────────────────────────────────
		monitorEnabled, monitorTotal, monitorOffline, monitorDegraded, err := src.ServerMonitorSummary(ctx)
		monitorStatus, monitorDetail := ComponentStatusHealthy, ""
		switch {
		case !monitorEnabled:
			monitorDetail = "disabled"
		case err != nil:
			monitorStatus = ComponentStatusDegraded
			monitorDetail = "snapshot unavailable"
			slog.WarnContext(ctx, "system health server monitor snapshot failed", slog.String("error_class", "server_monitor_snapshot_unavailable"))
		case monitorTotal == 0:
			monitorStatus = ComponentStatusDegraded
			monitorDetail = "no active instances"
		case monitorOffline > 0 || monitorDegraded > 0:
			monitorStatus = ComponentStatusDegraded
			monitorDetail = formatInt64Triple("offline_nodes", monitorOffline, "degraded_nodes", monitorDegraded, "total", monitorTotal)
		default:
			monitorDetail = formatInt64("total", monitorTotal)
		}
		components = append(components, Component{Name: "server_monitor", Status: monitorStatus, Detail: monitorDetail})

		// ── 推导顶层状态 ───────────────────────────────────────────
		// 变异守护:若把这里替换成永远 healthy,测试就会变红。
		top := deriveTopLevel(components)

		writeJSON(w, http.StatusOK, HealthResponse{Status: top, Components: components, Runtime: collectRuntimeInfo()})
	}
}

// deriveTopLevel 取各组件中最差的状态作为系统状态返回。
func deriveTopLevel(components []Component) TopLevelStatus {
	result := TopLevelStatusHealthy
	for _, c := range components {
		switch c.Status {
		case ComponentStatusUnhealthy:
			return TopLevelStatusUnhealthy // 最差情况 —— 短路返回
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

func formatInt64Triple(k1 string, v1 int64, k2 string, v2 int64, k3 string, v3 int64) string {
	return k1 + "=" + int64str(v1) + " " + k2 + "=" + int64str(v2) + " " + k3 + "=" + int64str(v3)
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
	// 反转
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
