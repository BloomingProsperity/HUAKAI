package hermesops

import (
	"context"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

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
		Description:  "分页列出当前租户的上游供应商目录、协议和启用状态；只读。",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  ObjectSchema(paginationProperties(nil)),
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			limit, offset, err := pageArgs(req.Args)
			if err != nil {
				return ToolResult{}, err
			}
			rows, err := deps.List(ctx, admindb.ListAdminProvidersByTenantParams{TenantID: req.TenantID, PageOffset: int32(offset), PageLimit: int32(limit + 1)})
			if err != nil {
				return ToolResult{}, err
			}
			rows, page := trimPage(rows, limit, offset)
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
				"page":           page,
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
		Description:  "分页列出当前租户的渠道目录、所属池、故障切换状态码和启用状态；只读。",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  ObjectSchema(paginationProperties(nil)),
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			limit, offset, err := pageArgs(req.Args)
			if err != nil {
				return ToolResult{}, err
			}
			rows, err := deps.List(ctx, admindb.ListAdminChannelsByTenantParams{TenantID: req.TenantID, PageOffset: int32(offset), PageLimit: int32(limit + 1)})
			if err != nil {
				return ToolResult{}, err
			}
			rows, page := trimPage(rows, limit, offset)
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
				"page":          page,
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
