package chatpipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const (
	clientSessionIDMaxLength = 200
	clientSessionHashPrefix  = "client-session:"
	clientSessionHashDomain  = "huakai:client-session:v1:"
)

var clientSessionIDHeaderPriority = []string{
	"X-Session-ID",
	"X-Amp-Thread-Id",
	"Session-Id",
	"X-Client-Request-Id",
}

var openAIMetadataUserIDSessionSuffixRE = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

func RequestClientSessionID(r *http.Request, clientProtocol proto.ClientProtocol, rawBody []byte) string {
	if r != nil {
		for _, header := range clientSessionIDHeaderPriority {
			if id := normalizeClientSessionID(r.Header.Get(header)); id != "" {
				return id
			}
		}
	}
	if !isOpenAIClientProtocol(clientProtocol) {
		return ""
	}
	return openAITopLevelClientSessionID(rawBody)
}

func isOpenAIClientProtocol(clientProtocol proto.ClientProtocol) bool {
	switch clientProtocol {
	case proto.ClientProtocolOpenAIChat, proto.ClientProtocolOpenAIResponses:
		return true
	default:
		return false
	}
}

func openAITopLevelClientSessionID(rawBody []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &top); err != nil || top == nil {
		return ""
	}
	for _, key := range []string{"conversation_id", "session_id"} {
		raw, ok := top[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		if id := normalizeClientSessionID(value); id != "" {
			return id
		}
	}
	if raw, ok := top["metadata"]; ok {
		var metadata struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(raw, &metadata); err == nil {
			userID := strings.TrimSpace(metadata.UserID)
			if match := openAIMetadataUserIDSessionSuffixRE.FindStringSubmatch(userID); len(match) == 2 {
				return normalizeClientSessionID(match[1])
			}
			return normalizeClientSessionID(metadata.UserID)
		}
	}
	return ""
}

func normalizeClientSessionID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > clientSessionIDMaxLength {
		return ""
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return id
}

func RequestSessionHash(clientProtocol proto.ClientProtocol, rawBody []byte, promptHash, clientSessionID string) string {
	return requestSessionHash(clientProtocol, rawBody, promptHash, clientSessionID)
}

func requestSessionHash(clientProtocol proto.ClientProtocol, rawBody []byte, promptHash, clientSessionID string) string {
	if clientSessionID != "" {
		return clientSessionHash(clientSessionID)
	}
	if promptHash != "" {
		return promptHash
	}
	if clientProtocol == proto.ClientProtocolOpenAIResponses {
		if previousID := openAIResponsesPreviousResponseID(rawBody); previousID != "" {
			return previousID
		}
	}
	return promptHash
}

func clientSessionHash(clientSessionID string) string {
	sum := sha256.Sum256([]byte(clientSessionHashDomain + clientSessionID))
	return clientSessionHashPrefix + hex.EncodeToString(sum[:])
}

func openAIResponsesPreviousResponseID(rawBody []byte) string {
	var req struct {
		PreviousResponseID string `json:"previous_response_id"`
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return ""
	}
	return req.PreviousResponseID
}

type DeliveryTracker = deliveryTracker

func NewDeliveryTracker(w http.ResponseWriter) *DeliveryTracker {
	return newDeliveryTracker(w)
}

func (w *deliveryTracker) Started() bool {
	return w.started()
}

func (w *deliveryTracker) StatusCode() int {
	return w.statusCode()
}

type deliveryTracker struct {
	http.ResponseWriter
	startedFlag bool
	status      int
}

func newDeliveryTracker(w http.ResponseWriter) *deliveryTracker {
	return &deliveryTracker{ResponseWriter: w}
}

func (w *deliveryTracker) WriteHeader(statusCode int) {
	if !w.startedFlag {
		w.startedFlag = true
		w.status = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *deliveryTracker) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && !w.startedFlag {
		w.startedFlag = true
		w.status = http.StatusOK
	}
	return n, err
}

func (w *deliveryTracker) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *deliveryTracker) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *deliveryTracker) started() bool {
	return w != nil && w.startedFlag
}

func (w *deliveryTracker) statusCode() int {
	if w == nil || w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func StreamingProviderRequestBody(env *proto.HCSF, family string) ([]byte, error) {
	return streamingProviderRequestBody(env, family)
}

func streamingProviderRequestBody(env *proto.HCSF, family string) ([]byte, error) {
	// 先归一到 marshal 形态族(kimi/qwen/... → openai_chat;openai_codex →
	// openai_responses;gemini_advanced_session → gemini_messages)。controls
	// 注入的字段形态(max_tokens vs max_output_tokens、gemini generationConfig)
	// 与 stream 字段注入方式都跟"形态"走;跟原始族名走会在跨协议流式
	// (anthropic→codex / openai→gemini_advanced 等)把 openai_chat 形态的
	// controls 注进 Responses/Gemini body。同形态直通路径不经过本函数
	// (needsStreamingHCSFTranslation fast-path),此处只服务真翻译路径。
	endpointFamily := family
	family = gateway.HCSFEndpointModelFamily(endpointFamily)
	body, err := gateway.MarshalToProviderRequest(env, endpointFamily)
	if err != nil {
		return nil, err
	}
	body, err = injectStreamingRequestControls(body, env, family)
	if err != nil {
		return nil, err
	}
	switch family {
	case "gemini_messages", "dify_chat", "ollama_native":
		// 这三族跳过 forceStreamingRequest:gemini 的流式开关由 endpoint 路径
		// (streamGenerateContent)决定,dify 由 body 内 response_mode 决定,
		// 两者注 openai 形 stream:true 即污染 body;ollama_native 的 stream
		// 字段已由 marshal 按 StreamPlan 显式写入(再注 true 虽幂等,但 stream
		// 字段的真相源必须唯一收敛在 marshal,禁止两处写)。
		return body, nil
	}
	return forceStreamingRequest(body)
}

func injectStreamingRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	if family == "dify_chat" {
		// Dify 无 per-request 控制参数(模型/采样在 app 侧配置),openai 形
		// controls 字段一律不可注入;被丢弃的 controls 已在 marshal 内记 loss。
		return raw, nil
	}
	if family == "ollama_native" {
		// Ollama 的采样控制已在 marshal 阶段嵌进 options{}(num_predict 等);
		// 顶层二次注入 openai 形 max_tokens/temperature 是协议污染。
		return raw, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if family == "gemini_messages" {
		return injectStreamingGeminiRequestControls(body, env)
	}
	c := env.RequestControls
	if c.MaxTokens != nil {
		if family == "openai_responses" {
			body["max_output_tokens"] = *c.MaxTokens
		} else {
			body["max_tokens"] = *c.MaxTokens
		}
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	if c.ParallelToolCalls != nil && family != "anthropic_messages" {
		body["parallel_tool_calls"] = *c.ParallelToolCalls
	}
	if len(c.StopSequences) > 0 && family == "anthropic_messages" {
		body["stop_sequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		body["stop"] = c.Stop
	} else if len(c.StopSequences) > 0 {
		body["stop"] = c.StopSequences
	}
	if len(c.ToolChoice) > 0 {
		body["tool_choice"] = streamingRawJSONValue(c.ToolChoice)
	}
	if len(c.Tools) > 0 {
		body["tools"] = streamingControlTools(family, c.Tools)
	}
	if c.ResponseFormat != nil {
		// D5 raw-passthrough:同非流式逻辑(hcsf_graph_marshal_helpers.go 的
		// injectRequestControls)。Schema 存的是 inbound 原始 response_format /
		// text 整体。同协议流式 marshal 1:1 还原;Chat response_format 投到
		// Responses 时改写为 text.format。两种路径都不能再包
		// {"type":"raw","schema":...} 让上游 4xx reject。
		if c.ResponseFormat.Type == "raw" && len(c.ResponseFormat.Schema) > 0 {
			switch family {
			case "openai_responses":
				if text, ok := gateway.OpenAIResponsesTextFromChatResponseFormatRaw(c.ResponseFormat.Schema); ok {
					body["text"] = text
				} else {
					body["text"] = streamingRawJSONValue(c.ResponseFormat.Schema)
				}
			case "openai_chat":
				body["response_format"] = streamingRawJSONValue(c.ResponseFormat.Schema)
			}
		} else {
			rf := map[string]any{"type": c.ResponseFormat.Type}
			if len(c.ResponseFormat.Schema) > 0 {
				rf["schema"] = streamingRawJSONValue(c.ResponseFormat.Schema)
			}
			if c.ResponseFormat.Strict != nil {
				rf["strict"] = *c.ResponseFormat.Strict
			}
			if family == "openai_responses" {
				body["text"] = map[string]any{"format": rf}
			} else if family == "openai_chat" {
				body["response_format"] = rf
			}
		}
	}
	mergeStreamingRequestPassthrough(body, env)
	return json.Marshal(body)
}

func mergeStreamingRequestPassthrough(body map[string]any, env *proto.HCSF) {
	if env == nil || env.Passthrough == nil || len(env.Passthrough.Extra) == 0 {
		return
	}
	for key, raw := range env.Passthrough.Extra {
		if _, exists := body[key]; exists {
			continue
		}
		body[key] = streamingRawJSONValue(raw)
	}
}
func injectStreamingGeminiRequestControls(body map[string]any, env *proto.HCSF) ([]byte, error) {
	c := env.RequestControls
	generation := map[string]any{}
	if existing, ok := body["generationConfig"].(map[string]any); ok {
		for k, v := range existing {
			generation[k] = v
		}
	}
	if c.MaxTokens != nil {
		generation["maxOutputTokens"] = *c.MaxTokens
	}
	if c.Temperature != nil {
		generation["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		generation["topP"] = *c.TopP
	}
	if len(c.StopSequences) > 0 {
		generation["stopSequences"] = c.StopSequences
	} else if len(c.Stop) > 0 {
		generation["stopSequences"] = c.Stop
	}
	if c.ResponseFormat != nil && len(c.ResponseFormat.Schema) > 0 {
		var raw map[string]any
		if json.Unmarshal(c.ResponseFormat.Schema, &raw) == nil {
			if v, ok := raw["responseMimeType"]; ok && v != "" {
				generation["responseMimeType"] = v
			}
			if v, ok := raw["responseSchema"]; ok {
				generation["responseSchema"] = v
			}
		}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	if len(c.Tools) > 0 {
		body["tools"] = streamingGeminiControlTools(c.Tools)
	}
	return json.Marshal(body)
}

func streamingGeminiControlTools(tools []proto.CanonicalTool) []any {
	decls := make([]any, 0, len(tools))
	for _, tool := range tools {
		decls = append(decls, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  streamingRawJSONValue(tool.InputSchema),
		})
	}
	if len(decls) == 0 {
		return nil
	}
	return []any{map[string]any{"functionDeclarations": decls}}
}

func forceStreamingRequest(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["stream"] = true
	return json.Marshal(body)
}

func streamingControlTools(family string, tools []proto.CanonicalTool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		schema := streamingRawJSONValue(tool.InputSchema)
		switch family {
		case "openai_chat":
			out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema}})
		case "openai_responses":
			out = append(out, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema})
		default:
			out = append(out, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
	}
	return out
}

func streamingRawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	return v
}
