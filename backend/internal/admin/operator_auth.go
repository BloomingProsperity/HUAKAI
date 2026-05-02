// AdminResolver authenticates an operator's bearer against admin_tokens.
//
// Pipeline (mirrors auth.APIKeyResolver):
//
//	parse Bearer header -> derive 16-char key_prefix -> LookupAdminTokenByPrefix
//	(<= 5 candidates) -> bcrypt.CompareHashAndPassword on each -> check
//	status + expires_at -> return AdminIdentity{TokenID, Role, ScopeTenantID}
//
// CMB-1: this resolver lives in internal/admin and is never imported from
// internal/router or auth's hot path. CMB-5: errors NEVER include the
// plaintext bearer or hash.

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// AdminIdentity is the resolved operator context produced by AdminResolver.
// ScopeTenantID is non-zero only when Role==RoleTenantOperator; for
// platform_admin the field is 0 and the handler RBAC permits cross-tenant.
type AdminIdentity struct {
	TokenID       int64
	Role          string
	ScopeTenantID int64
	Bootstrap     bool
}

// AdminResolver authenticates inbound admin requests against admin_tokens.
type AdminResolver struct {
	q *db.Queries
}

// NewAdminResolver wraps a sqlc.Queries handle.
func NewAdminResolver(q *db.Queries) *AdminResolver {
	return &AdminResolver{q: q}
}

// Resolve parses the Authorization header and authenticates the operator.
// Returns AdminIdentity on success; ErrAdminUnauthorized for any
// credential failure mode (D1 anti-enumeration); ErrAdminBackend for
// transient datastore failures.
func (r *AdminResolver) Resolve(ctx context.Context, req *http.Request) (AdminIdentity, error) {
	if r == nil || r.q == nil {
		return AdminIdentity{}, fmt.Errorf("%w: resolver not configured", ErrAdminBackend)
	}
	bearer, ok := parseAdminBearer(req.Header.Get("Authorization"))
	if !ok {
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	if !strings.HasPrefix(bearer, "hk_admin_") {
		// Customer keys (hk_live_/hk_test_) are not admin credentials.
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	if len(bearer) < PrefixLen {
		return AdminIdentity{}, ErrAdminUnauthorized
	}
	prefix := bearer[:PrefixLen]

	rows, err := r.q.LookupAdminTokenByPrefix(ctx, prefix)
	if err != nil {
		return AdminIdentity{}, fmt.Errorf("%w: lookup: %v", ErrAdminBackend, err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.Status != "active" {
			continue
		}
		if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now) {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(row.KeyHash), []byte(bearer)); err != nil {
			continue
		}
		var scope int64
		if row.ScopeTenantID != nil {
			scope = *row.ScopeTenantID
		}
		return AdminIdentity{
			TokenID:       row.ID,
			Role:          row.Role,
			ScopeTenantID: scope,
			Bootstrap:     row.Bootstrap,
		}, nil
	}
	return AdminIdentity{}, ErrAdminUnauthorized
}

// parseAdminBearer extracts the token from "Authorization: Bearer <token>".
// Same shape as auth.parseBearer but kept local to avoid CMB-1 import
// of internal/auth from this package.
func parseAdminBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// CanIssueForTenant returns nil if the identity is allowed to issue a key
// for tenantID, or ErrAdminForbidden otherwise.
//
// Rules:
//   - platform_admin may issue for any tenant.
//   - tenant_operator may issue only for its ScopeTenantID.
func (i AdminIdentity) CanIssueForTenant(tenantID int64) error {
	switch i.Role {
	case RolePlatformAdmin:
		return nil
	case RoleTenantOperator:
		if i.ScopeTenantID == tenantID {
			return nil
		}
		return ErrAdminForbidden
	default:
		return ErrAdminUnauthorized
	}
}

var _ = errors.New // keep errors import live for future expansion
