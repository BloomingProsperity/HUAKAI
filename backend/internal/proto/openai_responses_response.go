package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ----------------------------------------------------------------------------
// D10 CanonicalToClientResponse — HCSF buffered → OpenAI Responses JSON 序列化
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
	// 文本 block 合并到 message item；非文本 item 先 flush，避免把 reasoning
	// continuation state 移到错误的 output 顺序。
	var msgTexts []map[string]any
	messageSeq := 0
	flushMessage := func() {
		if len(msgTexts) == 0 {
			return
		}
		messageSeq++
		msgID := "msg_" + resp.ID
		if messageSeq > 1 {
			msgID = fmt.Sprintf("msg_%s_%d", resp.ID, messageSeq)
		}
		output = append(output, map[string]any{
			"type":    "message",
			"id":      msgID,
			"role":    "assistant",
			"content": msgTexts,
		})
		msgTexts = nil
	}
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			msgTexts = append(msgTexts, map[string]any{"type": "output_text", "text": b.Text})
		case "tool_use":
			flushMessage()
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
		case "thinking", "reasoning", "redacted_thinking":
			flushMessage()
			output = append(output, openAIResponsesReasoningOutputItem(resp.ID, i, b))
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d10_pending", "d10_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}
	flushMessage()

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

func openAIResponsesReasoningOutputItem(responseID string, index int, b CanonicalContentBlock) map[string]any {
	summary := make([]map[string]string, 0, 1)
	if text := firstNonEmptyString(b.Thinking, b.ReasoningSummary, b.Text); text != "" {
		summary = append(summary, map[string]string{"type": "summary_text", "text": text})
	}
	item := map[string]any{
		"type":    "reasoning",
		"id":      fmt.Sprintf("rs_%s_%d", responseID, index),
		"status":  "completed",
		"summary": summary,
	}
	if encrypted := openAIResponsesReasoningState(b); encrypted != "" {
		item["encrypted_content"] = encrypted
	}
	return item
}

func openAIResponsesReasoningState(b CanonicalContentBlock) string {
	if b.Signature != "" {
		return b.Signature
	}
	if raw := bytes.TrimSpace(b.Data); len(raw) > 0 {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return string(raw)
	}
	return ""
}
