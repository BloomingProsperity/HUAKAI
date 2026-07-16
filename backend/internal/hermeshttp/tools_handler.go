package hermeshttp

import (
	"errors"
	"net/http"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// toolDescriptor 是 GET /v1/hermes/tools 列表中的一条目:某工具的身份 + 入参 schema
// + 所需 role + read-only 标记,使 operator / assistant 无需反复试错即可发现哪些工具
// 可调用。
type toolDescriptor struct {
	Name                 string            `json:"name"`
	Category             string            `json:"category"`
	Description          string            `json:"description"`
	ReadOnly             bool              `json:"read_only"`
	Mutating             bool              `json:"mutating"`
	RequiresConfirmation bool              `json:"requires_confirmation"`
	RequiredRole         string            `json:"required_role"`
	InputSchema          map[string]string `json:"input_schema"`
}

// listTools 服务 GET /v1/hermes/tools。它由 H1 中间件挂载做 admin 门控;这里只要求
// 一个已解析的身份(由中间件保证),并列出每一个已注册的工具。
func (h handler) listTools(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireIdentity(w, r); !ok {
		return
	}
	if h.tools == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tools_unavailable", "hermes tool registry unset")
		return
	}
	specs := h.tools.List()
	out := make([]toolDescriptor, 0, len(specs))
	for _, s := range specs {
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		out = append(out, toolDescriptor{
			Name:                 s.Name,
			Category:             string(s.Category),
			Description:          s.Description,
			ReadOnly:             s.ReadOnly,
			Mutating:             s.Mutating,
			RequiresConfirmation: s.RequiresConfirmation,
			RequiredRole:         s.RequiredRole,
			InputSchema:          schema,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

type toolExecuteRequest struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	// Confirm + CorrelationID 驱动 WAVE H4 mutating 工具的 dry-run+confirm 流程。
	// 对只读工具会被忽略。对 mutating 工具,Confirm=false(默认)返回只读 preview +
	// 一个 correlation_id;Confirm=true 且 correlation_id 匹配时,恰好执行一次该变更。
	Confirm       bool   `json:"confirm,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// executeTool 服务 POST /v1/hermes/tool-execute。它:
//  1. 解析(已做过 scope 校验的)身份 + operator actor,
//  2. 按调用方的 role 对该工具做授权(RBAC 下限),
//  3. 拒绝时——记录一条 denied 的 hermes_tool_calls 行 + 403,
//  4. 放行时——运行只读工具,记录一条 ok/error 的 hermes_tool_calls 行,并把本次
//     调用以 hermes.tool.<name> 的形式镜像写入 hermes_audit_events,然后返回结构化结果。
//
// 每条路径在返回前都会记录一条 tool-call 行,因此成功、错误、拒绝都可审计。会改动
// 状态的工具无法到达此处——本 wave 只注册了只读工具。
func (h handler) executeTool(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	if h.tools == nil || h.toolCalls == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_tools_unavailable", "hermes tool spine unset")
		return
	}

	var req toolExecuteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "hermes_tool_name_required", "tool_name is required")
		return
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}

	actor, _ := adminActorFromContext(r.Context())

	// WAVE H4:mutating 工具只能经由 dry-run + confirm 流程(5 层安全)派发。它绝不
	// 应到达下方的只读 Authorize/Run 路径。在只读 RBAC 之前就把它分流出去,使得任何
	// 变更都不可能绕过 confirm 门 + advisory lock + atomic audit 而运行。
	if mutSpec, ok := h.tools.Get(req.ToolName); ok && mutSpec.Mutating {
		// KNOB A(运行时 kill-switch):当 mutating 工具被禁用时,在这里拒绝 mutating
		// 分支——在 previewMutation 或 confirmMutation 之前——使单一节流点同时覆盖
		// dry-run preview 与已确认的执行。记录一条 denied 的 hermes_tool_calls 行
		// (与其它 L1 denial 审计对齐,复用 mutate handler 的 denial 记录器),让被拒绝
		// 的尝试可审计,然后 403。下方的只读路径不受影响。
		if h.mutatingDisabled {
			h.recordMutatingDenied(r, ident, actor, req)
			writeError(w, http.StatusForbidden, "hermes_mutating_disabled", "hermes mutating tools are disabled at runtime")
			return
		}
		h.executeMutatingTool(w, r, ident, actor, req)
		return
	}

	// RBAC:校验 role 下限。tenant scope 在本 handler 运行前已由 H1 中间件
	// (CanIssueForTenant)执行——跨 tenant 的调用方永远到不了这里。未知工具或权限
	// 不足均视为拒绝,记录为一条 denied 行。
	spec, authErr := h.tools.Authorize(req.ToolName, actor.Role)
	if authErr != nil {
		h.recordToolCall(r, ident, actor, req, hermesops.ResultDenied, nil, "")
		switch {
		case errors.Is(authErr, hermesops.ErrToolUnknown):
			writeError(w, http.StatusNotFound, "hermes_tool_unknown", "unknown tool")
		default:
			writeError(w, http.StatusForbidden, "hermes_tool_forbidden", "operator role may not run this tool")
		}
		return
	}

	toolReq := hermesops.ToolRequest{
		TenantID:    ident.TenantID,
		ActorUserID: ident.UserID,
		Role:        actor.Role,
		Args:        req.Args,
	}
	result, runErr := h.tools.Run(r.Context(), req.ToolName, toolReq)
	if runErr != nil {
		errorClass := classifyToolRunError(runErr)
		h.recordToolCall(r, ident, actor, req, hermesops.ResultError, nil, errorClass)
		h.mirrorToolAudit(r, ident, spec.Name, hermes.AuditResultFailure)
		writeToolRunError(w, runErr)
		return
	}

	// 成功:记录 ok 行(含脱敏后的 summary)+ 镜像写入 hermes 审计账本,然后返回
	// 结构化结果。
	h.recordToolCall(r, ident, actor, req, hermesops.ResultOK, result.Summary, result.ErrorClass)
	h.mirrorToolAudit(r, ident, spec.Name, hermes.AuditResultSuccess)

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":   spec.Name,
		"result":      result.Summary,
		"error_class": emptyStringToNil(result.ErrorClass),
		"read_only":   spec.ReadOnly,
	})
}

// recordToolCall 追加一条脱敏后的 hermes_tool_calls 行。写入出错时它仅尽力记日志
// (不让请求失败)——诊断结果已经算出,审计写入的瞬时故障不应将其掩盖;hermes 审计
// 镜像提供了第二条轨迹。summary 在 hermesops.RecordToolCall 内部脱敏(在「仅诊断类
// 工具」之上的纵深防御)。
func (h handler) recordToolCall(r *http.Request, ident sessionauth.Identity, actor adminActor, req toolExecuteRequest, status hermesops.ResultStatus, summary map[string]any, errorClass string) {
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
	}
	if err := hermesops.RecordToolCall(r.Context(), h.toolCalls, rec); err != nil {
		logToolCallWriteFailure(req.ToolName, err)
	}
}

// mirrorToolAudit 写入按动作的 hermes_audit_events 行(复用既有 RecordAudit 路径,
// 后者会折入 operator 的 admin_actor 归因并脱敏 args)。没有白名单动作的工具(对已注册
// 工具不应发生)会被跳过。瞬时审计故障只记日志、不向外暴露——权威的 tool-call 行是
// hermes_tool_calls 那条记录。
func (h handler) mirrorToolAudit(r *http.Request, ident sessionauth.Identity, toolName, result string) {
	if h.svc == nil {
		return
	}
	action, ok := hermes.ToolAuditAction(toolName)
	if !ok {
		return
	}
	args := map[string]any{"tool_name": toolName}
	if err := h.svc.RecordAudit(r.Context(), ident.TenantID, ident.UserID, action,
		withAdminActor(r.Context(), args), result, correlationID(r), requestID(r)); err != nil {
		logToolAuditMirrorFailure(toolName, err)
	}
}

func classifyToolRunError(err error) string {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		return "dependency_unwired"
	default:
		return "tool_execution_failed"
	}
}

func writeToolRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermesops.ErrInvalidArgs):
		writeError(w, http.StatusBadRequest, "hermes_tool_invalid_args", "invalid or missing tool arguments")
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_unavailable", "tool dependency is not wired")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_tool_failed", "tool execution failed")
	}
}

func emptyStringToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
