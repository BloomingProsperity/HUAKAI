package proto

import (
	"encoding/json"
	"fmt"
)

// HopHop 是链路单跳名称的闭集合，避免任意字符串进入回执与日志。
type HopHop string

const (
	HopIngress  HopHop = "ingress"
	HopRouter   HopHop = "router"
	HopPool     HopHop = "pool"
	HopAccount  HopHop = "account"
	HopProvider HopHop = "provider"
	HopResponse HopHop = "response"
)

// HopAttestation 记录一次请求在各处理阶段的证据。敏感内容不能写入 Detail。
type HopAttestation struct {
	SchemaVersion string   `json:"schema_version,omitempty"`
	HopIndex      int      `json:"hop_index,omitempty"`
	HopKind       string   `json:"hop_kind,omitempty"`
	Actor         string   `json:"actor,omitempty"`
	StartedAt     string   `json:"started_at,omitempty"`
	EndedAt       string   `json:"ended_at,omitempty"`
	DecisionRef   string   `json:"decision_ref,omitempty"`
	FeatureRefs   []string `json:"feature_refs,omitempty"`
	AltEventID    string   `json:"alt_event_id,omitempty"`

	Hop       HopHop `json:"hop,omitempty"`
	Timestamp string `json:"ts,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	// AccountIDHash 只能保存散列后的账号标识，不能记录原始账号 ID。
	AccountIDHash string          `json:"account_id_hash,omitempty"`
	PoolID        string          `json:"pool_id,omitempty"`
	RouteID       string          `json:"route_id,omitempty"`
	Provider      string          `json:"provider,omitempty"`
	Endpoint      string          `json:"endpoint,omitempty"`
	DurationMS    int64           `json:"duration_ms,omitempty"`
	Detail        json.RawMessage `json:"detail,omitempty"`
}

// ModelChain 保存请求模型、路由决策模型和上游报告模型，用于对外展示一致性结论。
type ModelChain struct {
	Requested        string `json:"requested"`
	RouteDecided     string `json:"route_decided"`
	UpstreamReported string `json:"upstream_reported,omitempty"`
	Verdict          string `json:"verdict,omitempty"`
}

// IsConsistent 判断当前已知的模型标识是否一致。上游尚未返回模型名时只比较前两项。
func (m *ModelChain) IsConsistent() bool {
	if m == nil {
		return true
	}
	if m.Requested == "" || m.RouteDecided == "" {
		return false
	}
	if m.Requested != m.RouteDecided {
		return false
	}
	return m.UpstreamReported == "" || m.UpstreamReported == m.Requested
}

var allValidHops = map[HopHop]struct{}{
	HopIngress:  {},
	HopRouter:   {},
	HopPool:     {},
	HopAccount:  {},
	HopProvider: {},
	HopResponse: {},
}

// IsValidHop 检查单跳名称是否属于约定集合。
func IsValidHop(h HopHop) bool {
	if h == "" {
		return false
	}
	_, ok := allValidHops[h]
	return ok
}

// MismatchReason 标明模型链不一致的具体维度。
type MismatchReason string

const (
	MismatchRouteDecidedDiffersFromRequested MismatchReason = "route_decided_differs_from_requested"
	MismatchUpstreamDiffersFromRequested     MismatchReason = "upstream_reported_differs_from_requested"
	MismatchRouteEmpty                       MismatchReason = "route_decided_empty"
	MismatchRequestedEmpty                   MismatchReason = "requested_empty"
)

// EmitModelMismatchLoss 将模型链不一致转为可审计的非静默告警条目。
func EmitModelMismatchLoss(mc *ModelChain) []ProtocolLossEntry {
	if mc == nil {
		return nil
	}
	var losses []ProtocolLossEntry
	if mc.Requested == "" {
		loss, _ := NewClientLossEntry(
			ProtocolLossWarning,
			"model_chain.requested is empty; cannot verify model integrity",
			string(MismatchRequestedEmpty),
			"",
			"",
		)
		losses = append(losses, loss)
	}
	if mc.RouteDecided == "" {
		loss, _ := NewClientLossEntry(
			ProtocolLossWarning,
			"model_chain.route_decided is empty; router did not annotate route",
			string(MismatchRouteEmpty),
			"",
			"",
		)
		losses = append(losses, loss)
	}
	if mc.Requested != "" && mc.RouteDecided != "" && mc.Requested != mc.RouteDecided {
		loss, _ := NewClientLossEntry(
			ProtocolLossWarning,
			fmt.Sprintf("model substitution detected: requested=%q route_decided=%q", mc.Requested, mc.RouteDecided),
			string(MismatchRouteDecidedDiffersFromRequested),
			"",
			"",
		)
		losses = append(losses, loss)
	}
	if mc.UpstreamReported != "" && mc.Requested != "" && mc.UpstreamReported != mc.Requested {
		loss, _ := NewClientLossEntry(
			ProtocolLossWarning,
			fmt.Sprintf("upstream model deviation: requested=%q upstream_reported=%q", mc.Requested, mc.UpstreamReported),
			string(MismatchUpstreamDiffersFromRequested),
			"",
			"",
		)
		losses = append(losses, loss)
	}
	return losses
}
