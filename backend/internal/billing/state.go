package billing

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// StreamState 是 F-OBS-003 账务态；数值必须与 0017_stream_state migration 一致。
type StreamState int16

const (
	StreamStateAcquired StreamState = 0
	StreamStateInFlight StreamState = 1
	StreamStatePartial  StreamState = 2
	StreamStateFailed   StreamState = 3
)

const maxStreamTerminatedReasonLen = 64

// Attempt 是一次 claim_id + attempt_seq + provider_account_id 的流式账务快照。
type Attempt struct {
	State                  StreamState
	DeliveredTokenCount    int64
	StreamTerminatedReason string
}

func NewAttempt() Attempt {
	return Attempt{State: StreamStateAcquired}
}

func (s StreamState) Valid() bool {
	return s >= StreamStateAcquired && s <= StreamStateFailed
}

func (s StreamState) String() string {
	switch s {
	case StreamStateAcquired:
		return "acquired"
	case StreamStateInFlight:
		return "inflight"
	case StreamStatePartial:
		return "partial"
	case StreamStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func (s StreamState) DBValue() int16 {
	if !s.Valid() {
		return int16(StreamStateFailed)
	}
	return int16(s)
}

func (s StreamState) Chargeable() bool {
	return s == StreamStatePartial
}

func CanTransitionStreamState(from, to StreamState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StreamStateAcquired:
		return to == StreamStateInFlight || to == StreamStateFailed
	case StreamStateInFlight:
		return to == StreamStatePartial || to == StreamStateFailed
	case StreamStatePartial, StreamStateFailed:
		return false
	default:
		return false
	}
}

func (a Attempt) Transition(to StreamState) (Attempt, error) {
	if !CanTransitionStreamState(a.State, to) {
		return a, fmt.Errorf("billing: invalid stream state transition %s -> %s", a.State, to)
	}
	a.State = to
	return a, nil
}

func (a Attempt) Normalized() Attempt {
	if !a.State.Valid() {
		a.State = StreamStateFailed
	}
	if a.DeliveredTokenCount < 0 {
		a.DeliveredTokenCount = 0
	}
	a.StreamTerminatedReason = normalizeTerminatedReason(a.StreamTerminatedReason)
	return a
}

func AttemptFromSettleRequest(req SettleRequest) Attempt {
	if req.StreamAttempt != nil {
		return req.StreamAttempt.Normalized()
	}
	return AttemptFromGatewayDraft(req.Stream, req.Draft)
}

func AttemptFromGatewayDraft(stream bool, draft gateway.UsageRecordDraft) Attempt {
	delivered := draft.DeliveredTokenCount
	if output := int64(draft.TokensOutput); output > delivered {
		delivered = output
	}
	reason := draft.StreamTerminatedReason
	if reason == "" {
		reason = TerminatedReasonForEndClass(draft.EndClass, delivered)
	}

	state := StreamStatePartial
	// AMBIGUOUS_USAGE 默认不正向收费:进入 reconciliation 留待真对账。
	// 例外:已向用户交付可估内容(EstimatedOutputTokens+EstimatedReasoningTokens>0)时
	// 判 Partial 可计费——内容已发出而 reconciliation 是 refund-only/zero-finalize 永不
	// 补收, 留 Failed = 永久零收漏钱(SM-05)。与 gatewayhttp 估算计费同口径:gateway 侧
	// 对同一判据走 estimatedStreamingCost 估出正成本, 此处放行 Chargeable 让其落账;
	// 无可估交付的歧义流仍判 Failed 留待对账。
	if draft.EndClass == gateway.AmbiguousUsage {
		if draft.EstimatedOutputTokens+draft.EstimatedReasoningTokens > 0 {
			state = StreamStatePartial
		} else {
			state = StreamStateFailed
		}
	} else if stream {
		switch {
		case delivered > 0:
			state = StreamStatePartial
		case draft.EndClass == "" || draft.EndClass == gateway.UnknownTermination:
			state = StreamStateFailed
		case draft.EndClass == gateway.StreamEndGraceful && draft.TokensInput == 0 && draft.TokensOutput == 0:
			// 纯缓存命中的成功流: 无新 fresh input/output, 但 usage 报告了 cache 创建/读取
			// token, 仍产生真实 cache 成本。之前一律判 Failed → CostForAttempt 把 cache 成本
			// 一并归零、不写 usage_record(漏计 + 丢审计行)。cache 桶非零时改判 chargeable
			// AmbiguousUsage 已在上方分支单独处理, 不会进入此 else-if 走到本 cache 分支被复活。
			// 参考项目对照与计费方向决策见 docs/process/plans/2026-05-29-s1015-cache-stream-fu-claude.md。
			if streamDraftHasCacheTokens(draft) {
				state = StreamStatePartial
			} else {
				state = StreamStateFailed
			}
		case draft.EndClass == gateway.StreamEndGraceful:
			state = StreamStatePartial
		default:
			state = StreamStateFailed
		}
	}

	return Attempt{
		State:                  state,
		DeliveredTokenCount:    delivered,
		StreamTerminatedReason: reason,
	}.Normalized()
}

func TerminatedReasonForEndClass(endClass gateway.StreamEndClass, delivered int64) string {
	switch endClass {
	case gateway.ClientDisconnect:
		return "client_gone"
	case gateway.FirstTokenTimeout, gateway.InterEventTimeout, gateway.TotalStreamTimeout:
		return "upstream_timeout"
	case gateway.UpstreamError5xx:
		return "upstream_5xx"
	case gateway.StreamEndGraceful:
		return ""
	case gateway.UpstreamEOFNoTerminal, gateway.UnknownTermination:
		if delivered > 0 {
			return "upstream_5xx"
		}
		return "output_token_zero"
	case gateway.AmbiguousUsage:
		return "output_token_zero"
	default:
		if delivered > 0 {
			return "upstream_5xx"
		}
		return "output_token_zero"
	}
}

// streamDraftHasCacheTokens 判断 stream draft 是否携带任何 cache 创建/读取 token。
// 纯缓存命中的成功流(graceful, 零 fresh input/output)据此判为可计费, 使其 cache 成本
// 落账并写 usage_record。
func streamDraftHasCacheTokens(draft gateway.UsageRecordDraft) bool {
	return draft.CacheCreationTokens > 0 ||
		draft.CacheCreation5mTokens > 0 ||
		draft.CacheCreation1hTokens > 0 ||
		draft.CacheReadTokens > 0
}

func CostForAttempt(actualCost decimal.Decimal, attempt Attempt) decimal.Decimal {
	if attempt.Normalized().State.Chargeable() {
		return actualCost
	}
	return decimal.Zero
}

func normalizeTerminatedReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) <= maxStreamTerminatedReasonLen {
		return reason
	}
	return reason[:maxStreamTerminatedReasonLen]
}
