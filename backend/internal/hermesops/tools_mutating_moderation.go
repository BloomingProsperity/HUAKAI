package hermesops

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

// 本文件声明内容审核关键词启用与停用工具，用于切换本租户的一条关键词规则。
//
// 它们包装已有的 moderation_keywords.enabled 列翻转,绝不重新实现审核匹配逻辑。与 0160 的
// alert_rule_enable/disable 同构:设置 Mutating=true + RequiresConfirmation=true,提供 Resolve
// (只读的目标解析 + dry-run 预览)与 Mutate(真正的翻转),而非 Run;5 层安全契约的编排
// (RBAC 底线、dry-run+confirm、原子审计、advisory lock、幂等)位于 orchestrator 层。
//
// 安全语义(本切片的核心不变量):
//   - 这是**安全敏感**操作:disable 等于临时关掉一个内容过滤器(被该关键词拦的内容会放行),
//     但它是**可逆的 B 级**运营操作(翻 enabled 列,enable 随时翻回),所以 LLM 可在对话里提议它
//     (Proposable=true)。对比 renew_trigger 凭证轮换 Proposable=false:不可逆 / A 级,LLM 永不提议。
//   - RequiresConfirmation=true —— 即便 LLM 提议了,真正执行前仍需 operator 一键确认;LLM 绝不
//     能直接执行(关闭内容过滤器这种安全敏感动作必须有人类 operator 把关)。
//   - 租户 scope 在**三层**绑死(纵深防御):① Resolve 用 req.TenantID 调 GetKeyword(读侧按租户
//     过滤);② Resolve 里对解析出的 keyword 复检 kw.TenantID==req.TenantID;③ Mutate 里
//     SetEnabledInTx 的 SQL WHERE tenant_id=$1 再次按租户过滤。任一层都足以阻断跨租户改动。
//   - 只对未软删(deleted_at IS NULL)的关键词 toggle:GetKeyword 与 SetEnabledInTx 的 SQL 都带
//     deleted_at IS NULL,已删的关键词既预览不到(ErrTargetResolution)也翻不动(0 行)。
//
// PRIVACY:本切片刻意**不**在预览/摘要里露出 keyword 原文。moderation keyword 本质是 operator 的
// 内容拦截词(可能含敏感/攻击性词条),不同于 alert_rule 的 rule_name(纯运营标签);为稳妥起见,
// 预览只露 reason_code(分类码,枚举形态)+ keyword_length(长度计数)+ id + enabled 状态,让
// operator 凭 id+reason_code+长度仍能辨认要 toggle 的是哪条规则,而 LLM-提议预览这条链路上绝不
// 回显拦截词原文。这是相对 alert_rule(露 rule_name)更紧的取舍,理由记录在本切片报告里。

// ModerationKeywordMutationDeps 把 moderation_keyword_enable/disable 工具接到已有的
// moderation_keywords 存储上。GetKeyword 是 Resolve 用来读取当前状态 + 校验租户归属 + 渲染预览的
// 只读读取(返回需含 TenantID/Keyword/Enabled)。SetEnabledInTx 在 orchestrator 事务内翻转
// moderation_keywords.enabled(从而该翻转与审计行原子一致)。
type ModerationKeywordMutationDeps struct {
	GetKeyword     func(ctx context.Context, tenantID, id int64) (moderation.KeywordRule, error)
	SetEnabledInTx func(ctx context.Context, tx pgx.Tx, tenantID, id int64, enabled bool) error
}

// ModerationKeywordEnableSpec 构建 moderation_keyword_enable 改动型工具:把一条审核关键词置为
// enabled=true(规则恢复参与内容拦截)。Args: { "keyword_id": <int64> }。
func ModerationKeywordEnableSpec(deps ModerationKeywordMutationDeps) ToolSpec {
	return moderationKeywordToggleSpec(ToolModerationKeywordEnable, "Enable a content-moderation keyword rule so it resumes blocking matching content. MUTATING — dry-run + confirm required.", true, deps)
}

// ModerationKeywordDisableSpec 构建 moderation_keyword_disable 改动型工具:把一条审核关键词置为
// enabled=false(规则停止拦截,被它拦的内容会放行)。Args: { "keyword_id": <int64> }。
func ModerationKeywordDisableSpec(deps ModerationKeywordMutationDeps) ToolSpec {
	return moderationKeywordToggleSpec(ToolModerationKeywordDisable, "Disable a content-moderation keyword rule so it stops blocking matching content. MUTATING — dry-run + confirm required.", false, deps)
}

// moderationKeywordToggleSpec 是 enable/disable 共用的构建器 —— 它们只在目标 enabled 值上不同,
// 因此 resolve/mutate 逻辑是同一条路径(dry-run 预览不会与实际动作偏离,因为两者都从同一个预期的
// next-state targetEnabled 推导)。
func moderationKeywordToggleSpec(name, description string, targetEnabled bool, deps ModerationKeywordMutationDeps) ToolSpec {
	return ToolSpec{
		Name:                 name,
		Category:             CategoryMutating,
		Description:          description,
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		// 审核关键词 enable/disable 是一个可逆的 B 级改动(翻 enabled 列,随时翻回)→ 可被 LLM
		// 提议(LLM 可以提议它;在它真正执行前仍需 operator 确认)。对比 renew_trigger(凭证轮换),
		// 其 Proposable 保持 false:不可逆 / A 级,LLM 永不提议。
		Proposable: true,
		// enable/disable 有 scope 限制:platform_admin 或目标租户内的 tenant_operator
		// 身份授权和 Resolve 目标复检共同强制租户作用域；此处允许租户运营者进入解析阶段。
		RequiredRole: RoleTenantOperator,
		InputSchema: ObjectSchema(map[string]any{
			"keyword_id": PositiveIntegerSchema("要切换状态的内容审核关键词规则 ID"),
		}, "keyword_id"),
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.GetKeyword == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			keywordID, err := ArgInt(req.Args, "keyword_id")
			if err != nil {
				return MutationPlan{}, err
			}
			kw, err := deps.GetKeyword(ctx, req.TenantID, keywordID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: moderation keyword %d not found for tenant %d", ErrTargetResolution, keywordID, req.TenantID)
			}
			// 针对解析出的行复检租户归属(在 GetKeyword 的租户过滤之上再做纵深防御):
			// 一次改动绝不能触及解析出的租户之外的关键词。
			if kw.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: moderation keyword tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "moderation_keyword",
				TargetID:   keywordID,
				LockKey:    fmt.Sprintf("hermes:moderation_keyword_toggle:%d:%d", req.TenantID, keywordID),
				Preview: map[string]any{
					"target_type": "moderation_keyword",
					"keyword_id":  keywordID,
					// PRIVACY:只露 reason_code(枚举分类)+ keyword_length(计数),绝不回显拦截词
					// 原文。operator 凭 id+reason_code+长度即可辨认目标规则。
					"reason_code":     kw.ReasonCode,
					"keyword_length":  len(kw.Keyword),
					"current_enabled": kw.Enabled,
					"next_enabled":    targetEnabled,
					"no_op":           kw.Enabled == targetEnabled,
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
			// 租户 scope 在 SetEnabledInTx 的 SQL WHERE 里再次绑死(第三处)。
			if err := deps.SetEnabledInTx(ctx, tx, req.TenantID, plan.TargetID, targetEnabled); err != nil {
				return ToolResult{}, err
			}
			summary := map[string]any{
				"keyword_id":       plan.TargetID,
				"previous_enabled": plan.Preview["current_enabled"],
				"enabled":          targetEnabled,
			}
			return ToolResult{Summary: summary}, nil
		},
	}
}
