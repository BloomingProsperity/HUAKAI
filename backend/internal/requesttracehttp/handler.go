// Package requesttracehttp 提供按网关请求标识的调用链路时间线投影(只读):
//
//	GET /admin/v1/requests/{request_id}/trace   部署者 / 租户管理员
//	GET /v1/me/requests/{request_id}/trace      最终用户(会话鉴权,只看自己的请求)
//
// 投影把分散在账本 claim、用量记录、账本事件、账号级审计事件与信任链收据里的事实按同一网关标识
// 服务端拼成有序时间线,并按三身份做读投影脱敏:最终用户只看模型、池、状态、用量与费用,不看
// 账号代号、渠道、选号明细与账号级审计;租户管理员看到本租户资源代号与已脱敏的尝试明细;部署者
// 看全部代号,任何角色都不会看到凭据、上游地址或请求正文(这些事实本就不落库)。
//
// 权限只来自认证上下文与库内归属:最终用户与租户管理员对"无权 / 不存在 / 明细已过保留期且无账本"
// 统一返回同一 404 形状;只有部署者可以区分"不存在"。任一事实表无对应行只让对应段为空并打
// coverage 标记,不让整个查询失败。
package requesttracehttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	tracedb "github.com/BloomingProsperity/HUAKAI/internal/db/requesttracedb"
)

// Store 是投影需要的五条只读查询。
type Store interface {
	GetRequestTraceClaim(context.Context, tracedb.GetRequestTraceClaimParams) (tracedb.GetRequestTraceClaimRow, error)
	ListRequestTraceUsageRecords(context.Context, tracedb.ListRequestTraceUsageRecordsParams) ([]tracedb.ListRequestTraceUsageRecordsRow, error)
	ListRequestTraceBillingEvents(context.Context, tracedb.ListRequestTraceBillingEventsParams) ([]tracedb.ListRequestTraceBillingEventsRow, error)
	ListRequestTraceAuditEvents(context.Context, tracedb.ListRequestTraceAuditEventsParams) ([]tracedb.ListRequestTraceAuditEventsRow, error)
	GetRequestTraceReceipt(context.Context, tracedb.GetRequestTraceReceiptParams) (tracedb.GetRequestTraceReceiptRow, error)
	GetRequestTraceCostReceipt(context.Context, tracedb.GetRequestTraceCostReceiptParams) (tracedb.GetRequestTraceCostReceiptRow, error)
}

// AdminAuth 从请求派生部署者或租户管理员身份。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Deps 是接口依赖。Store 为空表示网关未配置数据库真相,接口 fail-closed 返回 503。
type Deps struct {
	Auth  AdminAuth
	Store Store
	Now   func() time.Time
}

// NewAdminHandler 构造管理面投影:租户管理员锁本租户;部署者可选 tenant_id(省略则跨租户按标识查)。
func NewAdminHandler(d Deps) http.HandlerFunc {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil || d.Auth == nil {
			adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "request trace projection is not configured")
			return
		}
		requestID, ok := pathRequestID(w, r)
		if !ok {
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			adminhttpcore.WriteTenantScopeAuthError(w, err)
			return
		}
		viewer, tenantID, ok := resolveAdminScope(w, r, ident)
		if !ok {
			return
		}
		resp, err := project(r.Context(), d.Store, requestID, tenantID, 0, viewer, now())
		writeProjection(w, resp, err, viewer)
	}
}

// NewUserHandler 构造最终用户投影:会话鉴权,按认证上下文里的租户与用户收敛,不接受任何范围参数。
func NewUserHandler(d Deps) http.HandlerFunc {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "request trace projection is not configured")
			return
		}
		requestID, ok := pathRequestID(w, r)
		if !ok {
			return
		}
		ident, ok := auth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			adminhttpcore.WriteJSONError(w, http.StatusUnauthorized, "session_required", "a valid user session is required")
			return
		}
		resp, err := project(r.Context(), d.Store, requestID, ident.TenantID, ident.UserID, ViewerUser, now())
		writeProjection(w, resp, err, ViewerUser)
	}
}

// resolveAdminScope 把管理身份映射为脱敏档位与租户范围。租户解析复用 adminhttpcore 的点查解析器:
// 租户管理员锁本租户(显式他人 → 403);部署者显式正整数 tenant_id 则限定该租户,省略则跨租户按标识查(0)。
func resolveAdminScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (Viewer, int64, bool) {
	var viewer Viewer
	switch ident.Role {
	case admin.RoleTenantOperator:
		viewer = ViewerTenant
	case admin.RolePlatformAdmin:
		viewer = ViewerPlatform
	default:
		adminhttpcore.WriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		return "", 0, false
	}
	tenantID, ok := adminhttpcore.ResolveTenantScopeLookup(w, r, ident)
	if !ok {
		return "", 0, false
	}
	return viewer, tenantID, true
}

// pathRequestID 读取路径里的请求标识。与收据接口一致:支持含一个斜杠的标识(两段路由拼回),
// 非空、不超过 MaxRequestIDLength 字节、不含空白与控制字符;非法即 400,不做模糊搜索。
func pathRequestID(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID == "" {
		host := strings.TrimSpace(chi.URLParam(r, "request_id_host"))
		tail := strings.TrimSpace(chi.URLParam(r, "request_id_tail"))
		if host != "" && tail != "" {
			requestID = host + "/" + tail
		}
	}
	switch {
	case requestID == "":
		adminhttpcore.WriteJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id path parameter required")
		return "", false
	case len(requestID) > MaxRequestIDLength:
		adminhttpcore.WriteJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id length must be <= 256 bytes")
		return "", false
	case requestIDForbidden.MatchString(requestID):
		adminhttpcore.WriteJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id must not contain whitespace or control characters")
		return "", false
	case strings.Count(requestID, "/") > 1:
		// 与收据接口同一口径:标识最多含一个斜杠(百分号编码的多个斜杠在这里被拒绝)。
		adminhttpcore.WriteJSONError(w, http.StatusBadRequest, "invalid_request_id", "request_id may contain at most one slash")
		return "", false
	}
	return requestID, true
}

// errNotFound 表示本主体在库内没有任何可见事实(无权 / 不存在 / 明细过期且无账本)。
var errNotFound = errors.New("requesttrace: no visible facts")

func writeProjection(w http.ResponseWriter, resp *Response, err error, viewer Viewer) {
	switch {
	case errors.Is(err, errNotFound):
		// 最终用户与租户管理员对无权、不存在、过期无账本一律同一形状;部署者的"不存在"也是同一码,
		// 但部署者跨租户查询时它只可能表示不存在或未落盘。
		adminhttpcore.WriteJSONError(w, http.StatusNotFound, "request_not_found", "no visible facts for this request id")
	case err != nil:
		// 真相不可用就明确 503,不返回空投影冒充"没有发生过"。
		adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "request_trace_query_failed", "request trace projection is temporarily unavailable")
	default:
		adminhttpcore.WriteJSON(w, http.StatusOK, resp)
	}
}
