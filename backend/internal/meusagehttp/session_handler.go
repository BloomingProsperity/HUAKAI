package meusagehttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// NewSessionHandler 服务「当前登录用户跨其全部 API Key 的逐请求用量日志」。
//
// 与 NewHandler(API-key 鉴权、按单个 APIKeyID 收敛、供 relay 客户端看自己那把 key)不同:
// 本处理器挂在 /v1/me 会话组,身份**只**来自经 SessionMiddleware 校验的会话上下文
// (auth.SessionFromContext),绝不读请求体/查询里的任何用户标识,因此用户无法越权读他人用量。
// 过滤维度是 user_id(ListUsageRecords 新增的末位 narg),复用与 NewHandler 完全一致的
// 分页游标、DTO 与映射逻辑,仅替换鉴权来源与收敛维度。
//
// 只读:不触碰热路径、配额、计费、鉴权或信任链的任何写入(沿用 F-OBS-001 约束)。
func NewSessionHandler(store UsageStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "me usage dependency unset")
			return
		}
		ident, ok := auth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		q, ok := parseQuery(w, r.URL)
		if !ok {
			return
		}
		tenantID := ident.TenantID
		userID := ident.UserID
		rows, err := store.ListUsageRecords(r.Context(), dbbilling.ListUsageRecordsParams{
			TenantID:        &tenantID,
			UserID:          &userID,
			FromTs:          q.FromTs,
			ToTs:            q.ToTs,
			HasCursor:       q.HasCursor,
			CursorCreatedAt: q.CursorCreatedAt,
			CursorID:        q.CursorID,
			PageLimit:       q.FetchLimit,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_query_failed", "usage backend unavailable")
			return
		}
		next := ""
		if int32(len(rows)) > q.Limit {
			last := rows[q.Limit-1]
			next = encodeCursor(last.CreatedAt, last.ID)
			rows = rows[:q.Limit]
		}
		items := make([]usageRecord, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapUsageRecord(row, ident.TenantID))
		}
		writeJSON(w, http.StatusOK, listResponse{Items: items, NextCursor: next})
	}
}
