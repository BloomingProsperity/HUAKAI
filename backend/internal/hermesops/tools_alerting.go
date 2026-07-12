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

// AlertEventListDeps 是 alert_event_list 工具的只读依赖:List 包装 alerting.PostgresStore.ListEvents
// (按 tenant_id SELECT-only)。nil 时工具按依赖检查 fail-closed。
type AlertEventListDeps struct {
	List func(ctx context.Context, in alerting.ListEventsInput) ([]alerting.AlertEvent, error)
}

// alertEventListLimit 是单次列出告警事件上限(防超大读)。
const alertEventListLimit = 200

// AlertEventListSpec 构建只读 alert_event_list 工具:列出本租户的告警事件(可按 state 过滤),补
// alert_rule_list(规则配置)缺的"实际触发了什么"。让 Hermes 能回答"现在有什么告警在响、最近触发过什么"。
// 租户 scope 取自已鉴权 req.TenantID(ListEvents 按 tenant_id 过滤)。
//
// 隐私 safe-by-construction(alertEventShape):AlertEvent 全是结构化(枚举/数值/时间戳/布尔)。Dimensions
// 与 AlertRule.Filters **同源**(触发时取 normalizeStringMap(rule.Filters),即运营自填的规则过滤标签),
// 非用户内容/密钥,以拷贝形式投出。投影排 TenantID。
//
// Args: { "state": <string, optional 按 firing/resolved/manual_resolved 过滤> }
func AlertEventListSpec(deps AlertEventListDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolAlertEventList,
		Category:     CategoryDiagnostic,
		Description:  "List the tenant's alert events (optionally filtered by state firing/resolved/manual_resolved): rule id, state, observed/threshold/metric values, dimensions, fired-at/resolved-at, email-sent. Complements alert_rule_list (config) with what actually fired. Lets you answer 'what alerts are firing now, what fired recently'. READ ONLY.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"state": "filter by event state (firing/resolved/manual_resolved, optional)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.List == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			// 未知/缺失 state → "":SQL 把 '' 当 no-filter;任意非法值只匹配不到行返回空(参数已绑定,不注入)。
			state, _ := ArgString(req.Args, "state")
			rows, err := deps.List(ctx, alerting.ListEventsInput{
				TenantID: req.TenantID,
				State:    alerting.EventState(state),
				Limit:    alertEventListLimit,
				Offset:   0,
			})
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]map[string]any, 0, len(rows))
			byState := map[string]int{}
			for _, e := range rows {
				byState[string(e.State)]++
				items = append(items, alertEventShape(e))
			}
			return ToolResult{Summary: map[string]any{
				"event_count": len(rows),
				"by_state":    intCountMap(byState),
				"items":       items,
			}}, nil
		},
	}
}

// alertEventShape 把一条 AlertEvent 投影成告警事件诊断字段。显式列举(safe-by-construction):只露结构化
// 枚举/数值/时间戳/布尔 + Dimensions(同 Filters 来源的规则标签,拷贝投出);有意不投 TenantID。
func alertEventShape(e alerting.AlertEvent) map[string]any {
	return map[string]any{
		"id":              e.ID,
		"rule_id":         e.RuleID,
		"state":           string(e.State),
		"observed_value":  e.ObservedValue,
		"threshold_value": floatPtrAny(e.ThresholdValue),
		"metric_value":    floatPtrAny(e.MetricValue),
		"dimensions":      copyStringMap(e.Dimensions),
		"fired_at":        e.FiredAt.UTC(),
		"resolved_at":     tsPtr(e.ResolvedAt),
		"email_sent":      e.EmailSent,
	}
}

// floatPtrAny 把可空 *float64 投成 float64 或 nil。
func floatPtrAny(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
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
