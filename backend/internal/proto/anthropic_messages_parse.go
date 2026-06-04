package proto

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 解析与构造 helper：parseAnthropicSystemField / parseAnthropicContent /
// parseAnthropicTools / parseAnthropicToolResultContent / buildAnthropicImageNode /
// maybeEmitAnthropicCacheControl。
//
// 这些是 RequestToCanonical 拆出来的"块级处理"函数，保持 anthropic_messages_request.go
// 主流程清晰；同时方便单测覆盖。

// parseAnthropicSystemField 解 system 字段；string 直接返回；array of
// {type:text,text:...} 拼接成 \n 分隔字符串并 emit info loss 提示。
func parseAnthropicSystemField(raw json.RawMessage) (string, []ProtocolLossEntry, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, fmt.Errorf("proto: anthropic_messages 'system' must be string or block array: %w", err)
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	loss, _ := NewClientLossEntry(ProtocolLossInfo, "system_block_array_concatenated", "system_array_to_string", CapabilityText, "")
	return strings.Join(parts, "\n"), []ProtocolLossEntry{loss}, nil
}

// parseAnthropicContent 解 message.content；string 视为单一 text block，array 原样返回。
func parseAnthropicContent(raw json.RawMessage, msgIdx int) ([]anthropicContentBlock, []ProtocolLossEntry, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []anthropicContentBlock{{Type: "text", Text: s}}, nil, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content must be string or block array", msgIdx)
	}
	return blocks, nil, nil
}

// parseAnthropicTools 解顶层 tools 数组为 CanonicalTool 列表。
// Anthropic tool 形态：{ name, description, input_schema }。
func parseAnthropicTools(rawTools []json.RawMessage) ([]CanonicalTool, error) {
	out := make([]CanonicalTool, 0, len(rawTools))
	for i, rt := range rawTools {
		var t struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		if err := json.Unmarshal(rt, &t); err != nil {
			return nil, fmt.Errorf("proto: anthropic_messages tools[%d] parse: %w", i, err)
		}
		if t.Name == "" {
			return nil, fmt.Errorf("proto: anthropic_messages tools[%d] missing 'name'", i)
		}
		out = append(out, CanonicalTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

// parseAnthropicToolResultContent 解 tool_result.content 字段；string 或 array of
// {type, text/source}。要求 ToolResultNode.Content 非 nil。
func parseAnthropicToolResultContent(raw json.RawMessage) ([]CanonicalContentBlock, []ProtocolLossEntry, error) {
	if len(raw) == 0 {
		return []CanonicalContentBlock{}, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []CanonicalContentBlock{{Type: "text", Text: s}}, nil, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, fmt.Errorf("tool_result.content must be string or block array: %w", err)
	}
	out := make([]CanonicalContentBlock, 0, len(blocks))
	var losses []ProtocolLossEntry
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, CanonicalContentBlock{Type: "text", Text: b.Text})
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "d1_tool_result_image_pending", "d1_pending_block_type", CapabilityToolResult, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_unknown_block_type:"+b.Type, "unknown_block_type", CapabilityToolResult, "")
			losses = append(losses, loss)
		}
	}
	return out, losses, nil
}

// buildAnthropicImageNode 把 Anthropic image source 转 ImageNode。
// source.type=base64 → SourceKind=inline_base64；source.type=url → SourceKind=url。
// media_type 必填；不识别 source.type 返回错误（不可 silent drop）。
func buildAnthropicImageNode(src *anthropicImageSource, mi, bi int) (*ImageNode, []ProtocolLossEntry, error) {
	if src.Type == "" {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source missing type", mi, bi)
	}
	var kind DataSourceKind
	var value string
	switch src.Type {
	case "base64":
		kind = DataSourceInlineBase64
		value = src.Data
		if value == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.data required for base64", mi, bi)
		}
	case "url":
		kind = DataSourceURL
		value = src.URL
		if value == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.url required for url type", mi, bi)
		}
	default:
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.type=%q not supported", mi, bi, src.Type)
	}
	if src.MediaType == "" {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.media_type required", mi, bi)
	}
	return &ImageNode{
		SourceKind: kind,
		MediaType:  src.MediaType,
		Locator:    DataLocator{Kind: kind, Value: value},
	}, nil, nil
}

// maybeEmitAnthropicCacheControl 当 content block 携带 cache_control marker 时，
// 新建一个 CacheControlNode（breakpoint ref 指向刚生成的内容节点）。
// edgeSeq / nodeSeq 走指针让调用方共享计数。
func maybeEmitAnthropicCacheControl(env *HCSF, cc *anthropicCacheControl, targetNodeID string, mi, bi int, nodeSeq *int, edgeSeq *int) {
	if cc == nil {
		return
	}
	*nodeSeq++
	cacheNodeID := fmt.Sprintf("n_cache_%d", *nodeSeq)
	scope := CacheScopeBlock
	env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
		ID:          cacheNodeID,
		Kind:        CapabilityCacheControl,
		StreamReady: StreamReadyYes,
		Source:      &NodeSourceRef{MessageIndex: &mi, BlockIndex: &bi},
		CacheControl: &CacheControlNode{
			Scope:                  scope,
			BreakpointRefs:         []string{targetNodeID},
			SanitizeSystemMetadata: true,
		},
	})
	env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
		Capability: CapabilityCacheControl, NodeID: cacheNodeID, Verdict: ProjectionPreserved,
	})
}
