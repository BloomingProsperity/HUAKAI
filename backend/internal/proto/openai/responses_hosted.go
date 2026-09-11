package openai

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func appendResponsesBufferedItem(out *proto.CanonicalResponse, item responsesOutputItem) error {
	out.Usage = proto.AddHostedCallUsage(out.Usage, item.Type, item.Status)
	switch item.Type {
	case "message":
		for _, part := range item.Content {
			if part.Type == "output_text" && part.Text != "" {
				block := proto.CanonicalContentBlock{Type: "text", Text: part.Text}
				if len(part.Annotations) > 0 {
					block.Annotations = append(json.RawMessage(nil), part.Annotations...)
				}
				out.Content = append(out.Content, block)
			}
		}
	case "function_call":
		args := json.RawMessage(item.Arguments)
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out.Content = append(out.Content, proto.CanonicalContentBlock{
			Type:   "tool_use",
			CallID: firstNonEmptyResponseString(item.CallID, item.ID),
			Name:   item.Name,
			Input:  args,
		})
	case "reasoning":
		var summary strings.Builder
		for _, part := range item.Summary {
			summary.WriteString(part.Text)
		}
		out.Content = append(out.Content, proto.CanonicalContentBlock{
			Type:             "thinking",
			Thinking:         summary.String(),
			ReasoningSummary: summary.String(),
			Signature:        item.EncryptedContent,
		})
	default:
		if !proto.HostedCallItem(item.Type) {
			return nil
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		out.Content = append(out.Content, proto.CanonicalContentBlock{
			Type:   item.Type,
			CallID: firstNonEmptyResponseString(item.CallID, item.ID),
			Name:   item.Name,
			Raw:    raw,
		})
	}
	return nil
}

func addResponsesOutputHostedUsage(usage proto.CanonicalUsage, output []responsesOutputItem) proto.CanonicalUsage {
	for _, item := range output {
		usage = proto.AddHostedCallUsage(usage, item.Type, item.Status)
	}
	return usage
}
