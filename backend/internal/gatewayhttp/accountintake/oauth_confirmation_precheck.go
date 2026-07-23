package accountintake

import "github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"

// precheckOAuthConfirmations 在破坏性 Claim 之前用非破坏 Plan 校验必需确认项。
//
// 背景(bug 修复):OAuthService.Execute 过去先 Claim(消费并 wipe 掉 staged 加密密文)
// 再进内层 Execute 才校验确认项。缺确认时该项判 conflict、账号不写,但 staged token 已被
// 消费——重试即 ErrStagedCredentialReplay,逼用户重走整个浏览器 OAuth。此处在 Claim 前用
// 已 Plan 出的 RequiredConfirmations 预检,缺确认时返回与内层一致的 confirmation_required
// 冲突(blocked=true),不消费 staged token,使补齐确认后可直接重试成功。
//
// 只对 create/update 动作校验确认(与内层 Execute 一致);其它动作在真正 Execute 时产出结果。
func precheckOAuthConfirmations(plan intake.Plan, confirmations []string) (ExecutionResult, bool) {
	confirmed := confirmationSet(confirmations)
	out := ExecutionResult{Items: make([]ExecutionItem, 0, len(plan.Items))}
	blocked := false
	for _, item := range plan.Items {
		if item.Action != intake.ActionCreate && item.Action != intake.ActionUpdate {
			continue
		}
		missing := missingConfirmations(item.RequiredConfirmations, confirmed)
		if len(missing) == 0 {
			continue
		}
		blocked = true
		result := ExecutionItem{
			Index: item.Index, PlannedAction: item.Action,
			Status:  StatusConflict,
			Code:    "confirmation_required",
			Message: "缺少执行该项所需的明确确认",
		}
		result.Warnings = append(result.Warnings, missing...)
		addExecutionSummary(&out.Summary, result.Status)
		out.Items = append(out.Items, result)
	}
	return out, blocked
}
