// Package adminhttp 负责接线面向运营者的 admin 端点,挂载在
// /admin/v1/ 之下。本切片交付 api_keys 的签发 + 列表 +
// 吊销能力面;后续切片会加上 /admin/v1/users、/admin/v1/pools 等。
//
// 跨模块边界以 AGENTS.md 和 docs/HUAKAI工程设计手册.md 为准：
// 本包永不 import internal/router 或 internal/auth。
//     客户热路径不受影响。
// 明文 bearer 仅在 POST 响应体中向运营者呈现一次。
//     永不写日志,永不在错误响应里回显。
// 写操作经由 internal/admin(admin_tokens / api_keys /
//     admin_audit_events)。不改动任何 billing/pool/registry。

package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// AdminAPIKeysDeps 是 api_keys 处理器所需依赖树的子集。
// cmd/gateway/main.go 里的具体依赖通过 duck typing 隐式满足它。
type AdminAPIKeysDeps struct {
	Auth    adminAPIKeysAuth
	Issuer  adminAPIKeysIssuer
	Revoker adminAPIKeysRevoker
	Queries adminAPIKeysQueries // 用于 LIST(只读)
}

type adminAPIKeysAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminAPIKeysIssuer interface {
	Issue(context.Context, admin.IssueRequest) (admin.IssueResult, error)
}

type adminAPIKeysRevoker interface {
	Revoke(context.Context, admin.RevokeRequest) (admin.RevokeResult, error)
}

type adminAPIKeysQueries interface {
	AdminCheckTenantExists(context.Context, int64) (bool, error)
	AdminListAPIKeysForTenant(context.Context, admindb.AdminListAPIKeysForTenantParams) ([]admindb.AdminListAPIKeysForTenantRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

// MountAPIKeyRoutes 把 POST/GET/POST-revoke 处理器挂到调用方的
// chi router 上(通常作用域为 /admin/v1/api-keys)。
func MountAPIKeyRoutes(r chi.Router, d AdminAPIKeysDeps) {
	r.Post("/", newIssueHandler(d))
	r.Get("/", newListHandler(d))
	r.Post("/{id}/revoke", newRevokeHandler(d))
}

// -----------------------------------------------------------------------------
// POST /admin/v1/api-keys —— 签发
// -----------------------------------------------------------------------------

type issueRequestBody struct {
	TenantID    int64   `json:"tenant_id"`
	UserID      int64   `json:"user_id"`
	Name        string  `json:"name"`
	Environment string  `json:"environment,omitempty"` // "live" 或 "test";默认 "live"
	ExpiresAt   *string `json:"expires_at,omitempty"`  // RFC3339;null = 永不过期
	Reason      string  `json:"reason,omitempty"`
}

type issueResponseBody struct {
	ID              int64   `json:"id"`
	TenantID        int64   `json:"tenant_id"`
	UserID          int64   `json:"user_id"`
	Name            string  `json:"name"`
	KeyPrefix       string  `json:"key_prefix"`
	Status          string  `json:"status"`
	ExpiresAt       *string `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
	PlaintextBearer string  `json:"plaintext_bearer"` // 仅显示一次
}

func newIssueHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Issuer == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin issuance dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 上限 64 KiB
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req issueRequestBody
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.TenantID == 0 || req.UserID == 0 || req.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_fields",
				"tenant_id, user_id, and name are required")
			return
		}
		env := admin.EnvLive
		switch req.Environment {
		case "", "live":
			env = admin.EnvLive
		case "test":
			env = admin.EnvTest
		default:
			writeError(w, http.StatusBadRequest, "invalid_environment",
				"environment must be 'live' or 'test'")
			return
		}
		var expiresAt *time.Time
		if req.ExpiresAt != nil && *req.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_expires_at",
					"expires_at must be RFC3339")
				return
			}
			// 拒绝已经过期的请求。
			// 否则签发器会铸出一个 key + 明文 bearer,
			// 而客户侧的 resolver 会立刻拒绝它。
			if !t.After(time.Now()) {
				writeError(w, http.StatusBadRequest, "expires_at_in_past",
					"expires_at must be strictly in the future")
				return
			}
			expiresAt = &t
		}

		result, err := d.Issuer.Issue(r.Context(), admin.IssueRequest{
			Caller:      ident,
			TenantID:    req.TenantID,
			UserID:      req.UserID,
			Name:        req.Name,
			Environment: env,
			ExpiresAt:   expiresAt,
			Reason:      req.Reason,
			RequestID:   middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}

		// 该响应头提醒运营者:key 仅显示一次(D3)。
		w.Header().Set("X-Huakai-Key-Display", "once-only")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		var expStr *string
		if result.ExpiresAt != nil {
			s := result.ExpiresAt.Format(time.RFC3339)
			expStr = &s
		}
		_ = json.NewEncoder(w).Encode(issueResponseBody{
			ID:              result.APIKeyID,
			TenantID:        req.TenantID,
			UserID:          req.UserID,
			Name:            req.Name,
			KeyPrefix:       result.KeyPrefix,
			Status:          result.Status,
			ExpiresAt:       expStr,
			CreatedAt:       result.CreatedAt.UTC().Format(time.RFC3339),
			PlaintextBearer: result.Plaintext,
		})
	}
}

// -----------------------------------------------------------------------------
// GET /admin/v1/api-keys —— 列表(按租户作用域或平台范围)
// -----------------------------------------------------------------------------

type listItemBody struct {
	ID            int64   `json:"id"`
	TenantID      int64   `json:"tenant_id"`
	UserID        int64   `json:"user_id"`
	Name          string  `json:"name"`
	KeyPrefix     string  `json:"key_prefix"`
	Status        string  `json:"status"`
	ExpiresAt     *string `json:"expires_at"`
	LastUsedAt    *string `json:"last_used_at"`
	RevokedAt     *string `json:"revoked_at"`
	RevokedReason *string `json:"revoked_reason"`
	CreatedAt     string  `json:"created_at"`
}

func newListHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Queries == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin list dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// 租户作用域:tenant_operator 只能列出自己的作用域;platform_admin
		// 必须传 ?tenant_id=N(L0 阶段不提供全机队列表)。
		tenantParam := r.URL.Query().Get("tenant_id")
		if tenantParam == "" {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required")
			return
		}
		tenantID, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id",
				"tenant_id must be a positive int64")
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}

		// 在执行 list 查询 + 审计写入之前,先校验租户存在。
		// 否则一个未知的 tenant_id 会顺利通过 list(返回空结果),
		// 随后撞上 admin_audit_events.tenant_id 外键约束,表现为 503,
		// 而非契约所规定的、确定性的 404。
		exists, err := d.Queries.AdminCheckTenantExists(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_lookup_failed",
				fmt.Sprintf("tenant existence check failed: %v", err))
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "tenant_not_found",
				fmt.Sprintf("tenant %d not found", tenantID))
			return
		}

		// 格式错误的分页参数必须被拒绝,而非
		// 静默地强制回退为默认值。否则客户端的分页 bug 看起来
		// 就像成功读取了第一页。
		limit := int32(50)
		if s := r.URL.Query().Get("limit"); s != "" {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil || v < 1 || v > 500 {
				writeError(w, http.StatusBadRequest, "invalid_limit",
					"limit must be a positive integer between 1 and 500")
				return
			}
			limit = int32(v)
		}
		offset := int32(0)
		if s := r.URL.Query().Get("offset"); s != "" {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil || v < 0 {
				writeError(w, http.StatusBadRequest, "invalid_offset",
					"offset must be a non-negative integer")
				return
			}
			offset = int32(v)
		}

		rows, err := d.Queries.AdminListAPIKeysForTenant(r.Context(), admindb.AdminListAPIKeysForTenantParams{
			TenantID:   tenantID,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error",
				fmt.Sprintf("admin list failed: %v", err))
			return
		}

		// 对成功的 admin 读操作记审计。Payload 被限定作用域
		//(tenant + 计数 + 分页)——绝不含逐行明文或
		// prefix 数据。
		actorRole := ident.Role
		if actorRole == "" {
			actorRole = admin.RoleTenantOperator
		}
		listPayload, _ := json.Marshal(map[string]any{
			"tenant_id":   tenantID,
			"row_count":   len(rows),
			"page_limit":  limit,
			"page_offset": offset,
		})
		tenantPtr := tenantID
		reqIDPtr := middleware.GetReqID(r.Context())
		var reqIDArg *string
		if reqIDPtr != "" {
			reqIDArg = &reqIDPtr
		}
		// 审计行是 admin 读操作契约的一部分。
		// 若写不进去,则 fail closed(503),让运营者
		// 在审计管道恢复健康后重试,而不是静默地
		// 丢弃审计轨迹。
		if _, err := d.Queries.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantPtr,
			ActorID:    ident.AuditActor(),
			ActorRole:  actorRole,
			Action:     "list_api_keys",
			TargetType: "api_key",
			RequestID:  reqIDArg,
			Payload:    listPayload,
		}); err != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_write_failed",
				fmt.Sprintf("admin list audit insert failed: %v", err))
			return
		}

		items := make([]listItemBody, 0, len(rows))
		for _, row := range rows {
			item := listItemBody{
				ID:        row.ID,
				TenantID:  row.TenantID,
				UserID:    row.UserID,
				Name:      row.Name,
				KeyPrefix: row.KeyPrefix,
				Status:    row.Status,
				CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
			}
			if row.ExpiresAt.Valid {
				s := row.ExpiresAt.Time.UTC().Format(time.RFC3339)
				item.ExpiresAt = &s
			}
			if row.LastUsedAt.Valid {
				s := row.LastUsedAt.Time.UTC().Format(time.RFC3339)
				item.LastUsedAt = &s
			}
			if row.RevokedAt.Valid {
				s := row.RevokedAt.Time.UTC().Format(time.RFC3339)
				item.RevokedAt = &s
			}
			item.RevokedReason = row.RevokedReason
			items = append(items, item)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// -----------------------------------------------------------------------------
// POST /admin/v1/api-keys/{id}/revoke
// -----------------------------------------------------------------------------

type revokeRequestBody struct {
	TenantID int64  `json:"tenant_id"` // 该 key 所属的租户
	Reason   string `json:"reason"`
}

type revokeResponseBody struct {
	ID             int64 `json:"id"`
	AlreadyRevoked bool  `json:"already_revoked"`
}

func newRevokeHandler(d AdminAPIKeysDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Revoker == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin revoke dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_api_key_id",
				"id must be a positive int64")
			return
		}

		// 让 MaxBytesReader 的截断显式暴露出来,这样一个
		// 超大的 body——即便其起始字节恰好能解析成合法 JSON——
		// 也无法蒙混过关。与签发处理器保持一致。
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req revokeRequestBody
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
		}
		if req.TenantID == 0 {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"body.tenant_id required (the tenant this api_key belongs to)")
			return
		}

		result, err := d.Revoker.Revoke(r.Context(), admin.RevokeRequest{
			Caller:    ident,
			APIKeyID:  id,
			TenantID:  req.TenantID,
			Reason:    req.Reason,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(revokeResponseBody{
			ID:             result.APIKeyID,
			AlreadyRevoked: result.AlreadyRevoked,
		})
	}
}

// -----------------------------------------------------------------------------
// 错误映射辅助函数
// -----------------------------------------------------------------------------

// writeAdminAuthError 处理 AdminResolver.Resolve 返回的
// ErrAdminUnauthorized + ErrAdminBackend。其它 admin 错误经由
// writeAdminError 处理。
func writeAdminAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin auth backend transient failure")
	default:
		writeError(w, http.StatusUnauthorized, "admin_unauthorized",
			"missing or invalid admin credential")
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden",
			"caller cannot act on this tenant scope")
	case errors.Is(err, admin.ErrAdminRateLimited):
		// docs/openapi/openapi.yaml 中共享的 RateLimited 响应
		// 要求带 Retry-After。每小时 30 次的窗口按 actor 滑动;
		// 这里给出保守的 60s,让行为良好的客户端退避而不会反复抖动。
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "admin_rate_limited",
			"issuance rate limit exceeded")
	case errors.Is(err, admin.ErrAdminBadRequest):
		writeError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
	case errors.Is(err, admin.ErrAdminNotFound):
		writeError(w, http.StatusNotFound, "admin_not_found",
			"target resource not found")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin backend transient failure")
	default:
		writeError(w, http.StatusInternalServerError, "admin_unknown_error",
			err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// 用 encoding/json 编码而非 fmt %q 手拼:%q 对部分控制字节会产出非法 JSON。本入口的
	// default 分支会回显 err.Error(),可能携带控制字符,故必须用 JSON 编码器保证响应可被严格解析。
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}
