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

// internal_tool_handler.go 是会话式只读工具循环(WAVE H3b,方案 B)的 gateway 侧权威。
// Python runner 的 ops 助手会在对话中途回调进 gateway 来运行一个诊断工具;本 handler 是
// 唯一负责授权 + 执行 + 审计该调用的地方。
//
// 安全契约(每一项都在此 fail-closed 地强制执行):
//  1. 认证:调用方必须出示一个有效的 internal_token(即 bridge 为本会话铸造的那同一份
//     HMAC tenant|user|request_id|exp)。无效/过期的 token => 401,不运行任何工具。
//  2. 会话绑定:token 的 request_id 必须能解析到一个已绑定的 operator 身份(role +
//     tenant 范围 + admin_token_id)。无绑定 => 401。
//  3. 身份一致性:token 的 tenant/user 必须与已绑定会话的 tenant/user 匹配。不匹配 =>
//     401(会话 A 的 token 绝不能驱动会话 B 的 operator 范围)。
//  4. 只读过滤(结构性 mutation 守卫):请求的工具必须已注册且为非 mutating。mutating
//     工具名(account_pause、dlq_replay、renew_trigger 等)在 dispatch 之前即被拒绝——
//     即便去掉这个检查,hermesops.Registry.Run 本身也会以 ErrNotMutating 拒绝 mutating
//     工具。两道独立闸门;mutation 无法触达。
//  5. RBAC role 下限:Registry.Run 拿已绑定 operator 的 role 对照工具的 RequiredRole 做
//     授权。低于下限 => denied(已记录)。
//  6. tenant 范围:工具始终以已绑定会话的 TenantID 运行。runner 无法指定 tenant;没有
//     tenant 参数。工具绝不能通过此路径读取另一个 tenant 的数据。
//  7. 审计:每次调用(ok / error / denied / rejected)都记录一行带 operator 归属
//     (admin_actor_token_id)的脱敏 hermes_tool_calls。
//  8. 隐私:只有工具脱敏后的 Summary(枚举/计数/id)会回传给 runner;store 还会对 args +
//     summary 再次脱敏作为纵深防御。

// ReadOnlyToolRunner 是本 handler 从工具 registry 所需的那个窄只读 dispatch 面。
// *hermesops.Registry 满足它。它刻意不暴露 AuthorizeMutating / Resolve / Mutate——mutating
// 能力在结构上不存在于本 handler 的依赖中,因此即便被误调用,这里也没有任何方法能运行
// mutation。
type ReadOnlyToolRunner interface {
	// Get 报告一个已注册的 spec(仅用于把某个名字归类为 mutating,以便做显式的 dispatch
	// 前拒绝 + 审计)。它从不执行任何东西。
	Get(name string) (hermesops.ToolSpec, bool)
	// Run 对一个只读工具做授权(role 下限)+ dispatch。它会以 ErrNotMutating 拒绝 mutating
	// 工具,因此它本身就是一道 mutation 守卫。
	Run(ctx context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error)
	// ReadOnlyCatalog 返回面向 LLM 的只读工具目录。
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

// InternalToolHandler 处理 runner 在对话中途的工具调用。
type InternalToolHandler struct {
	secret    []byte
	bindings  *SessionBindings
	tools     ReadOnlyToolRunner
	toolCalls hermesops.ToolCallInserter
	now       func() time.Time
	// toolLoopEnabled 是 KNOB B 的 runner 回调闸门。为 false 时,ServeHTTP 在解析 operator
	// 之前就拒绝每次调用(403 llm_toolloop_disabled),因此即便会话已绑定,LLM 会话式工具
	// 循环也完全关闭。bridge 侧闸门(不注入目录)是协同的一方;这里是强制执行的一方。
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

// NewInternalToolHandler 接线 handler。secret 是 internal-token 的 HMAC 密钥(与 bridge 的
// 相同)。registry / inserter / bindings 为 nil 会让 handler fail closed(503 / 401)而非
// panic。toolLoopEnabled 是 KNOB B:为 false 时 handler 在碰 token 之前就拒绝每次调用(403)。
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

// internalToolRequest 是 runner -> gateway 的工具调用请求体。它只携带工具名 + args。它刻意
// 不含 tenant / role / actor 字段:operator 范围来自经验证的会话绑定,绝不来自 runner,因此
// runner 无法越权扩大范围或冒充另一个 operator。
type internalToolRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	// Mode 选择 dispatch 路径。"" / "execute" => 旧的只读工具路径(Phase B 之前唯一的路径)。
	// "propose" => LLM-提议路径:mutating 工具被 DRY-RUN 解析(从不执行),返回一个单次
	// correlation_id 供之后 OPERATOR 确认。不带该字段的旧 runner 解码为 Mode="",行为不变。
	Mode string `json:"mode,omitempty"`
}

// internalToolResponse 是 gateway -> runner 的工具结果体。它返回 agent 要喂回对话的脱敏
// 诊断 summary。在拒绝 / 错误时,它携带一个简短的 status + error_class(无 PII、无 secret)。
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

// ServeHTTP 处理 POST <internal_base>/tool-execute。它与 runner 的其它 /internal/* 回调
//(bootstrap/refresh/keys)共用 gateway 的 listener;防护来自应用层的 internal_token
//(HMAC-SHA256、短 TTL、constant-time 比较),而非网络隔离——该路由位于同一个公网 listener
// 上,因此 token 闸门是横亘在调用方与工具之间的唯一屏障。为 /internal/* 单独设 loopback
// listener / 来源 ACL 是后续的加固工作。
func (h *InternalToolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || len(h.secret) == 0 || h.bindings == nil || h.tools == nil {
		writeInternalToolError(w, http.StatusServiceUnavailable, "tool_spine_unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeInternalToolError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	// KNOB B(运行时 kill-switch):当 LLM 会话式工具循环被禁用时,在解析 operator 之前就
	// 拒绝每次调用——不检查 token、不运行工具、不为这种被策略拒绝的调用写审计行。
	// 普通的 /v1/hermes/chat 不受影响(它永远不会到达本 handler)。
	if !h.toolLoopEnabled {
		writeInternalToolError(w, http.StatusForbidden, "llm_toolloop_disabled")
		return
	}

	// (1) 经 internal_token 认证 runner。bridge 是为本会话铸造它的;无效/过期的 token
	// 永远到不了工具。
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

	// (4) 只读过滤——结构性 mutation 守卫。与目录的 allow-test(catalog.go)对称:一个工具
	// 只有在被显式标记为 ReadOnly 且非 Mutating 时才可 dispatch。以 (Mutating || !ReadOnly)
	// 拒绝,也会排除任何二者皆非的未来工具(ReadOnly 标志未设置),默认 fail-safe。LLM 在此
	// 无法触达 account_pause / dlq_replay / renew_trigger(或任何非只读工具)。
	if spec, found := h.tools.Get(req.ToolName); found && (spec.Mutating || !spec.ReadOnly) {
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "mutating_tool_rejected")
		writeInternalToolError(w, http.StatusForbidden, "mutating_tool_forbidden")
		return
	}

	// (5)(6) 经只读 Run dispatch:它强制 role 下限,并作为第二道独立守卫以 ErrNotMutating
	// 拒绝 mutating 工具。TenantID 被钉死为已绑定会话——runner 没提供任何 tenant。
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

	// (7) 记录带 operator 归属的 ok 行;(8) 只把脱敏后的 summary 返回给 runner。
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

// resolveOperator 验证 internal_token 并解析出已绑定的 operator。它强制(1)token 有效性、
//(2)绑定存在且未过期、(3)token 与绑定之间的 tenant/user 一致性。任何失败都返回(零值,
// false),使调用方 fail closed 返回 401。
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
	// 身份一致性:为某个 (tenant,user) 签发的 token 必须与已绑定会话的 (tenant,user) 匹配。
	// 防止某会话的有效 token 被配上另一个会话的 request_id 绑定。
	if op.TenantID != claims.TenantID || op.ActorUserID != claims.UserID {
		return SessionOperator{}, false
	}
	// 会话式路径仅限 admin:一个没有 operator role / 没有 admin actor token 的绑定绝不能
	// 授权工具。
	if strings.TrimSpace(op.Role) == "" || op.AdminActorTokenID <= 0 {
		return SessionOperator{}, false
	}
	return op, true
}

// writeRunError 把 Run 的错误映射成状态码 + 一条经审计的 denied/error 行。role 或 mutating
// 守卫的拒绝记为 denied 行(RBAC 结果);依赖 / args / 执行失败记为 error 行。
func (h *InternalToolHandler) writeRunError(w http.ResponseWriter, r *http.Request, op SessionOperator, req internalToolRequest, runErr error) {
	switch {
	case errors.Is(runErr, hermesops.ErrToolUnknown):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "unknown_tool")
		writeInternalToolError(w, http.StatusNotFound, "unknown_tool")
	case errors.Is(runErr, hermesops.ErrToolForbidden):
		h.recordCall(r, op, req, hermesops.ResultDenied, nil, "role_forbidden")
		writeInternalToolError(w, http.StatusForbidden, "tool_forbidden")
	case errors.Is(runErr, hermesops.ErrNotMutating):
		// registry 自身的 mutation 守卫触发了(一个 mutating 工具到达了 Run)。单独记录,
		// 使审计显示是第二道闸门抓住了它。
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

// recordCall 追加一行带 operator 归属的脱敏 hermes_tool_calls。Best-effort:瞬时的审计写入
// 失败会被吞掉(工具结果已计算完成)——它不得暴露 operator 的数据或阻塞对话。store 在 insert
// 之前会对 args + summary 脱敏。
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
