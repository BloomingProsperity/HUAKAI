package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ----------------------------------------------------------------------------
// D6 CanonicalToClientResponse — HCSF buffered → OpenAI Chat completion JSON 序列化
// ----------------------------------------------------------------------------

type openAIChatCompletion struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created,omitempty"`
	Model   string                  `json:"model"`
	Choices []openAIChatChoice      `json:"choices"`
	Usage   openAIChatResponseUsage `json:"usage"`
}

type openAIChatChoice struct {
	Index        int                 `json:"index"`
	Message      openAIChatChoiceMsg `json:"message"`
	FinishReason *string             `json:"finish_reason"`
}

type openAIChatChoiceMsg struct {
	Role      string                       `json:"role"`
	Content   *string                      `json:"content"` // 存在 tool_calls 时为 null
	ToolCalls []openAIChatResponseToolCall `json:"tool_calls,omitempty"`
}

type openAIChatResponseToolCall struct {
	ID       string                         `json:"id"`
	Type     string                         `json:"type"`
	Function openAIChatResponseToolCallFunc `json:"function"`
}

type openAIChatResponseToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponseUsage struct {
	PromptTokens     int                           `json:"prompt_tokens"`
	CompletionTokens int                           `json:"completion_tokens"`
	TotalTokens      int                           `json:"total_tokens"`
	PromptDetails    *openAIChatUsagePromptDetails `json:"prompt_tokens_details,omitempty"`
}

type openAIChatUsagePromptDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func canonicalToOpenAIFinishReason(c CanonicalStopReason) (*string, []ProtocolLossEntry) {
	switch c {
	case CanonicalStopEndTurn, CanonicalStopSequence:
		s := "stop"
		return &s, nil
	case CanonicalStopMaxTokens:
		s := "length"
		return &s, nil
	case CanonicalStopToolUse:
		s := "tool_calls"
		return &s, nil
	case CanonicalStopRefusal:
		s := "content_filter"
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "canonical_refusal_mapped_to_content_filter", "refusal_to_content_filter", "", "")
		return &s, []ProtocolLossEntry{loss}
	case CanonicalStopUnknown, "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unknown", "stop_reason_unknown", "", "")
		return nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unmapped:"+string(c), "stop_reason_unmapped", "", "")
		return nil, []ProtocolLossEntry{loss}
	}
}

// CanonicalToClientResponse 把 HCSF buffered envelope 序列化为 OpenAI Chat
// completion JSON 响应。
func (o *OpenAIChatClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: openai_chat CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: openai_chat CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	// 拼 message.content / tool_calls
	var textParts []string
	var toolCalls []openAIChatResponseToolCall
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_chat content[%d] tool_use missing call_id or name", i)
			}
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openAIChatResponseToolCall{
				ID:   b.CallID,
				Type: "function",
				Function: openAIChatResponseToolCallFunc{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d5_pending", "d5_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d5_pending", "d5_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}

	msg := openAIChatChoiceMsg{Role: "assistant"}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		// OpenAI 规范：有 tool_calls 时 content 可以是 null。
		if len(textParts) > 0 {
			joined := joinNonEmpty(textParts, "\n")
			msg.Content = &joined
		}
	} else {
		joined := joinNonEmpty(textParts, "\n")
		msg.Content = &joined
	}

	finish, stopLoss := canonicalToOpenAIFinishReason(resp.StopReason)
	losses = append(losses, stopLoss...)

	usage := openAIChatResponseUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		usage.PromptDetails = &openAIChatUsagePromptDetails{CachedTokens: resp.Usage.CacheReadInputTokens}
	}

	out := openAIChatCompletion{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []openAIChatChoice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   usage,
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat marshal response: %w", err)
	}
	// 合并上游响应的顶层透传字段(如 system_fingerprint / service_tier / prompt_filter_results
	// 等 typed struct 未建模的键)。与 anthropic_messages 序列化器对称: 非流式响应必经
	// CanonicalToClientResponse, 不补 merge 则这些字段被静默丢弃。MergeExtrasInto 保证 typed
	// 字段优先、不覆盖已知键。
	if resp.Passthrough != nil {
		body, err = MergeExtrasInto(body, resp.Passthrough)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: openai_chat merge response passthrough: %w", err)
		}
	}
	return body, losses, nil
}
