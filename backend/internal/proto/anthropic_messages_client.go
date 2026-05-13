package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// P-2 D1 anthropic_messages ClientAdapter — text + system + tool_use + tool_result。
//
// 范围（按 P-2 synthesis §5.1 + Q4 决策 B：proto + forwarder 优先，HTTP route 后置）：
//   - 入：Anthropic Messages API v1 request body（JSON）
//   - 出：HCSF v0.4 request envelope，含 RequestMeta + RequestControls + Messages
//     + CapabilityGraph（text / tool_use / tool_result 节点 + requires 边） +
//     ProviderProjection client baseline。
//   - image / thinking / cache_control 在 D1.x 后续小片实现；本片遇到这些 type
//     触发 v0.4 ProtocolLossEntry（warning，非 silent drop，INV-7）。
//
// 其它 3 个 hookpoint（CanonicalToClientResponse / CanonicalEventToClientChunk /
// FinalizeClientStream）落在 D2/D3/D4，本文件返回 ErrNotImplemented stub，避免
// silent fallback。

// AnthropicMessagesClient 实现 ClientAdapter 接口；零值即可使用。
// 注册到 ClientAdapterRegistry：reg.Register(ClientProtocolAnthropicMessages, &AnthropicMessagesClient{}).
type AnthropicMessagesClient struct{}

// 编译期检查接口实现。
var _ ClientAdapter = (*AnthropicMessagesClient)(nil)

// anthropicMessagesRequest 是 D1 需要的 Anthropic Messages body 子集；
// tools / tool_choice / metadata / thinking 暂保留 RawMessage，D1.x 解析。
type anthropicMessagesRequest struct {
	Model         string                     `json:"model"`
	MaxTokens     *int                       `json:"max_tokens"`
	Messages      []anthropicMessage         `json:"messages"`
	System        json.RawMessage            `json:"system,omitempty"`
	Stream        *bool                      `json:"stream"`
	Temperature   *float64                   `json:"temperature"`
	TopP          *float64                   `json:"top_p"`
	StopSequences []string                   `json:"stop_sequences,omitempty"`
	Tools         []json.RawMessage          `json:"tools,omitempty"`
	ToolChoice    json.RawMessage            `json:"tool_choice,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
	Thinking      json.RawMessage            `json:"thinking,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthropicContentBlock 覆盖 Anthropic content array 全部 type；D1 仅 text 字段被用。
type anthropicContentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	// tool_use 字段（D1.x）
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result 字段（D1.x）
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	// image / thinking 等 D1.x 字段保留 RawMessage 提示用，本片不解。
	Source json.RawMessage `json:"source,omitempty"`
}

// RequestToCanonical 把 Anthropic Messages body 解为 HCSF v0.4 request envelope。
//
// 必要前置：调用方在 context 中通过 ContextWithRequestMetaSeed 注入
// RequestID / ClientProtocol / ProtocolFamily / IngressPath 等 meta；否则返回
// ErrMissingRequestMetaSeed（synthesis Q5 决策 A）。
func (a *AnthropicMessagesClient) RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	seed, ok := RequestMetaSeedFromContext(ctx)
	if !ok {
		return nil, nil, ErrMissingRequestMetaSeed
	}

	var req anthropicMessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages parse body: %w", err)
	}
	if req.Model == "" {
		return nil, nil, errors.New("proto: anthropic_messages missing required field 'model'")
	}
	if len(req.Messages) == 0 {
		return nil, nil, errors.New("proto: anthropic_messages 'messages' must not be empty")
	}

	env := NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = req.Model

	// RequestControls
	env.RequestControls.MaxTokens = req.MaxTokens
	env.RequestControls.Temperature = req.Temperature
	env.RequestControls.TopP = req.TopP
	if len(req.StopSequences) > 0 {
		env.RequestControls.StopSequences = req.StopSequences
	}
	if len(req.ToolChoice) > 0 {
		env.RequestControls.ToolChoice = req.ToolChoice
	}
	if len(req.Tools) > 0 {
		tools, err := parseAnthropicTools(req.Tools)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Tools = tools
	}

	var losses []ProtocolLossEntry

	// 解 system 字段（string 或 block array）
	if len(req.System) > 0 {
		sysStr, sysLoss, err := parseAnthropicSystemField(req.System)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.SystemPrompt = sysStr
		losses = append(losses, sysLoss...)
	}

	// StreamPlan
	if req.Stream != nil && *req.Stream {
		env.StreamPlan.Mode = StreamModeStreaming
	}

	// Messages + CapabilityGraph
	// toolCallIDToNodeID 记录 tool_use 节点 → tool_use canonical id 映射，便于
	// 后续 tool_result 块 emit requires 边（INV-19）。
	toolCallIDToNodeID := make(map[string]string)
	nodeSeq := 0
	edgeSeq := 0
	for mi, m := range req.Messages {
		if m.Role == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d] missing role", mi)
		}
		cm := CanonicalMessage{Role: m.Role}

		blocks, blockLoss, err := parseAnthropicContent(m.Content, mi)
		if err != nil {
			return nil, nil, err
		}
		losses = append(losses, blockLoss...)

		for bi, b := range blocks {
			msgIdx := mi
			blkIdx := bi
			switch b.Type {
			case "text":
				cm.Content = append(cm.Content, CanonicalContentBlock{Type: "text", Text: b.Text})
				nodeSeq++
				nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityText,
					StreamReady: StreamReadyYes,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					Text:        &TextNode{Role: m.Role, Block: CanonicalContentBlock{Type: "text", Text: b.Text}},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityText, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_use missing id or name", mi, bi)
				}
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				nodeSeq++
				nodeID := fmt.Sprintf("n_tool_use_%d", nodeSeq)
				cm.Content = append(cm.Content, CanonicalContentBlock{
					Type: "tool_use", CallID: b.ID, Name: b.Name, Input: input,
				})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityToolUse,
					StreamReady: StreamReadyPartial,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					ToolUse: &ToolUseNode{
						ToolCallID: b.ID,
						Name:       b.Name,
						Input:      input,
						Status:     ToolNodeComplete,
					},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityToolUse, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				toolCallIDToNodeID[b.ID] = nodeID
			case "tool_result":
				if b.ToolUseID == "" {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_result missing tool_use_id", mi, bi)
				}
				resultBlocks, contentLoss, err := parseAnthropicToolResultContent(b.Content)
				if err != nil {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_result: %w", mi, bi, err)
				}
				losses = append(losses, contentLoss...)
				nodeSeq++
				nodeID := fmt.Sprintf("n_tool_result_%d", nodeSeq)
				rawResult, _ := json.Marshal(resultBlocks)
				cm.Content = append(cm.Content, CanonicalContentBlock{
					Type:       "tool_result",
					CallID:     b.ToolUseID,
					ToolResult: rawResult,
				})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityToolResult,
					StreamReady: StreamReadyYes,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					ToolResult: &ToolResultNode{
						ToolCallID: b.ToolUseID,
						Content:    resultBlocks,
						Status:     ToolNodeComplete,
						IsError:    false,
					},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityToolResult, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				// emit requires 边：tool_result → tool_use（INV-19）
				fromNodeID, ok := toolCallIDToNodeID[b.ToolUseID]
				if !ok {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d] tool_result references unknown tool_use_id=%q", mi, b.ToolUseID)
				}
				edgeSeq++
				env.CapabilityGraph.Edges = append(env.CapabilityGraph.Edges, CapabilityEdge{
					ID:       fmt.Sprintf("e_req_%d", edgeSeq),
					Type:     EdgeRequires,
					From:     nodeID,
					To:       fromNodeID,
					Required: true,
					Reason:   "tool_result requires originating tool_use (INV-19)",
				})
			case "image", "thinking":
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "d1_block_type_pending:"+b.Type, "d1_pending_block_type", CapabilityText, "")
				losses = append(losses, loss)
			default:
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "anthropic_unknown_block_type:"+b.Type, "unknown_block_type", CapabilityText, "")
				losses = append(losses, loss)
			}
		}
		env.Messages = append(env.Messages, cm)
	}

	// system 也 emit CapabilityText 节点（role=system），方便 graph-level 检索。
	if env.RequestControls.SystemPrompt != "" {
		nodeSeq++
		nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
		env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
			ID:          nodeID,
			Kind:        CapabilityText,
			StreamReady: StreamReadyYes,
			Source:      &NodeSourceRef{RequestField: "system"},
			Text: &TextNode{
				Role:  "system",
				Block: CanonicalContentBlock{Type: "text", Text: env.RequestControls.SystemPrompt},
			},
		})
		env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
			Capability: CapabilityText,
			NodeID:     nodeID,
			Verdict:    ProjectionPreserved,
		})
	}

	// 后续 D1.x 字段（thinking / metadata）尚未实现；如果 body 携带，emit info/warning
	// loss 提示，不中断（INV-7 反 silent drop）。tools / tool_choice 本片已支持。
	if len(req.Thinking) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "d1_thinking_not_yet_implemented", "d1_pending", CapabilityThinking, "")
		losses = append(losses, loss)
	}
	if len(req.Metadata) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "d1_metadata_not_yet_implemented", "d1_pending", "", "")
		losses = append(losses, loss)
	}

	return env, losses, nil
}

// parseAnthropicSystemField 解 system 字段；string 直接返回；array of {type:text,text:...}
// 拼接成 \n 分隔字符串并 emit info loss 提示。
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

// parseAnthropicContent 解 message.content 字段；string 视为单一 text block；
// array of blocks 原样返回（调用方 switch 分发 type）。
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
// Anthropic tool 形态：{ name: "...", description: "...", input_schema: {...} }。
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

// parseAnthropicToolResultContent 解 tool_result.content 字段；可以是 string 或
// array of { type, text/source }。返回 CanonicalContentBlock 数组（用于
// ToolResultNode.Content 字段；INV-18 要求非 nil）。
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

// anthropicMessagesResponse 是 Anthropic Messages API response 的最小映射；
// 字段顺序按官方 docs 排：id / type / role / content / model / stop_reason /
// stop_sequence / usage。
type anthropicMessagesResponse struct {
	ID           string                            `json:"id"`
	Type         string                            `json:"type"`
	Role         string                            `json:"role"`
	Content      []anthropicResponseContentBlock   `json:"content"`
	Model        string                            `json:"model"`
	StopReason   *string                           `json:"stop_reason"`
	StopSequence *string                           `json:"stop_sequence"`
	Usage        anthropicResponseUsage            `json:"usage"`
}

type anthropicResponseContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicResponseUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// canonicalToAnthropicStopReason 映射 canonical stop reason → Anthropic stop_reason；
// 不能表达时返回 nil + warning loss（INV-7 反 silent drop）。
func canonicalToAnthropicStopReason(c CanonicalStopReason) (*string, []ProtocolLossEntry) {
	switch c {
	case CanonicalStopEndTurn:
		s := "end_turn"
		return &s, nil
	case CanonicalStopMaxTokens:
		s := "max_tokens"
		return &s, nil
	case CanonicalStopSequence:
		s := "stop_sequence"
		return &s, nil
	case CanonicalStopToolUse:
		s := "tool_use"
		return &s, nil
	case CanonicalStopRefusal:
		// Anthropic Messages API 现没有 "refusal" 终态；映射为 end_turn + warning。
		s := "end_turn"
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_refusal_downgraded_to_end_turn", "anthropic_no_refusal_state", "", "")
		return &s, []ProtocolLossEntry{loss}
	case CanonicalStopUnknown, "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unknown", "stop_reason_unknown", "", "")
		return nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unmapped:"+string(c), "stop_reason_unmapped", "", "")
		return nil, []ProtocolLossEntry{loss}
	}
}

// CanonicalToClientResponse 把 HCSF buffered envelope 序列化为 Anthropic Messages
// response JSON（D2 第一片：text + tool_use + usage + stop_reason）。
//
// 输入约束：canonical 必须是 buffered envelope（BufferedResponse != nil；INV-6）。
func (a *AnthropicMessagesClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: anthropic_messages CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: anthropic_messages CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	// content blocks
	contentOut := make([]anthropicResponseContentBlock, 0, len(resp.Content))
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			contentOut = append(contentOut, anthropicResponseContentBlock{
				Type: "text", Text: b.Text,
			})
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: anthropic_messages CanonicalToClientResponse content[%d] tool_use missing call_id or name", i)
			}
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			contentOut = append(contentOut, anthropicResponseContentBlock{
				Type: "tool_use", ID: b.CallID, Name: b.Name, Input: input,
			})
		case "tool_result":
			// Assistant response 中不应出现 tool_result（属于 user 角色）；emit warning。
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			// CanonicalContentBlock 没专门字段，但 ReasoningSummary 可能填；D1.x thinking。
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d1_pending", "d1_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d1_pending", "d1_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}

	// stop_reason 映射
	stopReason, stopLoss := canonicalToAnthropicStopReason(resp.StopReason)
	losses = append(losses, stopLoss...)

	out := anthropicMessagesResponse{
		ID:           resp.ID,
		Type:         "message",
		Role:         "assistant",
		Content:      contentOut,
		Model:        resp.Model,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage: anthropicResponseUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		},
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages marshal response: %w", err)
	}
	return body, losses, nil
}

// ----------------------------------------------------------------------------
// D3 / D4 streaming
// ----------------------------------------------------------------------------

// AnthropicMessagesStreamState 是 anthropic_messages client adapter 的 per-stream
// 状态；forwarder 在 stream 起点用 NewAnthropicMessagesStreamState 初始化，逐事件
// 传入 CanonicalEventToClientChunk / FinalizeClientStream。
//
// 设计：
//   - Started 标记 message_start 是否已 emit；二次重发被吞掉（INV idempotent）。
//   - Terminated 标记 message_stop 是否已 emit；FinalizeClientStream 不再重发。
//   - OpenBlocks 记录每个 index 当前是否处于"已 start，未 stop"中间态；FinalizeClientStream
//     用它合成补 content_block_stop 防止 client SDK hang。
//   - Usage 累积；message_delta 中 output_tokens 走累积值。
type AnthropicMessagesStreamState struct {
	Started    bool
	Terminated bool
	OpenBlocks map[int]bool
	Usage      CanonicalUsage
}

// NewAnthropicMessagesStreamState 构造一个空 state；map 已分配。
func NewAnthropicMessagesStreamState() *AnthropicMessagesStreamState {
	return &AnthropicMessagesStreamState{OpenBlocks: make(map[int]bool)}
}

// 内部 helper：把 *AnthropicMessagesStreamState 或 nil 转成统一非 nil 引用；
// state == nil 时返回临时新 state（容忍 forwarder 漏初始化）。
func anthropicStreamStateRef(state any) (*AnthropicMessagesStreamState, error) {
	if state == nil {
		return NewAnthropicMessagesStreamState(), nil
	}
	s, ok := state.(*AnthropicMessagesStreamState)
	if !ok {
		return nil, fmt.Errorf("proto: anthropic_messages stream state type mismatch: %T", state)
	}
	if s.OpenBlocks == nil {
		s.OpenBlocks = make(map[int]bool)
	}
	return s, nil
}

// CanonicalEventToClientChunk 把单条 CanonicalEvent 转成 Anthropic Messages SSE
// chunk（0..N 字节块）。
//
// Stream lifecycle 守门：
//   - message_start 之外的事件在 Started=false 时拒绝（INV - 流必须先 start）。
//   - Terminated=true 后所有事件吞掉，emit info loss，避免双发 terminal。
func (a *AnthropicMessagesClient) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error) {
	s, err := anthropicStreamStateRef(state)
	if err != nil {
		return nil, nil, err
	}
	evt, ok := canonicalEvt.(*CanonicalEvent)
	if !ok || evt == nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages canonical event expected *CanonicalEvent, got %T", canonicalEvt)
	}

	if s.Terminated {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "event_after_terminal_dropped:"+evt.Type, "post_terminal_event", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}

	switch evt.Type {
	case "message_start":
		if s.Started {
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "duplicate_message_start_dropped", "duplicate_message_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}
		s.Started = true
		payload := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            evt.MessageID,
				"type":          "message",
				"role":          "assistant",
				"model":         evt.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  zeroIfNilInt(evt.Usage, func(u *CanonicalUsage) int { return u.InputTokens }),
					"output_tokens": zeroIfNilInt(evt.Usage, func(u *CanonicalUsage) int { return u.OutputTokens }),
				},
			},
		}
		if evt.Usage != nil {
			s.Usage = *evt.Usage
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal message_start: %w", err)
		}
		return [][]byte{EmitSSEEvent("message_start", body)}, nil, nil

	case "content_block_start":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_start before message_start")
		}
		if evt.ContentBlock == nil {
			return nil, nil, errors.New("proto: anthropic_messages content_block_start missing content_block")
		}
		block, blockLoss := renderAnthropicResponseBlockForStart(evt.ContentBlock)
		payload := map[string]any{
			"type":          "content_block_start",
			"index":         evt.Index,
			"content_block": block,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal content_block_start: %w", err)
		}
		s.OpenBlocks[evt.Index] = true
		return [][]byte{EmitSSEEvent("content_block_start", body)}, blockLoss, nil

	case "content_block_delta":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_delta before message_start")
		}
		if evt.Delta == nil {
			return nil, nil, errors.New("proto: anthropic_messages content_block_delta missing delta")
		}
		delta, deltaLoss := renderAnthropicResponseDelta(evt.Delta)
		if delta == nil {
			// 整段 delta 不可表达 → 仅返回 loss，不 emit 字节。
			return nil, deltaLoss, nil
		}
		payload := map[string]any{
			"type":  "content_block_delta",
			"index": evt.Index,
			"delta": delta,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal content_block_delta: %w", err)
		}
		return [][]byte{EmitSSEEvent("content_block_delta", body)}, deltaLoss, nil

	case "content_block_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_stop before message_start")
		}
		payload := map[string]any{
			"type":  "content_block_stop",
			"index": evt.Index,
		}
		body, _ := json.Marshal(payload)
		delete(s.OpenBlocks, evt.Index)
		return [][]byte{EmitSSEEvent("content_block_stop", body)}, nil, nil

	case "message_delta":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages message_delta before message_start")
		}
		deltaInner := map[string]any{}
		if evt.StopReason != "" {
			mapped, stopLoss := canonicalToAnthropicStopReason(evt.StopReason)
			if mapped != nil {
				deltaInner["stop_reason"] = *mapped
			} else {
				deltaInner["stop_reason"] = nil
			}
			deltaInner["stop_sequence"] = nil
			payload := map[string]any{
				"type":  "message_delta",
				"delta": deltaInner,
			}
			if evt.Usage != nil {
				s.Usage = *evt.Usage
				payload["usage"] = map[string]any{
					"output_tokens": evt.Usage.OutputTokens,
				}
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("message_delta", body)}, stopLoss, nil
		}
		// 没 stop_reason 时只更新 usage。
		if evt.Usage != nil {
			s.Usage = *evt.Usage
			payload := map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{},
				"usage": map[string]any{"output_tokens": evt.Usage.OutputTokens},
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("message_delta", body)}, nil, nil
		}
		// 全空 message_delta 视作 ping 上游；emit info loss 不发字节。
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "empty_message_delta_dropped", "empty_message_delta", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	case "message_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages message_stop before message_start")
		}
		s.Terminated = true
		var out [][]byte
		// 若仍有未关闭 block，补 content_block_stop（INV idempotent + 客户端 SDK 兼容）。
		for idx := range s.OpenBlocks {
			stopBody, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": idx})
			out = append(out, EmitSSEEvent("content_block_stop", stopBody))
		}
		s.OpenBlocks = make(map[int]bool)
		body, _ := json.Marshal(map[string]any{"type": "message_stop"})
		out = append(out, EmitSSEEvent("message_stop", body))
		return out, nil, nil

	case "ping":
		body, _ := json.Marshal(map[string]any{"type": "ping"})
		return [][]byte{EmitSSEEvent("ping", body)}, nil, nil

	case "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "empty_canonical_event_type_dropped", "empty_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_canonical_event_type:"+evt.Type, "unknown_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}
}

// FinalizeClientStream 在 stream 结束时被 forwarder 调用；若没收到 message_stop，
// 合成补 content_block_stop（per-open-block） + message_stop，确保客户端 SDK 不
// hang。已 terminated 时返回 empty。
func (a *AnthropicMessagesClient) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	s, err := anthropicStreamStateRef(state)
	if err != nil {
		return nil, err
	}
	if s.Terminated {
		return nil, nil
	}
	if !s.Started {
		// 流根本没开始（上游异常）；无 chunk 可补，调用方应另发 error event。
		return nil, nil
	}
	var out [][]byte
	for idx := range s.OpenBlocks {
		body, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": idx})
		out = append(out, EmitSSEEvent("content_block_stop", body))
	}
	s.OpenBlocks = make(map[int]bool)
	body, _ := json.Marshal(map[string]any{"type": "message_stop"})
	out = append(out, EmitSSEEvent("message_stop", body))
	s.Terminated = true
	return out, nil
}

// renderAnthropicResponseBlockForStart 把 canonical content block 渲染为
// content_block_start 的 inner block 对象。text → empty text；tool_use → name+id+empty input。
func renderAnthropicResponseBlockForStart(b *CanonicalContentBlock) (map[string]any, []ProtocolLossEntry) {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": ""}, nil
	case "tool_use":
		return map[string]any{
			"type":  "tool_use",
			"id":    b.CallID,
			"name":  b.Name,
			"input": json.RawMessage("{}"),
		}, nil
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "block_start_type_d1_pending:"+b.Type, "d1_pending_block_start", "", "")
		return map[string]any{"type": b.Type}, []ProtocolLossEntry{loss}
	}
}

// renderAnthropicResponseDelta 把 CanonicalContentDelta 渲染为 Anthropic SSE delta
// inner object。返回 nil delta 表示无可表达字节，调用方仅发 loss。
func renderAnthropicResponseDelta(d *CanonicalContentDelta) (map[string]any, []ProtocolLossEntry) {
	switch d.Type {
	case "text_delta":
		return map[string]any{"type": "text_delta", "text": d.Text}, nil
	case "input_json_delta":
		partial := d.PartialJSON
		if len(partial) == 0 {
			partial = json.RawMessage(`""`)
		}
		return map[string]any{"type": "input_json_delta", "partial_json": partial}, nil
	case "thinking_delta":
		// D1.x thinking — 当前 emit warning + 不发字节。
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "thinking_delta_d1_pending", "d1_thinking_delta_pending", CapabilityThinking, "")
		return nil, []ProtocolLossEntry{loss}
	case "signature_delta":
		// signature_delta 默认 drop（与 anthropic upstream adapter 默认行为一致）。
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "signature_delta_default_drop", "signature_delta_default_drop", CapabilityThinking, "")
		return nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_delta_type:"+d.Type, "unknown_delta_type", "", "")
		return nil, []ProtocolLossEntry{loss}
	}
}

// zeroIfNilInt 安全取 usage 字段；nil 返回 0。
func zeroIfNilInt(u *CanonicalUsage, get func(*CanonicalUsage) int) int {
	if u == nil {
		return 0
	}
	return get(u)
}
