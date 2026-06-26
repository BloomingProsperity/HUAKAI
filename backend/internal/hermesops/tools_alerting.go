package hermesops

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

// AlertRuleListDeps 是 alert_rule_list 工具的只读依赖:List 包装 alerting.PostgresStore.ListRules
// (按 tenant_id SELECT-only)。nil 时工具按依赖检查 fail-closed。
type AlertRuleListDeps struct {
	List func(ctx context.Context, in alerting.ListRulesInput) ([]alerting.AlertRule, error)
}

// alertRuleListLimit 是单次列出告警规则上限(防超大读)。
const alertRuleListLimit = 200

// AlertRuleListSpec 构建只读 alert_rule_list 工具:列出本租户的告警规则及配置,让 Hermes 能回答"我配了
// 哪些告警、对什么 metric、阈值多少、上次什么时候触发"。租户 scope 取自已鉴权 req.TenantID
// (ListRules 按 tenant_id 过滤)。
//
// 隐私 safe-by-construction(alertRuleShape):AlertRule 全是结构化配置(枚举/数值/布尔/时间戳)。Filters 是
// **运营自填的规则过滤标签**——是规则定义的一部分(操作员经 admin API 创建规则时自己填的 metric 分组/过滤
// 键值,如 {"provider_account_id":"5"}),非用户请求内容/密钥;以拷贝形式投出(不共享底层引用)。投影排
// TenantID(调用方已知)。
func AlertRuleListSpec(deps AlertRuleListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolAlertRuleList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's alert rules and their config: name, metric, comparator+threshold, severity, window/sustained/cooldown seconds, notify-email, filters, enabled, last-triggered. Lets you answer 'what alerts have I configured, on what metric, at what threshold, when last fired'. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			rows, err := deps.List(ctx, alerting.ListRulesInput{TenantID: req.TenantID, Limit: alertRuleListLimit, Offset: 0})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			bySeverity := map[string]int{}
			enabledCount := 0
			for _, r := range rows {
				bySeverity[string(r.Severity)]++
				if r.Enabled {
					enabledCount++
				}
				items = append(items, alertRuleShape(r))
			}
			return ToolResult{Summary: map[string]any{
				"rule_count":    len(rows),
				"enabled_count": enabledCount,
				"by_severity":   intCountMap(bySeverity),
				"items":         items,
			}}, nil
		},
	}
}

// alertRuleShape 把一条 AlertRule 投影成告警配置诊断字段。显式列举(safe-by-construction):只露结构化
// 枚举/数值/布尔/时间戳 + 运营自填的 Filters(规则定义,拷贝投出);有意不投 TenantID。
func alertRuleShape(r alerting.AlertRule) map[string]any {
	return map[string]any{
		"id":                r.ID,
		"name":              r.Name,
		"metric":            r.Metric,
		"metric_type":       string(r.MetricType),
		"comparator":        string(r.Comparator),
		"threshold":         r.Threshold,
		"severity":          string(r.Severity),
		"window_seconds":    r.WindowSeconds,
		"sustained_seconds": r.SustainedSeconds,
		"cooldown_seconds":  r.CooldownSeconds,
		"notify_email":      r.NotifyEmail,
		"filters":           copyStringMap(r.Filters),
		"enabled":           r.Enabled,
		"last_triggered_at": tsPtr(r.LastTriggeredAt),
		"created_at":        r.CreatedAt.UTC(),
		"updated_at":        r.UpdatedAt.UTC(),
	}
}
