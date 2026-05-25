// Package userkeyhttp 暴露 user 自助 api_keys CRUD endpoint。
//
// 路由由 cmd/gateway/routes.go 在 session middleware 之内挂入 /v1/api-keys/*;
// 没 session middleware 包裹本包 handler 必直接拒 503 — 防止有人误把它挂到
// 公开路径上。
//
// 与 internal/gatewayhttp (frozen) 的差异:本包是按 CLAUDE.md #13 的"按职责
// 组织"在 frozen package 拆分前新建,只承担 user-self-service api_keys 的 HTTP
// 层。后续如有其他 user-facing CRUD (e.g. /v1/usage),按相同 pattern 起新包,
// **绝不**回填进 gatewayhttp。
package userkeyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

// UserKeyService 是 handler 依赖的最小接口;production 注入 *userkey.Service,
// 测试注入 stub 验路径分支。
type UserKeyService interface {
	Issue(ctx context.Context, req userkey.IssueRequest) (userkey.IssueResult, error)
	List(ctx context.Context, req userkey.ListRequest) ([]userkey.KeyDescriptor, error)
	Get(ctx context.Context, tenantID, userID, apiKeyID int64) (userkey.KeyDescriptor, error)
	Revoke(ctx context.Context, req userkey.RevokeRequest) (userkey.RevokeResult, error)
}

// Deps 是挂载路由所需的依赖集合。Service nil → 全部 endpoint 返 503。
type Deps struct {
	Service UserKeyService
}

// MountUserAPIKeyRoutes 挂载 /v1/api-keys/* 路由。caller (routes.go) 必须
// 已在外层套 auth.SessionMiddleware;本包仅从 context 读 session,不签发 session。
func MountUserAPIKeyRoutes(r chi.Router, d Deps) {
	r.Post("/", newCreateHandler(d))
	r.Get("/", newListHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Delete("/{id}", newRevokeHandler(d))
}

// ---- request / response DTO ----

type createRequest struct {
	Name        string  `json:"name"`
	Environment string  `json:"environment,omitempty"` // "live" | "test";缺省 live
	ExpiresAt   *string `json:"expires_at,omitempty"`  // RFC3339;nil = 永不过期
}

type createResponse struct {
	APIKeyID  int64   `json:"api_key_id"`
	Plaintext string  `json:"plaintext"` // **只此一次** 出现
	KeyPrefix string  `json:"key_prefix"`
	Status    string  `json:"status"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	CreatedAt string  `json:"created_at"`
	Notice    string  `json:"notice"` // 提醒用户立即保存
}

type listResponse struct {
	APIKeys []apiKeyView `json:"api_keys"`
	Count   int          `json:"count"`
}

type apiKeyView struct {
	APIKeyID      int64   `json:"api_key_id"`
	Name          string  `json:"name"`
	KeyPrefix     string  `json:"key_prefix"`
	Status        string  `json:"status"`
	ExpiresAt     *string `json:"expires_at,omitempty"`
	LastUsedAt    *string `json:"last_used_at,omitempty"`
	RevokedAt     *string `json:"revoked_at,omitempty"`
	RevokedReason string  `json:"revoked_reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type revokeRequest struct {
	Reason string `json:"reason,omitempty"`
}

type revokeResponse struct {
	APIKeyID       int64 `json:"api_key_id"`
	AlreadyRevoked bool  `json:"already_revoked"`
}

// ---- handler 构造 ----

func newCreateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		var expiresAt *time.Time
		if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_expires_at", "expires_at must be RFC3339 timestamp")
				return
			}
			expiresAt = &t
		}
		env := userkey.Environment(strings.TrimSpace(req.Environment))
		out, err := d.Service.Issue(r.Context(), userkey.IssueRequest{
			TenantID:    ident.TenantID,
			UserID:      ident.UserID,
			Name:        req.Name,
			Environment: env,
			ExpiresAt:   expiresAt,
			RequestID:   requestIDFromReq(r),
		})
		if err != nil {
			writeUserKeyError(w, err)
			return
		}
		resp := createResponse{
			APIKeyID:  out.APIKeyID,
			Plaintext: out.Plaintext,
			KeyPrefix: out.KeyPrefix,
			Status:    out.Status,
			CreatedAt: out.CreatedAt.UTC().Format(time.RFC3339Nano),
			Notice:    "plaintext is shown only once; save it now — subsequent reads return key_prefix only",
		}
		if out.ExpiresAt != nil {
			s := out.ExpiresAt.UTC().Format(time.RFC3339Nano)
			resp.ExpiresAt = &s
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		offset, limit, ok := parsePagination(w, r)
		if !ok {
			return
		}
		rows, err := d.Service.List(r.Context(), userkey.ListRequest{
			TenantID: ident.TenantID,
			UserID:   ident.UserID,
			Offset:   offset,
			Limit:    limit,
		})
		if err != nil {
			writeUserKeyError(w, err)
			return
		}
		out := listResponse{APIKeys: make([]apiKeyView, 0, len(rows)), Count: len(rows)}
		for _, row := range rows {
			out.APIKeys = append(out.APIKeys, descriptorToView(row))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		row, err := d.Service.Get(r.Context(), ident.TenantID, ident.UserID, id)
		if err != nil {
			writeUserKeyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, descriptorToView(row))
	}
}

func newRevokeHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req revokeRequest
		// body 可选;空 body 也合法
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		out, err := d.Service.Revoke(r.Context(), userkey.RevokeRequest{
			TenantID:  ident.TenantID,
			UserID:    ident.UserID,
			APIKeyID:  id,
			Reason:    req.Reason,
			RequestID: requestIDFromReq(r),
		})
		if err != nil {
			writeUserKeyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, revokeResponse{APIKeyID: out.APIKeyID, AlreadyRevoked: out.AlreadyRevoked})
	}
}

// ---- helpers ----

// resolveSession 取 session ident;Service nil → 503;无 session → 401。
//
// session 应由外层 auth.SessionMiddleware 注入,如果没注入 → handler 仍能
// fail-closed 而不是把 ident.UserID=0 当合法 caller。
func resolveSession(w http.ResponseWriter, r *http.Request, d Deps) (sessionauth.SessionIdentity, bool) {
	if d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "userkey_service_unavailable", "user api key service unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_api_key_id", "api_key_id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parsePagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q := r.URL.Query()
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = n
	}
	limit := userkey.PageLimitDefault
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > userkey.PageLimitMax {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be in (0, "+strconv.Itoa(userkey.PageLimitMax)+"]")
			return 0, 0, false
		}
		limit = n
	}
	return offset, limit, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func descriptorToView(d userkey.KeyDescriptor) apiKeyView {
	v := apiKeyView{
		APIKeyID:      d.APIKeyID,
		Name:          d.Name,
		KeyPrefix:     d.KeyPrefix,
		Status:        d.Status,
		RevokedReason: d.RevokedReason,
		CreatedAt:     d.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     d.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if d.ExpiresAt != nil {
		s := d.ExpiresAt.UTC().Format(time.RFC3339Nano)
		v.ExpiresAt = &s
	}
	if d.LastUsedAt != nil {
		s := d.LastUsedAt.UTC().Format(time.RFC3339Nano)
		v.LastUsedAt = &s
	}
	if d.RevokedAt != nil {
		s := d.RevokedAt.UTC().Format(time.RFC3339Nano)
		v.RevokedAt = &s
	}
	return v
}

func requestIDFromReq(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	return r.Header.Get("X-Request-ID")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}

// writeUserKeyError 把 userkey 包错误映射到 HTTP status + 公开 code。
//
// 404 (ErrNotFound) 同时覆盖"不存在"和"别人的 key" — 不区分,防 ID 枚举。
func writeUserKeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userkey.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name", "name must be non-empty and ≤ "+strconv.Itoa(userkey.MaxNameLen))
	case errors.Is(err, userkey.ErrInvalidExpiry):
		writeError(w, http.StatusBadRequest, "invalid_expires_at", "expires_at must be a future RFC3339 timestamp")
	case errors.Is(err, userkey.ErrInvalidEnv):
		writeError(w, http.StatusBadRequest, "invalid_environment", "environment must be 'live' or 'test'")
	case errors.Is(err, userkey.ErrActiveKeyCapHit):
		writeError(w, http.StatusConflict, "active_key_cap_reached",
			"you have reached the active api_keys cap ("+strconv.Itoa(userkey.MaxActiveKeysPerUser)+"); revoke an existing key first")
	case errors.Is(err, userkey.ErrNotFound):
		writeError(w, http.StatusNotFound, "api_key_not_found", "api_key not found")
	case errors.Is(err, userkey.ErrServiceMisconfig):
		writeError(w, http.StatusServiceUnavailable, "userkey_service_unavailable", "user api key service unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "userkey_backend_error", "user api key backend transient failure")
	}
}
