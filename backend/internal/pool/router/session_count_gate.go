package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
)

// GateFailureSessionCount 是这样一种失败 reason:当加入当前 session 会超过
// 账号的最大并发 session 上限时,该账号被从轮转中下架。
const GateFailureSessionCount GateFailureReason = "session_count_limit"

// SessionCountGate 实现了 Gate。当满足以下条件时,它会把账号从选号中剔除:
//   - 账号的 MaxSessions 为正,且
//   - registry 非 nil,且
//   - 把请求的 sessionHash 作为一个新 session 加入会超过上限。
//
// 在其余所有情况下(MaxSessions==0、registry 为 nil、已有 session、未达上限),
// 该 gate 是 no-op —— 按设计 fail-open,这样 bug 至多只会让上限失效,
// 而绝不会错误地把一个健康账号下架。
type SessionCountGate struct {
	Registry *sessioncap.Registry // nil -> 始终放行(fail-open)
}


func (g SessionCountGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return true, "", nil
	}
	max := account.MaxSessions
	if max <= 0 {
		// opt-in:未设置上限 -> 始终可选(默认安全)
		return true, "", nil
	}
	if g.Registry == nil {
		// 未注入 registry -> fail-open
		return true, "", nil
	}
	if g.Registry.WouldExceed(account.ID, req.SessionHash, max) {
		return false, GateFailureSessionCount, nil
	}
	return true, "", nil
}
