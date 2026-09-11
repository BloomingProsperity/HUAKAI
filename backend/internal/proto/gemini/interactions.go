package gemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const officialInteractionRawKey = "official_interaction"

// IsInteractionsPath 识别官方会话协议入口与出站 path。
func IsInteractionsPath(path string) bool {
	p := strings.TrimSpace(path)
	return p == "/v1beta/interactions" || strings.HasPrefix(p, "/v1beta/interactions/")
}

type interactionsRequestProbe struct {
	Input json.RawMessage `json:"input"`
}

type interactionsResponseProbe struct {
	Object string `json:"object"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Usage  *struct {
		TotalTokens       int `json:"total_tokens"`
		TotalInputTokens  int `json:"total_input_tokens"`
		TotalOutputTokens int `json:"total_output_tokens"`
	} `json:"usage"`
	Steps []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"steps"`
}

func interactionsInputMessages(raw []byte) ([]proto.CanonicalMessage, bool) {
	var probe interactionsRequestProbe
	if json.Unmarshal(raw, &probe) != nil || len(bytes.TrimSpace(probe.Input)) == 0 {
		return nil, false
	}
	var asString string
	if json.Unmarshal(probe.Input, &asString) == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return nil, false
		}
		return []proto.CanonicalMessage{{
			Role:    "user",
			Content: []proto.CanonicalContentBlock{{Type: "text", Text: text}},
		}}, true
	}
	var items []json.RawMessage
	if json.Unmarshal(probe.Input, &items) != nil {
		return nil, false
	}
	var messages []proto.CanonicalMessage
	for _, item := range items {
		text := interactionItemText(item)
		if text == "" {
			continue
		}
		messages = append(messages, proto.CanonicalMessage{
			Role:    "user",
			Content: []proto.CanonicalContentBlock{{Type: "text", Text: text}},
		})
	}
	if len(messages) == 0 {
		return nil, false
	}
	return messages, true
}

func interactionItemText(raw json.RawMessage) string {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var obj struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	if text := strings.TrimSpace(obj.Text); text != "" {
		return text
	}
	var b strings.Builder
	for _, part := range obj.Content {
		if strings.TrimSpace(part.Text) != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.TrimSpace(part.Text))
		}
	}
	for _, part := range obj.Parts {
		if strings.TrimSpace(part.Text) != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.TrimSpace(part.Text))
		}
	}
	return b.String()
}

func emptyInteractionsEnvelope(seed *proto.RequestMetaSeed, model string) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, nil, errors.New("proto: gemini missing path model")
	}
	env := proto.NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = model
	return env, nil, nil
}

func officialInteractionJSON(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	var wrap struct {
		Interaction json.RawMessage `json:"interaction"`
	}
	if json.Unmarshal(raw, &wrap) == nil && len(bytes.TrimSpace(wrap.Interaction)) > 2 {
		return wrap.Interaction
	}
	return raw
}

func interactionCanonicalResponse(raw []byte) (proto.CanonicalResponse, bool) {
	raw = officialInteractionJSON(raw)
	var gen struct {
		Candidates json.RawMessage `json:"candidates"`
	}
	if json.Unmarshal(raw, &gen) == nil && len(bytes.TrimSpace(gen.Candidates)) > 2 {
		return proto.CanonicalResponse{}, false
	}
	var probe interactionsResponseProbe
	if json.Unmarshal(raw, &probe) != nil {
		return proto.CanonicalResponse{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(probe.Object), "interaction") && strings.TrimSpace(probe.Status) == "" {
		return proto.CanonicalResponse{}, false
	}
	out := proto.CanonicalResponse{
		ID:    strings.TrimSpace(probe.ID),
		Model: strings.TrimSpace(probe.Model),
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
			officialInteractionRawKey: append(json.RawMessage(nil), raw...),
		}},
	}
	if probe.Usage != nil {
		out.Usage = proto.CanonicalUsage{
			InputTokens:  probe.Usage.TotalInputTokens,
			OutputTokens: probe.Usage.TotalOutputTokens,
			TotalTokens:  probe.Usage.TotalTokens,
		}
		if out.Usage.TotalTokens == 0 {
			out.Usage.TotalTokens = out.Usage.InputTokens + out.Usage.OutputTokens
		}
	}
	for _, step := range probe.Steps {
		if !strings.EqualFold(strings.TrimSpace(step.Type), "model_output") {
			continue
		}
		for _, part := range step.Content {
			if !strings.EqualFold(strings.TrimSpace(part.Type), "text") {
				continue
			}
			if text := strings.TrimSpace(part.Text); text != "" {
				out.Content = append(out.Content, proto.CanonicalContentBlock{Type: "text", Text: text})
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(probe.Status), "completed") {
		out.StopReason = proto.CanonicalStopEndTurn
	}
	return out, true
}

func officialInteractionRaw(resp *proto.CanonicalResponse) []byte {
	if resp == nil || resp.Passthrough == nil || resp.Passthrough.Extra == nil {
		return nil
	}
	return bytes.TrimSpace(resp.Passthrough.Extra[officialInteractionRawKey])
}
