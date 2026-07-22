package proto

import (
	"encoding/json"
	"fmt"
)

// HCSFVersion 是 HCSFEnvelope 锁定的结构版本。
const HCSFVersion = "0.4"

// HCSFEnvelope 是请求、缓冲响应与重放事件共用的内存载体。
type HCSFEnvelope struct {
	Version            string                     `json:"version"`
	RequestMeta        RequestMeta                `json:"request_meta"`
	RequestControls    RequestControls            `json:"request_controls"`
	Messages           []CanonicalMessage         `json:"messages"`
	BufferedResponse   *CanonicalResponse         `json:"buffered_response,omitempty"`
	StreamEvents       []CanonicalEvent           `json:"stream_events,omitempty"`
	CapabilityGraph    CapabilityGraph            `json:"capability_graph"`
	ProviderProjection ProviderProjection         `json:"provider_projection"`
	StreamPlan         StreamPlan                 `json:"stream_plan"`
	Accounting         Accounting                 `json:"accounting"`
	Policy             Policy                     `json:"policy"`
	Extensions         map[string]json.RawMessage `json:"extensions,omitempty"`

	// Passthrough 保存已识别但当前规范尚无一等字段的原生参数，不参与 JSON 输出。
	Passthrough *PassthroughEnvelope `json:"-"`
}

// NewEmptyEnvelope 构造最小合法信封，供测试与固定用例作为起点。
func NewEmptyEnvelope() *HCSFEnvelope {
	return &HCSFEnvelope{
		Version:         HCSFVersion,
		RequestMeta:     RequestMeta{},
		RequestControls: RequestControls{},
		Messages:        []CanonicalMessage{},
		CapabilityGraph: CapabilityGraph{
			Nodes: []CapabilityNode{},
			Edges: []CapabilityEdge{},
		},
		ProviderProjection: ProviderProjection{
			CapabilityResults: []CapabilityProjection{},
		},
		StreamPlan: StreamPlan{
			Mode:                     StreamModeBuffered,
			EventClasses:             []string{},
			FlushPolicy:              "per_event",
			TerminalRequired:         true,
			SyntheticTerminalAllowed: true,
			FallbackBoundary:         FallbackAfterFirstByteBlocked,
			MidStreamFallbackPolicy:  MidStreamFallbackNone,
		},
		Accounting: Accounting{},
		Policy: Policy{
			DataRetention: DataRetentionNode{
				Value:       DataRetentionUnknown,
				Enforcement: "unknown",
				AuditLabel:  "unknown",
			},
			Auth:      AuthPolicyStandard,
			Audit:     AuditPolicy{Visibility: AuditVisible, Label: "default"},
			Redaction: RedactionPublic,
		},
	}
}

// ValidateEnvelopeVersionGuard 仅检查热路径上必须满足的空值与版本约束。
// 完整结构校验由 ValidateEnvelope 及调试构建承担。
func ValidateEnvelopeVersionGuard(env *HCSFEnvelope) error {
	if env == nil {
		return &ValidationError{Inv: "INV-0", Message: "envelope is nil"}
	}
	if env.Version != HCSFVersion {
		return &ValidationError{
			Inv:     "INV-4",
			Message: fmt.Sprintf("Version must be %q, got %q", HCSFVersion, env.Version),
		}
	}
	return nil
}
