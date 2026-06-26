package proto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// P-2 D0 ClientAdapter 的共享基础设施（client wire ↔ canonical）。
//
// 范围（按 P-2 ClientAdapter synthesis §5.1 Q5 决策 A）：
//   - RequestMetaSeed + context 注入；client adapter 的 RequestToCanonical
//     从 context 读 metadata 填充 HCSF RequestMeta 必填字段。
//   - ClientAdapterRegistry：client_protocol → adapter 映射。
//   - SSE emit 辅助：拼装 SSE event/data/DONE。
//   - 类型化 ProtocolLossEntry 构造器：强制 v0.4 Severity + Reason/Code 非空，
//     避免 silent drop。
//
// 本文件不实现具体 vendor parsing；D1/D5/D9 各 client adapter 在其自身文件落具体逻辑。

// ----------------------------------------------------------------------------
// RequestMetaSeed + context 注入
// ----------------------------------------------------------------------------

// RequestMetaSeed 在 HTTP 入口（gatewayhttp）填充后注入 context；client adapter 的
// RequestToCanonical 通过 [RequestMetaSeedFromContext] 读取，用以满足 HCSF
// RequestMeta 必填字段（RequestID/ClientProtocol/ProtocolFamily/Model 通过 body
// 解析提供，IngressPath/Tenant/Route/Account/AcquisitionToken 由 seed 提供）。
//
// 设计选择：用 context value 而不是扩展 ClientAdapter 接口签名，避免破坏现有
// 四 hookpoint 接口（synthesis Q5 决策 A）。
type RequestMetaSeed struct {
	// RequestID 必填；HUAKAI 内部 request 追踪 ID。
	RequestID string

	// ClientProtocol 必填；openai_chat / openai_responses / anthropic_messages。
	ClientProtocol ClientProtocol

	// ProtocolFamily 必填；用于 forwarder / dispatcher 选择 upstream。
	ProtocolFamily string

	// IngressPath 必填；如 /v1/chat/completions、/v1/messages、/v1/responses。
	IngressPath string

	// Model 可选；path-scoped 的 native 协议（如 Gemini v1beta）把模型名带在
	// URL 里而不是 JSON body 里。
	Model string

	// TenantID 可选；0 表示无租户上下文。
	TenantID int64

	// RouteID 可选；沿用 gateway ForwardRequest.RouteID。
	RouteID string

	// AccountID 可选；选中的 provider_account_id；仅做审计 / PASR 反馈。
	AccountID int64

	// AcquisitionToken 可选；UUID 字符串。
	AcquisitionToken string

	// EvidenceLabel 可选；默认 mock，P-6 后才可填 smoke / real。
	EvidenceLabel EvidenceLabel

	// ForceFormat 可选；默认 false，保持客户端响应格式现状。
	ForceFormat bool
}

type requestMetaSeedKey struct{}

// ContextWithRequestMetaSeed 把 seed 挂到 context；多次调用会覆盖。
func ContextWithRequestMetaSeed(ctx context.Context, seed RequestMetaSeed) context.Context {
	return context.WithValue(ctx, requestMetaSeedKey{}, &seed)
}

// RequestMetaSeedFromContext 取出 seed；找不到返回 ok=false。
// 不复制，调用方仅读不改。
func RequestMetaSeedFromContext(ctx context.Context) (*RequestMetaSeed, bool) {
	v, ok := ctx.Value(requestMetaSeedKey{}).(*RequestMetaSeed)
	return v, ok
}

// ErrMissingRequestMetaSeed RequestToCanonical 期望 seed 而 context 未注入时返回。
var ErrMissingRequestMetaSeed = errors.New("proto: RequestMetaSeed not found in context")

// ApplyToRequestMeta 把 seed 必填字段写入 RequestMeta；Model / UpstreamProtocol /
// UpstreamModel 由 adapter 解析 body + 查 registry 单独填。
// 仅在 seed.RequestID / ClientProtocol / ProtocolFamily / IngressPath 全部非空
// 时才返回 nil；否则返回明确错误，避免后续 ValidateEnvelope 报出模糊错误。
func (s *RequestMetaSeed) ApplyToRequestMeta(meta *RequestMeta) error {
	if s == nil {
		return ErrMissingRequestMetaSeed
	}
	if meta == nil {
		return errors.New("proto: ApplyToRequestMeta nil meta")
	}
	if s.RequestID == "" {
		return errors.New("proto: RequestMetaSeed.RequestID required")
	}
	if s.ClientProtocol == "" {
		return errors.New("proto: RequestMetaSeed.ClientProtocol required")
	}
	if s.ProtocolFamily == "" {
		return errors.New("proto: RequestMetaSeed.ProtocolFamily required")
	}
	if s.IngressPath == "" {
		return errors.New("proto: RequestMetaSeed.IngressPath required")
	}
	meta.RequestID = s.RequestID
	meta.ClientProtocol = s.ClientProtocol
	meta.ProtocolFamily = s.ProtocolFamily
	meta.IngressPath = s.IngressPath
	if s.Model != "" {
		meta.Model = s.Model
	}
	meta.TenantID = s.TenantID
	meta.RouteID = s.RouteID
	meta.AccountID = s.AccountID
	meta.AcquisitionToken = s.AcquisitionToken
	if s.EvidenceLabel != "" {
		meta.EvidenceLabel = s.EvidenceLabel
	}
	return nil
}

// ----------------------------------------------------------------------------
// ClientAdapterRegistry
// ----------------------------------------------------------------------------

// ClientAdapterRegistry 维护 client_protocol → ClientAdapter 映射；与
// UpstreamProtocol 用的 protocol_selector.BuildDefaultProtocolAdapterRegistry
// 解耦（synthesis Agreements A2：职责分离）。
type ClientAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[ClientProtocol]ClientAdapter
}

// NewClientAdapterRegistry 构造空 registry。调用方负责注册。
func NewClientAdapterRegistry() *ClientAdapterRegistry {
	return &ClientAdapterRegistry{
		adapters: make(map[ClientProtocol]ClientAdapter),
	}
}

// ErrClientAdapterAlreadyRegistered Register 时 protocol 已被占用。
var ErrClientAdapterAlreadyRegistered = errors.New("proto: client adapter already registered for protocol")

// Register 注册一个 client adapter；重复 register 同 protocol 返回错误，避免
// silent overwrite。protocol == "" 或 adapter == nil 也会拒绝。
func (r *ClientAdapterRegistry) Register(protocol ClientProtocol, adapter ClientAdapter) error {
	if r == nil {
		return errors.New("proto: nil ClientAdapterRegistry")
	}
	if protocol == "" {
		return errors.New("proto: empty ClientProtocol")
	}
	if adapter == nil {
		return errors.New("proto: nil ClientAdapter")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[protocol]; exists {
		return fmt.Errorf("%w: %s", ErrClientAdapterAlreadyRegistered, protocol)
	}
	r.adapters[protocol] = adapter
	return nil
}

// Lookup 取 adapter；未注册返回 ok=false。
func (r *ClientAdapterRegistry) Lookup(protocol ClientProtocol) (ClientAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[protocol]
	return a, ok
}

// Protocols 返回已注册 protocol 列表，按字典序排序，便于测试断言稳定。
func (r *ClientAdapterRegistry) Protocols() []ClientProtocol {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ClientProtocol, 0, len(r.adapters))
	for p := range r.adapters {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ----------------------------------------------------------------------------
// SSE 输出辅助函数
// ----------------------------------------------------------------------------

// EmitSSEEvent 拼装一个具名 SSE event：
//
//	event: <name>\ndata: <data bytes>\n\n
//
// Anthropic Messages SSE 与 OpenAI Responses SSE 均需要 named event，OpenAI Chat
// 不强制 event name（用 EmitSSEDataLine 即可）。data 不做额外 JSON 验证。
func EmitSSEEvent(name string, data []byte) []byte {
	var buf bytes.Buffer
	if name != "" {
		buf.WriteString("event: ")
		buf.WriteString(name)
		buf.WriteByte('\n')
	}
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")
	return buf.Bytes()
}

// EmitSSEDataLine 不带 event 名的 SSE chunk，OpenAI Chat 兼容形态：
//
//	data: <data bytes>\n\n
func EmitSSEDataLine(data []byte) []byte {
	return EmitSSEEvent("", data)
}

// EmitSSEDone 输出 OpenAI Chat 风格终止哨兵 `data: [DONE]\n\n`。
// OpenAI Responses 默认不发（synthesis Q8 决策 B：不追加 [DONE]）。
func EmitSSEDone() []byte {
	return []byte("data: [DONE]\n\n")
}

// ----------------------------------------------------------------------------
// 类型化 ClientLossEntry 构造器
// ----------------------------------------------------------------------------

// NewClientLossEntry 构造一条 v0.4 ProtocolLossEntry，强制 Severity + Reason
// 至少一项非空，避免 silent drop。Code 推荐填稳定机器可读码。
// Direction 默认 client_to_canonical；调用方可在返回后覆盖。
func NewClientLossEntry(severity ProtocolLossSeverity, reason, code string, capability CapabilityKind, nodeID string) (ProtocolLossEntry, error) {
	if severity == "" {
		return ProtocolLossEntry{}, errors.New("proto: client loss entry requires non-empty Severity")
	}
	if reason == "" && code == "" {
		return ProtocolLossEntry{}, errors.New("proto: client loss entry requires Reason or Code")
	}
	return ProtocolLossEntry{
		Severity:   severity,
		Reason:     reason,
		Code:       code,
		Capability: capability,
		NodeID:     nodeID,
		Direction:  string(DirectionClientToCanonical),
	}, nil
}
