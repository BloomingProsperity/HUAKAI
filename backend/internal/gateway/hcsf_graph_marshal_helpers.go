package gateway

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func openAIChatToolCall(t *proto.ToolUseNode) map[string]any {
	return map[string]any{
		"id":   t.ToolCallID,
		"type": "function",
		"function": map[string]any{
			"name":      t.Name,
			"arguments": rawJSONString(t.Input),
		},
	}
}

func responseMessage(role string, content []any) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": content}
}

func anthropicResultContent(env *proto.HCSF, blocks []proto.CanonicalContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image":
			var src map[string]any
			if len(b.Image) > 0 && json.Unmarshal(b.Image, &src) == nil {
				out = append(out, map[string]any{"type": "image", "source": src})
			}
		default:
			addMarshalLossRaw(env, "anthropic_messages", proto.CapabilityToolResult, "", "tool_result content block unsupported by Anthropic projection", "unsupported_tool_result_content")
		}
	}
	return out
}

func flattenContent(blocks []proto.CanonicalContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			parts = append(parts, b.Text)
		default:
			if raw, err := json.Marshal(b); err == nil {
				parts = append(parts, string(raw))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func thinkingBlocks(t *proto.ThinkingNode) []proto.CanonicalContentBlock {
	if t == nil || (t.Redaction != "" && t.Redaction != proto.RedactionPublic) {
		return nil
	}
	return t.Blocks
}

func responsesThinkingItems(t *proto.ThinkingNode) []any {
	if t == nil || (t.Redaction != "" && t.Redaction != proto.RedactionPublic) {
		return nil
	}
	var items []any
	for _, b := range t.Blocks {
		if b.Text != "" {
			items = append(items, map[string]any{
				"type":    "reasoning",
				"summary": []any{map[string]any{"type": "summary_text", "text": b.Text}},
			})
		}
	}
	return items
}

func cacheTargets(env *proto.HCSF) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == proto.CapabilityCacheControl && n.CacheControl != nil {
			for _, ref := range n.CacheControl.BreakpointRefs {
				out[ref] = map[string]any{"type": "ephemeral"}
			}
		}
	}
	return out
}

func withCache(block map[string]any, nodeID string, targets map[string]map[string]any, applied map[string]bool) map[string]any {
	if cc, ok := targets[nodeID]; ok {
		block["cache_control"] = cc
		applied[nodeID] = true
	}
	return block
}

func emitUnappliedCacheLoss(env *proto.HCSF, family string, targets map[string]map[string]any, applied map[string]bool) {
	for ref := range targets {
		if !applied[ref] {
			addMarshalLossRaw(env, family, proto.CapabilityCacheControl, ref, "cache_control breakpoint target was not rendered", "cache_target_not_rendered")
		}
	}
}

func anthropicImageBlock(env *proto.HCSF, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Image == nil {
		addMarshalLoss(env, "anthropic_messages", n, "image node missing payload", "missing_image_payload")
		return nil, false
	}
	switch n.Image.SourceKind {
	case proto.DataSourceInlineBase64:
		return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": n.Image.MediaType, "data": n.Image.Locator.Value}}, true
	case proto.DataSourceURL:
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": n.Image.Locator.Value}}, true
	default:
		addMarshalLoss(env, "anthropic_messages", n, "image source kind unsupported by Anthropic Messages", "unsupported_image_source")
		return nil, false
	}
}

func openAIImagePart(env *proto.HCSF, family string, n proto.CapabilityNode) (map[string]any, bool) {
	if n.Image == nil {
		addMarshalLoss(env, family, n, "image node missing payload", "missing_image_payload")
		return nil, false
	}
	imageURL := n.Image.Locator.Value
	if n.Image.SourceKind == proto.DataSourceInlineBase64 {
		imageURL = "data:" + n.Image.MediaType + ";base64," + n.Image.Locator.Value
	} else if n.Image.SourceKind != proto.DataSourceURL {
		addMarshalLoss(env, family, n, "image source kind unsupported by provider request schema", "unsupported_image_source")
		return nil, false
	}
	if family == "openai_chat" {
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}}, true
	}
	return map[string]any{"type": "input_image", "image_url": imageURL}, true
}

func mergeResponsesNative(env *proto.HCSF, body map[string]any) {
	if env.Extensions == nil {
		return
	}
	raw := env.Extensions["openai_responses:native_body"]
	if len(raw) == 0 {
		return
	}
	var native map[string]any
	if json.Unmarshal(raw, &native) != nil {
		return
	}
	for k, v := range native {
		body[k] = v
	}
}

func injectRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
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
		body["tool_choice"] = rawJSONValue(c.ToolChoice)
	}
	if len(c.Tools) > 0 {
		body["tools"] = renderControlTools(family, c.Tools)
	}
	if c.ResponseFormat != nil {
		rf := map[string]any{"type": c.ResponseFormat.Type}
		if len(c.ResponseFormat.Schema) > 0 {
			rf["schema"] = rawJSONValue(c.ResponseFormat.Schema)
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
	return json.Marshal(body)
}

func renderControlTools(family string, tools []proto.CanonicalTool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := rawJSONValue(t.InputSchema)
		switch family {
		case "openai_chat":
			out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": schema}})
		case "openai_responses":
			out = append(out, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": schema})
		default:
			out = append(out, map[string]any{"name": t.Name, "description": t.Description, "input_schema": schema})
		}
	}
	return out
}
