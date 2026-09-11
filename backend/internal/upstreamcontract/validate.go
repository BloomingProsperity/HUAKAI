package upstreamcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

var (
	// ErrUnsupportedSampling 表示该模型不接受自定义采样参数。
	ErrUnsupportedSampling = errors.New("upstreamcontract: model rejects custom sampling")
	// ErrUnsupportedEffortNone 表示该模型不接受关闭推理。
	ErrUnsupportedEffortNone = errors.New("upstreamcontract: model rejects reasoning effort none")
	// ErrToolsRequireResponses 表示该模型的工具调用只能走 Responses 入口。
	ErrToolsRequireResponses = errors.New("upstreamcontract: tool calling requires responses ingress")
	// ErrAdaptiveThinkingLocked 表示该模型只能使用自适应思考。
	ErrAdaptiveThinkingLocked = errors.New("upstreamcontract: model requires adaptive thinking")
	// ErrForcedToolUse 表示该模型拒绝强制选工具。
	ErrForcedToolUse = errors.New("upstreamcontract: model rejects forced tool choice")
	// ErrAssistantPrefill 表示该模型拒绝助手预填。
	ErrAssistantPrefill = errors.New("upstreamcontract: model rejects assistant prefill")
)

// Rejection 是可对外展示的合同拒绝。Error() 只含稳定说明,不含包前缀。
type Rejection struct {
	Kind    error
	Message string
}

func (r Rejection) Error() string {
	if strings.TrimSpace(r.Message) != "" {
		return r.Message
	}
	return r.Kind.Error()
}

func (r Rejection) Unwrap() error {
	return r.Kind
}

func reject(kind error, format string, args ...any) error {
	return Rejection{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Validate 在选号与出站前核对官方请求合同。未知模型原样放行；
// 只有已确认的前沿族才施加拒绝。解析失败不在这里判非法，留给入口 JSON 门。
func Validate(ingress proto.ClientProtocol, models []string, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	for _, model := range models {
		if isGPT6Astra(model) {
			if err := validateGPT6Astra(ingress, root); err != nil {
				return err
			}
		}
		if isClaudeFableAlwaysOn(model) {
			if err := validateClaudeAlwaysOnThinking(root); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGPT6Astra(ingress proto.ClientProtocol, root map[string]json.RawMessage) error {
	if effortIsNone(root) {
		return reject(ErrUnsupportedEffortNone, "gpt-6-astra does not support reasoning effort none")
	}
	for _, key := range []string{"temperature", "top_p", "logprobs", "top_logprobs"} {
		if _, ok := root[key]; ok {
			return reject(ErrUnsupportedSampling, "gpt-6-astra does not accept %s", key)
		}
	}
	if ingress == proto.ClientProtocolOpenAIChat && requestDeclaresTools(root) {
		return reject(ErrToolsRequireResponses, "gpt-6-astra tool calling must use /v1/responses")
	}
	return nil
}

func validateClaudeAlwaysOnThinking(root map[string]json.RawMessage) error {
	if raw, ok := root["thinking"]; ok {
		var thinking map[string]json.RawMessage
		if err := json.Unmarshal(raw, &thinking); err == nil {
			var kind string
			_ = json.Unmarshal(thinking["type"], &kind)
			kind = strings.ToLower(strings.TrimSpace(kind))
			if kind == "disabled" {
				return reject(ErrAdaptiveThinkingLocked, "this model does not support thinking.type=disabled")
			}
			if kind == "enabled" {
				return reject(ErrAdaptiveThinkingLocked, "this model does not support a manual thinking budget")
			}
		}
	}
	if forcedToolChoice(root["tool_choice"]) {
		return reject(ErrForcedToolUse, "this model does not support forced tool_choice")
	}
	if lastMessageIsAssistant(root["messages"]) {
		return reject(ErrAssistantPrefill, "this model does not support assistant prefill")
	}
	return nil
}

func effortIsNone(root map[string]json.RawMessage) bool {
	if raw, ok := root["reasoning_effort"]; ok && jsonStringEquals(raw, "none") {
		return true
	}
	raw, ok := root["reasoning"]
	if !ok {
		return false
	}
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return false
	}
	return jsonStringEquals(reasoning["effort"], "none")
}

func requestDeclaresTools(root map[string]json.RawMessage) bool {
	return jsonArrayLen(root["tools"]) > 0 || jsonArrayLen(root["functions"]) > 0
}

func forcedToolChoice(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		switch strings.ToLower(strings.TrimSpace(asString)) {
		case "any", "required", "tool":
			return true
		default:
			return false
		}
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	var kind string
	_ = json.Unmarshal(obj["type"], &kind)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "any", "required", "tool":
		return true
	default:
		return false
	}
}

func lastMessageIsAssistant(raw json.RawMessage) bool {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return false
	}
	var role string
	_ = json.Unmarshal(messages[len(messages)-1]["role"], &role)
	return strings.EqualFold(strings.TrimSpace(role), "assistant")
}

func jsonStringEquals(raw json.RawMessage, want string) bool {
	var got string
	if json.Unmarshal(raw, &got) != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(got), want)
}

func jsonArrayLen(raw json.RawMessage) int {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return 0
	}
	return len(arr)
}
