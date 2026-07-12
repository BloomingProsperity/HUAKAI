package openai

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ResponsesUpstreamState 累计 Responses 流式 output item 的跨事件状态。
type ResponsesUpstreamState struct {
	ResponseID string
	Model      string
	Started    bool
	Terminated bool
	Items      map[int]*responsesItemState
}

type responsesItemState struct {
	Index            int
	ID               string
	Type             string
	CallID           string
	Name             string
	Signature        string
	Started          bool
	Stopped          bool
	TextDeltaSeen    bool
	ArgsDeltaSeen    bool
	ReasoningSeen    bool
	Text             strings.Builder
	Arguments        strings.Builder
	ReasoningSummary strings.Builder
}

type responsesStreamEvent struct {
	Type         string               `json:"type"`
	Response     *responsesObject     `json:"response,omitempty"`
	OutputIndex  *int                 `json:"output_index,omitempty"`
	ItemID       string               `json:"item_id,omitempty"`
	Item         *responsesOutputItem `json:"item,omitempty"`
	Delta        string               `json:"delta,omitempty"`
	Text         string               `json:"text,omitempty"`
	Arguments    string               `json:"arguments,omitempty"`
	Error        *responsesError      `json:"error,omitempty"`
	ContentIndex int                  `json:"content_index,omitempty"`
	Part         *responsesPart       `json:"part,omitempty"`
}

type responsesObject struct {
	ID                string                     `json:"id,omitempty"`
	Model             string                     `json:"model,omitempty"`
	Status            string                     `json:"status,omitempty"`
	Output            []responsesOutputItem      `json:"output,omitempty"`
	Usage             *responsesUsage            `json:"usage,omitempty"`
	IncompleteDetails *responsesIncomplete       `json:"incomplete_details,omitempty"`
	Error             *responsesError            `json:"error,omitempty"`
	Passthrough       *proto.PassthroughEnvelope `json:"-"`
}

type responsesOutputItem struct {
	ID               string                   `json:"id,omitempty"`
	Type             string                   `json:"type,omitempty"`
	Role             string                   `json:"role,omitempty"`
	CallID           string                   `json:"call_id,omitempty"`
	Name             string                   `json:"name,omitempty"`
	Arguments        string                   `json:"arguments,omitempty"`
	Status           string                   `json:"status,omitempty"`
	EncryptedContent string                   `json:"encrypted_content,omitempty"`
	Content          []responsesOutputContent `json:"content,omitempty"`
	Summary          []responsesReasoningPart `json:"summary,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type responsesReasoningPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type responsesPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type responsesIncomplete struct {
	Reason string `json:"reason,omitempty"`
}

type responsesError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type responsesUsage struct {
	InputTokens         int                          `json:"input_tokens,omitempty"`
	OutputTokens        int                          `json:"output_tokens,omitempty"`
	TotalTokens         int                          `json:"total_tokens,omitempty"`
	InputTokensDetails  *responsesInputTokenDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputTokenDetails `json:"output_tokens_details,omitempty"`
}

type responsesInputTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type responsesOutputTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}
