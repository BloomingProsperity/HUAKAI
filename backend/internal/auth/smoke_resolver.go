// Phase C smoke auth only; replace with api_keys/users-backed resolver
// after Owner approves the 0007 schema migration in Phase E.
//
// This resolver compares the inbound Authorization header to a single
// shared bearer token from env. It is deliberately not production-grade:
// no per-tenant keys, no rotation, no hashed storage. The chat-completions
// handler MUST 503 when SmokeAuthConfigured() returns false.

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// SmokeIdentity is the resolved caller. All fields populated for ANY
// successful resolution; downstream code should never see a partial value.
type SmokeIdentity struct {
	TenantID int64
	APIKeyID int64
	UserID   int64
}

// SmokeAuthResolver authenticates inbound requests against a shared
// bearer token from config. Phase C only.
type SmokeAuthResolver struct {
	BearerToken string
	TenantID    int64
	APIKeyID    int64
	UserID      int64
}

// ErrSmokeAuthMisconfigured is returned by Resolve when the resolver was
// constructed without all four required fields. The handler maps this to
// HTTP 503.
var ErrSmokeAuthMisconfigured = errors.New("auth: smoke resolver missing required env")

// ErrSmokeBearerMismatch is returned when the inbound Authorization
// header does not match the configured bearer token. The handler maps
// this to HTTP 401.
var ErrSmokeBearerMismatch = errors.New("auth: smoke bearer token mismatch")

// Resolve parses Authorization: Bearer <token> and returns the configured
// identity if it matches. Returns ErrSmokeBearerMismatch on miss.
func (r *SmokeAuthResolver) Resolve(_ context.Context, req *http.Request) (SmokeIdentity, error) {
	if r == nil || r.BearerToken == "" || r.TenantID == 0 || r.APIKeyID == 0 || r.UserID == 0 {
		return SmokeIdentity{}, ErrSmokeAuthMisconfigured
	}
	header := req.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return SmokeIdentity{}, ErrSmokeBearerMismatch
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" || token != r.BearerToken {
		return SmokeIdentity{}, ErrSmokeBearerMismatch
	}
	return SmokeIdentity{
		TenantID: r.TenantID,
		APIKeyID: r.APIKeyID,
		UserID:   r.UserID,
	}, nil
}
