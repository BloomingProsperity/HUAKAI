// Package clientshape — U6-D-2 atomic：客户端 wire shape 选择决策。
//
// 输入:
//   - request 路径 (`/v1/chat/completions` / `/v1/messages` / 通用)
//   - route 配置中已声明的 ClientProtocol (gateway-owned generic endpoint 用)
//   - clientid.Identity + 检测置信度
//   - identity_aware 路由标记 (operator policy)
//
// 输出 Decision:
//   - ClientProtocol (OpenAIChat / AnthropicMessages / OpenAIResponses)
//   - 决策来源 enum (path / route_config / identity / default)
//   - 置信度 + 冲突标记 (用于 metrics + audit)
//
// 选择优先级（参 docs/process/plans/2026-05-08-upgrade6-u6d-synthesis.md 综合 codex
// lane 的"path/route 优先"决策）：
//   1. explicit path: /v1/chat/completions → OpenAIChat 等显式路径强 contract
//   2. route_config: 显式路由配置中已声明的 ClientProtocol
//   3. identity (仅当 IdentityAware=true 且 confidence ≥ MinConfidence):
//      identity 填补 path 模糊 / route 未声明
//   4. default: 路径无法识别且 identity 未启用，返回 OpenAIChat (HUAKAI 默认)
//
// **identity 不覆盖 path/route**——header spoof 风险高于 confidence 信号。
// **identity 不影响 ProtocolFamily 选择**——upstream 仍由 model alias 驱动。
package clientshape

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// DecisionSource 标记 Decision.ClientProtocol 是怎么得来的。
// 用于 metrics label + audit 调试。
type DecisionSource string

const (
	// SourceExplicitPath: request URL path 强 contract（如 /v1/chat/completions）
	SourceExplicitPath DecisionSource = "explicit_path"
	// SourceRouteConfig: 显式 route 配置声明的 ClientProtocol
	SourceRouteConfig DecisionSource = "route_config"
	// SourceIdentity: 由 clientid.Identity 推断（仅当 IdentityAware=true 且
	// confidence 足够）
	SourceIdentity DecisionSource = "identity"
	// SourceDefault: 上述均不适用，回到 HUAKAI 默认 ClientProtocolOpenAIChat
	SourceDefault DecisionSource = "default"
)

// MinIdentityConfidence 是 identity 信号被信任的最低 confidence 阈值。
// codex synthesis：单一 threshold 不足，但作为 baseline 设 0.7；
// 真正决策还要看 path consistency（与 path-derived 的 protocol 一致才信）。
const MinIdentityConfidence = 0.7

// Inputs 是 Select 的输入聚合体。
type Inputs struct {
	// Path: HTTP request URL Path (e.g., "/v1/chat/completions")
	Path string

	// RouteConfigClient: route 配置中已声明的 ClientProtocol。
	// 零值（""）表示未声明；非零值时跳过 path 推断直接使用。
	RouteConfigClient proto.ClientProtocol

	// Identity / IdentityConf: clientid 检测结果（middleware 已挂在 ctx）
	Identity     clientid.Identity
	IdentityConf float64

	// IdentityAware: 当前 route 是否启用 identity-aware 模式。默认 false
	// 表示 path/route 决定 ClientProtocol，identity 仅作 metric 不参决策。
	// (运维通过 route 配置或 feature flag 切换)
	IdentityAware bool
}

// Decision 是 Select 输出。
type Decision struct {
	// ClientProtocol: 选定的客户端 wire format
	ClientProtocol proto.ClientProtocol

	// Source: 决策来源 (audit / metrics)
	Source DecisionSource

	// Confidence: 0.0-1.0；path/route 来源永远 1.0；identity 来源沿用
	// IdentityConf；default 来源 0.5
	Confidence float64

	// Conflict: identity 信号与 path 推断 ClientProtocol 不一致。
	// 不阻止决策——仍按 path 走——但记 metric。
	Conflict bool

	// ConflictReason: 当 Conflict=true 时填可读说明 (audit log)
	ConflictReason string
}

// Select 根据 Inputs 计算 ClientShape Decision。
// 纯函数, 无副作用, 无 ctx 依赖。
//
// 优先级（synthesis "explicit path 优先"——sonnet 抓到 RouteConfig 不应
// override well-known path 这一点）：
//  1. 路径命中已知 wire-contract (如 /v1/chat/completions) → 走 path
//  2. RouteConfig 显式声明（非空，且 path 未命中已知）→ 走 route_config
//     适用于 gateway-owned 通用 endpoint
//  3. identity 仅当 IdentityAware=true 且 confidence 足够 → 填补空白
//  4. fallback default → HUAKAI 默认 OpenAIChat
func Select(in Inputs) Decision {
	// 1. explicit path 优先（well-known endpoint 强 contract）
	if pathClient, pathHit := clientFromPath(in.Path); pathHit {
		conflict, reason := checkConflict(in, pathClient)
		return Decision{
			ClientProtocol: pathClient,
			Source:         SourceExplicitPath,
			Confidence:     1.0,
			Conflict:       conflict,
			ConflictReason: reason,
		}
	}

	// 2. route_config 显式声明（path 未命中已知 wire-contract 时使用）
	if in.RouteConfigClient != "" {
		conflict, reason := checkConflict(in, in.RouteConfigClient)
		return Decision{
			ClientProtocol: in.RouteConfigClient,
			Source:         SourceRouteConfig,
			Confidence:     1.0,
			Conflict:       conflict,
			ConflictReason: reason,
		}
	}

	// 3. identity 填空白（IdentityAware=true 且 confidence ≥ MinIdentityConfidence）
	if in.IdentityAware && in.IdentityConf >= MinIdentityConfidence {
		if c, ok := clientFromIdentity(in.Identity); ok {
			return Decision{
				ClientProtocol: c,
				Source:         SourceIdentity,
				Confidence:     in.IdentityConf,
			}
		}
	}

	// 4. default fallback — HUAKAI 历史默认 OpenAI Chat
	return Decision{
		ClientProtocol: proto.ClientProtocolOpenAIChat,
		Source:         SourceDefault,
		Confidence:     0.5,
	}
}

// clientFromPath 把 HTTP path 映射到 ClientProtocol。
// 支持的 path 列表与 HUAKAI 路由 mountRoutes 对齐。
// 返回 (protocol, hit)；hit=false 表示 path 未匹配任何已知模式。
func clientFromPath(path string) (proto.ClientProtocol, bool) {
	p := strings.ToLower(strings.TrimSpace(path))
	// 去掉 query string
	if idx := strings.IndexByte(p, '?'); idx >= 0 {
		p = p[:idx]
	}
	switch {
	case strings.HasPrefix(p, "/v1/chat/completions"):
		return proto.ClientProtocolOpenAIChat, true
	case strings.HasPrefix(p, "/v1/responses"):
		return proto.ClientProtocolOpenAIResponses, true
	case strings.HasPrefix(p, "/v1/messages"):
		return proto.ClientProtocolAnthropicMessages, true
	}
	return "", false
}

// clientFromIdentity 把 clientid.Identity 推断到 ClientProtocol。
// 当前是粗粒度映射；OCAW 实测后（U6-D-1 evidence artifact）可改 per-identity
// per-model strict/tolerant 映射。
//
// Cody 故意不映射: 因为 cody 同时支持 OpenAI 与 Anthropic；不能从 identity
// 单独决定 ClientProtocol，需要 model family 二次决定（U6-D-3+）。
func clientFromIdentity(id clientid.Identity) (proto.ClientProtocol, bool) {
	switch id {
	case clientid.IdentityCursor:
		return proto.ClientProtocolOpenAIChat, true
	case clientid.IdentityClaudeCode:
		return proto.ClientProtocolAnthropicMessages, true
	case clientid.IdentityChatUI:
		return proto.ClientProtocolOpenAIChat, true
	case clientid.IdentityCody, clientid.IdentityCurlScript, clientid.IdentityUnknown:
		return "", false
	}
	return "", false
}

// checkConflict 检查 identity 信号与 selected ClientProtocol 是否一致。
// 不一致仅记 conflict 标记 + reason；不阻止决策（path/route 已优先）。
func checkConflict(in Inputs, selected proto.ClientProtocol) (bool, string) {
	if in.IdentityConf < MinIdentityConfidence {
		return false, ""
	}
	idClient, ok := clientFromIdentity(in.Identity)
	if !ok {
		return false, ""
	}
	if idClient == selected {
		return false, ""
	}
	return true, "identity=" + string(in.Identity) + " 推断 " + string(idClient) +
		" 但 path/route 选 " + string(selected)
}
