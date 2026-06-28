package hermeshttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// errMutateRateLimited 用于标记当 S2 (c) 的 per-operator-token 限流器拒绝一次已确认
// 变更时,尽力写入的那条账本行。它是一个 handler 本地的分类哨兵(限流器位于 handler
// 内),并非变更结果——什么都还没开始,因此它必须读作 "rate_limited",绝不是
// "mutation_failed"。
var errMutateRateLimited = errors.New("hermeshttp: operator mutation rate limit exceeded")

// mutateRateKey 用 operator 的 admin token id 构造限流器 key,使配额按 operator
// token 划分(而非按 tenant):某个 operator 的突发流量无法限流到同一 tenant 内另一个
// operator。
func mutateRateKey(tokenID int64) string {
	return "tok:" + strconv.FormatInt(tokenID, 10)
}

// retryAfterSeconds 为被限流的变更渲染一个粗粒度(向上取整到秒、最小 1)的
// Retry-After 值,与 loginthrottle 限流器的粗粒度提示一致——不泄露精确的剩余配额。
func retryAfterSeconds(d time.Duration) string {
	secs := int64((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}

// executeMutatingTool 为一个 mutating 工具运行 WAVE H4 的 5 层安全流程:
//
//	L1 RBAC      — AuthorizeMutating 检查 role 下限(dlq_replay 仅限
//	               platform_admin;其余允许 tenant_operator)。拒绝时记录一条
//	               denied 行 + 403。tenant scope 已由 H1 中间件执行;Resolve 会再次
//	               核对目标行。
//	L2 dry-run + confirm — confirm=false 经由「与执行相同」的 Resolve 做一次只读
//	               preview,并返回一个一次性的 correlation_id;confirm=true 且
//	               correlation_id 有效时恰好执行一次该变更。陈旧/缺失/不匹配的
//	               correlation_id 即 400 且绝不执行。
//	L3 atomic audit — orchestrator 把变更 + tool_calls 行 + admin_audit_events 行
//	               一并提交(提交前先验证审计)。
//	L4 advisory lock — orchestrator 通过一个以 plan 为 key 的 pg advisory xact lock
//	               串行化对同一目标的并发变更。
//	L5 idempotency — correlation_id 一次性(执行时消费);dlq_replay 还会额外按记录的
//	               idempotency key 去重。
func (h handler) executeMutatingTool(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest) {
	// L1:role 下限 + mutating 工具的合法性检查。AuthorizeMutating 会拒绝只读工具
	// 以及权限不足的 role。
	spec, authErr := h.tools.AuthorizeMutating(req.ToolName, actor.Role)
	if authErr != nil {
		switch {
		case errors.Is(authErr, hermesops.ErrToolUnknown):
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusNotFound, "hermes_tool_unknown", "unknown tool")
		case errors.Is(authErr, hermesops.ErrNotMutating):
			// 不应发生(调用方已检查 Mutating),但 fail-closed。
			writeError(w, http.StatusBadRequest, "hermes_tool_not_mutating", "tool is not a mutating tool")
		case errors.Is(authErr, hermesops.ErrDependencyUnwired):
			writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "mutating tool is not fully wired")
		default:
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusForbidden, "hermes_tool_forbidden", "operator role may not run this mutating tool")
		}
		return
	}

	toolReq := hermesops.ToolRequest{
		TenantID:    ident.TenantID,
		ActorUserID: ident.UserID,
		Role:        actor.Role,
		Args:        req.Args,
	}

	// L2 + 目标解析。Resolve 是只读的,并由 preview 与执行共用,因此 preview 不会
	// 与实际动作产生偏差。
	plan, resolveErr := spec.Resolve(r.Context(), toolReq)
	if resolveErr != nil {
		h.recordMutatingError(r, ident, actor, req, resolveErr, req.Confirm)
		writeMutatingResolveError(w, resolveErr)
		return
	}

	if !req.Confirm {
		h.previewMutation(w, r, ident, actor, req, spec, plan)
		return
	}
	h.confirmMutation(w, r, ident, actor, req, spec, plan, toolReq)
}

// previewMutation 处理 confirm=false:它记录一条 dry-run 的 tool-call 行
// (status ok、dry_run=true),签发一个绑定到具体 tool/tenant/actor/target 的一次性
// correlation_id,并返回「将会改动什么」的 preview。它不做任何变更。
func (h handler) previewMutation(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, spec hermesops.ToolSpec, plan hermesops.MutationPlan) {
	if h.confirmCache == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "confirmation cache unavailable")
		return
	}
	correlationID, err := h.confirmCache.Issue(hermesconfirm.PendingConfirmation{
		ToolName: spec.Name,
		TenantID: ident.TenantID,
		ActorID:  ident.UserID,
		TokenID:  actor.TokenID,
		TargetID: plan.TargetID,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "could not issue correlation id")
		return
	}
	// 把 dry-run 记录到权威账本(尽力而为:preview 未做变更,因此账本的瞬时错误不应
	// 阻止 operator 看到 preview)。dry_run=true 将其与真实执行区分开。
	h.recordMutatingDryRun(r, ident, actor, req, plan)

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":             spec.Name,
		"mutating":              true,
		"dry_run":               true,
		"requires_confirmation": true,
		"correlation_id":        correlationID,
		"expires_in_seconds":    int(hermesconfirm.ConfirmTTL.Seconds()),
		"preview":               plan.Preview,
	})
}

// confirmMutation 处理 confirm=true:它消费该 correlation_id(一次性),在有效匹配时
// 经由 orchestrator 运行变更(L3 atomic audit + L4 advisory lock)。陈旧/缺失/不匹配的
// correlation_id 即 400 且绝不执行。
func (h handler) confirmMutation(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, spec hermesops.ToolSpec, plan hermesops.MutationPlan, toolReq hermesops.ToolRequest) {
	if h.mutator == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "mutation orchestrator unavailable")
		return
	}
	if h.confirmCache == nil || req.CorrelationID == "" {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_required", "confirm=true requires a correlation_id from a prior dry-run")
		return
	}
	// L2/L5:一次性消费。被重复使用 / 陈旧 / 错误工具 / 错误 tenant / 错误 actor-user /
	// 错误 operator-token 的 correlation_id 都会查不到任何记录而被拒绝——不做变更。
	// actor.TokenID 把本次 confirm 绑定到「签发该 preview 的同一个 operator admin
	// token」(而不仅是 tenant-user 上下文)。
	entry, ok := h.confirmCache.Consume(req.CorrelationID, spec.Name, ident.TenantID, ident.UserID, actor.TokenID)
	if !ok {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_invalid", "correlation_id is stale, unknown, or does not match this tool")
		return
	}
	// 纵深防御:被确认的目标必须正是 preview 所锚定的那个。对固定目标而言 Resolve 是
	// 确定性的,因此新解析出的 plan 的 TargetID 必须与锚定值相等(只有当 args 指向了与
	// preview 不同的目标时才会不同——此时应拒绝,而不是去改错误的行)。
	if entry.TargetID != plan.TargetID {
		writeError(w, http.StatusBadRequest, "hermes_tool_confirmation_invalid", "confirmation target does not match the previewed target")
		return
	}

	// S2 (c) PER-OPERATOR-TOKEN 限流:在一次性消费之后检查(使被拒绝的陈旧 id 永不
	// 消耗配额),且在构造 rec / 调用 Execute 之前检查(使只有真正已确认的执行才计数
	// ——preview 和 denial 到不了这里)。以 operator 的 admin token 为 key,使单个
	// operator 无法独占整个 mutating 配额。nil/禁用的限流器始终放行(旧行为)。
	if h.mutateRateLimiter != nil {
		if ok, retryAfter := h.mutateRateLimiter.Allow(mutateRateKey(actor.TokenID)); !ok {
			// 把限流记录到权威账本使其可审计,然后返回 429 并附粗粒度 Retry-After。
			// correlation_id 已被消费(一次性),因此 operator 必须重新 preview 才能
			// 重试——这是有意为之的设计。
			h.recordMutatingError(r, ident, actor, req, errMutateRateLimited, false)
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			writeError(w, http.StatusTooManyRequests, "hermes_tool_mutate_rate_limited", "operator mutation rate limit exceeded; retry later")
			return
		}
	}

	now := time.Now().UTC()
	rec := hermesops.MutationAuditRecord{
		TenantID:          ident.TenantID,
		ActorUserID:       ident.UserID,
		AdminActorTokenID: actor.TokenID,
		ToolName:          spec.Name,
		Args:              req.Args,
		Status:            hermesops.ResultOK,
		CorrelationID:     correlationID(r),
		RequestID:         requestID(r),
		CalledAt:          now,
		ReturnedAt:        now,
		DryRun:            false,
		AdminAction:       mutatingAuditAction(spec.Name),
		AdminRole:         actor.Role,
		TargetType:        plan.TargetType,
		TargetID:          plan.TargetID,
		AuditPayload:      mutationAuditPayload(spec.Name, plan, req.Args),
		// OwnTx 让 orchestrator 能区分两类提交阶段故障:一类是把已经持久化的(own-tx)
		// 变更遗留下来,另一类是把(in-tx)变更回滚掉,从而把尽力而为的 error_class
		// 分别定为 commit_uncertain 与 mutation_failed。
		OwnTx: hermesops.IsOwnTxMutation(spec.Name),
	}

	lockKey := plan.LockKey
	if lockKey == "" {
		lockKey = fmt.Sprintf("hermes:mutate:%d:%s:%d", ident.TenantID, spec.Name, plan.TargetID)
	}

	result, execErr := h.mutator.Execute(r.Context(), lockKey, rec, func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error) {
		// 变更在 orchestrator 的事务内运行(审计行已被接受)。orchestrator 通过 ctx
		// 把 tx 传递给那些绑定事务的工具。
		return spec.Mutate(ctx, toolReq, plan)
	})
	if execErr != nil {
		// orchestrator 已回滚:无变更、无审计行。在独立账本上尽力记录一条 error 行,
		// 使得即便原子路径已中止,失败的尝试仍可见。
		h.recordMutatingError(r, ident, actor, req, execErr, false)
		writeMutatingExecError(w, execErr)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":   spec.Name,
		"mutating":    true,
		"dry_run":     false,
		"result":      result.Summary,
		"error_class": emptyStringToNil(result.ErrorClass),
		"target_type": plan.TargetType,
		"target_id":   plan.TargetID,
	})
}

// mutatingAuditAction 把一个 mutating 工具名映射到它的 admin_audit_events 动作
// (hermes.tool.<name>)。每个 mutating 工具都必须在此映射出审计 action;未知名返回 ""——会被
// orchestrator 的 admin_audit_events.action CHECK 拒绝(fail-closed:漏映射的工具其 confirm 执行
// 会因空 action 违反 CHECK 而整事务回滚,绝不会无审计地落库)。新增 mutating 工具时必须同步补这里 +
// 迁移把 hermes.tool.<name> 加进 action CHECK,二者缺一即该工具 confirm 必然失败。
func mutatingAuditAction(toolName string) string {
	switch toolName {
	case hermesops.ToolDLQReplay:
		return "hermes.tool.dlq_replay"
	case hermesops.ToolAccountPause:
		return "hermes.tool.account_pause"
	case hermesops.ToolAccountResume:
		return "hermes.tool.account_resume"
	case hermesops.ToolRenewTrigger:
		return "hermes.tool.renew_trigger"
	case hermesops.ToolAlertRuleEnable:
		return "hermes.tool.alert_rule_enable"
	case hermesops.ToolAlertRuleDisable:
		return "hermes.tool.alert_rule_disable"
	case hermesops.ToolModerationKeywordEnable:
		return "hermes.tool.moderation_keyword_enable"
	case hermesops.ToolModerationKeywordDisable:
		return "hermes.tool.moderation_keyword_disable"
	default:
		return ""
	}
}

// mutationAuditPayload 为 admin_audit_events 行构造脱敏后的 previous->next 状态
// payload。它使用 plan 的 Preview(枚举/id/状态名)+ admin actor 归因。原始 args
// 不会被折入此处(tool_calls 行已记录脱敏后的 args);因此 renew_trigger 的凭证
// payload 永远不会进入 admin 审计 payload。
func mutationAuditPayload(toolName string, plan hermesops.MutationPlan, _ map[string]any) map[string]any {
	payload := map[string]any{
		"tool_name":   toolName,
		"target_type": plan.TargetType,
		"target_id":   plan.TargetID,
	}
	for k, v := range plan.Preview {
		payload[k] = v
	}
	return payload
}

// recordMutatingDryRun 在独立账本上把 dry-run preview 记录为一条 hermes_tool_calls
// 行(status ok、dry_run=true)。尽力而为:preview 未改动状态,因此账本的瞬时错误只
// 记日志、不向外暴露。
func (h handler) recordMutatingDryRun(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, plan hermesops.MutationPlan) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultOK, "", true, plan.Preview)
}

// recordMutatingDenied 在独立账本上记录一次 L1 拒绝(status denied),使被拒绝的
// mutating 工具尝试可审计。
func (h handler) recordMutatingDenied(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultDenied, "", false, nil)
}

// recordMutatingError 在独立账本上记录一次失败的 mutating 工具尝试(resolve 失败或
// 执行中止)(status error)。
func (h handler) recordMutatingError(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, err error, dryRun bool) {
	h.recordMutatingRow(r, ident, actor, req, hermesops.ResultError, classifyMutatingError(err), dryRun, nil)
}

// recordMutatingRow 为一条不经过 atomic orchestrator 的路径(denial / dry-run /
// 变更前错误)追加一条独立的 hermes_tool_calls 行。尽力而为(失败时记日志);对已提交
// 的变更而言,原子路径自身写的那条行才是权威记录。
func (h handler) recordMutatingRow(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, status hermesops.ResultStatus, errorClass string, dryRun bool, summary map[string]any) {
	if h.toolCalls == nil {
		return
	}
	now := time.Now().UTC()
	rec := hermesops.ToolCallAudit{
		TenantID:          ident.TenantID,
		ActorUserID:       ident.UserID,
		AdminActorTokenID: actor.TokenID,
		ToolName:          req.ToolName,
		Args:              req.Args,
		ResultSummary:     summary,
		Status:            status,
		ErrorClass:        errorClass,
		CorrelationID:     correlationID(r),
		RequestID:         requestID(r),
		CalledAt:          now,
		ReturnedAt:        now,
		DryRun:            dryRun,
	}
	if err := hermesops.RecordToolCall(r.Context(), h.toolCalls, rec); err != nil {
		logToolCallWriteFailure(req.ToolName, err)
	}
}

func classifyMutatingError(err error) string {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, hermesops.ErrTargetResolution):
		return "target_resolution_failed"
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		return "dependency_unwired"
	case errors.Is(err, errMutateRateLimited):
		// S2 (c):per-operator-token 限流器拒绝了本次 confirm——什么都还没开始,
		// 因此这是背压(back-pressure),绝不是失败/不确定的变更。
		return "rate_limited"
	case errors.Is(err, hermesops.ErrMutateBusy):
		// S2 (a):并发上限已饱和——什么都还没开始。
		return "mutate_busy"
	case errors.Is(err, hermesops.ErrMutateTimeoutUncertain):
		// S2 (b) OWN-TX deadline:内层 own-tx 可能在 tx deadline 触发之前已提交。
		// 在 commit_uncertain 和 mutation_failed 默认分支之前检查,使超时的 own-tx
		// 变更绝不会被误报为已回滚。单独一类,使对账能区分 deadline 不确定残留与
		// commit 不确定残留。
		return "mutate_timeout_uncertain"
	case errors.Is(err, hermesops.ErrCommitAfterOwnTxMutation):
		// own-tx 工具的变更已持久化,但携带审计行的 orchestrator 提交失败:结果
		// 不确定,而非干净的失败。在 mutation_failed 默认分支之前检查,使这种提交阶段
		// 残留绝不会被误标为「变更未发生」。
		return "commit_uncertain"
	case errors.Is(err, context.DeadlineExceeded):
		// S2 (b) IN-TX deadline:deadline 已原子地回滚该变更,因此这是一次干净的超时
		// (与 own-tx 不确定的情形是不同的类别)。
		return "mutate_timeout"
	default:
		return "mutation_failed"
	}
}

func writeMutatingResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid or missing tool arguments")
	case errors.Is(err, hermesops.ErrTargetResolution):
		writeError(w, http.StatusNotFound, "hermes_tool_target_not_found", "mutation target not found for this tenant")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "could not resolve mutation target")
	}
}

func writeMutatingExecError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermes.ErrInvalidInput), errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid mutation request")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	case errors.Is(err, hermesops.ErrMutateBusy):
		// S2 (a):并发上限已饱和,且有界的获取窗口已过——什么都还没开始。纯背压:
		// 429,稍后重试。
		writeError(w, http.StatusTooManyRequests, "hermes_tool_mutate_busy", "too many concurrent mutations in flight; retry shortly")
	case errors.Is(err, hermesops.ErrMutateTimeoutUncertain):
		// S2 (b) OWN-TX deadline:内层 own-tx 可能在 tx deadline 触发之前已提交。
		// 与 commit_uncertain 一样,不要断言回滚——结果不确定,需要对账。
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_mutate_timeout_uncertain", "mutation may have applied but the tx deadline was hit; reconciliation required")
	case errors.Is(err, hermesops.ErrCommitAfterOwnTxMutation):
		// 变更可能已持久化(own-tx),而审计提交失败:不要断言它已被回滚——而应
		// 给出「结果不确定」的信号。
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_commit_uncertain", "mutation may have applied but the audit commit failed; reconciliation required")
	case errors.Is(err, context.DeadlineExceeded):
		// S2 (b) IN-TX deadline:变更已原子地回滚——这是一次干净的网关超时(504),
		// 而非不确定的结果。
		writeError(w, http.StatusGatewayTimeout, "hermes_tool_mutate_timeout", "mutation exceeded its time budget and was rolled back")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "mutation failed and was rolled back")
	}
}
