package proto

import (
	"errors"
	"fmt"
)

var ErrUnsupportedFeature = errors.New("proto: unsupported feature for selected upstream")

// ClientProtocol is the client-side protocol enum from spec section 4.1.
type ClientProtocol string

const (
	ClientProtocolOpenAIChat        ClientProtocol = "openai_chat"
	ClientProtocolOpenAIResponses   ClientProtocol = "openai_responses"
	ClientProtocolAnthropicMessages ClientProtocol = "anthropic_messages"
	ClientProtocolGemini            ClientProtocol = "gemini"
)

// UpstreamProtocol is the upstream protocol enum from spec section 4.1.
type UpstreamProtocol string

const (
	UpstreamProtocolAnthropic   UpstreamProtocol = "anthropic"
	UpstreamProtocolOpenAI      UpstreamProtocol = "openai"
	UpstreamProtocolGemini      UpstreamProtocol = "gemini"
	UpstreamProtocolBedrock     UpstreamProtocol = "bedrock"
	UpstreamProtocolAntigravity UpstreamProtocol = "antigravity"
)

// FeatureName is the protocol capability feature enum from spec section 4.1.
type FeatureName string

const (
	FeatureTextStreaming          FeatureName = "text_streaming"
	FeatureToolUse                FeatureName = "tool_use"
	FeatureReasoningSummary       FeatureName = "reasoning_summary"
	FeatureParallelToolCalls      FeatureName = "parallel_tool_calls"
	FeatureStructuredOutputSchema FeatureName = "structured_output_schema"
	FeatureImageInput             FeatureName = "image_input"
	FeatureAudioInput             FeatureName = "audio_input"
	FeatureImageOutput            FeatureName = "image_output"
	FeatureMaxTokensFinishReason  FeatureName = "max_tokens_finish_reason"
	FeatureMaxCompletionTokens    FeatureName = "max_completion_tokens"
	FeatureStopSequenceEmit       FeatureName = "stop_sequence_emit"
	FeatureCacheBreakpoints       FeatureName = "cache_breakpoints"
	FeatureSignatureDelta         FeatureName = "signature_delta"
	FeatureSystemPromptArray      FeatureName = "system_prompt_array"
	FeatureMultiRoleMessages      FeatureName = "multi_role_messages"
)

var allFeatures = []FeatureName{
	FeatureTextStreaming, FeatureToolUse, FeatureReasoningSummary,
	FeatureParallelToolCalls, FeatureStructuredOutputSchema, FeatureImageInput,
	FeatureAudioInput, FeatureImageOutput, FeatureMaxTokensFinishReason,
	FeatureMaxCompletionTokens, FeatureStopSequenceEmit, FeatureCacheBreakpoints,
	FeatureSignatureDelta, FeatureSystemPromptArray, FeatureMultiRoleMessages,
}

// CapabilityMatrix is the in-memory protocol matrix from spec section 4.1.
type CapabilityMatrix map[ClientProtocol]map[UpstreamProtocol]map[FeatureName]Verdict

func (m CapabilityMatrix) Lookup(client ClientProtocol, upstream UpstreamProtocol, feature FeatureName) Verdict {
	if byUpstream, ok := m[client]; ok {
		if byFeature, ok := byUpstream[upstream]; ok {
			if verdict, ok := byFeature[feature]; ok {
				return verdict
			}
		}
	}
	return VerdictUnsupported
}

func DefaultMatrix() CapabilityMatrix {
	clients := []ClientProtocol{ClientProtocolOpenAIChat, ClientProtocolOpenAIResponses, ClientProtocolAnthropicMessages, ClientProtocolGemini}
	upstreams := []UpstreamProtocol{UpstreamProtocolAnthropic, UpstreamProtocolOpenAI, UpstreamProtocolGemini, UpstreamProtocolBedrock, UpstreamProtocolAntigravity}
	m := CapabilityMatrix{}
	for _, c := range clients {
		m[c] = map[UpstreamProtocol]map[FeatureName]Verdict{}
		for _, u := range upstreams {
			cell := map[FeatureName]Verdict{}
			for _, f := range allFeatures {
				cell[f] = VerdictPreserved
			}
			if u == UpstreamProtocolGemini || u == UpstreamProtocolBedrock || u == UpstreamProtocolAntigravity {
				cell[FeatureImageInput], cell[FeatureAudioInput], cell[FeatureImageOutput] = VerdictUnsupported, VerdictUnsupported, VerdictUnsupported
			}
			if (c == ClientProtocolAnthropicMessages && u == UpstreamProtocolOpenAI) || (c == ClientProtocolOpenAIChat && u == UpstreamProtocolAnthropic) {
				cell[FeatureReasoningSummary], cell[FeatureSignatureDelta] = VerdictLossy, VerdictLossy
			}
			m[c][u] = cell
		}
	}
	return m
}

func (m CapabilityMatrix) Validate(req CanonicalRequest, client ClientProtocol, upstream UpstreamProtocol) ([]ProtocolLossEntry, error) {
	var losses []ProtocolLossEntry
	for _, feature := range detectFeaturesInRequest(req) {
		verdict := m.Lookup(client, upstream, feature)
		switch verdict {
		case VerdictPreserved:
		case VerdictLossy:
			losses = append(losses, newLossEntry(feature, DirectionCanonicalToUpstream, verdict, "feature translated with reduced protocol fidelity"))
		case VerdictUnsupported:
			losses = append(losses, newLossEntry(feature, DirectionCanonicalToUpstream, verdict, "feature has no defined translation path for selected route"))
			return losses, fmt.Errorf("%w: %s", ErrUnsupportedFeature, feature)
		}
	}
	return losses, nil
}

func detectFeaturesInRequest(req CanonicalRequest) []FeatureName {
	seen := map[FeatureName]bool{}
	add := func(f FeatureName) { seen[f] = true }
	for _, item := range []struct {
		on bool
		f  FeatureName
	}{
		{req.Stream, FeatureTextStreaming},
		{len(req.Tools) > 0 || req.ToolChoice != nil, FeatureToolUse},
		{req.ParallelToolCalls, FeatureParallelToolCalls},
		{len(req.ResponseFormat) > 0, FeatureStructuredOutputSchema},
		{len(req.StopSequences) > 0, FeatureStopSequenceEmit},
		{req.SystemPrompt != "", FeatureSystemPromptArray},
	} {
		if item.on {
			add(item.f)
		}
	}
	if req.MaxTokens > 0 {
		add(FeatureMaxCompletionTokens)
		add(FeatureMaxTokensFinishReason)
	}
	roles := map[string]bool{}
	for _, msg := range req.Messages {
		roles[msg.Role] = true
		for _, b := range msg.Content {
			switch b.Type {
			case "tool_use", "tool_result":
				add(FeatureToolUse)
			case "image":
				add(FeatureImageInput)
			case "audio":
				add(FeatureAudioInput)
			case "image_output":
				add(FeatureImageOutput)
			case "reasoning_summary":
				add(FeatureReasoningSummary)
			case "cache_breakpoint":
				add(FeatureCacheBreakpoints)
			case "signature_delta":
				add(FeatureSignatureDelta)
			}
		}
	}
	if len(roles) > 1 {
		add(FeatureMultiRoleMessages)
	}
	out := make([]FeatureName, 0, len(seen))
	for _, f := range allFeatures {
		if seen[f] {
			out = append(out, f)
		}
	}
	return out
}

func NewLossEntry(feature FeatureName, dir Direction, verdict Verdict, note string) ProtocolLossEntry {
	return ProtocolLossEntry{Feature: string(feature), Direction: string(dir), Verdict: verdict, Note: note}
}

func newLossEntry(feature FeatureName, dir Direction, verdict Verdict, note string) ProtocolLossEntry {
	return NewLossEntry(feature, dir, verdict, note)
}
