package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ----------------------------------------------------------------------------
// D10 CanonicalToClientResponse — HCSF buffered → OpenAI Responses JSON
// ----------------------------------------------------------------------------

// canonicalToResponsesStatus 映射 canonical stop → OpenAI Responses status
// + 可选 incomplete_details。OpenAI Responses 用 status="completed"/"incomplete"
// 而不是 finish_reason 字符串。
func canonicalToResponsesStatus(c CanonicalStopReason) (status string, incomplete *openAIResponsesIncomplete, losses []ProtocolLossEntry) {
	switch c {
	case CanonicalStopEndTurn, CanonicalStopSequence, CanonicalStopToolUse:
		return "completed", nil, nil
	case CanonicalStopMaxTokens:
		return "incomplete", &openAIResponsesIncomplete{Reason: "max_output_tokens"}, nil
	case CanonicalStopRefusal:
		return "incomplete", &openAIResponsesIncomplete{Reason: "content_filter"}, nil
	case CanonicalStopUnknown, "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unknown_for_responses", "stop_reason_unknown", "", "")
		return "completed", nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unmapped:"+string(c), "stop_reason_unmapped", "", "")
		return "completed", nil, []ProtocolLossEntry{loss}
	}
}

// CanonicalToClientResponse 把 HCSF buffered envelope 序列化为 OpenAI Responses
// API response JSON。
func (o *OpenAIResponsesClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: openai_responses CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: openai_responses CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	output := make([]map[string]any, 0, len(resp.Content))
	// 文本 block 合并到一个 message item（assistant role）下；tool_use 各自成
	// function_call output item。
	var msgTexts []map[string]any
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			msgTexts = append(msgTexts, map[string]any{"type": "output_text", "text": b.Text})
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses content[%d] tool_use missing call_id or name", i)
			}
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        "fc_" + b.CallID,
				"call_id":   b.CallID,
				"name":      b.Name,
				"arguments": args,
			})
		case "tool_result":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d10_pending", "d10_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d10_pending", "d10_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}
	if len(msgTexts) > 0 {
		// 把 message item 放在 output 数组前部，function_call 在后部（与 OpenAI 规范一致）。
		msgItem := map[string]any{
			"type":    "message",
			"id":      "msg_" + resp.ID,
			"role":    "assistant",
			"content": msgTexts,
		}
		output = append([]map[string]any{msgItem}, output...)
	}

	status, incomplete, statusLoss := canonicalToResponsesStatus(resp.StopReason)
	losses = append(losses, statusLoss...)

	usage := openAIResponsesUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		usage.InputTokensDetails = &openAIResponsesUsageInputDetails{CachedTokens: resp.Usage.CacheReadInputTokens}
	}
	if canonical.Accounting.ReasoningTokens > 0 {
		usage.OutputTokensDetails = &openAIResponsesUsageOutputDetails{ReasoningTokens: canonical.Accounting.ReasoningTokens}
	}

	out := openAIResponsesResponse{
		ID:                resp.ID,
		Object:            "response",
		Model:             resp.Model,
		Status:            status,
		IncompleteDetails: incomplete,
		Output:            output,
		Usage:             usage,
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses marshal response: %w", err)
	}
	return body, losses, nil
}
