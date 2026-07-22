package hermesops

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 改动型工具复用现有管理写路径，并强制经过目标解析、预览、人工确认、原子日志、
// 目标锁和幂等保护。预览与结果只携带枚举、计数、编号和状态，不返回凭证材料。

// ---------------------------------------------------------------------------
// account_pause / account_resume
// ---------------------------------------------------------------------------

// AccountMutationDeps 把账号暂停/恢复工具接到现有的账号启用状态读取。
// 真正写入通过编排器事务中的管理查询完成，使账号状态与日志同成同败。
type AccountMutationDeps struct {
	GetAccount func(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
}

// AccountPauseSpec 构建 account_pause 改动型工具:它通过与 admin handler 相同的路径禁用某个
// provider account(enabled=false),并附带 channelhealth 的手动 pause 协调。Args: { "account_id": <int64> }。
func AccountPauseSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountPause, "暂停一个上游账号，调度器将不再选择该账号；需要先预览并由管理员确认。", false, deps)
}

// AccountResumeSpec 构建 account_resume 改动型工具:它重新启用某个 provider account(enabled=true)
// + channelhealth 的手动 resume 协调。Args: { "account_id": <int64> }。
func AccountResumeSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountResume, "恢复一个上游账号，调度器可重新选择该账号；需要先预览并由管理员确认。", true, deps)
}

// accountToggleSpec 是 pause/resume 共用的构建器 —— 它们只在目标 enabled 值 + 审计动作上不同,
// 因此 resolve/mutate 逻辑是同一条路径(dry-run 预览不会与实际动作偏离,因为两者在这里
// 都从同一个预期的 next-state 推导)。
func accountToggleSpec(name, description string, targetEnabled bool, deps AccountMutationDeps) ToolSpec {
	return ToolSpec{
		Name:                 name,
		Category:             CategoryMutating,
		Description:          description,
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		// 账号启停可逆，模型可以生成提议，但执行仍要求管理员确认。
		Proposable: true,
		// 中间件限定管理员租户，Resolve 再复检目标行归属。
		RequiredRole: RoleTenantOperator,
		InputSchema: ObjectSchema(map[string]any{
			"account_id": PositiveIntegerSchema("要切换状态的上游账号 ID"),
		}, "account_id"),
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.GetAccount == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return MutationPlan{}, err
			}
			account, err := deps.GetAccount(ctx, admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: req.TenantID})
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: account %d not found for tenant %d", ErrTargetResolution, accountID, req.TenantID)
			}
			// 针对解析出的行复检租户归属(在 GetAccount 的租户过滤之上再做纵深防御):
			// 一次改动绝不能触及解析出的租户之外的行。
			if account.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: account tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "provider_account",
				TargetID:   accountID,
				LockKey:    fmt.Sprintf("hermes:account_toggle:%d:%d", req.TenantID, accountID),
				Preview: map[string]any{
					"target_type":     "provider_account",
					"account_id":      accountID,
					"current_enabled": account.Enabled,
					"next_enabled":    targetEnabled,
					"health_state":    account.HealthState,
					"no_op":           account.Enabled == targetEnabled,
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.GetAccount == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tx := txFromContext(ctx)
			if tx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID := plan.TargetID
			actorID := fmt.Sprintf("%s:%d", req.ActorSource, req.ActorID)
			// 在 orchestrator 事务内翻转 enabled,从而该状态变化与 tool_calls + admin_audit 行原子一致。
			if err := admindb.New(tx).UpdateProviderAccountEnabled(ctx, admindb.UpdateProviderAccountEnabledParams{
				Enabled: targetEnabled, ActorID: &actorID, ID: accountID, TenantID: req.TenantID,
			}); err != nil {
				return ToolResult{}, err
			}
			summary := map[string]any{
				"account_id":       accountID,
				"previous_enabled": plan.Preview["current_enabled"],
				"enabled":          targetEnabled,
			}
			return ToolResult{Summary: summary}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// txFromContext:orchestrator 通过 context 传递其 tx,使工具的 Mutate
// 可以执行绑定到 tx 的写入,而无需扩宽 Mutate 的签名。
// ---------------------------------------------------------------------------

type mutationTxKey struct{}

// withMutationTx 把 orchestrator 事务塞进 context,供 mutate 回调使用。仅由 orchestrator 使用。
func withMutationTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, mutationTxKey{}, tx)
}

// txFromContext 取回 orchestrator 事务。缺失时返回 nil
// (需要 tx 的改动型工具会以 ErrDependencyUnwired 做 fail-closed)。
func txFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(mutationTxKey{}).(pgx.Tx)
	return tx
}
