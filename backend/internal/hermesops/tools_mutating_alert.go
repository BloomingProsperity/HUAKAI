package hermesops

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

// 本文件声明 Phase B"扩可提议覆盖面"的首批新增 MUTATING 工具:alert_rule_enable /
// alert_rule_disable —— 启用/禁用本租户的一条告警规则(alert rule)。
//
// 它们包装已有的 alerting store 的规则 enabled 翻转,绝不重新实现告警逻辑。与 0146 的四个
// mutating 工具同构:设置 Mutating=true + RequiresConfirmation=true,提供 Resolve(只读的
// 目标解析 + dry-run 预览)与 Mutate(真正的翻转),而非 Run;5 层安全契约的编排(RBAC 底线、
// dry-run+confirm、原子审计、advisory lock、幂等)位于 orchestrator 层。
//
// 安全语义(本切片的核心不变量):
//   - Proposable=true —— enable/disable 是**可逆的 B 级**运营操作(翻 enabled 列,随时翻回),
//     所以 LLM 可在对话里提议它(对比 renew_trigger 凭证轮换 Proposable=false:不可逆 / A 级,
//     LLM 永不提议)。
//   - RequiresConfirmation=true —— 即便 LLM 提议了,真正执行前仍需 operator 一键确认;LLM 绝
//     不能直接执行。
//   - 租户 scope 在**两处**绑死(纵深防御):Resolve 里对解析出的 rule 复检
//     rule.TenantID==req.TenantID;Mutate 里 SetEnabledInTx 的 SQL WHERE tenant_id=$1 再次按
//     租户过滤。任一处都足以阻断跨租户改动,两处叠加杜绝单点失效。
//
// PRIVACY:Preview / Summary 只携带 enum / id / name(规则名,运营自取的规则标签)/ 状态名,
// 绝不携带用户 prompt / completion / 密钥。

// AlertRuleMutationDeps 把 alert_rule_enable/disable 工具接到已有的 alerting store 上。
// GetRule 是 Resolve 用来读取当前状态 + 校验租户归属的只读读取。SetEnabledInTx 在 orchestrator
// 事务内翻转 alert_rules.enabled(从而该翻转与审计行原子一致)。
type AlertRuleMutationDeps struct {
	GetRule        func(ctx context.Context, tenantID, id int64) (alerting.AlertRule, error)
	SetEnabledInTx func(ctx context.Context, tx pgx.Tx, tenantID, id int64, enabled bool) (alerting.AlertRule, error)
}

// AlertRuleEnableSpec 构建 alert_rule_enable 改动型工具:把一条告警规则置为 enabled=true
// (规则恢复参与评估)。Args: { "rule_id": <int64> }。
func AlertRuleEnableSpec(deps AlertRuleMutationDeps) ToolSpec {
	return alertRuleToggleSpec(ToolAlertRuleEnable, "Enable an alert rule so it resumes evaluating against metrics. MUTATING — dry-run + confirm required.", true, deps)
}

// AlertRuleDisableSpec 构建 alert_rule_disable 改动型工具:把一条告警规则置为 enabled=false
// (规则停止评估、不再触发)。Args: { "rule_id": <int64> }。
func AlertRuleDisableSpec(deps AlertRuleMutationDeps) ToolSpec {
	return alertRuleToggleSpec(ToolAlertRuleDisable, "Disable an alert rule so it stops evaluating and firing. MUTATING — dry-run + confirm required.", false, deps)
}

// alertRuleToggleSpec 是 enable/disable 共用的构建器 —— 它们只在目标 enabled 值上不同,因此
// resolve/mutate 逻辑是同一条路径(dry-run 预览不会与实际动作偏离,因为两者都从同一个预期的
// next-state targetEnabled 推导)。
func alertRuleToggleSpec(name, description string, targetEnabled bool, deps AlertRuleMutationDeps) ToolSpec {
	return ToolSpec{
		Name:                 name,
		Category:             CategoryMutating,
		Description:          description,
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		// 告警规则 enable/disable 是一个可逆的 B 级改动 → 可被 LLM 提议
		// (LLM 可以提议它;在它真正执行前仍需 operator 确认)。对比
		// renew_trigger(凭证轮换),其 Proposable 保持 false。
		Proposable: true,
		// enable/disable 有 scope 限制:platform_admin 或目标租户内的 tenant_operator
		// (H1 中间件 + Resolve 复检负责强制租户 scope;此底线放行 tenant_operator)。
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"rule_id": "alert rule id to toggle (int64, required)",
		},
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.GetRule == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			ruleID, err := ArgInt(req.Args, "rule_id")
			if err != nil {
				return MutationPlan{}, err
			}
			rule, err := deps.GetRule(ctx, req.TenantID, ruleID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: alert rule %d not found for tenant %d", ErrTargetResolution, ruleID, req.TenantID)
			}
			// 针对解析出的行复检租户归属(在 GetRule 的租户过滤之上再做纵深防御):
			// 一次改动绝不能触及解析出的租户之外的规则。
			if rule.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: alert rule tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "alert_rule",
				TargetID:   ruleID,
				LockKey:    fmt.Sprintf("hermes:alert_rule_toggle:%d:%d", req.TenantID, ruleID),
				Preview: map[string]any{
					"target_type":     "alert_rule",
					"rule_id":         ruleID,
					"rule_name":       rule.Name,
					"current_enabled": rule.Enabled,
					"next_enabled":    targetEnabled,
					"no_op":           rule.Enabled == targetEnabled,
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.SetEnabledInTx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tx := txFromContext(ctx)
			if tx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			// 在 orchestrator 事务内翻转 enabled,从而该状态变化与 tool_calls + admin_audit 行原子一致。
			// 租户 scope 在 SetEnabledInTx 的 SQL WHERE 里再次绑死(第二处)。
			if _, err := deps.SetEnabledInTx(ctx, tx, req.TenantID, plan.TargetID, targetEnabled); err != nil {
				return ToolResult{}, err
			}
			summary := map[string]any{
				"rule_id":          plan.TargetID,
				"previous_enabled": plan.Preview["current_enabled"],
				"enabled":          targetEnabled,
			}
			return ToolResult{Summary: summary}, nil
		},
	}
}
