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

// SessionMiddleware validates the bearer session and stamps the identity into the request
// context. The resolver derives the client IP used for session drift/anomaly checks; it MUST
// be the same trusted-proxy-aware resolver used at login (usersession.Create) and refresh, or
// behind a reverse proxy the stored baseline IP (real client) and the validation IP (proxy
// socket) diverge and DetectDrift can falsely revoke a valid session (S2-109). A nil resolver
// is safe and falls back to RemoteAddr — matching pre-S2-109 behavior for direct exposure.
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
