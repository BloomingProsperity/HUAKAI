package hermesops

import (
	"context"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// catalogListLimit 是单次列出目录条目上限(防超大读)。
const catalogListLimit = 200

// ---- provider_catalog_list ------------------------------------------------

// ProviderCatalogListDeps 是 provider_catalog_list 工具的只读依赖:List 包装
// admindb.Queries.ListAdminProvidersByTenant(按 tenant_id SELECT-only,SQL 含 deleted_at IS NULL)。
type ProviderCatalogListDeps struct {
	List func(ctx context.Context, params admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error)
}

// ProviderCatalogListSpec 构建只读 provider_catalog_list 工具:列出本租户已定义的上游供应商类型目录。
// 租户 scope 取自已鉴权 req.TenantID。Row 全是结构化目录数据(code/显示名/协议/启用/时间),无 PII/密钥,
// 行内甚至不含 TenantID;providerCatalogShape 仍显式列举投影(防未来新增字段自动外泄)。
func ProviderCatalogListSpec(deps ProviderCatalogListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolProviderCatalogList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's upstream provider catalog: code, display name, upstream protocol, enabled, created-at. Lets you answer 'which provider types have I onboarded'. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			rows, err := deps.List(ctx, admindb.ListAdminProvidersByTenantParams{TenantID: req.TenantID, PageOffset: 0, PageLimit: catalogListLimit})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			enabledCount := 0
			for _, p := range rows {
				if p.Enabled {
					enabledCount++
				}
				items = append(items, providerCatalogShape(p))
			}
			return ToolResult{Summary: map[string]any{
				"provider_count": len(rows),
				"enabled_count":  enabledCount,
				"items":          items,
			}}, nil
		},
	}
}

func providerCatalogShape(p admindb.ListAdminProvidersByTenantRow) map[string]any {
	return map[string]any{
		"id":                p.ID,
		"code":              p.Code,
		"display_name":      p.DisplayName,
		"upstream_protocol": p.UpstreamProtocol,
		"enabled":           p.Enabled,
		"created_at":        tsAny(p.CreatedAt),
	}
}

// ---- channel_catalog_list -------------------------------------------------

// ChannelCatalogListDeps 是 channel_catalog_list 工具的只读依赖:List 包装
// admindb.Queries.ListAdminChannelsByTenant(按 tenant_id SELECT-only,SQL 含 deleted_at IS NULL)。
type ChannelCatalogListDeps struct {
	List func(ctx context.Context, params admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error)
}

// ChannelCatalogListSpec 构建只读 channel_catalog_list 工具:列出本租户已定义的渠道目录及其所属 pool group。
// 租户 scope 取自已鉴权 req.TenantID。Row 全是结构化目录数据(pool_group_id/名称/failover 状态码/启用/时间),
// 无 PII/密钥;channelCatalogShape 显式列举投影。
func ChannelCatalogListSpec(deps ChannelCatalogListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolChannelCatalogList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's channel catalog: pool group id, name, failover status codes, enabled, created-at. Lets you answer 'what channels have I defined and which pool they belong to'. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			rows, err := deps.List(ctx, admindb.ListAdminChannelsByTenantParams{TenantID: req.TenantID, PageOffset: 0, PageLimit: catalogListLimit})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			enabledCount := 0
			for _, c := range rows {
				if c.Enabled {
					enabledCount++
				}
				items = append(items, channelCatalogShape(c))
			}
			return ToolResult{Summary: map[string]any{
				"channel_count": len(rows),
				"enabled_count": enabledCount,
				"items":         items,
			}}, nil
		},
	}
}

func channelCatalogShape(c admindb.ListAdminChannelsByTenantRow) map[string]any {
	return map[string]any{
		"id":                    c.ID,
		"pool_group_id":         c.PoolGroupID,
		"name":                  c.Name,
		"failover_status_codes": c.FailoverStatusCodes,
		"enabled":               c.Enabled,
		"created_at":            tsAny(c.CreatedAt),
	}
}
