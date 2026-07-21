// R6 辅助：把 Classification 桥接到 UsageRecordDraft 的 routing-reason + end-class。
// 规格：docs/specs/rate-limiting.md §A13 / DR-009 §1 Q1 / F-GW-002 Phase D。
//
// 综合说明：docs/process/plans/2026-05-04-r6-wire-codeparallel-synthesis.md。
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
)

// RoutingReasonPayload 是写入 UsageRecordDraft.RoutingReason 的 JSON schema。
// 字段形态遵循 §A13 审计 payload 契约；顺序保持以获得稳定的磁盘格式。
// 所有值均为字符串（Go 把 `type X string` 序列化为字符串），从而使
// 线格式与厂商无关。
type RoutingReasonPayload struct {
	RuleID        string `json:"rule_id"`
	RuleVersion   int    `json:"rule_version"`
	ErrorClass    string `json:"error_class"`
	Tier          string `json:"tier"`
	RetryAction   string `json:"retry_action"`
	FsmTransition string `json:"fsm_transition"`
	RetryAfterMs  int64  `json:"retry_after_ms"`
	Confidence    string `json:"confidence"`
}

// streamEndClassForErrorClass 把 12 个 A13 ErrorClass 值映射为 F-GW-002 的
// StreamEndClass。用 switch（而非 map）让 `go vet` 在新增 ErrorClass 却
// 未更新映射时能标出缺失的 case。
func streamEndClassForErrorClass(class ErrorClass) StreamEndClass {
	switch class {
	case ErrorClassOAuthInvalidGrant,
		ErrorClassTokenRevoked,
		ErrorClassCredentialRejected,
		ErrorClassKYCRequired,
		ErrorClassOrgDisabled,
		ErrorClassWorkspaceDeactivated,
		ErrorClassCreditExhausted:
		return UpstreamAuthFailure
	case ErrorClassRateLimited:
		return UpstreamRateLimit
	case ErrorClassServerError, ErrorClassOverloaded:
		return UpstreamError5xx
	case ErrorClassPlatformPolicy, ErrorClassRequestTooLarge:
		// 413 request_too_large 属于 4xx 类客户端错误；end-class 相同。
		return UpstreamError4xx
	case ErrorClassNetworkTimeout, ErrorClassUpstreamTimeout:
		// upstream_timeout（504）与 network_timeout（网关侧）都映射到
		// inter-event timeout 这个 end-class，用于重试预算核算。
		return InterEventTimeout
	default:
		return UnknownTermination
	}
}

// ApplyClassificationToDraft 把 Classification 元数据写入 UsageRecordDraft：
//
//  1. 把 RoutingReasonPayload 序列化成 JSON 写入 d.RoutingReason。
//  2. 把 c.Class 映射为 StreamEndClass 并写入 d.EndClass —— 但仅当
//     d.EndClass 当前为零值（""）或 UnknownTermination 时才写。
//     转发器先前已作出的判定（如 ClientDisconnect、FirstTokenTimeout）
//     会被保留。
//
// draft 为 nil 时静默空操作，这样防御性的调用方无需做 nil 判断。
func ApplyClassificationToDraft(d *UsageRecordDraft, c Classification) {
	if d == nil {
		return
	}

	payload := RoutingReasonPayload{
		RuleID:        c.RuleID,
		RuleVersion:   c.RuleVersion,
		ErrorClass:    string(c.Class),
		Tier:          string(c.Tier),
		RetryAction:   string(c.RetryAction),
		FsmTransition: string(c.FsmTransition),
		RetryAfterMs:  c.RetryAfterMs,
		Confidence:    string(c.Confidence),
	}
	if encoded, err := json.Marshal(payload); err == nil {
		d.RoutingReason = encoded
	}

	if d.EndClass == "" || d.EndClass == UnknownTermination {
		d.EndClass = streamEndClassForErrorClass(c.Class)
	}
}

// ClassifyAndApply 是一站式组合器：对上游响应做 Classify 并应用到 draft。
// 返回 Classification（供调用方据此做重试 / 冷却决策）以及可能的 Classify error。
//
// 以下情况返回 error 且不改动 d：
//   - d 为 nil
//   - Classify 返回 error（如负数状态码）
func ClassifyAndApply(d *UsageRecordDraft, httpStatus int, headers http.Header, body []byte, provider string) (Classification, error) {
	if d == nil {
		return Classification{}, errors.New("gateway: ClassifyAndApply called with nil draft")
	}

	c, err := Classify(httpStatus, headers, body, provider)
	if err != nil {
		return Classification{}, err
	}
	ApplyClassificationToDraft(d, c)
	return c, nil
}
