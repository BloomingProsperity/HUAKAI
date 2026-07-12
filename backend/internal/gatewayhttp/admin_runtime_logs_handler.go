package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"
)

// 运行日志查询/清理/采集健康端点。运行日志是进程级平台数据(无租户列),
// 一律只放 platform_admin;「实时」体验由前端轮询增量拉取实现(键集分页)。

type AdminRuntimeLogsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type RuntimeLogStore interface {
	ListRuntimeLogs(ctx context.Context, p logsink.ListParams) ([]logsink.RuntimeLogRow, error)
	CleanupRuntimeLogs(ctx context.Context, before time.Time) (int64, error)
}

type RuntimeLogSinkHealth interface {
	Health() (queueLen int, inserted, dropped int64, lastFlush time.Time)
}

type RuntimeLogAuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminRuntimeLogsDeps struct {
	Auth  AdminRuntimeLogsAuth
	Store RuntimeLogStore
	Sink  RuntimeLogSinkHealth
	Audit RuntimeLogAuditStore
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
		if _, ok := resolveRuntimeLogsAdmin(w, r, d); !ok {
			return
		}
		q := r.URL.Query()
		params := logsink.ListParams{
			Level:     strings.TrimSpace(q.Get("level")),
			Component: strings.TrimSpace(q.Get("component")),
			RequestID: strings.TrimSpace(q.Get("request_id")),
		}
		if params.Level != "" && params.Level != "warn" && params.Level != "error" {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "level must be warn or error")
			return
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
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_query_failed", err.Error())
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

type adminRuntimeLogsCleanupRequest struct {
	Before string `json:"before"`
}

func newAdminRuntimeLogsCleanupHandler(d AdminRuntimeLogsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveRuntimeLogsAdmin(w, r, d)
		if !ok {
			return
		}
		var req adminRuntimeLogsCleanupRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		before, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Before))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "runtime_logs_invalid", "before must be RFC3339 timestamp")
			return
		}
		// 审计先行:清理是抹除运维证据的破坏性操作,审计行写不进(含依赖未接线)就拒绝执行,
		// 保证不存在"无痕清理"。删除行数事后经运行日志补记(见下)。
		if d.Audit == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "runtime logs audit dependency unset")
			return
		}
		reqID := chimiddleware.GetReqID(r.Context())
		reason := "cleanup runtime logs before " + before.Format(time.RFC3339)
		payload, _ := json.Marshal(map[string]any{"before": before.Format(time.RFC3339)})
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			ActorID: ident.AuditActor(), ActorRole: string(ident.Role),
			Action: "cleanup_runtime_logs", TargetType: "runtime_logs",
			RequestID: &reqID, Reason: &reason, Payload: payload,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_audit_failed", "audit write failed; cleanup aborted")
			return
		}
		deleted, err := d.Store.CleanupRuntimeLogs(r.Context(), before)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "runtime_logs_cleanup_failed", err.Error())
			return
		}
		// 删除行数入运行日志(warn 级会被 sink 采回本表),补全事后可查的数量证据。
		slog.Warn("runtime logs cleanup executed",
			"actor", ident.AuditActor(), "before", before.Format(time.RFC3339), "deleted", deleted)
		writeAuditJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
	}
}

func newAdminRuntimeLogsHealthHandler(d AdminRuntimeLogsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveRuntimeLogsAdmin(w, r, d); !ok {
			return
		}
		if d.Sink == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "runtime log sink unset")
			return
		}
		queueLen, inserted, dropped, lastFlush := d.Sink.Health()
		body := map[string]any{
			"queue_len": queueLen,
			"inserted":  inserted,
			"dropped":   dropped,
		}
		if !lastFlush.IsZero() {
			body["last_flush_at"] = lastFlush.Format(time.RFC3339)
		}
		writeAuditJSON(w, http.StatusOK, body)
	}
}
