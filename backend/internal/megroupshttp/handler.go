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

// matchAllModels 向 routes repo 请求调用方所在 tier 可达的每个 pool group,
// 不区分 model。ModelPatternMatches 把 "*" 视为通配符,因此传入它会得到该
// tier 的完整可达 group 集合。
const matchAllModels = "*"

// AuthResolver 从请求中推导调用方身份。基于会话的实现会拒绝任何不是有效
// 已登录用户的请求,因此下游使用的 tenant/user 对无法被请求输入影响。
type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

// UserGroupReader 返回调用方当前的路由 tier(users.user_group)。
type UserGroupReader interface {
	UserGroup(ctx context.Context, tenantID, userID int64) (string, error)
}

// RoutesRepo 报告调用方所在 tier 可以到达哪些 pool group。
type RoutesRepo interface {
	GroupRoutes(ctx context.Context, tenantID int64, userGroup, model string) (subscriptionenforce.GroupRoutes, error)
}

// RatioLister 返回该 tenant 已配置的 pool-group 倍率。
type RatioLister interface {
	ListRatios(ctx context.Context, tenantID int64) ([]pricingcatalog.GroupPricingRatio, error)
}

// PoolNameLister 返回该 tenant 各 pool group 的展示名,按 id 索引。
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
		// tenant + user 只取自会话身份;绝不取自请求,因此调用方无法读取
		// 另一个 tenant 的 group(CMB-5)。
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
			// 只有当运营者把该 group 的 ratio 标记为 public 时才披露倍率。
			// 非公开的内部成本倍率会被隐去,has_public_ratio 保持 false,
			// 这样用户永远看不到内部定价,也看不到某个未配置 group 的误导性
			// 默认值。
			if row, ok := ratioByGroup[poolGroupID]; ok && row.PublicRatio {
				view.Ratio = row.RatioString()
				view.HasPublicRatio = true
			}
			out.Items = append(out.Items, view)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// SessionResolver 把已校验的 /v1/me 会话上下文适配为 AuthResolver 形状,
// 拒绝任何缺少完整身份标识会话用户的请求。
type SessionResolver struct{}

func (SessionResolver) Resolve(ctx context.Context, _ *http.Request) (auth.Identity, error) {
	ident, ok := auth.SessionFromContext(ctx)
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		return auth.Identity{}, auth.ErrUnauthorized
	}
	return auth.Identity{TenantID: ident.TenantID, UserID: ident.UserID}, nil
}

// PostgresUserGroupReader 用普通(非加锁)的 SELECT 读取 users.user_group,
// 这样此只读端点永远不会与锁住同一 user 行的订阅升级/降级事务发生争用。
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
		// 一个没有 user 行的有效会话会被当作默认 tier,而非硬错误;下游路由
		// 会产出一个空的 allowed 集合。
		return "default", nil
	}
	if err != nil {
		return "", err
	}
	return group, nil
}

// PostgresPoolNameLister 把 pool_group_id 映射到该 tenant 处于活跃状态
//(已启用、未软删除)的 group 的展示名。
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
