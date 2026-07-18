package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/logretention"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"
)

// 统一运行日志查询、固定保留清理和健康端点。字段可能带租户关联，但不会汇总
// 其他权限域的领域日志；当前端点仍只放 platform_admin。

type AdminRuntimeLogsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type RuntimeLogStore interface {
	ListRuntimeLogs(ctx context.Context, p logsink.ListParams) ([]logsink.RuntimeLogRow, error)
}

type RuntimeLogSinkHealth interface {
	DetailedHealth() logsink.HealthSnapshot
}

type RuntimeLogRetention interface {
	RunOnce(context.Context) (logretention.Result, error)
	Health() logretention.Health
}

type RuntimeLogAuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminRuntimeLogsDeps struct {
	Auth      AdminRuntimeLogsAuth
	Store     RuntimeLogStore
	Sink      RuntimeLogSinkHealth
	Retention RuntimeLogRetention
	Audit     RuntimeLogAuditStore
}

func MountAdminRuntimeLogRoutes(r chi.Router, d AdminRuntimeLogsDeps) {
	r.Get("/runtime-logs", newAdminRuntimeLogsListHandler(d))
	r.Post("/runtime-logs/cleanup", newAdminRuntimeLogsCleanupHandler(d))
	r.Get("/runtime-logs/health", newAdminRuntimeLogsHealthHandler(d))
}

func resolveRuntimeLogsAdmin(w http.ResponseWriter, r *http.Request, d AdminRuntimeLogsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "runtime logs dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform admin required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func newAdminRuntimeLogsListHandler(d AdminRuntimeLogsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveRuntimeLogsAdmin(w, r, d)
		if !ok {
			return
		}
		q := r.URL.Query()
		if !validateRuntimeLogQuery(w, q) {
			return
		}
		params := logsink.ListParams{
			Level:             strings.TrimSpace(q.Get("level")),
			Category:          strings.TrimSpace(q.Get("category")),
			EventType:         strings.TrimSpace(q.Get("event_type")),
			Result:            strings.TrimSpace(q.Get("result")),
			ErrorClass:        strings.TrimSpace(q.Get("error_class")),
			ErrorCode:         strings.TrimSpace(q.Get("error_code")),
			Component:         strings.TrimSpace(q.Get("component")),
			ActorKind:         strings.TrimSpace(q.Get("actor_kind")),
			RequestID:         strings.TrimSpace(q.Get("request_id")),
			TraceID:           strings.TrimSpace(q.Get("trace_id")),
			UpstreamRequestID: strings.TrimSpace(q.Get("upstream_request_id")),
			IdempotencyKey:    strings.TrimSpace(q.Get("idempotency_key")),
			RecoveryState:     strings.TrimSpace(q.Get("recovery_state")),
		}
		if params.Level != "" && params.Level != "info" && params.Level != "warn" && params.Level != "error" {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "level must be info, warn, or error")
			return
		}
		if params.Category != "" && !logcontract.ValidCategory(params.Category) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid category")
			return
		}
		if params.EventType != "" && !logcontract.ValidMachineIdentifier(params.EventType) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid event_type")
			return
		}
		if params.Result != "" && !logcontract.ValidResult(params.Result) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid result")
			return
		}
		if params.ErrorClass != "" && !logcontract.ValidErrorClass(params.ErrorClass) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid error_class")
			return
		}
		if params.ErrorCode != "" && !logcontract.ValidMachineIdentifier(params.ErrorCode) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid error_code")
			return
		}
		if params.ActorKind != "" && !logcontract.ValidActorKind(params.ActorKind) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid actor_kind")
			return
		}
		if params.RecoveryState != "" && !logcontract.ValidRecoveryState(params.RecoveryState) {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "invalid recovery_state")
			return
		}
		if raw := strings.TrimSpace(q.Get("tenant_id")); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "tenant_id must be a positive integer")
				return
			}
			params.TenantID = value
		}
		if raw := strings.TrimSpace(q.Get("before_id")); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "before_id must be a positive integer")
				return
			}
			params.BeforeID = v
		}
		if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || v <= 0 || v > 500 {
				writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "limit must be in 1..500")
				return
			}
			params.Limit = int32(v)
		}
		rows, err := d.Store.ListRuntimeLogs(r.Context(), params)
		if err != nil {
			slog.Error("查询运行日志失败",
				logcontract.FieldCategory, string(logcontract.CategoryError),
				logcontract.FieldEventType, "runtime_logs.query_failed",
				logcontract.FieldResult, string(logcontract.ResultServerFailure),
				logcontract.FieldErrorClass, string(logcontract.ErrorDependency),
				logcontract.FieldErrorCode, "runtime_logs_query_failed",
				logcontract.FieldRetryable, true,
				logcontract.FieldActorKind, string(logcontract.ActorPlatformAdmin),
				logcontract.FieldActorRef, ident.AuditActor(),
				"request_id", chimiddleware.GetReqID(r.Context()),
			)
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_query_failed", "runtime log query failed")
			return
		}
		// 键集游标:最后一行 id 作为下一页 before_id;空页无游标。
		nextBefore := int64(0)
		if len(rows) > 0 {
			nextBefore = rows[len(rows)-1].ID
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"items":          rows,
			"next_before_id": nextBefore,
		})
	}
}

var runtimeLogQueryKeys = map[string]struct{}{
	"level": {}, "category": {}, "event_type": {}, "result": {}, "error_class": {},
	"error_code": {}, "component": {}, "actor_kind": {}, "tenant_id": {}, "request_id": {},
	"trace_id": {}, "upstream_request_id": {}, "idempotency_key": {}, "recovery_state": {},
	"before_id": {}, "limit": {},
}

func validateRuntimeLogQuery(w http.ResponseWriter, query map[string][]string) bool {
	for key, values := range query {
		if _, ok := runtimeLogQueryKeys[key]; !ok {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "unknown query parameter")
			return false
		}
		if len(values) != 1 {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "query parameters must not repeat")
			return false
		}
	}
	return true
}

type adminRuntimeLogsCleanupRequest struct {
	Confirm bool `json:"confirm"`
}

func newAdminRuntimeLogsCleanupHandler(d AdminRuntimeLogsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveRuntimeLogsAdmin(w, r, d)
		if !ok {
			return
		}
		var req adminRuntimeLogsCleanupRequest
		if !decodeRuntimeLogsCleanupJSON(w, r, &req) {
			return
		}
		if !req.Confirm {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_confirmation_required", "confirm must be true")
			return
		}
		if d.Audit == nil || d.Retention == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "runtime log retention dependency unset")
			return
		}
		reqID := chimiddleware.GetReqID(r.Context())
		reason := "手工触发全局日志固定 30 天保留"
		payload, _ := json.Marshal(map[string]any{"retention_days": logretention.RetentionDays})
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			ActorID: ident.AuditActor(), ActorRole: string(ident.Role),
			Action: "cleanup_runtime_logs", TargetType: "runtime_logs",
			RequestID: &reqID, Reason: &reason, Payload: payload,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_audit_failed", "audit write failed; cleanup aborted")
			return
		}
		result, err := d.Retention.RunOnce(r.Context())
		if err != nil {
			if errors.Is(err, logretention.ErrAlreadyRunning) {
				writeJSONError(w, http.StatusConflict, "runtime_logs_cleanup_running", err.Error())
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_cleanup_failed", "log retention failed; inspect health for the failed table")
			return
		}
		slog.Info("手工触发日志保留任务完成",
			logcontract.FieldCategory, string(logcontract.CategoryOperation),
			logcontract.FieldEventType, "log_retention.manual_trigger_completed",
			logcontract.FieldResult, string(logcontract.ResultSuccess),
			logcontract.FieldActorKind, string(logcontract.ActorPlatformAdmin),
			logcontract.FieldActorRef, ident.AuditActor(),
			logcontract.FieldTargetType, "global_logs",
			"request_id", reqID,
			"deleted", result.Deleted,
			"cutoff", result.Cutoff.Format(time.RFC3339),
		)
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func decodeRuntimeLogsCleanupJSON(w http.ResponseWriter, r *http.Request, dst *adminRuntimeLogsCleanupRequest) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func newAdminRuntimeLogsHealthHandler(d AdminRuntimeLogsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveRuntimeLogsAdmin(w, r, d); !ok {
			return
		}
		if d.Sink == nil || d.Retention == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "runtime log sink unset")
			return
		}
		sinkHealth := d.Sink.DetailedHealth()
		retentionHealth := d.Retention.Health()
		body := map[string]any{
			"sink": map[string]any{
				"queue_len": sinkHealth.QueueLen, "queue_capacity": sinkHealth.QueueCapacity,
				"priority_queue_len": sinkHealth.PriorityQueueLen, "priority_capacity": sinkHealth.PriorityCapacity,
				"info_queue_len": sinkHealth.InfoQueueLen, "info_capacity": sinkHealth.InfoCapacity,
				"inserted": sinkHealth.Inserted, "dropped": sinkHealth.Dropped,
				"priority_dropped": sinkHealth.PriorityDropped, "info_dropped": sinkHealth.InfoDropped,
				"failed_batches": sinkHealth.FailedBatches,
			},
			"retention": retentionHealthMap(retentionHealth),
		}
		if !sinkHealth.LastFlush.IsZero() {
			body["sink"].(map[string]any)["last_flush_at"] = sinkHealth.LastFlush.Format(time.RFC3339)
		}
		writeAuditJSON(w, http.StatusOK, body)
	}
}

func retentionHealthMap(health logretention.Health) map[string]any {
	result := map[string]any{
		"retention_days": health.RetentionDays, "running": health.Running,
		"last_duration_ms": health.LastDurationMS, "last_deleted": health.LastDeleted,
		"total_deleted": health.TotalDeleted, "last_batches": health.LastBatches,
		"has_more": health.HasMore, "lease_conflict_count": health.LeaseConflictCount,
		"consecutive_failures": health.ConsecutiveFailures,
	}
	if !health.LastAttemptAt.IsZero() {
		result["last_attempt_at"] = health.LastAttemptAt.Format(time.RFC3339)
	}
	if !health.LastSuccessAt.IsZero() {
		result["last_success_at"] = health.LastSuccessAt.Format(time.RFC3339)
	}
	if !health.CurrentCutoff.IsZero() {
		result["current_cutoff"] = health.CurrentCutoff.Format(time.RFC3339)
	}
	if health.LastErrorClass != "" {
		result["last_error_class"] = health.LastErrorClass
	}
	if health.LastErrorTable != "" {
		result["last_error_table"] = health.LastErrorTable
	}
	return result
}
