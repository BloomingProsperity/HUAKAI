package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// proposeMode 是 internalToolRequest.Mode 选择 Phase B LLM-提议路径的取值(LLM 在对话里提议的
// mutating 工具会被 DRY-RUN 解析、返回 needs_confirmation,绝不在此执行)。其它任何 mode 取值
//("" / "execute")走旧的只读 dispatch 路径,故不带 mode 的旧 runner 行为不变。
const proposeMode = "propose"

// proposeStatusNeedsConfirmation 是提议路径返回给 runner 的状态:提议已解析(dry-run),现等待
// 一个 OPERATOR 确认。LLM 把 correlation_id 转达给 operator,但自己无法确认。
const proposeStatusNeedsConfirmation = "needs_confirmation"

// internal_tool_handler.go is the gateway-side authority for the conversational
// READ-ONLY tool loop (WAVE H3b, Option B). The Python runner's ops assistant
// calls back into the gateway mid-conversation to run a diagnostic tool; this
// handler is the ONLY place that authorizes + executes + audits that call.
//
// SAFETY CONTRACT (every item is enforced here, fail-closed):
//  1. Authentication: the caller MUST present a valid internal_token (the same
//     HMAC tenant|user|request_id|exp the bridge minted for this session). An
//     invalid/expired token => 401, no tool runs.
//  2. Session binding: the token's request_id MUST resolve to a bound operator
//     identity (role + tenant scope + admin_token_id). No binding => 401.
//  3. Identity consistency: the token's tenant/user MUST match the bound
//     session's tenant/user. A mismatch => 401 (a token for session A can never
//     drive session B's operator scope).
//  4. READ-ONLY filter (the structural mutation guard): the requested tool MUST
//     be registered AND non-mutating. A mutating tool name (account_pause,
//     dlq_replay, renew_trigger, ...) is REJECTED before dispatch — and even if
//     this check were removed, hermesops.Registry.Run itself refuses a mutating
//     tool with ErrNotMutating. Two independent gates; a mutation is unreachable.
//  5. RBAC role floor: Registry.Run authorizes the bound operator's role against
//     the tool's RequiredRole. Below floor => denied (recorded).
//  6. Tenant scope: the tool ALWAYS runs with the bound session's TenantID. The
//     runner cannot name a tenant; there is no tenant arg. A tool can never read
//     another tenant's data through this path.
//  7. Audit: every call (ok / error / denied / rejected) records a sanitized
//     hermes_tool_calls row with the operator attribution (admin_actor_token_id).
//  8. Privacy: only the tool's sanitized Summary (enums/counts/ids) crosses back
//     to the runner; the store re-sanitizes args + summary as defense in depth.

// ReadOnlyToolRunner is the narrow read-only dispatch surface this handler needs
// from the tool registry. *hermesops.Registry satisfies it. It deliberately does
// NOT expose AuthorizeMutating / Resolve / Mutate — the mutating capability is
// structurally absent from this handler's dependency, so there is no method here
// that could run a mutation even if mis-called.
type ReadOnlyToolRunner interface {
	// Get reports a registered spec (used only to classify a name as mutating for
	// the explicit pre-dispatch rejection + audit). It never executes anything.
	Get(name string) (hermesops.ToolSpec, bool)
	// Run authorizes (role floor) + dispatches a READ-ONLY tool. It refuses a
	// mutating tool with ErrNotMutating, so it is itself a mutation guard.
	Run(ctx context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error)
	// ReadOnlyCatalog returns the LLM-facing read-only tool catalog.
	ReadOnlyCatalog() []hermesops.CatalogTool
}

// ProposalResolver 是 Phase B 提议分支所需的窄 DRY-RUN 面。它只暴露 ResolveProposal——一个
// 返回 MutationPlan、绝不返回 Mutate 句柄的只读解析。*hermesops.Registry 满足它。
//
// 与 ReadOnlyToolRunner 一样,它刻意不含 Mutate / Resolve / AuthorizeMutating:handler 没有任何
// 能执行 state change 的方法,所以 LLM-提议路径是结构性只读的——它至多产出一个 dry-run 预览 +
// 一个单次 correlation_id,由一条独立的、operator 认证的路径(H1 确认端点)随后消费来执行真正的
// mutation。守门的是这里"没有 Mutate 句柄"这一结构事实,而非 handler 可跳过的运行时检查。
type ProposalResolver interface {
	// ResolveProposal 对 LLM 提议的某个 MUTATING 工具做授权(角色下限)+ DRY-RUN 解析,只返回
	// 只读的 MutationPlan。它 fail-closed 地拒绝:只读工具(ErrNotMutating)、未标记 Proposable 的
	// mutating 工具(ErrNotProposable)、以及角色不足(ErrToolForbidden)。它从不执行任何东西。
	ResolveProposal(ctx context.Context, name, actorRole string, req hermesops.ToolRequest) (hermesops.MutationPlan, error)
}

// InternalToolHandler serves the runner's mid-conversation tool calls.
type InternalToolHandler struct {
	secret    []byte
	bindings  *SessionBindings
	tools     ReadOnlyToolRunner
	toolCalls hermesops.ToolCallInserter
	now       func() time.Time
	// toolLoopEnabled is KNOB B's runner-callback gate. When false, ServeHTTP
	// refuses every call (403 llm_toolloop_disabled) before resolving the operator,
	// so the LLM conversational tool loop is fully off even if a session were bound.
	// The bridge-side gate (no catalog injection) is the cooperating half; this is
	// the enforcing half.
	toolLoopEnabled bool
	// proposer 是 Phase B 的 DRY-RUN 解析面(仅 ResolveProposal)。提议路径未接线时它为 nil,此时
	// 一个 propose 调用 fail-closed(503)。它刻意不暴露任何 Mutate 句柄——见 ProposalResolver。
	proposer ProposalResolver
	// confirmCache 是共享的单次 correlation-id store。提议路径在此 Issue 一个 correlation_id;
	// operator H1 确认路径从同一实例 Consume 它,故 operator 确认的正是 LLM 所提的那条提议。
	// nil => propose fail-closed(503)。
	confirmCache *hermesconfirm.Cache
	// proposeEnabled 是 Phase B 提议 KNOB。默认关:mode=propose 调用在任何解析之前即被拒
	//(403 llm_propose_disabled),故接入提议路径在 Owner 翻开它之前是零生产行为变。与
	// toolLoopEnabled 正交(且额外受其门控)。
	proposeEnabled bool
}

// NewInternalToolHandler wires the handler. secret is the internal-token HMAC
// secret (same as the bridge's). A nil registry / inserter / bindings makes the
// handler fail closed (503 / 401) rather than panic. toolLoopEnabled is KNOB B:
// when false the handler refuses every call (403) before touching the token.
//
// 末尾三个参数接入 Phase B LLM-提议路径:proposer 是 DRY-RUN 解析面(nil => propose fail-closed
// 503),confirmCache 是 operator 确认路径也读取的共享 correlation-id store(nil => 503),
// proposeEnabled 是提议 KNOB(调用点默认关 => 零行为变)。proposer/cache 为 nil 或
// proposeEnabled=false 时,handler 完全保持其既有只读行为不变。
func NewInternalToolHandler(secret []byte, bindings *SessionBindings, tools ReadOnlyToolRunner, toolCalls hermesops.ToolCallInserter, now func() time.Time, toolLoopEnabled bool, proposer ProposalResolver, confirmCache *hermesconfirm.Cache, proposeEnabled bool) *InternalToolHandler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &InternalToolHandler{
		secret:          append([]byte(nil), secret...),
		bindings:        bindings,
		tools:           tools,
		toolCalls:       toolCalls,
		now:             now,
		toolLoopEnabled: toolLoopEnabled,
		proposer:        proposer,
		confirmCache:    confirmCache,
		proposeEnabled:  proposeEnabled,
	}
}

// internalToolRequest is the runner -> gateway tool-call request body. It carries
// ONLY a tool name + args. It deliberately has NO tenant / role / actor field:
// the operator scope comes from the verified session binding, never from the
// runner, so the runner cannot escalate scope or impersonate another operator.
type internalToolRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	// Mode 选择 dispatch 路径。"" / "execute" => 旧的只读工具路径(Phase B 之前唯一的路径)。
	// "propose" => LLM-提议路径:mutating 工具被 DRY-RUN 解析(从不执行),返回一个单次
	// correlation_id 供之后 OPERATOR 确认。不带该字段的旧 runner 解码为 Mode="",行为不变。
	Mode string `json:"mode,omitempty"`
}

// internalToolResponse is the gateway -> runner tool-result body. It returns the
// sanitized diagnostic summary the agent feeds back into the conversation. On a
// denial / error it carries a short status + error_class (no PII, no secrets).
type internalToolResponse struct {
	ToolName   string         `json:"tool_name"`
	Status     string         `json:"status"`
	Result     map[string]any `json:"result,omitempty"`
	ErrorClass string         `json:"error_class,omitempty"`
	// 提议路径字段(仅当 Status == "needs_confirmation" 时填)。它们是 LLM 转达给 operator 的
	// 确认句柄:一个单次、短 TTL 的 correlation_id、它的寿命、脱敏后的 dry-run 预览、以及目标身份。
	// LLM 无法用它们确认——确认是 operator-only 的。全部 omitempty,故只读响应与 Phase B 之前
	// 字节级一致。
	CorrelationID    string         `json:"correlation_id,omitempty"`
	ExpiresInSeconds int            `json:"expires_in_seconds,omitempty"`
	Preview          map[string]any `json:"preview,omitempty"`
	TargetType       string         `json:"target_type,omitempty"`
	TargetID         int64          `json:"target_id,omitempty"`
}

// ServeHTTP handles POST <internal_base>/tool-execute. It shares the gateway's
// listener with the runner's other /internal/* callbacks (bootstrap/refresh/keys);
// the protection is the application-layer internal_token (HMAC-SHA256, short TTL,
// constant-time compare), NOT network isolation — the route is on the same public
// listener, so the token gate is the only thing standing between a caller and a
// tool. A separate loopback listener / source ACL on /internal/* is a hardening
// follow-up.
func (h *InternalToolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || len(h.secret) == 0 || h.bindings == nil || h.tools == nil {
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_spine_unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeInternalToolError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	// KNOB B (runtime kill-switch): when the LLM conversational tool loop is
	// disabled, refuse every call BEFORE resolving the operator — no token is
	// inspected, no tool runs, no audit row is written for a refused-by-policy call.
	// Plain /v1/hermes/chat is unaffected (it never reaches this handler).
	if !h.toolLoopEnabled {
		writeInternalToolError(w, http.StatusForbidden, "llm_toolloop_disabled")
		return
	}

	// (1) Authenticate the runner via the internal_token. The bridge minted it for
	// THIS session; an invalid/expired token never reaches a tool.
	op, ok := h.resolveOperator(r)
	if !ok {
		writeInternalToolError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req internalToolRequest
	if !decodeInternalJSON(w, r, &req) {
		return
	}
	req.ToolName = strings.TrimSpace(req.ToolName)
	if req.ToolName == "" {
		writeInternalToolError(w, http.StatusBadRequest, "tool_name_required")
		return
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}

	// Phase B —— LLM-提议分支。当 runner 把调用标记为 mode=propose(LLM 在对话里提议的某个
	// mutating 工具),对它做 DRY-RUN 解析并返回 needs_confirmation 句柄;在此绝不执行(本 handler
	// 没有 Mutate)。真正的 state change 只发生在之后——OPERATOR 经独立的 operator 认证 H1 确认
	// 路径确认时。它分支在下面的只读过滤之前(否则那个 mutating 工具名会被 403),且本身受
	// serveProposal 内的 proposeEnabled KNOB(默认关)门控。
	if req.Mode == proposeMode {
		h.serveProposal(w, r, op, req)
		return
	}

	// (4) READ-ONLY filter — the structural mutation guard. Symmetric with the
	// catalog's allow-test (catalog.go): a tool is dispatchable ONLY if it is
	// explicitly ReadOnly AND not Mutating. Rejecting on (Mutating || !ReadOnly)
	// also excludes any future tool that is neither (an unset ReadOnly flag),
	// fail-safe by default. The LLM cannot reach account_pause / dlq_replay /
	// renew_trigger (or any non-read-only tool) here.
	if spec, found := h.tools.Get(req.ToolName); found && (spec.Mutating || !spec.ReadOnly) {
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "mutating_tool_rejected")
		writeInternalToolError(w, http.StatusForbidden, "mutating_tool_forbidden")
		return
	}

	// (5)(6) Dispatch through the read-only Run: it enforces the role floor and
	// refuses a mutating tool (ErrNotMutating) as the SECOND independent guard.
	// TenantID is pinned to the bound session — the runner supplied no tenant.
	toolReq := hermesops.ToolRequest{
		TenantID:    op.TenantID,
		ActorUserID: op.ActorUserID,
		Role:        op.Role,
		Args:        req.Args,
	}
	result, runErr := h.tools.Run(r.Context(), req.ToolName, toolReq)
	if runErr != nil {
		h.writeRunError(w, r, op, req, runErr)
		return
	}

	// (7) Record the ok row with operator attribution; (8) return only the
	// sanitized summary to the runner.
	h.recordCall(r, op, req, hermesops.ResultOK, result.Summary, result.ErrorClass)
	writeInternalToolJSON(w, http.StatusOK, internalToolResponse{
		ToolName:   req.ToolName,
		Status:     string(hermesops.ResultOK),
		Result:     result.Summary,
		ErrorClass: result.ErrorClass,
	})
}

// serveProposal 处理 mode=propose 调用:对 LLM 提议的某个 mutating 工具做 DRY-RUN 解析并返回
// needs_confirmation 句柄。它绝不执行——本路径没有 Mutate 句柄。operator 之后经独立的 operator
// 认证 H1 确认端点确认这个 correlation_id,该端点从同一个共享 cache 里 Consume 它。
func (h *InternalToolHandler) serveProposal(w http.ResponseWriter, r *http.Request, op SessionOperator, req internalToolRequest) {
	// KNOB(默认关):LLM-提议路径在 Owner 激活它之前是惰性的。禁用时一个 propose 调用在任何
	// 解析之前即被拒(403)——无 dry-run、无 correlation_id、无审计行——故合并本路径是零生产
	// 行为变。
	if !h.proposeEnabled {
		writeInternalToolError(w, http.StatusForbidden, "llm_propose_disabled")
		return
	}
	// Fail closed:提议路径同时需要只读的提议解析器和共享 confirm cache(operator 经同一个 cache
	// 确认所发的 correlation_id)。任一依赖为 nil => 503,绝不产出无法解析或无法确认的提议。
	if h.proposer == nil || h.confirmCache == nil {
		writeInternalToolError(w, http.StatusServiceUnavailable, "propose_unavailable")
		return
	}

	// TenantID 钉死为绑定会话的租户——runner 没提供任何租户,故 LLM 无法对另一个租户的目标提议
	// mutation。
	toolReq := hermesops.ToolRequest{
		TenantID:    op.TenantID,
		ActorUserID: op.ActorUserID,
		Role:        op.Role,
		Args:        req.Args,
	}
	// 只读 dry-run 解析。ResolveProposal 强制角色下限,拒绝只读工具(ErrNotMutating)以及未标记
	// Proposable 的 mutating 工具(ErrNotProposable——如 renew_trigger 凭证轮换),只返回
	// MutationPlan。它不持有任何 Mutate 句柄,故此处绝不能改变 state。
	plan, err := h.proposer.ResolveProposal(r.Context(), req.ToolName, op.Role, toolReq)
	if err != nil {
		h.writeProposeError(w, r, op, req, err)
		return
	}

	// Issue 一个单次、短 TTL 的 correlation_id,绑定到精确的(工具、租户、actor-user、operator-token、
	// target)。因为同一个共享 cache 也支撑 operator H1 确认路径,只有会话本人的 operator 之后才能
	// 确认这条提议——六元组绑定会拒绝任何其它 actor。
	correlationID, issueErr := h.confirmCache.Issue(hermesconfirm.PendingConfirmation{
		ToolName: req.ToolName,
		TenantID: op.TenantID,
		ActorID:  op.ActorUserID,
		TokenID:  op.AdminActorTokenID,
		TargetID: plan.TargetID,
	})
	if issueErr != nil {
		h.recordCall(r, op, req, hermesops.ResultError, nil, "propose_failed")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_unavailable")
		return
	}
	// 把 dry-run 记到审计账本(status ok、dry_run=true),镜像 operator H1 预览。Best-effort:提议
	// 没有 mutate,故瞬时账本错误不得阻塞返回提议。
	h.recordProposalDryRun(r, op, req, plan.Preview)

	writeInternalToolJSON(w, http.StatusOK, internalToolResponse{
		ToolName:         req.ToolName,
		Status:           proposeStatusNeedsConfirmation,
		CorrelationID:    correlationID,
		ExpiresInSeconds: int(hermesconfirm.ConfirmTTL.Seconds()),
		Preview:          plan.Preview,
		TargetType:       plan.TargetType,
		TargetID:         plan.TargetID,
	})
}

// writeProposeError 把 ResolveProposal 的错误映射成状态码 + 一条审计 denied/error 行。角色 /
// 非 mutating / 非 Proposable 拒绝记为 denied 行(授权结果);args / target / 依赖失败记为
// error 行。它镜像 writeRunError,但补上提议路径独有的拒绝(ErrNotProposable + ErrTargetResolution)。
// 本路径从未运行过任何 mutation。
func (h *InternalToolHandler) writeProposeError(w http.ResponseWriter, r *http.Request, op SessionOperator, req internalToolRequest, err error) {
	switch {
	case errors.Is(err, hermesops.ErrToolUnknown):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "unknown_tool")
		writeInternalToolError(w, http.StatusNotFound, "unknown_tool")
	case errors.Is(err, hermesops.ErrToolForbidden):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "role_forbidden")
		writeInternalToolError(w, http.StatusForbidden, "tool_forbidden")
	case errors.Is(err, hermesops.ErrNotMutating):
		// 经 mode=propose 提议了一个只读工具——提议路径只面向 mutating 工具。拒绝(未执行任何东西;
		// 记 denied)。
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "tool_not_mutating")
		writeInternalToolError(w, http.StatusBadRequest, "tool_not_mutating")
	case errors.Is(err, hermesops.ErrNotProposable):
		// 头牌提议门:未标记 Proposable 的 mutating 工具(如 renew_trigger 凭证轮换)绝不能被 LLM
		// 提议。operator 仍可经 H1 确认路径直接驱动它。
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "tool_not_proposable")
		writeInternalToolError(w, http.StatusForbidden, "tool_not_proposable")
	case errors.Is(err, hermesops.ErrInvalidArgs):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "invalid_args")
		writeInternalToolError(w, http.StatusBadRequest, "invalid_args")
	case errors.Is(err, hermesops.ErrTargetResolution):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "target_resolution_failed")
		writeInternalToolError(w, http.StatusNotFound, "target_not_found")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "dependency_unwired")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_unavailable")
	default:
		h.recordCall(r, op, req, hermesops.ResultError, nil, "propose_failed")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_failed")
	}
}

// resolveOperator verifies the internal_token and resolves the bound operator.
// It enforces (1) token validity, (2) a present+unexpired binding, and (3)
// tenant/user consistency between the token and the binding. Any failure returns
// (zero, false) so the caller fails closed with 401.
func (h *InternalToolHandler) resolveOperator(r *http.Request) (SessionOperator, bool) {
	token := bearerFromRequest(r)
	if token == "" {
		return SessionOperator{}, false
	}
	claims, err := VerifyInternalToken(token, h.secret, h.now().UTC())
	if err != nil {
		return SessionOperator{}, false
	}
	op, ok := h.bindings.Lookup(claims.RequestID)
	if !ok {
		return SessionOperator{}, false
	}
	// Identity consistency: a token issued for (tenant,user) must match the bound
	// session's (tenant,user). Prevents a valid token for one session from being
	// paired with another session's request_id binding.
	if op.TenantID != claims.TenantID || op.ActorUserID != claims.UserID {
		return SessionOperator{}, false
	}
	// The conversational path is admin-only: a binding with no operator role / no
	// admin actor token can never authorize a tool.
	if strings.TrimSpace(op.Role) == "" || op.AdminActorTokenID <= 0 {
		return SessionOperator{}, false
	}
	return op, true
}

// writeRunError maps a Run error to a status + audited denied/error row. A role
// or mutating-guard rejection is a denied row (RBAC outcome); a dependency /
// args / execution failure is an error row.
func (h *InternalToolHandler) writeRunError(w http.ResponseWriter, r *http.Request, op SessionOperator, req internalToolRequest, runErr error) {
	switch {
	case errors.Is(runErr, hermesops.ErrToolUnknown):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "unknown_tool")
		writeInternalToolError(w, http.StatusNotFound, "unknown_tool")
	case errors.Is(runErr, hermesops.ErrToolForbidden):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "role_forbidden")
		writeInternalToolError(w, http.StatusForbidden, "tool_forbidden")
	case errors.Is(runErr, hermesops.ErrNotMutating):
		// The registry's own mutation guard fired (a mutating tool reached Run).
		// Recorded distinctly so the audit shows the second gate caught it.
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "mutating_tool_rejected")
		writeInternalToolError(w, http.StatusForbidden, "mutating_tool_forbidden")
	case errors.Is(runErr, hermesops.ErrInvalidArgs):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "invalid_args")
		writeInternalToolError(w, http.StatusBadRequest, "invalid_args")
	case errors.Is(runErr, hermesops.ErrDependencyUnwired):
		h.recordCall(r, op, req, hermesops.ResultError, nil, "dependency_unwired")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_unavailable")
	default:
		h.recordCall(r, op, req, hermesops.ResultError, nil, "tool_execution_failed")
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_failed")
	}
}

// recordCall appends one sanitized hermes_tool_calls row with the operator
// attribution. Best-effort: a transient audit-write failure is swallowed (the
// tool result is already computed) — it must not surface the operator's data or
// block the conversation. The store sanitizes args + summary before insert.
func (h *InternalToolHandler) recordCall(r *http.Request, op SessionOperator, req internalToolRequest, status hermesops.ResultStatus, summary map[string]any, errorClass string) {
	h.appendToolCall(r, op, req, status, summary, errorClass, false)
}

// recordProposalDryRun 把 Phase B 的 dry-run 提议记成一行 hermes_tool_calls(status ok、
// dry_run=true),镜像 operator H1 预览行。summary 是脱敏后的 plan Preview。与 recordCall 一样
// best-effort:提议没有 mutate,故瞬时账本错误不得阻塞返回提议。
func (h *InternalToolHandler) recordProposalDryRun(r *http.Request, op SessionOperator, req internalToolRequest, preview map[string]any) {
	h.appendToolCall(r, op, req, hermesops.ResultOK, preview, "", true)
}

// appendToolCall 是 recordCall(dry_run=false)与 recordProposalDryRun(dry_run=true)共享的
// best-effort 写入器。inserter 为 nil 时是 no-op;store 在 insert 前会对 args + summary 脱敏。
func (h *InternalToolHandler) appendToolCall(r *http.Request, op SessionOperator, req internalToolRequest, status hermesops.ResultStatus, summary map[string]any, errorClass string, dryRun bool) {
	if h.toolCalls == nil {
		return
	}
	now := h.now().UTC()
	_ = hermesops.RecordToolCall(r.Context(), h.toolCalls, hermesops.ToolCallAudit{
		TenantID:          op.TenantID,
		ActorUserID:       op.ActorUserID,
		AdminActorTokenID: op.AdminActorTokenID,
		ToolName:          req.ToolName,
		Args:              req.Args,
		ResultSummary:     summary,
		Status:            status,
		ErrorClass:        errorClass,
		CorrelationID:     strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		RequestID:         strings.TrimSpace(r.Header.Get("X-Request-ID")),
		CalledAt:          now,
		ReturnedAt:        now,
		DryRun:            dryRun,
	})
}

func bearerFromRequest(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func decodeInternalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeInternalToolError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func writeInternalToolJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeInternalToolError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}
