package adminhttpcore

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteJSONError(w http.ResponseWriter, status int, code, message string) {
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func Reason(got, fallback string) *string {
	if reason := strings.TrimSpace(got); reason != "" {
		return &reason
	}
	return &fallback
}

func CanAccessTenant(ident admin.AdminIdentity, tenantID int64) bool {
	if ident.Role == admin.RolePlatformAdmin {
		return true
	}
	return ident.Role == admin.RoleTenantOperator && ident.ScopeTenantID > 0 && ident.ScopeTenantID == tenantID
}

func ParseRequiredPositiveInt64(w http.ResponseWriter, raw, code, message string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		WriteJSONError(w, http.StatusBadRequest, code, message)
		return 0, false
	}
	return id, true
}

// ParseRequiredPositiveQueryInt64 读取必填的正整数查询参数，并保持管理端错误合同一致。
func ParseRequiredPositiveQueryInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	return ParseRequiredPositiveInt64(
		w,
		strings.TrimSpace(r.URL.Query().Get(name)),
		name+"_required",
		name+" query parameter must be positive",
	)
}

func WriteAudit(ctx context.Context, r *http.Request, store AuditStore, ident admin.AdminIdentity, tenantID *int64, action, targetType string, targetID *int64, reason *string, payload []byte) error {
	actorID := ident.AuditActor()
	reqID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: targetType, TargetID: targetID,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}
