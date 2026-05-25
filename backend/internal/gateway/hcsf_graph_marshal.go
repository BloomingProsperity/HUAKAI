package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// MarshalToProviderRequest 把 HCSF graph 投影为目标 provider endpoint 的请求 body。
// 不支持的 capability 必须写 ProtocolLossEntry，禁止静默丢弃。
func MarshalToProviderRequest(env *proto.HCSF, endpointFamily string) ([]byte, error) {
	if env == nil {
		return nil, errors.New("gateway: nil HCSF envelope")
	}
	switch endpointFamily {
	case "anthropic_messages":
		return marshalAnthropicMessages(env)
	case "openai_chat":
		return marshalOpenAIChat(env)
	case "openai_responses":
		return marshalOpenAIResponses(env)
	default:
		return nil, fmt.Errorf("gateway: unsupported HCSF endpoint family %q", endpointFamily)
	}
}

func hcsfModel(env *proto.HCSF) string {
	return firstNonEmpty(env.RequestMeta.UpstreamModel, env.RequestMeta.Model)
}

func anthropicRole(role string) string {
	if role == "assistant" {
		return "assistant"
	}
	return "user"
}

func openAITextRole(role string) string {
	if role == "" {
		return "user"
	}
	return role
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	return v
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func isAnthropicRequestThinkingControl(n proto.CapabilityNode) bool {
	return n.Source != nil && n.Source.RequestField == "thinking"
}

func anthropicRequestThinkingControl(env *proto.HCSF, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Thinking == nil {
		addMarshalLoss(env, "anthropic_messages", n, "thinking request control node missing payload", "missing_thinking_payload")
		return nil, false
	}
	if n.Thinking.BudgetTokens <= 0 {
		addMarshalLoss(env, "anthropic_messages", n, "thinking request control missing budget_tokens", "missing_thinking_budget_tokens")
		return nil, false
	}
	// RequestToCanonical 只为顶层 thinking.type=enabled 建此节点。
	return map[string]any{"type": "enabled", "budget_tokens": n.Thinking.BudgetTokens}, true
}

func marshalAnthropicMessages(env *proto.HCSF) ([]byte, error) {
	body := map[string]any{"model": hcsfModel(env), "messages": []any{}, "stream": false}
	cache := cacheTargets(env)
	applied := map[string]bool{}
	var messages []any
	var system []map[string]any

	appendMsg := func(role string, block map[string]any) {
		if len(messages) == 0 {
			messages = append(messages, map[string]any{"role": role, "content": []any{block}})
			return
		}
		last := messages[len(messages)-1].(map[string]any)
		if last["role"] != role {
			messages = append(messages, map[string]any{"role": role, "content": []any{block}})
			return
		}
		last["content"] = append(last["content"].([]any), block)
	}

	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addMarshalLoss(env, "anthropic_messages", n, "text node missing payload", "missing_text_payload")
				continue
			}
			block := withCache(map[string]any{"type": "text", "text": n.Text.Block.Text}, n.ID, cache, applied)
			if n.Text.Role == "system" {
				system = append(system, block)
				continue
			}
			appendMsg(anthropicRole(n.Text.Role), block)
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addMarshalLoss(env, "anthropic_messages", n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			appendMsg("assistant", withCache(map[string]any{"type": "tool_use", "id": n.ToolUse.ToolCallID, "name": n.ToolUse.Name, "input": rawJSONValue(n.ToolUse.Input)}, n.ID, cache, applied))
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addMarshalLoss(env, "anthropic_messages", n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			block := map[string]any{"type": "tool_result", "tool_use_id": n.ToolResult.ToolCallID, "content": anthropicResultContent(env, n.ToolResult.Content)}
			if n.ToolResult.IsError {
				block["is_error"] = true
			}
			appendMsg("user", withCache(block, n.ID, cache, applied))
		case proto.CapabilityImage:
			block, ok := anthropicImageBlock(env, n)
			if ok {
				appendMsg("user", withCache(block, n.ID, cache, applied))
			}
		case proto.CapabilityThinking:
			if isAnthropicRequestThinkingControl(n) {
				if thinking, ok := anthropicRequestThinkingControl(env, n); ok {
					body["thinking"] = thinking
				}
				continue
			}
			for _, b := range thinkingBlocks(n.Thinking) {
				appendMsg("assistant", withCache(map[string]any{"type": "thinking", "thinking": b.Text}, n.ID, cache, applied))
			}
		case proto.CapabilityCacheControl:
			continue
		default:
			addMarshalLoss(env, "anthropic_messages", n, "capability unsupported by anthropic_messages marshal", "unsupported_capability")
		}
	}
	if len(system) == 0 && env.RequestControls.SystemPrompt != "" {
		body["system"] = env.RequestControls.SystemPrompt
	} else if len(system) == 1 {
		if _, ok := system[0]["cache_control"]; ok {
			body["system"] = []any{system[0]}
		} else {
			body["system"] = system[0]["text"]
		}
	} else if len(system) > 1 {
		arr := make([]any, 0, len(system))
		for _, b := range system {
			arr = append(arr, b)
		}
		body["system"] = arr
	}
	body["messages"] = messages
	emitUnappliedCacheLoss(env, "anthropic_messages", cache, applied)
	return json.Marshal(body)
}

func marshalOpenAIChat(env *proto.HCSF) ([]byte, error) {
	body := map[string]any{"model": hcsfModel(env), "messages": []any{}, "stream": false}
	var messages []any
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addMarshalLoss(env, "openai_chat", n, "text node missing payload", "missing_text_payload")
				continue
			}
			messages = append(messages, map[string]any{"role": openAITextRole(n.Text.Role), "content": n.Text.Block.Text})
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addMarshalLoss(env, "openai_chat", n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{openAIChatToolCall(n.ToolUse)}})
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addMarshalLoss(env, "openai_chat", n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": n.ToolResult.ToolCallID, "content": flattenContent(n.ToolResult.Content)})
		case proto.CapabilityImage:
			if block, ok := openAIImagePart(env, "openai_chat", n); ok {
				messages = append(messages, map[string]any{"role": "user", "content": []any{block}})
			}
		case proto.CapabilityThinking, proto.CapabilityCacheControl:
			addMarshalLoss(env, "openai_chat", n, "capability not supported by OpenAI Chat request schema", "unsupported_capability")
		default:
			addMarshalLoss(env, "openai_chat", n, "capability unsupported by openai_chat marshal", "unsupported_capability")
		}
	}
	body["messages"] = messages
	return json.Marshal(body)
}

func marshalOpenAIResponses(env *proto.HCSF) ([]byte, error) {
	body := map[string]any{"model": hcsfModel(env), "input": []any{}, "stream": false}
	var input []any
	var instructions []string
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addMarshalLoss(env, "openai_responses", n, "text node missing payload", "missing_text_payload")
				continue
			}
			if n.Text.Role == "system" {
				instructions = append(instructions, n.Text.Block.Text)
				continue
			}
			input = append(input, responseMessage(openAITextRole(n.Text.Role), []any{map[string]any{"type": "input_text", "text": n.Text.Block.Text}}))
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addMarshalLoss(env, "openai_responses", n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			input = append(input, map[string]any{"type": "function_call", "call_id": n.ToolUse.ToolCallID, "name": n.ToolUse.Name, "arguments": rawJSONString(n.ToolUse.Input)})
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addMarshalLoss(env, "openai_responses", n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": n.ToolResult.ToolCallID, "output": flattenContent(n.ToolResult.Content)})
		case proto.CapabilityImage:
			if block, ok := openAIImagePart(env, "openai_responses", n); ok {
				input = append(input, responseMessage("user", []any{block}))
			}
		case proto.CapabilityThinking:
			input = append(input, responsesThinkingItems(n.Thinking)...)
		case proto.CapabilityCacheControl:
			addMarshalLoss(env, "openai_responses", n, "cache_control has no OpenAI Responses request projection", "unsupported_cache_control")
		default:
			addMarshalLoss(env, "openai_responses", n, "capability unsupported by openai_responses marshal", "unsupported_capability")
		}
	}
	if env.RequestControls.SystemPrompt != "" {
		instructions = append([]string{env.RequestControls.SystemPrompt}, instructions...)
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n")
	}
	body["input"] = input
	mergeResponsesNative(env, body)
	return json.Marshal(body)
}

func addMarshalLoss(env *proto.HCSF, family string, n proto.CapabilityNode, reason, code string) {
	addMarshalLossRaw(env, family, n.Kind, n.ID, reason, code)
}

func addMarshalLossRaw(env *proto.HCSF, family string, cap proto.CapabilityKind, nodeID, reason, code string) {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, reason, code, cap, nodeID)
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = family
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}
