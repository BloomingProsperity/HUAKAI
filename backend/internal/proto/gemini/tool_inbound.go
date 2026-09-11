package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type geminiFunctionResponseBody struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

// ErrGeminiThoughtStateMissing 表示 Gemini 3 多轮工具环缺少必须回放的思考状态。
var ErrGeminiThoughtStateMissing = fmt.Errorf("gemini tool round-trip is missing required thought state")

func geminiPartToCanonicalBlocks(env *proto.HCSF, msgIndex, partIndex int, part geminiClientPart) ([]proto.CanonicalContentBlock, []proto.ProtocolLossEntry) {
	var blocks []proto.CanonicalContentBlock
	var losses []proto.ProtocolLossEntry
	if len(bytes.TrimSpace(part.VideoMetadata)) > 0 {
		attachGeminiPartPassthrough(env, msgIndex, partIndex, "videoMetadata", part.VideoMetadata)
		losses = append(losses, geminiClientInfoLoss("gemini part videoMetadata preserved as request passthrough; HCSF has no first-class Gemini video metadata field yet", "gemini_part_video_metadata_passthrough", proto.CapabilityVideo))
	}
	if len(bytes.TrimSpace(part.MediaResolution)) > 0 {
		attachGeminiPartPassthrough(env, msgIndex, partIndex, "mediaResolution", part.MediaResolution)
		losses = append(losses, geminiClientInfoLoss("gemini part mediaResolution preserved as request passthrough; HCSF has no first-class media resolution control yet", "gemini_part_media_resolution_passthrough", proto.CapabilityVideo))
	}
	if part.Text != nil {
		blocks = append(blocks, proto.CanonicalContentBlock{Type: "text", Text: *part.Text})
	}
	if len(bytes.TrimSpace(part.InlineData)) > 0 {
		raw := cloneRaw(part.InlineData)
		blocks = append(blocks, proto.CanonicalContentBlock{Type: "image", Image: raw})
	}
	if part.FunctionCall != nil {
		callID := part.FunctionCall.ID
		if callID == "" {
			callID = part.FunctionCall.Name
		}
		blocks = append(blocks, proto.CanonicalContentBlock{
			Type:      "tool_use",
			CallID:    callID,
			Name:      part.FunctionCall.Name,
			Input:     normalizeGeminiFunctionArgs(part.FunctionCall.Args),
			Signature: firstNonEmpty(part.FunctionCall.ThoughtSignature, part.ThoughtSignature),
		})
	}
	if len(bytes.TrimSpace(part.FunctionResponse)) > 0 {
		block, parseErr := geminiFunctionResponseBlock(part.FunctionResponse)
		if parseErr != nil {
			losses = append(losses, geminiClientWarningLoss("gemini functionResponse part is not a valid tool_result: "+parseErr.Error(), "gemini_function_response_invalid", proto.CapabilityToolResult))
		} else {
			blocks = append(blocks, block)
		}
	}
	return blocks, losses
}

func attachGeminiPartPassthrough(env *proto.HCSF, msgIndex, partIndex int, field string, raw json.RawMessage) {
	if env == nil || field == "" || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	if env.Passthrough == nil {
		env.Passthrough = &proto.PassthroughEnvelope{}
	}
	if env.Passthrough.Extra == nil {
		env.Passthrough.Extra = map[string]json.RawMessage{}
	}
	key := fmt.Sprintf("contents[%d].parts[%d].%s", msgIndex, partIndex, field)
	env.Passthrough.Extra[key] = cloneRaw(raw)
}

func geminiFunctionResponseBlock(raw json.RawMessage) (proto.CanonicalContentBlock, error) {
	var body geminiFunctionResponseBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return proto.CanonicalContentBlock{}, fmt.Errorf("decode functionResponse: %w", err)
	}
	name := strings.TrimSpace(body.Name)
	callID := strings.TrimSpace(body.ID)
	if callID == "" {
		callID = name
	}
	if callID == "" {
		return proto.CanonicalContentBlock{}, fmt.Errorf("functionResponse missing name and id")
	}
	text := flattenGeminiFunctionResponse(body.Response)
	result, err := json.Marshal([]proto.CanonicalContentBlock{{Type: "text", Text: text}})
	if err != nil {
		return proto.CanonicalContentBlock{}, err
	}
	return proto.CanonicalContentBlock{
		Type:       "tool_result",
		CallID:     callID,
		Name:       name,
		ToolResult: result,
	}, nil
}

func flattenGeminiFunctionResponse(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var asMap map[string]any
	if err := json.Unmarshal(trimmed, &asMap); err == nil {
		if content, ok := asMap["content"]; ok {
			switch typed := content.(type) {
			case string:
				return typed
			default:
				encoded, err := json.Marshal(typed)
				if err == nil {
					return string(encoded)
				}
			}
		}
	}
	return string(trimmed)
}

func geminiToolResultCapabilityNode(id string, source *proto.NodeSourceRef, block proto.CanonicalContentBlock) proto.CapabilityNode {
	return proto.CapabilityNode{
		ID:          id,
		Kind:        proto.CapabilityToolResult,
		StreamReady: proto.StreamReadyYes,
		Source:      source,
		ToolResult: &proto.ToolResultNode{
			ToolCallID: block.CallID,
			Content:    []proto.CanonicalContentBlock{{Type: "text", Text: flattenStoredToolResult(block.ToolResult)}},
			Status:     proto.ToolNodeComplete,
		},
	}
}

func flattenStoredToolResult(raw json.RawMessage) string {
	var blocks []proto.CanonicalContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil && len(blocks) > 0 {
		var parts []string
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(bytes.TrimSpace(raw))
}

func linkGeminiToolResultEdges(env *proto.HCSF) error {
	if env == nil {
		return nil
	}
	toolUseByCall := map[string]string{}
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind != proto.CapabilityToolUse || node.ToolUse == nil {
			continue
		}
		callID := strings.TrimSpace(node.ToolUse.ToolCallID)
		if callID == "" {
			continue
		}
		if existing, ok := toolUseByCall[callID]; ok && existing != node.ID {
			return fmt.Errorf("proto: gemini duplicate tool_use id %q", callID)
		}
		toolUseByCall[callID] = node.ID
	}
	edgeSeq := 0
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind != proto.CapabilityToolResult || node.ToolResult == nil {
			continue
		}
		callID := strings.TrimSpace(node.ToolResult.ToolCallID)
		from, ok := toolUseByCall[callID]
		if !ok {
			return fmt.Errorf("proto: gemini tool_result references unknown tool call %q", callID)
		}
		edgeSeq++
		env.CapabilityGraph.Edges = append(env.CapabilityGraph.Edges, proto.CapabilityEdge{
			ID:       fmt.Sprintf("e_gemini_tool_%d", edgeSeq),
			Type:     proto.EdgeRequires,
			From:     node.ID,
			To:       from,
			Required: true,
			Reason:   "tool_result requires tool_use",
		})
	}
	return nil
}

// EnforceThoughtCarry 在 Gemini 3 工具回放时回填上一轮真实思考状态。
// 官方合同只强制当前轮第一步的首个 functionCall 带签名；禁止合成。
func EnforceThoughtCarry(env *proto.HCSF) error {
	if env == nil || !geminiThoughtStateRequired(env.RequestMeta.Model, env.RequestMeta.UpstreamModel) {
		return nil
	}
	replayThoughtCarry(env)
	if !geminiHasToolResult(env) {
		return nil
	}
	if firstGeminiToolUseThoughtMissing(env) {
		return fmt.Errorf("%w: model=%q", ErrGeminiThoughtStateMissing, firstNonEmpty(env.RequestMeta.UpstreamModel, env.RequestMeta.Model))
	}
	return nil
}

func geminiHasToolResult(env *proto.HCSF) bool {
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind == proto.CapabilityToolResult && node.ToolResult != nil {
			return true
		}
	}
	return false
}

func firstGeminiToolUseThoughtMissing(env *proto.HCSF) bool {
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind != proto.CapabilityToolUse || node.ToolUse == nil {
			continue
		}
		return strings.TrimSpace(node.ToolUse.OpaqueState) == ""
	}
	return true
}

func replayThoughtCarry(env *proto.HCSF) {
	if env == nil {
		return
	}
	var carry string
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind != proto.CapabilityToolUse || node.ToolUse == nil {
			continue
		}
		if state := strings.TrimSpace(node.ToolUse.OpaqueState); state != "" {
			carry = state
			break
		}
	}
	if carry == "" {
		return
	}
	applied := false
	for i := range env.CapabilityGraph.Nodes {
		node := &env.CapabilityGraph.Nodes[i]
		if node.Kind != proto.CapabilityToolUse || node.ToolUse == nil {
			continue
		}
		if strings.TrimSpace(node.ToolUse.OpaqueState) != "" {
			applied = true
			continue
		}
		if !applied {
			node.ToolUse.OpaqueState = carry
			applied = true
		}
	}
}

func geminiThoughtStateRequired(models ...string) bool {
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if strings.Contains(normalized, "gemini-3") {
			return true
		}
	}
	return false
}
