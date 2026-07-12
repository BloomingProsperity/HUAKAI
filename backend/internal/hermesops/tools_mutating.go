package hermesops

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 本文件声明 WAVE H4 的 MUTATING 工具 spec(即"修复"能力)。
// 每个工具都包装一个已有的网关改动函数 —— 绝不重新实现该改动。改动型工具设置
// Mutating=true + RequiresConfirmation=true,并提供 Resolve(只读的目标解析 + dry-run 预览)
// 和 Mutate(真正的改动),而不是 Run。5 层安全契约的编排(RBAC 底线、dry-run+confirm、
// 原子审计、advisory lock、幂等)位于 HTTP/orchestrator 层;这些 spec 只负责提供
// 读取(Resolve)和被包装的改动(Mutate)。
//
// PRIVACY:每一份 Preview / ToolResult.Summary 都只携带 enum / 计数 / ids / 状态名。
// 轮换后的凭证材料绝不返回给调用方 —— renew_trigger 只露出轮换后的版本号 + state。

// ---------------------------------------------------------------------------
// account_pause / account_resume
// ---------------------------------------------------------------------------

// AccountMutationDeps 把 account pause/resume 工具接到已有的 provider-account enabled 改动
// + channelhealth 手动覆盖协调上。GetAccount 是 Resolve 用来读取当前状态 + 校验租户归属的读取。
// SetEnabledTx 在 orchestrator 事务内翻转 provider_accounts.enabled(从而该翻转与审计行原子一致)。
// Coordinate(可选)在 enable/disable 提交之后执行 channelhealth 的手动 pause/resume —— 它是
// 派生缓存的协调,而非真实来源(source of truth),所以瞬时协调错误会被上报但不会回滚已提交的
// enable/disable(这与已有 admin handler 把 enabled 与 channel-health 当作两个独立权威的处理方式一致)。
type AccountMutationDeps struct {
	GetAccount func(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	// Coordinate 在 enabled 翻转提交后,为该 account 执行 channelhealth 的 ManualPause/ManualResume。
	// 可选(nil => 跳过)。它会拿到解析后的 account 行 + actor,以便构建 channel key。
	Coordinate func(ctx context.Context, account admindb.AdminProviderAccountRow, pause bool, actorID, reason string) error
}

// AccountPauseSpec 构建 account_pause 改动型工具:它通过与 admin handler 相同的路径禁用某个
// provider account(enabled=false),并附带 channelhealth 的手动 pause 协调。Args: { "account_id": <int64> }。
func AccountPauseSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountPause, "Pause (disable) a provider account so the dispatcher stops selecting it; coordinates channel-health manual pause. MUTATING — dry-run + confirm required.", false, deps)
}

// AccountResumeSpec 构建 account_resume 改动型工具:它重新启用某个 provider account(enabled=true)
// + channelhealth 的手动 resume 协调。Args: { "account_id": <int64> }。
func AccountResumeSpec(deps AccountMutationDeps) ToolSpec {
	return accountToggleSpec(ToolAccountResume, "Resume (re-enable) a provider account so the dispatcher can select it again; coordinates channel-health manual resume. MUTATING — dry-run + confirm required.", true, deps)
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
		// account enable/disable 是一个可逆的 B 级改动 → 可被 LLM 提议
		// (LLM 可以提议它;在它真正执行前仍需 operator 确认)。对比
		// renew_trigger(凭证轮换),其 Proposable 保持 false:operator
		// 可以直接驱动它,但 LLM 永远不提议它。
		Proposable: true,
		// pause/resume 有 scope 限制:platform_admin 或目标租户内的 tenant_operator
		// (H1 中间件 + Resolve 复检负责强制租户 scope;此底线放行 tenant_operator)。
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"account_id": "provider account id to toggle (int64, required)",
		},
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
			actorID := fmt.Sprintf("%d", req.ActorUserID)
			// 在 orchestrator 事务内翻转 enabled,从而该状态变化与 tool_calls + admin_audit 行原子一致。
			if err := admindb.New(tx).UpdateProviderAccountEnabled(ctx, admindb.UpdateProviderAccountEnabledParams{
				Enabled: targetEnabled, ActorID: &actorID, ID: accountID, TenantID: req.TenantID,
			}); err != nil {
				return ToolResult{}, err
			}
			// channelhealth 协调在 enabled 翻转已暂存进 tx 之后运行;它使用自己的 store/tx。
			// 瞬时协调失败会在 summary 中暴露,但不会中止已提交的翻转 ——
			// enabled 列才是 dispatcher 的真实来源(source of truth)。
			coordinated := false
			coordErr := ""
			if deps.Coordinate != nil {
				account, err := deps.GetAccount(ctx, admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: req.TenantID})
				if err == nil {
					if cErr := deps.Coordinate(ctx, account, !targetEnabled, actorID, "hermes ops "+req.Role); cErr != nil {
						coordErr = "channel_health_coordination_failed"
					} else {
						coordinated = true
					}
				} else {
					coordErr = "channel_health_lookup_failed"
				}
			}
			summary := map[string]any{
				"account_id":       accountID,
				"previous_enabled": plan.Preview["current_enabled"],
				"enabled":          targetEnabled,
				"coordinated":      coordinated,
			}
			return ToolResult{Summary: summary, ErrorClass: coordErr}, nil
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
