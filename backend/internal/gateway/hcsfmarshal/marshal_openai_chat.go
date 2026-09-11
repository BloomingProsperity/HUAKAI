package hcsfmarshal

import (
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/thinkingnorm"
)

func marshalOpenAIChat(env *proto.HCSF) ([]byte, error) {
	if err := proto.RejectHostedToolsOnFamily("openai_chat", env.RequestControls.Tools); err != nil {
		return nil, err
	}
	body := map[string]any{"model": hcsfModel(env), "messages": []any{}, "stream": false}
	var messages []any
	pendingReasoning := ""
	flushReasoning := func(preferLastAssistant bool) {
		if pendingReasoning == "" {
			return
		}
		if preferLastAssistant && attachReasoningToLastAssistant(messages, pendingReasoning) {
			pendingReasoning = ""
			return
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": nil, "reasoning_content": pendingReasoning})
		pendingReasoning = ""
	}
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addMarshalLoss(env, "openai_chat", n, "text node missing payload", "missing_text_payload")
				continue
			}
			role := openAITextRole(n.Text.Role)
			if role != "assistant" {
				flushReasoning(true)
			}
			messages = append(messages, map[string]any{"role": role, "content": n.Text.Block.Text})
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addMarshalLoss(env, "openai_chat", n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			msg := map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{openAIChatToolCall(n.ToolUse)}}
			if pendingReasoning != "" {
				msg["reasoning_content"] = pendingReasoning
				pendingReasoning = ""
			}
			messages = append(messages, msg)
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addMarshalLoss(env, "openai_chat", n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			flushReasoning(true)
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": n.ToolResult.ToolCallID, "content": flattenContent(n.ToolResult.Content)})
		case proto.CapabilityImage:
			flushReasoning(true)
			if block, ok := openAIImagePart(env, "openai_chat", n); ok {
				messages = append(messages, map[string]any{"role": "user", "content": []any{block}})
			}
		case proto.CapabilityThinking:
			if isOpenAIChatRequestThinkingControl(n) {
				continue
			}
			if thinkingnorm.DropUnsignedHistory(env.RequestMeta.Provider, n) {
				addMarshalLoss(env, "openai_chat", n, "official family dropped unsigned thinking history", "official_unsigned_thinking_dropped")
				continue
			}
			if text := thinkingReplayText(n.Thinking); text != "" {
				pendingReasoning = text
			}
		case proto.CapabilityCacheControl:
			addMarshalLoss(env, "openai_chat", n, "capability not supported by OpenAI Chat request schema", "unsupported_capability")
		default:
			addMarshalLoss(env, "openai_chat", n, "capability unsupported by openai_chat marshal", "unsupported_capability")
		}
	}
	flushReasoning(true)
	body["messages"] = messages
	return json.Marshal(body)
}
