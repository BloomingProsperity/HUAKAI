package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

type AdminModelSyncDeps struct {
	Auth    adminModelSyncAuth
	Service adminModelSyncService
}

type adminModelSyncAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminModelSyncService interface {
	SyncWithActor(context.Context, string, string) (modelsync.SyncResult, error)
}

type modelSyncRequestBody struct {
	Reason string `json:"reason,omitempty"`
}

type modelSyncResponseBody struct {
	Object        string                    `json:"object"`
	CompletedAt   string                    `json:"completed_at"`
	TotalAdded    int                       `json:"total_added"`
	TotalUpdated  int                       `json:"total_updated"`
	TotalDisabled int                       `json:"total_disabled"`
	Results       []modelSyncResultItemBody `json:"results"`
}

type modelSyncResultItemBody struct {
	Vendor        string `json:"vendor"`
	Added         int    `json:"added"`
	Updated       int    `json:"updated"`
	Reactivated   int    `json:"reactivated"`
	Disabled      int    `json:"disabled"`
	Unchanged     int    `json:"unchanged"`
	SnapshotBumps int    `json:"snapshot_bumps"`
}

func MountModelSyncRoutes(r chi.Router, d AdminModelSyncDeps) {
	// SessionSafe:触发全局模型目录同步(从上游拉取,可重跑),登录 admin(session)可直接写;前端确认弹窗防误触发。
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/", newModelSyncHandler(d))
}

func newModelSyncHandler(d AdminModelSyncDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"model sync dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// 全局模型目录会影响所有继承 global catalog 的租户，只允许平台管理员触发。
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}

		var body modelSyncRequestBody
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
				if err.Error() != "EOF" {
					writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
					return
				}
			}
		}
		reason := strings.TrimSpace(body.Reason)
		if utf8.RuneCountInString(reason) > 200 {
			writeError(w, http.StatusBadRequest, "invalid_reason",
				"reason must be 200 characters or fewer")
			return
		}
		if reason == "" {
			reason = "admin_manual"
		}

		actor := ident.AuditActor()
		result, err := d.Service.SyncWithActor(r.Context(), reason, actor)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "model_sync_failed",
				"model catalog sync failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, modelSyncResponse(result))
	}
}

func modelSyncResponse(result modelsync.SyncResult) modelSyncResponseBody {
	items := make([]modelSyncResultItemBody, 0, len(result.Results))
	for _, item := range result.Results {
		items = append(items, modelSyncResultItemBody{
			Vendor:        string(item.Vendor),
			Added:         item.Added,
			Updated:       item.Updated,
			Reactivated:   item.Reactivated,
			Disabled:      item.Disabled,
			Unchanged:     item.Unchanged,
			SnapshotBumps: item.SnapshotBumps,
		})
	}
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	return modelSyncResponseBody{
		Object:        "admin_model_sync_result",
		CompletedAt:   completedAt.UTC().Format(time.RFC3339),
		TotalAdded:    result.TotalAdded,
		TotalUpdated:  result.TotalUpdated,
		TotalDisabled: result.TotalDisabled,
		Results:       items,
	}
}
