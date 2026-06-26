package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type sessionContextKey struct{}

type SessionIdentity struct {
	TenantID   int64
	UserID     int64
	FamilyID   string
	TokenID    string
	Generation int
}

type SessionValidator interface {
	Validate(context.Context, string, string, string) (usersession.ValidatedSession, error)
}

func ContextWithSession(ctx context.Context, ident SessionIdentity) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, ident)
}

func SessionFromContext(ctx context.Context) (SessionIdentity, bool) {
	ident, ok := ctx.Value(sessionContextKey{}).(SessionIdentity)
	return ident, ok
}

// SessionMiddleware 校验 bearer session 并把 identity 盖进请求
// context。resolver 推导用于 session drift/anomaly 检查的 client IP; 它必须
// 与登录 (usersession.Create) 和 refresh 时使用的、感知可信代理的同一个 resolver
// 一致, 否则在反向代理之后, 存储的基线 IP (真实客户端) 与校验 IP (代理
// socket) 会发生分歧, 导致 DetectDrift 误吊销一个有效 session。resolver 为 nil
// 是安全的, 会回退到 RemoteAddr —— 与直接暴露时的旧行为一致。
func SessionMiddleware(validator SessionValidator, resolver *clientip.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if validator == nil {
				writeSessionAuthError(w, http.StatusServiceUnavailable, "session_auth_not_configured", "session auth dependency unset")
				return
			}
			token, ok := parseSessionBearer(r.Header.Get("Authorization"))
			if !ok {
				writeSessionAuthError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
				return
			}
			validated, err := validator.Validate(r.Context(), token, resolver.ClientIP(r), r.UserAgent())
			if err != nil {
				switch {
				case errors.Is(err, usersession.ErrSigningKeyMissing):
					writeSessionAuthError(w, http.StatusServiceUnavailable, "session_auth_not_configured", "session signing key is not configured")
				case errors.Is(err, usersession.ErrTokenExpired):
					writeSessionAuthError(w, http.StatusUnauthorized, "session_token_expired", "session token is expired")
				default:
					writeSessionAuthError(w, http.StatusUnauthorized, "session_token_invalid", "session token is invalid")
				}
				return
			}
			ctx := ContextWithSession(r.Context(), SessionIdentity{
				TenantID: validated.TenantID, UserID: validated.UserID, FamilyID: validated.FamilyID,
				TokenID: validated.TokenID, Generation: validated.Generation,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func parseSessionBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func writeSessionAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
