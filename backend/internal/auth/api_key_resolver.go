// Phase L0 minimum: table-backed inbound auth resolver.
// Replaces the SmokeAuthResolver path used during Phase C v0.1.
//
// Pipeline per docs/process/plans/2026-04-30-n4-l0-minimum.md (synthesized):
//
//	parse Bearer header → derive 16-char key_prefix → LookupAPIKeysByPrefix
//	(<= 5 candidates) → bcrypt.CompareHashAndPassword on each → check
//	status + expires_at → return Identity{TenantID, APIKeyID, UserID}
//
// Boundary contracts (docs/specs/_invariants/cross-module-boundaries.md):
// This is the Auth layer; the layered call order is
//     Auth → Registry → Router. Resolver does NOT import router or call
//     Pool/Adapter/Ledger.
// Plaintext bearer is never logged. Errors return only the
//     key_prefix (never the suffix or full token) for debugging.
// The only write in this package is best-effort auth telemetry:
//     last_used_at is touched after successful verification, and touch
//     failure must not reject otherwise valid credentials.
//
// All authentication failures map to a single ErrUnauthorized return
// (D10 in synthesized plan) so the handler can map to HTTP 401 without
// leaking enumeration signal (revoked vs expired vs not-found).

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipallow"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// Identity is the resolved inbound auth context produced by Resolve.
// Mirrors the fields the chat handler needs to populate
// router.RequestContext + ledger ReserveRequest. Fields are populated
// only on success; partial values are never returned.
type Identity struct {
	TenantID int64
	APIKeyID int64
	UserID   int64
	// AllowedModels is the raw comma-separated per-key model allowlist.
	// Nil/blank means unrestricted; model-bearing ingress handlers enforce it
	// after they parse the request model.
	AllowedModels *string
	// UserGroup 是该用户当前订阅档位 (users.user_group, 默认 'default')。
	// 供 R-SUB-WIRE-1 分组→路由的 GroupPolicyGate 在 pool 选择时限制可用渠道。
	// 空字符串视同无限制 (向后兼容老链路)。
	UserGroup string
}

// APIKeyPrefixLen is the number of leading characters of the bearer
// token that are stored verbatim in api_keys.key_prefix and used for
// indexed lookup. 16 chars (covers "hk_live_" or "hk_test_" plus 8
// chars of randomness) keeps lookup selective enough to bound the
// bcrypt-verify-fanout caused by colliding prefixes.
const APIKeyPrefixLen = 16

// MaxBcryptFanout caps how many candidate rows a single Resolve call
// will bcrypt-compare. The SQL query also LIMITs to this value; the
// constant exists so the cap is visible at the resolver layer too.
const MaxBcryptFanout = 5

// lastUsedTouchTimeout keeps best-effort telemetry from coupling auth
// availability to row locks or slow writes on api_keys.
const lastUsedTouchTimeout = 100 * time.Millisecond

// ErrUnauthorized is returned for ANY CREDENTIAL-LEVEL failure: bad
// header, malformed bearer, prefix miss, bcrypt mismatch, key revoked,
// key expired, user disabled. The handler maps this to HTTP 401.
//
// Discriminating credential failure modes externally would leak account
// enumeration signal (D10 in synthesized plan). Operators see the
// distinction in audit logs only.
var ErrUnauthorized = errors.New("auth: unauthorized")

// ErrForbidden is returned after a credential is valid but the authenticated
// key's policy forbids the request, such as an IP allowlist miss. Handlers map
// it to HTTP 403.
var ErrForbidden = errors.New("auth: forbidden")

// ErrAuthMisconfigured signals the resolver was constructed without a
// valid dbauth.Queries handle. The handler maps this to HTTP 503 (D9).
var ErrAuthMisconfigured = errors.New("auth: resolver not configured")

// ErrAuthBackend signals a transient datastore failure during auth
// lookup (PG connection broken, context cancelled mid-query, missing
// table). The handler maps this to HTTP 503 — NOT 401 — so legitimate
// clients are not told their valid credentials are invalid during an
// infrastructure outage.
var ErrAuthBackend = errors.New("auth: backend datastore error")

// APIKeyResolver authenticates inbound requests against the api_keys
// table. Construct via NewAPIKeyResolver.
type APIKeyResolver struct {
	q                apiKeyQueries
	clientIPResolver *clientip.Resolver
}

type apiKeyQueries interface {
	LookupAPIKeysByPrefix(context.Context, string) ([]dbauth.LookupAPIKeysByPrefixRow, error)
	TouchAPIKeyLastUsed(context.Context, int64) error
}

// NewAPIKeyResolver wraps a sqlc.Queries handle. Pool/connection
// lifecycle is the caller's responsibility.
func NewAPIKeyResolver(q *dbauth.Queries) *APIKeyResolver {
	return &APIKeyResolver{q: q}
}

func NewAPIKeyResolverWithClientIPResolver(q *dbauth.Queries, resolver *clientip.Resolver) *APIKeyResolver {
	return &APIKeyResolver{q: q, clientIPResolver: resolver}
}

// Resolve parses the Authorization header and authenticates the request.
// On success, returns Identity populated from the matching api_keys row.
// On any failure, returns ErrUnauthorized — the handler chooses the
// HTTP status (401 for ErrUnauthorized, 503 for ErrAuthMisconfigured).
func (r *APIKeyResolver) Resolve(ctx context.Context, req *http.Request) (Identity, error) {
	if r == nil || r.q == nil {
		return Identity{}, ErrAuthMisconfigured
	}
	bearer, ok := parseBearer(req.Header.Get("Authorization"))
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	if !validBearerFormat(bearer) {
		return Identity{}, ErrUnauthorized
	}
	if len(bearer) < APIKeyPrefixLen {
		return Identity{}, ErrUnauthorized
	}
	prefix := bearer[:APIKeyPrefixLen]

	rows, err := r.q.LookupAPIKeysByPrefix(ctx, prefix)
	if err != nil {
		// Do not collapse infra failures to credential
		// failure. Handler maps ErrAuthBackend to 503.
		return Identity{}, fmt.Errorf("%w: lookup: %v", ErrAuthBackend, err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.KeyStatus != "active" {
			continue
		}
		if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now) {
			continue
		}
		// Tenant + user status checked
		// per-row via INNER JOIN (deleted_at IS NULL filters parents at
		// SQL layer; status is enforced here). One DB roundtrip total.
		if row.UserStatus != "active" {
			continue
		}
		if row.TenantStatus != "active" {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(row.KeyHash), []byte(bearer)); err != nil {
			continue
		}
		allowed, err := apikeyipallow.AllowsCSV(row.IpAllowlist, r.clientIPResolver.ClientIP(req))
		if err != nil {
			slog.WarnContext(ctx, "api_key_ip_allowlist_invalid",
				"tenant_id", row.TenantID,
				"api_key_id", row.ID,
				"error", err)
			return Identity{}, ErrForbidden
		}
		if !allowed {
			return Identity{}, ErrForbidden
		}
		touchCtx, cancel := context.WithTimeout(ctx, lastUsedTouchTimeout)
		touchErr := r.q.TouchAPIKeyLastUsed(touchCtx, row.ID)
		cancel()
		if touchErr != nil {
			slog.WarnContext(ctx, "api_key_last_used_touch_failed",
				"tenant_id", row.TenantID,
				"api_key_id", row.ID,
				"error", touchErr)
		}
		return Identity{
			TenantID:      row.TenantID,
			APIKeyID:      row.ID,
			UserID:        row.UserID,
			AllowedModels: row.AllowedModels,
			UserGroup:     row.UserGroup,
		}, nil
	}
	return Identity{}, ErrUnauthorized
}

// parseBearer extracts the token from "Authorization: Bearer <token>".
// Returns ("", false) when the header is missing or malformed.
func parseBearer(header string) (string, bool) {
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

// validBearerFormat enforces the HUAKAI bearer namespace: starts with
// "hk_live_" or "hk_test_" (D2 in synthesized plan). This refuses
// obviously-foreign tokens (e.g. "sk-...") before we waste a DB lookup.
func validBearerFormat(token string) bool {
	return strings.HasPrefix(token, "hk_live_") || strings.HasPrefix(token, "hk_test_")
}
