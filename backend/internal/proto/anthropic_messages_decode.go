package proto

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Anthropic Messages API 的内部请求形状与解析辅助。
type anthropicMessagesRequest struct {
	Model             string                     `json:"model"`
	MaxTokens         *int                       `json:"max_tokens"`
	MaxTokensToSample *int                       `json:"max_tokens_to_sample,omitempty"`
	Messages          []anthropicMessage         `json:"messages"`
	System            json.RawMessage            `json:"system,omitempty"`
	Stream            *bool                      `json:"stream"`
	Temperature       *float64                   `json:"temperature"`
	TopP              *float64                   `json:"top_p"`
	TopK              json.RawMessage            `json:"top_k,omitempty"`
	StopSequences     []string                   `json:"stop_sequences,omitempty"`
	Tools             []json.RawMessage          `json:"tools,omitempty"`
	ToolChoice        json.RawMessage            `json:"tool_choice,omitempty"`
	Metadata          map[string]json.RawMessage `json:"metadata,omitempty"`
	Thinking          json.RawMessage            `json:"thinking,omitempty"`
	ContextManagement json.RawMessage            `json:"context_management,omitempty"`
	OutputConfig      json.RawMessage            `json:"output_config,omitempty"`
	OutputFormat      json.RawMessage            `json:"output_format,omitempty"`
	Container         json.RawMessage            `json:"container,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
	Raw          json.RawMessage        `json:"-"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      json.RawMessage        `json:"content,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// parseAnthropicSystemField 解 system 字段；string 直接返回；array of
// {type:text,text:...} 拼接成换行分隔字符串并记录信息级损失。
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
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(raw, &rawBlocks); err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content must be string or block array", msgIdx)
	}
	blocks := make([]anthropicContentBlock, 0, len(rawBlocks))
	for i, rawBlock := range rawBlocks {
		var block anthropicContentBlock
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] parse: %w", msgIdx, i, err)
		}
		block.Raw = append(json.RawMessage(nil), rawBlock...)
		blocks = append(blocks, block)
	}
	return blocks, nil, nil
}

// parseAnthropicTools 解顶层 tools 数组为 CanonicalTool 列表。
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
		out = append(out, CanonicalTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out, nil
}

// parseAnthropicToolResultContent 解 tool_result.content 字段。
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
func buildAnthropicImageNode(src *anthropicImageSource, mi, bi int) (*ImageNode, []ProtocolLossEntry, error) {
	if src.Type == "" {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source missing type", mi, bi)
	}
	var kind DataSourceKind
	var value string
	switch src.Type {
	case "base64":
		kind, value = DataSourceInlineBase64, src.Data
		if value == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.data required for base64", mi, bi)
		}
	case "url":
		kind, value = DataSourceURL, src.URL
		if value == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.url required for url type", mi, bi)
		}
	default:
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.type=%q not supported", mi, bi, src.Type)
	}
	if src.MediaType == "" {
		return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image.source.media_type required", mi, bi)
	}
	return &ImageNode{SourceKind: kind, MediaType: src.MediaType, Locator: DataLocator{Kind: kind, Value: value}}, nil, nil
}

// maybeEmitAnthropicCacheControl 把内容块的缓存标记投影为能力节点。
func maybeEmitAnthropicCacheControl(env *HCSF, cc *anthropicCacheControl, targetNodeID string, mi, bi int, nodeSeq *int, edgeSeq *int) {
	if cc == nil {
		return
	}
	*nodeSeq++
	cacheNodeID := fmt.Sprintf("n_cache_%d", *nodeSeq)
	env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
		ID:          cacheNodeID,
		Kind:        CapabilityCacheControl,
		StreamReady: StreamReadyYes,
		Source:      &NodeSourceRef{MessageIndex: &mi, BlockIndex: &bi},
		CacheControl: &CacheControlNode{
			Scope:                  CacheScopeBlock,
			BreakpointRefs:         []string{targetNodeID},
			SanitizeSystemMetadata: true,
		},
	})
	env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
		Capability: CapabilityCacheControl, NodeID: cacheNodeID, Verdict: ProjectionPreserved,
	})
}
