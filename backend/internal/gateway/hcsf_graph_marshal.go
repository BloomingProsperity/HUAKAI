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
	case "gemini_messages":
		return marshalGeminiMessages(env)
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

func isOpenAIChatRequestThinkingControl(n proto.CapabilityNode) bool {
	return n.Source != nil && n.Source.RequestField == "reasoning_effort"
}

func anthropicRequestThinkingControl(env *proto.HCSF, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Thinking == nil {
		addMarshalLoss(env, "anthropic_messages", n, "thinking request control node missing payload", "missing_thinking_payload")
		return nil, false
	}
	// adaptive(claude-fable-5 / opus-4.7+ always-on thinking)无 budget_tokens：
	// 回写 {type:"adaptive"}，绝不要求 budget>0(否则 fable-5 thinking 被丢)。
	if n.Thinking.Mode == "adaptive" {
		return map[string]any{"type": "adaptive"}, true
	}
	if n.Thinking.BudgetTokens <= 0 {
		addMarshalLoss(env, "anthropic_messages", n, "thinking request control missing budget_tokens", "missing_thinking_budget_tokens")
		return nil, false
	}
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
				block := map[string]any{"type": "thinking", "thinking": firstNonEmpty(b.Thinking, b.Text, b.ReasoningSummary)}
				if sig := firstNonEmpty(b.Signature, n.Thinking.Signature); sig != "" {
					block["signature"] = sig
				}
				appendMsg("assistant", withCache(block, n.ID, cache, applied))
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
		case proto.CapabilityThinking:
			if isOpenAIChatRequestThinkingControl(n) {
				continue
			}
			addMarshalLoss(env, "openai_chat", n, "capability not supported by OpenAI Chat request schema", "unsupported_capability")
		case proto.CapabilityCacheControl:
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

func marshalGeminiMessages(env *proto.HCSF) ([]byte, error) {
	body := map[string]any{"contents": []any{}}
	if len(env.CapabilityGraph.Nodes) == 0 {
		return marshalGeminiMessagesFromLegacyMessages(env, body)
	}

	contents := []any{}
	systemParts := []any{}
	generation := geminiGenerationConfigFromControls(env)
	toolNames := geminiToolNames(env)

	appendContent := func(role string, part map[string]any) {
		if len(part) == 0 {
			return
		}
		if len(contents) > 0 {
			last := contents[len(contents)-1].(map[string]any)
			if last["role"] == role {
				last["parts"] = append(last["parts"].([]any), part)
				return
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": []any{part}})
	}

	if env.RequestControls.SystemPrompt != "" {
		systemParts = append(systemParts, map[string]any{"text": env.RequestControls.SystemPrompt})
	}
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addMarshalLoss(env, "gemini_messages", n, "text node missing payload", "missing_text_payload")
				continue
			}
			part := map[string]any{"text": n.Text.Block.Text}
			if n.Text.Role == "system" {
				systemParts = append(systemParts, part)
				continue
			}
			appendContent(geminiRole(n.Text.Role), part)
		case proto.CapabilityToolUse:
			if n.ToolUse == nil {
				addMarshalLoss(env, "gemini_messages", n, "tool_use node missing payload", "missing_tool_use_payload")
				continue
			}
			appendContent("model", geminiToolUsePart(n.ToolUse))
		case proto.CapabilityToolResult:
			if n.ToolResult == nil {
				addMarshalLoss(env, "gemini_messages", n, "tool_result node missing payload", "missing_tool_result_payload")
				continue
			}
			appendContent("user", geminiToolResultPart(env, n, toolNames))
		case proto.CapabilityImage:
			if part, ok := geminiImagePart(env, n); ok {
				appendContent("user", part)
			}
		case proto.CapabilityThinking:
			geminiApplyThinkingNode(env, n, generation)
		case proto.CapabilityCacheControl:
			addMarshalInfoLoss(env, "gemini_messages", n, "cache_control has no Gemini request projection", "unsupported_cache_control")
		default:
			addMarshalInfoLoss(env, "gemini_messages", n, "capability unsupported by gemini_messages marshal", "unsupported_capability")
		}
	}

	if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	body["contents"] = contents
	return json.Marshal(body)
}

func marshalGeminiMessagesFromLegacyMessages(env *proto.HCSF, body map[string]any) ([]byte, error) {
	contents := []any{}
	for mi, msg := range env.Messages {
		role := geminiRole(msg.Role)
		var parts []any
		for bi, block := range msg.Content {
			part, ok := geminiPartFromCanonicalBlock(env, block)
			if !ok {
				addMarshalLossRaw(env, "gemini_messages", capabilityFromBlockType(block.Type), "", fmt.Sprintf("canonical messages[%d].content[%d] unsupported by Gemini request schema", mi, bi), "unsupported_gemini_request_block")
				continue
			}
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	if env.RequestControls.SystemPrompt != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": env.RequestControls.SystemPrompt}},
		}
	}
	if generation := geminiGenerationConfigFromControls(env); len(generation) > 0 {
		body["generationConfig"] = generation
	}
	body["contents"] = contents
	return json.Marshal(body)
}

func geminiRole(role string) string {
	if role == "assistant" {
		return "model"
	}
	return "user"
}

func geminiPartFromCanonicalBlock(env *proto.HCSF, block proto.CanonicalContentBlock) (map[string]any, bool) {
	switch block.Type {
	case "text":
		return map[string]any{"text": block.Text}, true
	case "image":
		if len(block.Image) == 0 {
			addMarshalLossRaw(env, "gemini_messages", proto.CapabilityImage, "", "image block missing inlineData payload", "missing_gemini_inline_data")
			return nil, false
		}
		return map[string]any{"inlineData": rawJSONValue(block.Image)}, true
	case "tool_use":
		call := map[string]any{
			"name": block.Name,
			"args": rawJSONValue(block.Input),
		}
		if block.CallID != "" {
			call["id"] = block.CallID
		}
		return map[string]any{"functionCall": call}, true
	default:
		return nil, false
	}
}

func geminiGenerationConfigFromControls(env *proto.HCSF) map[string]any {
	c := env.RequestControls
	generation := map[string]any{}
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
	return generation
}

func geminiToolNames(env *proto.HCSF) map[string]string {
	names := map[string]string{}
	for _, n := range env.CapabilityGraph.Nodes {
		if n.ToolUse == nil {
			continue
		}
		if n.ToolUse.ToolCallID != "" {
			names[n.ToolUse.ToolCallID] = n.ToolUse.Name
		}
		if n.ToolUse.OriginalToolCallID != "" {
			names[n.ToolUse.OriginalToolCallID] = n.ToolUse.Name
		}
	}
	return names
}

func geminiToolUsePart(t *proto.ToolUseNode) map[string]any {
	call := map[string]any{
		"name": t.Name,
		"args": rawJSONValue(t.Input),
	}
	if t.ToolCallID != "" {
		call["id"] = t.ToolCallID
	}
	return map[string]any{"functionCall": call}
}

func geminiToolResultPart(env *proto.HCSF, n proto.CapabilityNode, toolNames map[string]string) map[string]any {
	name := toolNames[n.ToolResult.ToolCallID]
	if name == "" {
		name = n.ToolResult.ToolCallID
		addMarshalInfoLoss(env, "gemini_messages", n, "tool_result has no matching tool_use name; using call id as Gemini functionResponse name", "missing_tool_result_name")
	}
	response := map[string]any{"content": flattenContent(n.ToolResult.Content)}
	if n.ToolResult.IsError {
		response["isError"] = true
	}
	return map[string]any{"functionResponse": map[string]any{
		"name":     name,
		"response": response,
	}}
}

func geminiImagePart(env *proto.HCSF, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Image == nil {
		addMarshalLoss(env, "gemini_messages", n, "image node missing payload", "missing_image_payload")
		return nil, false
	}
	if n.Image.SourceKind != proto.DataSourceInlineBase64 {
		addMarshalInfoLoss(env, "gemini_messages", n, "image source kind unsupported by Gemini inlineData projection", "unsupported_image_source")
		return nil, false
	}
	if n.Image.MediaType == "" || n.Image.Locator.Value == "" {
		addMarshalLoss(env, "gemini_messages", n, "image inlineData missing mimeType or data", "missing_gemini_inline_data")
		return nil, false
	}
	return map[string]any{"inlineData": map[string]any{
		"mimeType": n.Image.MediaType,
		"data":     n.Image.Locator.Value,
	}}, true
}

func geminiApplyThinkingNode(env *proto.HCSF, n proto.CapabilityNode, generation map[string]any) {
	if n.Thinking == nil {
		addMarshalLoss(env, "gemini_messages", n, "thinking node missing payload", "missing_thinking_payload")
		return
	}
	if n.Thinking.BudgetTokens > 0 {
		if existing, ok := generation["thinkingConfig"].(map[string]any); ok {
			if existing["thinkingBudget"] != n.Thinking.BudgetTokens {
				addMarshalInfoLoss(env, "gemini_messages", n, "multiple thinking budgets projected; later Gemini budget overwrote earlier budget", "multiple_thinking_budgets")
			}
			existing["thinkingBudget"] = n.Thinking.BudgetTokens
		} else {
			generation["thinkingConfig"] = map[string]any{"thinkingBudget": n.Thinking.BudgetTokens}
		}
	}
	for _, b := range n.Thinking.Blocks {
		if firstNonEmpty(b.Text, b.Thinking, b.ReasoningSummary, string(b.Data)) != "" {
			addMarshalInfoLoss(env, "gemini_messages", n, "thinking content block has no Gemini request projection", "thinking_content_unprojected")
			return
		}
	}
}

func capabilityFromBlockType(blockType string) proto.CapabilityKind {
	switch blockType {
	case "text":
		return proto.CapabilityText
	case "image":
		return proto.CapabilityImage
	case "tool_use":
		return proto.CapabilityToolUse
	case "tool_result":
		return proto.CapabilityToolResult
	case "reasoning", "reasoning_summary":
		return proto.CapabilityThinking
	default:
		return ""
	}
}

func addMarshalLoss(env *proto.HCSF, family string, n proto.CapabilityNode, reason, code string) {
	addMarshalLossRaw(env, family, n.Kind, n.ID, reason, code)
}

func addMarshalLossRaw(env *proto.HCSF, family string, cap proto.CapabilityKind, nodeID, reason, code string) {
	addMarshalLossRawWithSeverity(env, proto.ProtocolLossWarning, family, cap, nodeID, reason, code)
}

func addMarshalInfoLoss(env *proto.HCSF, family string, n proto.CapabilityNode, reason, code string) {
	addMarshalLossRawWithSeverity(env, proto.ProtocolLossInfo, family, n.Kind, n.ID, reason, code)
}

func addMarshalLossRawWithSeverity(env *proto.HCSF, severity proto.ProtocolLossSeverity, family string, cap proto.CapabilityKind, nodeID, reason, code string) {
	loss, _ := proto.NewClientLossEntry(severity, reason, code, cap, nodeID)
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = family
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}
