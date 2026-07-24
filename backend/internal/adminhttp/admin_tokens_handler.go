// admin token(运维凭证)的签发 / 列举 / 吊销端点,挂在
// /admin/v1/admin-tokens 之下。区别于 api_keys_handler.go(后者管的是
// 客户 api_keys);本文件管的是 admin_tokens 行。
//
// 安全契约：
//   - 签发 / 列举 / 吊销 admin token 都是高权操作 —— internal/admin 侧的
//     AdminTokenIssuer 已做 platform_admin-only 的 fail-closed RBAC,本层
//     只负责解析 / 投影,绝不放宽。身份取自 Auth.Resolve(鉴权上下文),
//     绝不信 body 里的任何角色字段。
//   - 明文 bearer 只在 POST 签发响应里返一次;LIST 只返元数据(id/role/
//     前缀/状态/时间),绝不返明文或 hash。
//
// 写操作经由 internal/admin,审计写 admin_audit_events。

package adminhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// AdminTokensDeps 是 admin-token 端点所需依赖。具体依赖在 cmd/gateway 里
// 通过 duck typing 隐式满足这些接口。
type AdminTokensDeps struct {
	Auth   adminTokensAuth
	Issuer adminTokensIssuer
}

type adminTokensAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// adminTokensIssuer 抽象 internal/admin.AdminTokenIssuer 的三个方法,便于
// 测试时注入 fake。
type adminTokensIssuer interface {
	IssueToken(context.Context, admin.TokenIssueRequest) (admin.TokenIssueResult, error)
	RevokeToken(context.Context, admin.TokenRevokeRequest) (admin.TokenRevokeResult, error)
	ListTokens(context.Context, admin.AdminIdentity, int32, int32) ([]admin.TokenListItem, error)
}

// MountAdminTokenRoutes 把 POST(签发)/ GET(列举)/ POST-revoke 挂到
// 调用方的 chi router 上(作用域通常为 /admin/v1/admin-tokens)。
func MountAdminTokenRoutes(r chi.Router, d AdminTokensDeps) {
	r.Post("/", newTokenIssueHandler(d))
	r.Get("/", newTokenListHandler(d))
	r.Post("/{id}/revoke", newTokenRevokeHandler(d))
}

// -----------------------------------------------------------------------------
// POST /admin/v1/admin-tokens —— 签发
// -----------------------------------------------------------------------------

type tokenIssueRequestBody struct {
	Role      string  `json:"role"`                 // platform_admin 或 tenant_operator
	TenantID  *int64  `json:"tenant_id,omitempty"`  // tenant_operator 必填;platform_admin 必须省略
	ExpiresAt *string `json:"expires_at,omitempty"` // RFC3339;null = 永久
	Name      string  `json:"name,omitempty"`
	Note      string  `json:"note,omitempty"`
}

type tokenIssueResponseBody struct {
	ID              int64   `json:"id"`
	Role            string  `json:"role"`
	KeyPrefix       string  `json:"key_prefix"`
	Status          string  `json:"status"`
	ExpiresAt       *string `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
	PlaintextBearer string  `json:"plaintext_bearer"` // 仅显示一次
}

func newTokenIssueHandler(d AdminTokensDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Issuer == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin token issuance dependency unset")
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
		var req tokenIssueRequestBody
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.Role == "" {
			writeError(w, http.StatusBadRequest, "missing_fields", "role is required")
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
			if !t.After(time.Now()) {
				writeError(w, http.StatusBadRequest, "expires_at_in_past",
					"expires_at must be strictly in the future")
				return
			}
			expiresAt = &t
		}

		result, err := d.Issuer.IssueToken(r.Context(), admin.TokenIssueRequest{
			Caller:        ident,
			Role:          req.Role,
			ScopeTenantID: req.TenantID,
			ExpiresAt:     expiresAt,
			Name:          req.Name,
			Note:          req.Note,
			RequestID:     middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}

		w.Header().Set("X-Huakai-Key-Display", "once-only")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		var expStr *string
		if result.ExpiresAt != nil {
			s := result.ExpiresAt.UTC().Format(time.RFC3339)
			expStr = &s
		}
		_ = json.NewEncoder(w).Encode(tokenIssueResponseBody{
			ID:              result.TokenID,
			Role:            result.Role,
			KeyPrefix:       result.KeyPrefix,
			Status:          result.Status,
			ExpiresAt:       expStr,
			CreatedAt:       result.CreatedAt.UTC().Format(time.RFC3339),
			PlaintextBearer: result.Plaintext,
		})
	}
}

// -----------------------------------------------------------------------------
// GET /admin/v1/admin-tokens —— 列举(只返元数据)
// -----------------------------------------------------------------------------

type tokenListItemBody struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	KeyPrefix     string  `json:"key_prefix"`
	Role          string  `json:"role"`
	ScopeTenantID *int64  `json:"scope_tenant_id"`
	Bootstrap     bool    `json:"bootstrap"`
	Status        string  `json:"status"`
	ExpiresAt     *string `json:"expires_at"`
	LastUsedAt    *string `json:"last_used_at"`
	RevokedAt     *string `json:"revoked_at"`
	RevokedReason *string `json:"revoked_reason"`
	CreatedAt     string  `json:"created_at"`
}

func newTokenListHandler(d AdminTokensDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Issuer == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin token list dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}

		// 分页参数:格式错误必须被拒绝,而非静默回退默认值。
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

		// RBAC 在 issuer.ListTokens 内部前置(platform_admin-only,fail-closed)。
		items, err := d.Issuer.ListTokens(r.Context(), ident, limit, offset)
		if err != nil {
			writeAdminError(w, err)
			return
		}

		out := make([]tokenListItemBody, 0, len(items))
		for _, it := range items {
			body := tokenListItemBody{
				ID:            it.ID,
				Name:          it.Name,
				KeyPrefix:     it.KeyPrefix,
				Role:          it.Role,
				ScopeTenantID: it.ScopeTenantID,
				Bootstrap:     it.Bootstrap,
				Status:        it.Status,
				RevokedReason: it.RevokedReason,
				CreatedAt:     it.CreatedAt.UTC().Format(time.RFC3339),
			}
			body.ExpiresAt = rfc3339Ptr(it.ExpiresAt)
			body.LastUsedAt = rfc3339Ptr(it.LastUsedAt)
			body.RevokedAt = rfc3339Ptr(it.RevokedAt)
			out = append(out, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items":  out,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// -----------------------------------------------------------------------------
// POST /admin/v1/admin-tokens/{id}/revoke
// -----------------------------------------------------------------------------

type tokenRevokeRequestBody struct {
	Reason string `json:"reason"`
}

type tokenRevokeResponseBody struct {
	ID             int64 `json:"id"`
	AlreadyRevoked bool  `json:"already_revoked"`
}

func newTokenRevokeHandler(d AdminTokensDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Issuer == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin token revoke dependency unset")
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
			writeError(w, http.StatusBadRequest, "invalid_admin_token_id",
				"id must be a positive int64")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req tokenRevokeRequestBody
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
		}

		result, err := d.Issuer.RevokeToken(r.Context(), admin.TokenRevokeRequest{
			Caller:    ident,
			TokenID:   id,
			Reason:    req.Reason,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(tokenRevokeResponseBody{
			ID:             result.TokenID,
			AlreadyRevoked: result.AlreadyRevoked,
		})
	}
}

// rfc3339Ptr 把可选时间格式化为 RFC3339 字符串指针(nil 透传)。
func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
