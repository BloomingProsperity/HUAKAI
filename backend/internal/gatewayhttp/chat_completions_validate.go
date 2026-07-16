package gatewayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	Stream              bool          `json:"stream"`
	MaxTokens           *int          `json:"max_tokens"`
	MaxCompletionTokens *int          `json:"max_completion_tokens"`
	MaxOutputTokens     *int          `json:"max_output_tokens"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content 保留 raw JSON：OpenAI Chat 使用 string，Anthropic Messages API
	// 允许 string 或 content block 数组，不能在入口校验时静默丢失。
	Content json.RawMessage `json:"content"`
}

type chatValidatedRequest struct {
	Body            []byte
	Request         chatRequest
	ClientProtocol  proto.ClientProtocol
	ClientAdapter   proto.ClientAdapter
	RequestID       string
	ClientRequestID string
}

func validateChatCompletionsRequest(w http.ResponseWriter, r *http.Request, ctx context.Context) (chatValidatedRequest, bool) {
	body, ok := readChatRequestBody(w, r, ctx)
	if !ok {
		return chatValidatedRequest{}, false
	}
	if !rejectRemovedBodyFields(w, body) {
		return chatValidatedRequest{}, false
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeLoggedJSONError(ctx, middleware.GetReqID(ctx), w, http.StatusBadRequest, clienterr.CodeInvalidJSON, err)
		return chatValidatedRequest{}, false
	}
	clientProtocol, clientAdapter, ok := validateClientProtocol(w, r, req)
	if !ok {
		return chatValidatedRequest{}, false
	}
	if req.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return chatValidatedRequest{}, false
	}
	requestID := uuid.NewString()
	clientRequestID := r.Header.Get(middleware.RequestIDHeader)
	return chatValidatedRequest{
		Body:            body,
		Request:         req,
		ClientProtocol:  clientProtocol,
		ClientAdapter:   clientAdapter,
		RequestID:       requestID,
		ClientRequestID: clientRequestID,
	}, true
}

// relay 入站请求体上限:旧版硬写 1MiB,导致付费用户的带图(单张 base64 ~1.5MiB)、长上下文请求被
// 413。这里把上限抽出来默认抬到成熟中转站量级,运维可经 cmd/gateway 在启动时覆盖(读
// HUAKAI_MAX_REQUEST_BODY_MB)。放大后的滥用面由已有 per-key 限流(RPM/并发)兜住。
// (上游非流式响应上限暂未纳入:它在 gatewayhttp 与 internal/gateway HCSF 两条路径各一份须一致,
// 属 proxies 碰撞包,留作单独协调式 follow-up,见 docs/process/plans。)
const defaultMaxRequestBodyBytes int64 = 32 << 20 // 32 MiB

// maxRequestBodyBytes 是进程级 relay 入站请求体上限:启动 wiring 阶段经 ConfigureBodyLimits 一次性
// 设定,之后 serve 期间只读。用包级 set-once 配置(而非穿透每个 handler 自由函数签名),把 money 热
// 路径改动降到最小。约束:只在启动单线程阶段写,serve 后不再写。
var maxRequestBodyBytes = defaultMaxRequestBodyBytes

// ConfigureBodyLimits 在启动 wiring 阶段(router serve 之前、单次、非并发)设定入站请求体上限。
// 传入 <=0 保留默认。设定后只读,无并发写。
func ConfigureBodyLimits(maxRequestBody int64) {
	if maxRequestBody > 0 {
		maxRequestBodyBytes = maxRequestBody
	}
}

func readChatRequestBody(w http.ResponseWriter, r *http.Request, ctx context.Context) ([]byte, bool) {
	// 保留客户端原始 body，后续 dispatcher 直接交给 provider adapter。
	// 上限由 maxRequestBodyBytes 控制(默认 32MiB,可经 HUAKAI_MAX_REQUEST_BODY_MB 调整),
	// 旧版硬写 1MiB 会把带图/长上下文的合法请求 413 掉。
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeLoggedJSONError(ctx, middleware.GetReqID(ctx), w, http.StatusBadRequest, clienterr.CodeBodyReadError, err)
		return nil, false
	}
	return body, true
}

func rejectRemovedBodyFields(w http.ResponseWriter, body []byte) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err == nil {
		if _, found := keys["pool_group_id"]; found {
			writeJSONError(w, http.StatusBadRequest, "body_field_disallowed",
				"pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
			return false
		}
	}
	return true
}

func validateClientProtocol(w http.ResponseWriter, r *http.Request, req chatRequest) (proto.ClientProtocol, proto.ClientAdapter, bool) {
	var clientProtocol proto.ClientProtocol
	var clientAdapter proto.ClientAdapter
	if inferred, ok := proto.ClientProtocolByIngressPath(r.URL.Path); ok {
		clientProtocol = inferred
	} else if !req.Stream {
		writeJSONError(w, http.StatusNotFound, "unknown_route",
			fmt.Sprintf("no client protocol registered for ingress path %q", r.URL.Path))
		return "", nil, false
	}
	if !req.Stream {
		var adapterOK bool
		clientAdapter, adapterOK = proto.DefaultClientAdapterRegistry().Lookup(clientProtocol)
		if !adapterOK {
			writeJSONError(w, http.StatusServiceUnavailable, "adapter_unregistered",
				fmt.Sprintf("client adapter not registered for protocol %q", clientProtocol))
			return "", nil, false
		}
	}
	return clientProtocol, clientAdapter, true
}

// NewMessagesHandler 是 /v1/messages 端点 handler。它复用 chat completions
// 管线，只把 billing endpoint family 标为 messages。
func NewMessagesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "messages"
	}
	return NewChatCompletionsHandler(d)
}

// NewResponsesHandler 是 /v1/responses 端点 handler。它复用同一条
// auth/routing/billing/forwarding pipeline，仅把 billing endpoint family 标为
// openai_responses；真实上下游协议仍由 registry 的 ProtocolFamily 决定。
func NewResponsesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "openai_responses"
	}
	return NewChatCompletionsHandler(d)
}

// platformSettingsReader 是读取单个平台设置的最小接口。
// *platformsettings.Service 满足此接口。
type platformSettingsReader interface {
	Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// warmupInterceptEnabled 报告预热拦截是否启用。
// 当 PlatformSettings 为 nil，或该设置缺失/无效时，返回 false(安全默认)。
func warmupInterceptEnabled(ctx context.Context, settings platformSettingsReader) bool {
	if settings == nil {
		return false
	}
	s, err := settings.Get(ctx, platformsettings.KeyWarmupInterceptEnabled)
	if err != nil {
		return false
	}
	return s.Value == "true"
}

// clientBetaTokens 惰性解析并缓存客户端 anthropic-beta 请求头(DM-03)。
// 产出只被 anthropic 族出站 adapter 消费(与凭据 beta 合并去重;OAuth 池
// 账号侧另有白名单);attempt 重试不重复解析。
func (ex *chatExecution) clientBetaTokens() []string {
	if ex == nil || ex.r == nil {
		return nil
	}
	if !ex.inboundBetaTokensParsed {
		ex.inboundBetaTokensParsed = true
		ex.inboundBetaTokens = provider.ParseInboundBetaTokens(ex.r.Header.Values("Anthropic-Beta"))
	}
	return ex.inboundBetaTokens
}

// clientTailMessageRole 解析请求体最后一条消息的角色,供输入审核区分
// "新用户轮"与"agent 工具循环重发轮"(DM-16)。按客户端协议取字段:
// chat/anthropic=messages[].role;gemini=contents[].role;responses=input
// (字符串=用户输入;数组取尾项 role,无 role 的工具输出项归 "tool")。
// 解析失败返回 ""(未知,审核按首轮处理)。
func clientTailMessageRole(clientProtocol proto.ClientProtocol, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	switch clientProtocol {
	case proto.ClientProtocolOpenAIResponses:
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Input) == 0 {
			return ""
		}
		if req.Input[0] == '"' {
			return "user"
		}
		var items []struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(req.Input, &items); err != nil || len(items) == 0 {
			return ""
		}
		if role := strings.TrimSpace(items[len(items)-1].Role); role != "" {
			return strings.ToLower(role)
		}
		return "tool"
	case proto.ClientProtocolGemini:
		var req struct {
			Contents []struct {
				Role string `json:"role"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Contents) == 0 {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(req.Contents[len(req.Contents)-1].Role))
	default:
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(req.Messages[len(req.Messages)-1].Role))
	}
}
