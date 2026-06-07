package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// GeminiClient implements Gemini native v1beta client-protocol translation.
type GeminiClient struct{}

var _ proto.ClientAdapter = (*GeminiClient)(nil)

func init() {
	proto.RegisterDefaultClientAdapterFactory(proto.ClientProtocolGemini, func() proto.ClientAdapter {
		return &GeminiClient{}
	})
}

type geminiClientRequest struct {
	Contents          []geminiClientContent   `json:"contents,omitempty"`
	SystemInstruction *geminiClientContent    `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiClientTool      `json:"tools,omitempty"`
	ToolConfig        json.RawMessage         `json:"toolConfig,omitempty"`
	SafetySettings    json.RawMessage         `json:"safetySettings,omitempty"`
	CachedContent     string                  `json:"cachedContent,omitempty"`
}

type geminiClientContent struct {
	Parts []geminiClientPart `json:"parts,omitempty"`
	Role  string             `json:"role,omitempty"`
}

type geminiClientPart struct {
	Text             *string             `json:"text,omitempty"`
	InlineData       json.RawMessage     `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse json.RawMessage     `json:"functionResponse,omitempty"`
	VideoMetadata    json.RawMessage     `json:"videoMetadata,omitempty"`
	MediaResolution  json.RawMessage     `json:"mediaResolution,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMIMEType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}

type geminiClientTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
	GoogleSearch         json.RawMessage             `json:"googleSearch,omitempty"`
	CodeExecution        json.RawMessage             `json:"codeExecution,omitempty"`
	Retrieval            json.RawMessage             `json:"retrieval,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// RequestToCanonical converts Gemini generateContent JSON into HCSF.
func (c *GeminiClient) RequestToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	seed, ok := proto.RequestMetaSeedFromContext(ctx)
	if !ok {
		return nil, nil, proto.ErrMissingRequestMetaSeed
	}

	var req geminiClientRequest
	var extras proto.PassthroughEnvelope
	if err := proto.UnmarshalWithExtras(raw, &req, &extras); err != nil {
		return nil, nil, fmt.Errorf("proto: gemini parse body: %w", err)
	}
	model := strings.TrimSpace(seed.Model)
	if model == "" {
		model = geminiModelFromIngressPath(seed.IngressPath)
	}
	if model == "" {
		return nil, nil, errors.New("proto: gemini missing path model")
	}
	if len(req.Contents) == 0 {
		return nil, nil, errors.New("proto: gemini contents must not be empty")
	}

	env := proto.NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = model
	if strings.Contains(seed.IngressPath, ":streamGenerateContent") {
		env.StreamPlan.Mode = proto.StreamModeStreaming
	}

	var losses []proto.ProtocolLossEntry
	losses = append(losses, geminiRequestExtrasLosses(extras)...)
	applyGeminiGenerationConfig(env, req.GenerationConfig, &losses)
	if req.SystemInstruction != nil {
		env.RequestControls.SystemPrompt = geminiSystemText(*req.SystemInstruction, &losses)
	}
	if len(req.Tools) > 0 {
		tools, toolLosses, err := convertGeminiTools(req.Tools)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Tools = tools
		losses = append(losses, toolLosses...)
	}
	if len(bytes.TrimSpace(req.ToolConfig)) > 0 {
		losses = append(losses, geminiClientInfoLoss("gemini toolConfig has no typed HCSF control yet", "gemini_tool_config_unmodeled", proto.CapabilityToolUse))
	}
	if len(bytes.TrimSpace(req.SafetySettings)) > 0 {
		losses = append(losses, geminiClientInfoLoss("gemini safetySettings preserved as protocol loss metadata; HCSF has no safety policy node yet", "gemini_safety_settings_unmodeled", ""))
	}
	if req.CachedContent != "" {
		losses = append(losses, geminiClientInfoLoss("gemini cachedContent has no request-control field in HCSF yet", "gemini_cached_content_unmodeled", proto.CapabilityCacheControl))
	}

	nodeSeq := 0
	for mi, content := range req.Contents {
		role := canonicalRoleFromGemini(content.Role)
		msg := proto.CanonicalMessage{Role: role}
		for pi, part := range content.Parts {
			blocks, partLosses := geminiPartToCanonicalBlocks(env, mi, pi, part)
			losses = append(losses, partLosses...)
			for _, block := range blocks {
				bi := len(msg.Content)
				msg.Content = append(msg.Content, block)
				nodeSeq++
				node := geminiCapabilityNode(fmt.Sprintf("n_gemini_%d", nodeSeq), role, block, mi, bi)
				if node.ID != "" {
					env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, node)
					env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, proto.CapabilityProjection{
						Capability: node.Kind,
						NodeID:     node.ID,
						Verdict:    proto.ProjectionPreserved,
					})
				}
			}
			if len(blocks) == 0 && part.Text == nil && len(bytes.TrimSpace(part.InlineData)) == 0 && part.FunctionCall == nil {
				losses = append(losses, geminiClientWarningLoss(fmt.Sprintf("gemini contents[%d].parts[%d] has no supported payload", mi, pi), "gemini_empty_part", ""))
			}
		}
		if len(msg.Content) > 0 {
			env.Messages = append(env.Messages, msg)
		}
	}
	if len(env.Messages) == 0 {
		return nil, losses, errors.New("proto: gemini contents produced no canonical messages")
	}

	return env, losses, nil
}

// CanonicalToClientResponse converts an HCSF buffered response into Gemini JSON.
func (c *GeminiClient) CanonicalToClientResponse(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	_ = ctx
	if canonical == nil {
		return nil, nil, errors.New("proto: gemini CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: gemini CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse
	parts, losses, err := canonicalBlocksToGeminiParts(resp.Content)
	if err != nil {
		return nil, losses, err
	}
	finish, finishLosses := geminiFinishReasonFromCanonical(resp.StopReason)
	losses = append(losses, finishLosses...)
	out := geminiGenerateContentResponse{
		ResponseID:    resp.ID,
		ModelVersion:  resp.Model,
		UsageMetadata: geminiUsageFromCanonical(resp.Usage),
		Candidates: []geminiCandidate{{
			Index:        0,
			Content:      geminiContent{Role: "model", Parts: parts},
			FinishReason: finish,
		}},
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, losses, fmt.Errorf("proto: gemini marshal response: %w", err)
	}
	return body, losses, nil
}

type GeminiClientStreamState struct {
	ResponseID string
	Model      string
	Started    bool
	Terminated bool
	Usage      proto.CanonicalUsage
}

func geminiClientStreamStateRef(state any) (*GeminiClientStreamState, error) {
	if state == nil {
		return &GeminiClientStreamState{}, nil
	}
	s, ok := state.(*GeminiClientStreamState)
	if !ok {
		return nil, fmt.Errorf("proto: gemini stream state type mismatch: %T", state)
	}
	return s, nil
}

// CanonicalEventToClientChunk converts canonical events into Gemini SSE chunks.
func (c *GeminiClient) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []proto.ProtocolLossEntry, error) {
	_ = ctx
	s, err := geminiClientStreamStateRef(state)
	if err != nil {
		return nil, nil, err
	}
	evt, ok := canonicalEvt.(*proto.CanonicalEvent)
	if !ok || evt == nil {
		if value, ok := canonicalEvt.(proto.CanonicalEvent); ok {
			evt = &value
		} else {
			return nil, nil, fmt.Errorf("proto: gemini canonical event expected *CanonicalEvent, got %T", canonicalEvt)
		}
	}
	if s.Terminated {
		return nil, []proto.ProtocolLossEntry{geminiClientInfoLoss("canonical event after Gemini stream termination dropped", "gemini_post_termination_event", "")}, nil
	}

	switch evt.Type {
	case "message_start":
		s.Started = true
		s.ResponseID = evt.MessageID
		s.Model = evt.Model
		return nil, nil, nil
	case "content_block_start":
		if evt.ContentBlock == nil || evt.ContentBlock.Type != "tool_use" {
			return nil, nil, nil
		}
		part, err := geminiPartFromCanonicalBlock(*evt.ContentBlock)
		if err != nil {
			return nil, nil, err
		}
		return [][]byte{geminiSSEChunk(s, []geminiPart{part}, "", nil)}, nil, nil
	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil, errors.New("proto: gemini content_block_delta missing delta")
		}
		switch evt.Delta.Type {
		case "text_delta":
			text := evt.Delta.Text
			return [][]byte{geminiSSEChunk(s, []geminiPart{{Text: &text}}, "", nil)}, nil, nil
		case "reasoning_delta", "thinking_delta":
			text := evt.Delta.ReasoningText
			if text == "" {
				text = evt.Delta.Text
			}
			return [][]byte{geminiSSEChunk(s, []geminiPart{{Text: &text, Thought: true}}, "", nil)}, nil, nil
		default:
			return nil, []proto.ProtocolLossEntry{geminiClientWarningLoss("canonical delta type has no Gemini stream projection: "+evt.Delta.Type, "gemini_delta_unmapped", "")}, nil
		}
	case "message_delta":
		if evt.Usage != nil {
			s.Usage = *evt.Usage
		}
		if evt.StopReason == "" {
			if evt.Usage == nil {
				return nil, nil, nil
			}
			usage := s.Usage
			return [][]byte{geminiSSEChunk(s, nil, "", &usage)}, nil, nil
		}
		finish, losses := geminiFinishReasonFromCanonical(evt.StopReason)
		usage := (*proto.CanonicalUsage)(nil)
		if proto.UsageHasValue(s.Usage) {
			value := s.Usage
			usage = &value
		}
		s.Terminated = true
		return [][]byte{geminiSSEChunk(s, nil, finish, usage)}, losses, nil
	case "message_stop":
		s.Terminated = true
		return nil, nil, nil
	default:
		return nil, nil, nil
	}
}

func (c *GeminiClient) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	_ = ctx
	s, err := geminiClientStreamStateRef(state)
	if err != nil {
		return nil, err
	}
	if !s.Started || s.Terminated {
		return nil, nil
	}
	s.Terminated = true
	return [][]byte{geminiSSEChunk(s, nil, "STOP", nil)}, nil
}

func applyGeminiGenerationConfig(env *proto.HCSF, cfg *geminiGenerationConfig, losses *[]proto.ProtocolLossEntry) {
	if cfg == nil {
		return
	}
	env.RequestControls.Temperature = cfg.Temperature
	env.RequestControls.TopP = cfg.TopP
	env.RequestControls.MaxTokens = cfg.MaxOutputTokens
	env.RequestControls.StopSequences = append([]string(nil), cfg.StopSequences...)
	if cfg.TopK != nil {
		*losses = append(*losses, geminiClientInfoLoss("gemini generationConfig.topK has no HCSF request control yet", "gemini_top_k_unmodeled", ""))
	}
	if cfg.ResponseMIMEType != "" || len(bytes.TrimSpace(cfg.ResponseSchema)) > 0 {
		raw, _ := json.Marshal(map[string]any{
			"responseMimeType": cfg.ResponseMIMEType,
			"responseSchema":   rawJSONValue(cfg.ResponseSchema),
		})
		env.RequestControls.ResponseFormat = &proto.ResponseFormat{
			Type:   "gemini_generation_config",
			Schema: raw,
		}
	}
}

func geminiSystemText(content geminiClientContent, losses *[]proto.ProtocolLossEntry) string {
	var texts []string
	for _, p := range content.Parts {
		if p.Text != nil && *p.Text != "" {
			texts = append(texts, *p.Text)
		}
		if len(bytes.TrimSpace(p.InlineData)) > 0 || p.FunctionCall != nil || len(bytes.TrimSpace(p.FunctionResponse)) > 0 {
			*losses = append(*losses, geminiClientInfoLoss("gemini systemInstruction non-text part has no HCSF system prompt projection", "gemini_system_non_text_unmodeled", ""))
		}
	}
	return strings.Join(texts, "\n")
}

func convertGeminiTools(tools []geminiClientTool) ([]proto.CanonicalTool, []proto.ProtocolLossEntry, error) {
	var out []proto.CanonicalTool
	var losses []proto.ProtocolLossEntry
	for ti, tool := range tools {
		for fi, decl := range tool.FunctionDeclarations {
			if decl.Name == "" {
				return nil, nil, fmt.Errorf("proto: gemini tools[%d].functionDeclarations[%d] missing name", ti, fi)
			}
			out = append(out, proto.CanonicalTool{
				Name:        decl.Name,
				Description: decl.Description,
				InputSchema: normalizeRawObject(decl.Parameters),
			})
		}
		if len(bytes.TrimSpace(tool.GoogleSearch)) > 0 {
			losses = append(losses, geminiClientInfoLoss("gemini googleSearch tool has no HCSF tool declaration projection yet", "gemini_google_search_tool_unmodeled", proto.CapabilityToolUse))
		}
		if len(bytes.TrimSpace(tool.CodeExecution)) > 0 {
			losses = append(losses, geminiClientInfoLoss("gemini codeExecution tool has no HCSF tool declaration projection yet", "gemini_code_execution_tool_unmodeled", proto.CapabilityToolUse))
		}
		if len(bytes.TrimSpace(tool.Retrieval)) > 0 {
			losses = append(losses, geminiClientInfoLoss("gemini retrieval tool has no HCSF tool declaration projection yet", "gemini_retrieval_tool_unmodeled", proto.CapabilityToolUse))
		}
	}
	return out, losses, nil
}

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
			Type:   "tool_use",
			CallID: callID,
			Name:   part.FunctionCall.Name,
			Input:  normalizeGeminiFunctionArgs(part.FunctionCall.Args),
		})
	}
	if len(bytes.TrimSpace(part.FunctionResponse)) > 0 {
		losses = append(losses, geminiClientInfoLoss("gemini functionResponse part has no inbound HCSF tool_result projection yet", "gemini_function_response_unmodeled", proto.CapabilityToolResult))
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

func geminiCapabilityNode(id, role string, block proto.CanonicalContentBlock, msgIdx, blockIdx int) proto.CapabilityNode {
	source := &proto.NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blockIdx}
	switch block.Type {
	case "text":
		return proto.CapabilityNode{
			ID:          id,
			Kind:        proto.CapabilityText,
			StreamReady: proto.StreamReadyYes,
			Source:      source,
			Text:        &proto.TextNode{Role: role, Block: block},
		}
	case "image":
		image := geminiImageNode(block.Image)
		return proto.CapabilityNode{
			ID:          id,
			Kind:        proto.CapabilityImage,
			StreamReady: proto.StreamReadyNo,
			Source:      source,
			Image:       image,
		}
	case "tool_use":
		return proto.CapabilityNode{
			ID:          id,
			Kind:        proto.CapabilityToolUse,
			StreamReady: proto.StreamReadyPartial,
			Source:      source,
			ToolUse: &proto.ToolUseNode{
				ToolCallID: block.CallID,
				Name:       block.Name,
				Input:      normalizeRawObject(block.Input),
				Status:     proto.ToolNodeComplete,
			},
		}
	default:
		return proto.CapabilityNode{}
	}
}

func geminiImageNode(raw json.RawMessage) *proto.ImageNode {
	var inline geminiInlineData
	_ = json.Unmarshal(raw, &inline)
	return &proto.ImageNode{
		SourceKind: proto.DataSourceInlineBase64,
		MediaType:  inline.MIMEType,
		Locator: proto.DataLocator{
			Kind:  proto.DataSourceInlineBase64,
			Value: inline.Data,
		},
	}
}

func canonicalBlocksToGeminiParts(blocks []proto.CanonicalContentBlock) ([]geminiPart, []proto.ProtocolLossEntry, error) {
	var parts []geminiPart
	var losses []proto.ProtocolLossEntry
	for i, block := range blocks {
		part, err := geminiPartFromCanonicalBlock(block)
		if err != nil {
			losses = append(losses, geminiClientResponseWarningLoss(fmt.Sprintf("canonical block[%d] has no Gemini projection: %s", i, block.Type), "gemini_response_block_unmapped", ""))
			continue
		}
		parts = append(parts, part)
	}
	return parts, losses, nil
}

func geminiPartFromCanonicalBlock(block proto.CanonicalContentBlock) (geminiPart, error) {
	switch block.Type {
	case "text":
		text := block.Text
		return geminiPart{Text: &text}, nil
	case "reasoning", "reasoning_summary":
		text := firstNonEmpty(block.ReasoningSummary, block.Thinking, block.Text)
		return geminiPart{Text: &text, Thought: true}, nil
	case "tool_use":
		return geminiPart{FunctionCall: &geminiFunctionCall{
			ID:   block.CallID,
			Name: block.Name,
			Args: normalizeRawObject(block.Input),
		}}, nil
	case "image":
		return geminiPart{InlineData: cloneRaw(block.Image)}, nil
	default:
		return geminiPart{}, fmt.Errorf("unsupported block type %q", block.Type)
	}
}

func geminiFinishReasonFromCanonical(stop proto.CanonicalStopReason) (string, []proto.ProtocolLossEntry) {
	switch stop {
	case proto.CanonicalStopEndTurn, proto.CanonicalStopSequence, "":
		return "STOP", nil
	case proto.CanonicalStopMaxTokens:
		return "MAX_TOKENS", nil
	case proto.CanonicalStopRefusal, StopSafety:
		return "SAFETY", nil
	default:
		return "FINISH_REASON_UNSPECIFIED", []proto.ProtocolLossEntry{geminiClientResponseWarningLoss("canonical stop reason has no exact Gemini finishReason: "+string(stop), "gemini_finish_reason_unmapped", "")}
	}
}

func geminiUsageFromCanonical(usage proto.CanonicalUsage) *geminiUsageMetadata {
	if !proto.UsageHasValue(usage) {
		return nil
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	return &geminiUsageMetadata{
		PromptTokenCount:        usage.InputTokens,
		CandidatesTokenCount:    usage.OutputTokens,
		TotalTokenCount:         total,
		CachedContentTokenCount: usage.CacheReadInputTokens,
	}
}

func geminiSSEChunk(state *GeminiClientStreamState, parts []geminiPart, finish string, usage *proto.CanonicalUsage) []byte {
	chunk := geminiGenerateContentResponse{
		ResponseID:   state.ResponseID,
		ModelVersion: state.Model,
	}
	candidate := geminiCandidate{Index: 0}
	if len(parts) > 0 {
		candidate.Content = geminiContent{Role: "model", Parts: parts}
	}
	if finish != "" {
		candidate.FinishReason = finish
	}
	if len(parts) > 0 || finish != "" {
		chunk.Candidates = []geminiCandidate{candidate}
	}
	if usage != nil {
		chunk.UsageMetadata = geminiUsageFromCanonical(*usage)
	}
	body, _ := json.Marshal(chunk)
	return proto.EmitSSEDataLine(body)
}

func geminiModelFromIngressPath(path string) string {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if idx := strings.LastIndex(rest, ":"); idx > 0 {
		return rest[:idx]
	}
	return ""
}

func canonicalRoleFromGemini(role string) string {
	switch role {
	case "model", "assistant":
		return "assistant"
	case "system":
		return "system"
	default:
		return "user"
	}
}

func geminiRequestExtrasLosses(extras proto.PassthroughEnvelope) []proto.ProtocolLossEntry {
	if len(extras.Extra) == 0 {
		return nil
	}
	out := make([]proto.ProtocolLossEntry, 0, len(extras.Extra))
	for key := range extras.Extra {
		out = append(out, geminiClientInfoLoss("gemini top-level field has no typed HCSF projection: "+key, "gemini_top_level_unmodeled", ""))
	}
	return out
}

func geminiClientInfoLoss(reason, code string, capability proto.CapabilityKind) proto.ProtocolLossEntry {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossInfo, reason, code, capability, "")
	loss.Vendor = string(proto.ClientProtocolGemini)
	return loss
}

func geminiClientWarningLoss(reason, code string, capability proto.CapabilityKind) proto.ProtocolLossEntry {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, reason, code, capability, "")
	loss.Vendor = string(proto.ClientProtocolGemini)
	return loss
}

func geminiClientResponseWarningLoss(reason, code string, capability proto.CapabilityKind) proto.ProtocolLossEntry {
	loss := geminiClientWarningLoss(reason, code, capability)
	loss.Direction = string(proto.DirectionCanonicalToClient)
	return loss
}

func normalizeRawObject(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	return cloneRaw(trimmed)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return json.RawMessage(out)
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
