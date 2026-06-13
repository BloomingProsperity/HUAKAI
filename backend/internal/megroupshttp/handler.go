package megroupshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// matchAllModels asks the routes repo for every pool group reachable by the
// caller's tier regardless of model. ModelPatternMatches treats "*" as a
// wildcard, so passing it yields the tier's full reachable group set.
const matchAllModels = "*"

// AuthResolver derives the caller identity from the request. The session-backed
// implementation rejects anything that is not a valid logged-in user, so the
// tenant/user pair used downstream cannot be influenced by request input.
type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

// UserGroupReader returns the caller's current routing tier (users.user_group).
type UserGroupReader interface {
	UserGroup(ctx context.Context, tenantID, userID int64) (string, error)
}

// RoutesRepo reports which pool groups the caller's tier may reach.
type RoutesRepo interface {
	GroupRoutes(ctx context.Context, tenantID int64, userGroup, model string) (subscriptionenforce.GroupRoutes, error)
}

// RatioLister returns the tenant's configured pool-group multipliers.
type RatioLister interface {
	ListRatios(ctx context.Context, tenantID int64) ([]pricingcatalog.GroupPricingRatio, error)
}

// PoolNameLister returns display names for the tenant's pool groups, keyed by id.
type PoolNameLister interface {
	PoolNames(ctx context.Context, tenantID int64) (map[int64]string, error)
}

type Deps struct {
	Auth       AuthResolver
	UserGroups UserGroupReader
	RoutesRepo RoutesRepo
	Ratios     RatioLister
	Pools      PoolNameLister
}

type listResponse struct {
	Object    string      `json:"object"`
	UserGroup string      `json:"user_group"`
	Items     []groupView `json:"items"`
}

type groupView struct {
	PoolGroupID    int64  `json:"pool_group_id"`
	Name           string `json:"name"`
	Ratio          string `json:"ratio,omitempty"`
	HasPublicRatio bool   `json:"has_public_ratio"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.UserGroups == nil || d.RoutesRepo == nil || d.Ratios == nil || d.Pools == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "me groups dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "session policy forbids this request")
			return
		}
		if err != nil || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}

		ctx := r.Context()
		// Tenant + user come only from the session identity; never from the
		// request, so a caller cannot read another tenant's groups (CMB-5).
		userGroup, err := d.UserGroups.UserGroup(ctx, ident.TenantID, ident.UserID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "me_groups_unavailable", "user group lookup unavailable")
			return
		}

		routes, err := d.RoutesRepo.GroupRoutes(ctx, ident.TenantID, userGroup, matchAllModels)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "me_groups_unavailable", "group routing lookup unavailable")
			return
		}

		names, err := d.Pools.PoolNames(ctx, ident.TenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "me_groups_unavailable", "pool group lookup unavailable")
			return
		}

		ratios, err := d.Ratios.ListRatios(ctx, ident.TenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "me_groups_unavailable", "pricing ratio lookup unavailable")
			return
		}
		ratioByGroup := make(map[int64]pricingcatalog.GroupPricingRatio, len(ratios))
		for _, row := range ratios {
			ratioByGroup[row.PoolGroupID] = row
		}

		allowed := make([]int64, 0, len(routes.Allowed))
		for poolGroupID := range routes.Allowed {
			allowed = append(allowed, poolGroupID)
		}
		sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })

		out := listResponse{
			Object:    "me_group_list",
			UserGroup: userGroup,
			Items:     make([]groupView, 0, len(allowed)),
		}
		for _, poolGroupID := range allowed {
			view := groupView{PoolGroupID: poolGroupID, Name: names[poolGroupID]}
			// Disclose the multiplier only when the operator marked this group's
			// ratio public. Non-public internal cost multipliers are withheld and
			// has_public_ratio stays false, so users never see internal pricing or
			// a misleading default for an unconfigured group.
			if row, ok := ratioByGroup[poolGroupID]; ok && row.PublicRatio {
				view.Ratio = row.RatioString()
				view.HasPublicRatio = true
			}
			out.Items = append(out.Items, view)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// SessionResolver adapts the validated /v1/me session context into the
// AuthResolver shape, rejecting any request that lacks a fully-identified
// session user.
type SessionResolver struct{}

func (SessionResolver) Resolve(ctx context.Context, _ *http.Request) (auth.Identity, error) {
	ident, ok := auth.SessionFromContext(ctx)
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		return auth.Identity{}, auth.ErrUnauthorized
	}
	return auth.Identity{TenantID: ident.TenantID, UserID: ident.UserID}, nil
}

// PostgresUserGroupReader reads users.user_group with a plain (non-locking)
// SELECT so this read-only endpoint never contends with subscription
// upgrade/downgrade transactions that lock the same user row.
type PostgresUserGroupReader struct {
	pool *pgxpool.Pool
}

func NewPostgresUserGroupReader(pool *pgxpool.Pool) *PostgresUserGroupReader {
	return &PostgresUserGroupReader{pool: pool}
}

func (r *PostgresUserGroupReader) UserGroup(ctx context.Context, tenantID, userID int64) (string, error) {
	if r == nil || r.pool == nil {
		return "", errors.New("megroupshttp: user group reader not configured")
	}
	var group string
	err := r.pool.QueryRow(ctx,
		`SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2`,
		tenantID, userID,
	).Scan(&group)
	if errors.Is(err, pgx.ErrNoRows) {
		// A valid session with no user row is treated as the default tier rather
		// than a hard error; downstream routing yields an empty allowed set.
		return "default", nil
	}
	if err != nil {
		return "", err
	}
	return group, nil
}

// PostgresPoolNameLister maps pool_group_id to display name for the tenant's
// live (enabled, not soft-deleted) groups.
type PostgresPoolNameLister struct {
	pool *pgxpool.Pool
}

func NewPostgresPoolNameLister(pool *pgxpool.Pool) *PostgresPoolNameLister {
	return &PostgresPoolNameLister{pool: pool}
}

func (l *PostgresPoolNameLister) PoolNames(ctx context.Context, tenantID int64) (map[int64]string, error) {
	if l == nil || l.pool == nil {
		return nil, errors.New("megroupshttp: pool name lister not configured")
	}
	rows, err := l.pool.Query(ctx,
		`SELECT id, name FROM pool_groups WHERE tenant_id=$1 AND deleted_at IS NULL`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
