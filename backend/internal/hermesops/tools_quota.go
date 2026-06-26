package hermesops

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

// QuotaPolicyListDeps 是 quota_policy_list 工具的只读依赖:List 包装
// dbquota.Queries.ListQuotaPoliciesForAdmin(按 tenant_id SELECT-only)。nil 时工具按依赖检查 fail-closed。
type QuotaPolicyListDeps struct {
	List func(ctx context.Context, params dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error)
}

// quotaPolicyListLimit 是单次列出配额策略上限(防超大读)。
const quotaPolicyListLimit = 200

// QuotaPolicyListSpec 构建只读 quota_policy_list 工具:列出本租户的配额策略及配置,让 Hermes 能回答
// "我的配额怎么配的、作用在什么 scope/metric、限多少、什么窗口、是否启用"。租户 scope 取自已鉴权
// req.TenantID(ListQuotaPoliciesForAdmin 按 tenant_id 过滤)。
//
// 隐私 safe-by-construction(quotaPolicyShape):QuotaPolicy 全是结构化配置(枚举/数值/时间戳)。**有意不投**
// CreatedByActor / LastModifiedByActor(actor 标识,可能是 admin token id 形状,审计需求由 audit_lookup 工具
// 覆盖)与 TenantID(调用方已知)。limit_value / burst_value 是 pgtype.Numeric,以 float64 投出(诊断展示;
// 现实配额量级 float64 精确足够)。
func QuotaPolicyListSpec(deps QuotaPolicyListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolQuotaPolicyList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's quota policies and their config: scope (kind/id), metric, window kind+seconds, limit + burst value, mode, priority, enabled, valid-from/until. Lets you answer 'how is my quota configured, on whom, what limit'. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			rows, err := deps.List(ctx, dbquota.ListQuotaPoliciesForAdminParams{
				TenantID:   req.TenantID,
				PageOffset: 0,
				PageLimit:  quotaPolicyListLimit,
			})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			enabledCount := 0
			for _, p := range rows {
				if p.Enabled {
					enabledCount++
				}
				items = append(items, quotaPolicyShape(p))
			}
			return ToolResult{Summary: map[string]any{
				"policy_count":  len(rows),
				"enabled_count": enabledCount,
				"items":         items,
			}}, nil
		},
	}
}

// quotaPolicyShape 把一条 QuotaPolicy 投影成配额配置诊断字段。显式列举(safe-by-construction):只露
// 结构化枚举/数值/时间戳;有意不投 CreatedByActor/LastModifiedByActor(actor 标识)与 TenantID。
func quotaPolicyShape(p dbquota.QuotaPolicy) map[string]any {
	return map[string]any{
		"id":             p.ID,
		"scope_kind":     p.ScopeKind,
		"scope_id":       p.ScopeID,
		"metric":         p.Metric,
		"window_kind":    p.WindowKind,
		"window_seconds": p.WindowSeconds,
		"limit_value":    numericAny(p.LimitValue),
		"burst_value":    numericAny(p.BurstValue),
		"mode":           p.Mode,
		"priority":       p.Priority,
		"enabled":        p.Enabled,
		"valid_from":     tsAny(p.ValidFrom),
		"valid_until":    tsAny(p.ValidUntil),
		"created_at":     tsAny(p.CreatedAt),
		"updated_at":     tsAny(p.UpdatedAt),
	}
}

// numericAny 把可空 pgtype.Numeric 投成 float64(或 nil)。**诊断展示用**:float64 对 |value| ≤ 2^53
// (~9.0e15)的整数精确,现实配额量级(token/请求计数 ≤ ~1e12、金额 ≤ ~1e8)远在此范围内、精确足够;
// 极端超大配额(>~1e15)会丢低位精度,届时应改用 string/big.Int 投影(非本诊断工具当前所需)。无效值归一
// 成 nil 保证 JSON 形态稳定;Float64Value 出错亦归 nil(不 panic)。
func numericAny(n pgtype.Numeric) any {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return f.Float64
}
