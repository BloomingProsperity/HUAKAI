package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

const (
	mcpProtocolVersion = "2025-06-18"
	mcpMaxBodyBytes    = 1 << 20
)

type mcpToolRegistry interface {
	Get(string) (hermesops.ToolSpec, bool)
	Run(context.Context, string, hermesops.ToolRequest) (hermesops.ToolResult, error)
	ResolveProposal(context.Context, string, string, hermesops.ToolRequest) (hermesops.MutationPlan, error)
	CatalogForRole(string, bool) []hermesops.CatalogTool
}

// MCPHandler 是官方 Hermes 唯一可见的 HUAKAI 工具入口。它使用短时内部令牌恢复真实管理员和
// 固定租户，模型只能运行只读工具或生成等待管理员确认的可逆提议。
type MCPHandler struct {
	secret          []byte
	tools           mcpToolRegistry
	toolCalls       hermesops.ToolCallInserter
	confirmStore    hermesconfirm.Store
	now             func() time.Time
	enabled         bool
	proposalEnabled bool
}

func NewMCPHandler(secret []byte, tools mcpToolRegistry, calls hermesops.ToolCallInserter, confirmStore hermesconfirm.Store, now func() time.Time, enabled, proposalEnabled bool) *MCPHandler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MCPHandler{
		secret: append([]byte(nil), secret...), tools: tools, toolCalls: calls,
		confirmStore: confirmStore, now: now, enabled: enabled, proposalEnabled: proposalEnabled,
	}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpIdentity struct {
	Claims   InternalTokenClaims
	Operator SessionOperator
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || len(h.secret) == 0 || h.tools == nil {
		http.Error(w, "mcp unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.enabled {
		http.Error(w, "mcp disabled", http.StatusForbidden)
		return
	}
	identity, ok := h.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var request mcpRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mcpMaxBodyBytes))
	if err := decoder.Decode(&request); err != nil {
		h.writeRPCError(w, nil, -32700, "请求不是有效 JSON")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		h.writeRPCError(w, request.ID, -32600, "请求只能包含一个 JSON-RPC 对象")
		return
	}
	request.Method = strings.TrimSpace(request.Method)
	if request.JSONRPC != "2.0" || request.Method == "" {
		h.writeRPCError(w, request.ID, -32600, "无效的 JSON-RPC 请求")
		return
	}
	if len(request.ID) == 0 {
		h.handleNotification(w, request.Method)
		return
	}

	switch request.Method {
	case "initialize":
		h.initialize(w, request)
	case "ping":
		h.writeRPCResult(w, request.ID, map[string]any{})
	case "tools/list":
		h.listTools(w, request, identity)
	case "tools/call":
		h.callTool(w, r, request, identity)
	default:
		h.writeRPCError(w, request.ID, -32601, "方法不存在")
	}
}

func (h *MCPHandler) authenticate(r *http.Request) (mcpIdentity, bool) {
	token := bearerToken(r)
	if token == "" {
		return mcpIdentity{}, false
	}
	claims, err := VerifyInternalToken(token, h.secret, h.now().UTC())
	if err != nil || claims.Purpose != InternalTokenPurposeMCP || claims.TenantID <= 0 || claims.UserID <= 0 || claims.ActorID <= 0 || strings.TrimSpace(claims.ActorRole) == "" {
		return mcpIdentity{}, false
	}
	return mcpIdentity{
		Claims: claims,
		Operator: SessionOperator{
			TenantID: claims.TenantID, ActorSource: claims.ActorSource,
			ActorID: claims.ActorID, Role: claims.ActorRole, ExpiresAt: claims.ExpiresAt,
		},
	}, true
}

func (h *MCPHandler) initialize(w http.ResponseWriter, request mcpRequest) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) != nil {
		h.writeRPCError(w, request.ID, -32602, "初始化参数无效")
		return
	}
	version := mcpProtocolVersion
	switch params.ProtocolVersion {
	case "2024-11-05", "2025-03-26", mcpProtocolVersion:
		version = params.ProtocolVersion
	}
	h.writeRPCResult(w, request.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo":   map[string]any{"name": "huakai-operations", "version": "1.0"},
		"instructions": "只能读取当前管理员获授权租户的运维信息；改动型工具只生成等待人工确认的提议。",
	})
}

func (h *MCPHandler) listTools(w http.ResponseWriter, request mcpRequest, identity mcpIdentity) {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(request.Params) > 0 && (json.Unmarshal(request.Params, &params) != nil || strings.TrimSpace(params.Cursor) != "") {
		h.writeRPCError(w, request.ID, -32602, "本工具目录不使用分页游标")
		return
	}
	catalog := h.tools.CatalogForRole(identity.Operator.Role, h.proposalEnabled)
	tools := make([]map[string]any, 0, len(catalog))
	for _, tool := range catalog {
		description := tool.Description
		if tool.Mutating {
			description += " 此调用只生成待管理员确认的提议，不会直接修改系统。"
		}
		tools = append(tools, map[string]any{
			"name": tool.Name, "description": description, "inputSchema": tool.InputSchema,
			"annotations": map[string]any{
				"readOnlyHint": true, "destructiveHint": false,
				"idempotentHint": true, "openWorldHint": false,
			},
		})
	}
	h.writeRPCResult(w, request.ID, map[string]any{"tools": tools})
}

func (h *MCPHandler) callTool(w http.ResponseWriter, r *http.Request, request mcpRequest, identity mcpIdentity) {
	var params mcpCallParams
	if json.Unmarshal(request.Params, &params) != nil {
		h.writeRPCError(w, request.ID, -32602, "工具调用参数无效")
		return
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	startedAt := h.now().UTC()
	spec, found := h.tools.Get(params.Name)
	if !found {
		h.writeToolError(w, request.ID, "unknown_tool")
		return
	}
	if !hermesops.RoleAllowed(identity.Operator.Role, spec.RequiredRole) {
		_ = h.recordToolCall(r.Context(), identity, params, hermesops.ResultDenied, nil, "role_forbidden", false, startedAt)
		h.writeToolError(w, request.ID, "tool_forbidden")
		return
	}
	if err := hermesops.ValidateToolArguments(spec.InputSchema, params.Arguments); err != nil {
		_ = h.recordToolCall(r.Context(), identity, params, hermesops.ResultError, nil, "invalid_args", false, startedAt)
		h.writeToolError(w, request.ID, "invalid_args")
		return
	}
	if spec.Mutating {
		h.proposeTool(w, r.Context(), request.ID, identity, params, spec, startedAt)
		return
	}

	result, err := h.tools.Run(r.Context(), params.Name, hermesops.ToolRequest{
		TenantID: identity.Claims.TenantID, ActorSource: identity.Claims.ActorSource,
		ActorID: identity.Claims.ActorID, Role: identity.Claims.ActorRole, Args: params.Arguments,
	})
	if err != nil {
		class := mcpToolErrorClass(err)
		_ = h.recordToolCall(r.Context(), identity, params, hermesops.ResultError, nil, class, false, startedAt)
		h.writeToolError(w, request.ID, class)
		return
	}
	payload := map[string]any{"status": "ok", "result": result.Summary}
	if result.ErrorClass != "" {
		payload["error_class"] = result.ErrorClass
	}
	if err := h.recordToolCall(r.Context(), identity, params, hermesops.ResultOK, result.Summary, result.ErrorClass, false, startedAt); err != nil {
		h.writeToolError(w, request.ID, "audit_unavailable")
		return
	}
	h.writeToolResult(w, request.ID, payload, false)
}

func (h *MCPHandler) proposeTool(w http.ResponseWriter, ctx context.Context, id json.RawMessage, identity mcpIdentity, params mcpCallParams, spec hermesops.ToolSpec, startedAt time.Time) {
	if !h.proposalEnabled || !spec.Proposable || !spec.RequiresConfirmation {
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultDenied, nil, "tool_not_proposable", false, startedAt)
		h.writeToolError(w, id, "tool_not_proposable")
		return
	}
	if h.confirmStore == nil {
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultError, nil, "proposal_unavailable", false, startedAt)
		h.writeToolError(w, id, "proposal_unavailable")
		return
	}
	request := hermesops.ToolRequest{
		TenantID: identity.Claims.TenantID, ActorSource: identity.Claims.ActorSource,
		ActorID: identity.Claims.ActorID, Role: identity.Claims.ActorRole, Args: params.Arguments,
	}
	plan, err := h.tools.ResolveProposal(ctx, params.Name, identity.Claims.ActorRole, request)
	if err != nil {
		class := mcpToolErrorClass(err)
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultError, nil, class, false, startedAt)
		h.writeToolError(w, id, class)
		return
	}
	argsDigest, err := hermesconfirm.DigestArguments(params.Arguments)
	if err != nil {
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultError, nil, "proposal_unavailable", false, startedAt)
		h.writeToolError(w, id, "proposal_unavailable")
		return
	}
	planDigest, err := hermesconfirm.DigestPlan(plan.TargetType, plan.TargetID, plan.LockKey, plan.Preview)
	if err != nil {
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultError, nil, "proposal_unavailable", false, startedAt)
		h.writeToolError(w, id, "proposal_unavailable")
		return
	}
	pending := hermesconfirm.PendingConfirmation{
		ToolName: params.Name, TenantID: identity.Claims.TenantID,
		ActorSource: identity.Claims.ActorSource, ActorID: identity.Claims.ActorID, TargetID: plan.TargetID,
		ArgsDigest: argsDigest, PlanDigest: planDigest,
	}
	correlationID, err := h.confirmStore.Issue(ctx, pending)
	if err != nil {
		_ = h.recordToolCall(ctx, identity, params, hermesops.ResultError, nil, "proposal_unavailable", false, startedAt)
		h.writeToolError(w, id, "proposal_unavailable")
		return
	}
	payload := map[string]any{
		"status": "needs_confirmation", "correlation_id": correlationID,
		"expires_in_seconds": int(hermesconfirm.ConfirmTTL.Seconds()),
		"preview":            plan.Preview, "target_type": plan.TargetType, "target_id": plan.TargetID,
	}
	if err := h.recordToolCall(ctx, identity, params, hermesops.ResultOK, plan.Preview, "", true, startedAt); err != nil {
		if _, _, revokeErr := h.confirmStore.ConsumeWithStatus(ctx, correlationID, pending); revokeErr != nil {
			log.Printf("Hermes MCP 确认令牌撤销失败（工具=%s）：%v", params.Name, revokeErr)
		}
		h.writeToolError(w, id, "audit_unavailable")
		return
	}
	h.writeToolResult(w, id, payload, false)
}

func (h *MCPHandler) recordToolCall(ctx context.Context, identity mcpIdentity, params mcpCallParams, status hermesops.ResultStatus, summary map[string]any, errorClass string, dryRun bool, startedAt time.Time) error {
	if h.toolCalls == nil {
		err := errors.New("hermes MCP 工具日志存储未接线")
		log.Printf("Hermes MCP 工具日志写入失败（工具=%s）：%v", params.Name, err)
		return err
	}
	err := hermesops.RecordToolCall(ctx, h.toolCalls, hermesops.ToolCallAudit{
		TenantID: identity.Claims.TenantID, ActorSource: identity.Claims.ActorSource,
		ActorID: identity.Claims.ActorID, ActorRole: identity.Claims.ActorRole,
		ToolName: params.Name, Args: params.Arguments, ResultSummary: summary,
		Status: status, ErrorClass: errorClass, CorrelationID: identity.Claims.RequestID,
		RequestID: identity.Claims.RequestID, DryRun: dryRun,
		CalledAt: startedAt, ReturnedAt: h.now().UTC(),
	})
	if err != nil {
		log.Printf("Hermes MCP 工具日志写入失败（工具=%s）：%v", params.Name, err)
	}
	return err
}

func (h *MCPHandler) handleNotification(w http.ResponseWriter, method string) {
	switch method {
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (h *MCPHandler) writeToolError(w http.ResponseWriter, id json.RawMessage, class string) {
	h.writeToolResult(w, id, map[string]any{"status": "error", "error_class": class}, true)
}

func (h *MCPHandler) writeToolResult(w http.ResponseWriter, id json.RawMessage, payload map[string]any, isError bool) {
	text, err := json.Marshal(payload)
	if err != nil {
		h.writeRPCError(w, id, -32603, "工具结果编码失败")
		return
	}
	h.writeRPCResult(w, id, map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": payload,
		"isError":           isError,
	})
}

func (h *MCPHandler) writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	h.writeRPC(w, mcpResponse{JSONRPC: "2.0", ID: normalizedRPCID(id), Result: result})
}

func (h *MCPHandler) writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	h.writeRPC(w, mcpResponse{JSONRPC: "2.0", ID: normalizedRPCID(id), Error: &mcpRPCError{Code: code, Message: message}})
}

func (h *MCPHandler) writeRPC(w http.ResponseWriter, response mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func normalizedRPCID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func bearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 8 || !strings.EqualFold(value[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("存在额外 JSON 值")
	}
	return err
}

func mcpToolErrorClass(err error) string {
	switch {
	case errors.Is(err, hermesops.ErrToolUnknown):
		return "unknown_tool"
	case errors.Is(err, hermesops.ErrToolForbidden):
		return "role_forbidden"
	case errors.Is(err, hermesops.ErrNotMutating):
		return "tool_not_mutating"
	case errors.Is(err, hermesops.ErrNotProposable):
		return "tool_not_proposable"
	case errors.Is(err, hermesops.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, hermesops.ErrTargetResolution):
		return "target_not_found"
	case errors.Is(err, hermesops.ErrDependencyUnwired):
		return "dependency_unwired"
	default:
		return "tool_execution_failed"
	}
}
