package hermesops

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

// ChannelHealthListDeps 是 channel_health_list 工具的只读依赖:List 是 channelhealth 的按租户
// SELECT-only 读(返回结构化 Record,**不含** GetChannelHealth 那条 AuditEvent 自由 payload)。
type ChannelHealthListDeps struct {
	List func(ctx context.Context, tenantID int64, limit, offset int) ([]channelhealth.Record, error)
}

// ChannelHealthListSpec 构建只读 channel_health_list 工具:列出**整个租户**的逐通道健康记录,补
// account_health_diagnose(单账号)缺的"跨账号俯瞰——哪些通道在 cooling_down/disabled/manual_paused"。
// 可选 state 过滤("show me 所有 cooling_down 的通道")。投影复用 channelHealthShape(safe-by-construction:
// 只露 enum/时间戳/ids/计数,绝不露自由文本),Record 无密钥/用户 PII/自由 payload。租户 scope 取自已
// 鉴权的 req.TenantID。
//
// Args: { "state": <string, optional 按健康状态过滤> }
func ChannelHealthListSpec(deps ChannelHealthListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolChannelHealthList,
		Category:     CategoryDiagnostic,
		Description:  "分页列出当前租户的逐通道健康记录，可按状态筛选；只读，不返回自由文本原因。",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: ObjectSchema(paginationProperties(map[string]any{
			"state": StringSchema("按渠道健康状态筛选", "active", "degraded", "cooling_down", "ramping", "disabled", "manual_paused"),
		})),
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			limit, offset, err := pageArgs(req.Args)
			if err != nil {
				return ToolResult{}, err
			}
			rows, err := deps.List(ctx, req.TenantID, limit+1, offset)
			if err != nil {
				return ToolResult{}, err
			}
			rows, page := trimPage(rows, limit, offset)
			stateFilter, hasFilter := ArgString(req.Args, "state")
			items := make([]map[string]any, 0, len(rows))
			byState := map[string]int{}
			for _, r := range rows {
				st := string(r.State)
				byState[st]++ // by_state 统计全部(过滤前),items 才按 state 过滤
				if hasFilter && st != stateFilter {
					continue
				}
				items = append(items, channelHealthShape(r))
			}
			return ToolResult{Summary: map[string]any{
				"channel_count": len(rows),
				"by_state":      intCountMap(byState),
				"items":         items,
				"page":          page,
			}}, nil
		},
	}
}
