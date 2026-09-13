package geminihttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

func beginGeminiCountTokens(w http.ResponseWriter, r *http.Request, screener moderation.Screener, ident auth.Identity, body []byte) (context.Context, string, bool) {
	requestID := uuid.NewString()
	if !moderation.ApplyHTTP(w, r.Context(), screener, moderation.ScreenRequest{
		TenantID:       ident.TenantID,
		APIKeyID:       ident.APIKeyID,
		UserID:         ident.UserID,
		RequestID:      requestID,
		ClientProtocol: moderation.ProtocolGemini,
		Body:           body,
	}) {
		return r.Context(), "", false
	}
	// 请求 ID 通过显式 ctx 向下游传播(resolveModel/planRoute 均直接收 ctx)。
	ctx := context.WithValue(r.Context(), middleware.RequestIDKey, requestID)
	w.Header().Set(middleware.RequestIDHeader, requestID)
	return ctx, requestID, true
}
