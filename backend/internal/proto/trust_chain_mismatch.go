package proto

import "fmt"

// HUAKAI 信任链 T9 — model substitution detection。
//
// 设计依据：trust-chain GitHub survey 发现 RelayPlane/proxy@df3d3edc7c05:
// src/standalone-proxy.ts:2887 把 provider response model 与 requested model
// 在不一致时 log warning，但没强 audit。HUAKAI 在 RelayPlane pattern 之上
// 升级：mismatch 一律 emit ProtocolLossEntry（warning / error），让 settler
// 写入 ledger，user 可 verify endpoint 看到。
//
// 与 ModelChain.IsConsistent() 的关系：
//   - IsConsistent() 是布尔判断（true/false），不告诉调用方"哪一维不一致"。
//   - EmitModelMismatchLoss 返回**具体维度**的 ProtocolLossEntry 列表，
//     便于 settler / audit ledger 写明原因。
//
// 三个 dimension 任一不一致都产 1 条 loss（最多 2 条同时，因为 UpstreamReported
// 可能同时与 Requested 和 RouteDecided 都不一致）。

// MismatchReason 是 model chain mismatch 的具体维度。
type MismatchReason string

const (
	// MismatchRouteDecidedDiffersFromRequested 路由层偷换模型（最严重）。
	MismatchRouteDecidedDiffersFromRequested MismatchReason = "route_decided_differs_from_requested"

	// MismatchUpstreamDiffersFromRequested 上游 vendor 返回了与请求不一致的模型。
	MismatchUpstreamDiffersFromRequested MismatchReason = "upstream_reported_differs_from_requested"

	// MismatchRouteEmpty 路由决策模型为空（HUAKAI 内部错误）。
	MismatchRouteEmpty MismatchReason = "route_decided_empty"

	// MismatchRequestedEmpty 请求模型为空（input 错误）。
	MismatchRequestedEmpty MismatchReason = "requested_empty"
)

// EmitModelMismatchLoss 根据 ModelChain 状态生成具体 mismatch loss 条目。
// nil ModelChain 返回 nil（未启用 chain，不算 mismatch）。
// 完全一致返回 nil。
// 部分不一致按维度返回 1-2 条 loss；每条都是 warning 级别（不阻请求，让 user
// 决策；platform_admin 可配置升级为 error）。
//
// 调用方（settler / verify endpoint）应把这些 loss 写入 Accounting.HopChain
// 或 audit_ledger 的 ProtocolLoss 列表，最终透传给 user verify CLI。
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
