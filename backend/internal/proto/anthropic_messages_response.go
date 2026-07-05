package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// P-2 D2 anthropic_messages.CanonicalToClientResponse —— HCSF buffered envelope 响应信封
// 序列化为 Anthropic Messages API 响应 JSON。

// anthropicMessagesResponse 是 Anthropic Messages API response 的最小映射。
type anthropicMessagesResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Content      []json.RawMessage      `json:"content"`
	Model        string                 `json:"model"`
	StopReason   *string                `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence"`
	Usage        anthropicResponseUsage `json:"usage"`
}

type anthropicResponseContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type anthropicResponseUsage struct {
	InputTokens              int                             `json:"input_tokens"`
	OutputTokens             int                             `json:"output_tokens"`
	CacheCreationInputTokens int                             `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                             `json:"cache_read_input_tokens,omitempty"`
	CacheCreation            *anthropicResponseCacheCreation `json:"cache_creation,omitempty"`
}

type anthropicResponseCacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

// canonicalToAnthropicStopReason 映射 canonical stop reason → Anthropic stop_reason；
// 不能表达时返回 nil + warning loss（反 silent drop）。
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
// 响应 JSON（text + tool_use + usage + stop_reason 映射）。
// 输入约束：canonical 必须是 buffered envelope（BufferedResponse != nil；）。
func (a *AnthropicMessagesClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: anthropic_messages CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: anthropic_messages CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	contentOut := make([]json.RawMessage, 0, len(resp.Content))
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			if raw := bytes.TrimSpace(b.Raw); len(raw) > 0 {
				contentOut = append(contentOut, append(json.RawMessage(nil), raw...))
				continue
			}
			if err := appendAnthropicResponseContentBlock(&contentOut, anthropicResponseContentBlock{
				Type: "text", Text: b.Text,
			}); err != nil {
				return nil, nil, err
			}
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: anthropic_messages CanonicalToClientResponse content[%d] tool_use missing call_id or name", i)
			}
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			if err := appendAnthropicResponseContentBlock(&contentOut, anthropicResponseContentBlock{
				Type: "tool_use", ID: b.CallID, Name: b.Name, Input: input,
			}); err != nil {
				return nil, nil, err
			}
		case "thinking":
			thinking := b.Thinking
			if thinking == "" {
				thinking = firstNonEmptyString(b.Text, b.ReasoningSummary)
			}
			if err := appendAnthropicResponseContentBlock(&contentOut, anthropicResponseContentBlock{
				Type:      "thinking",
				Thinking:  thinking,
				Signature: b.Signature,
			}); err != nil {
				return nil, nil, err
			}
		case "redacted_thinking":
			if err := appendAnthropicResponseContentBlock(&contentOut, anthropicResponseContentBlock{
				Type: "redacted_thinking",
				Data: b.Data,
			}); err != nil {
				return nil, nil, err
			}
		case "tool_result":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d1_pending", "d1_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d1_pending", "d1_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			if raw := bytes.TrimSpace(b.Raw); len(raw) > 0 {
				contentOut = append(contentOut, append(json.RawMessage(nil), raw...))
				continue
			}
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}

	stopReason, stopLoss := canonicalToAnthropicStopReason(resp.StopReason)
	losses = append(losses, stopLoss...)

	usage := anthropicResponseUsage{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
	}
	if resp.Usage.CacheCreationInputTokens5m != 0 || resp.Usage.CacheCreationInputTokens1h != 0 {
		usage.CacheCreation = &anthropicResponseCacheCreation{
			Ephemeral5mInputTokens: resp.Usage.CacheCreationInputTokens5m,
			Ephemeral1hInputTokens: resp.Usage.CacheCreationInputTokens1h,
		}
	}

	out := anthropicMessagesResponse{
		ID:           resp.ID,
		Type:         "message",
		Role:         "assistant",
		Content:      contentOut,
		Model:        resp.Model,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage:        usage,
	}
	if resp.StopSequence != "" {
		out.StopSequence = &resp.StopSequence
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages marshal response: %w", err)
	}
	if resp.Passthrough != nil {
		body, err = MergeExtrasInto(body, resp.Passthrough)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: anthropic_messages merge response passthrough: %w", err)
		}
	}
	return body, losses, nil
}

func appendAnthropicResponseContentBlock(content *[]json.RawMessage, block anthropicResponseContentBlock) error {
	raw, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("proto: anthropic_messages marshal response content block: %w", err)
	}
	*content = append(*content, raw)
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
