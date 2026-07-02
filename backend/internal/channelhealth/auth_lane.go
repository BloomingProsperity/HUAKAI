package channelhealth

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
)

// AuthCooldownLane 是 auth 降级车道的接线接口(*authcooldown.Store 满足它)。抽成接口便于单测注入 spy。
// nil lane 时所有路由点短路 → 行为与接线前逐字节一致(kill-switch 关闭态)。
type AuthCooldownLane interface {
	// Suspend 记录一次 auth 失败(升 strike、算退避、按 class 决定是否 HardDisabled)。
	Suspend(ctx context.Context, accountID int64, class authcooldown.FailureClass, credVersion int, now time.Time)
	// Clear 彻底清除账号车道状态(reason 供日志区分:success/refresh/operator_resume)。
	Clear(ctx context.Context, accountID int64, reason string)
	// Eligible 选号门查询:ok=false 表示被 auth 车道移出选号;hardDisabled 供逃生阀判定。
	Eligible(accountID int64, now time.Time) (ok bool, hardDisabled bool)
}

// WithAuthCooldownLane 把 auth 降级车道接进 channelhealth Service:applySignal 据此路由
// SignalAuthChallenge→Suspend、SignalSuccess→Clear;运营 ForceActive/ManualResume→Clear。
// 传 nil(或不设该 option)→ 车道未接线,SignalAuthChallenge 变纯 no-op,行为不变。
func WithAuthCooldownLane(lane AuthCooldownLane) ServiceOption {
	return func(s *Service) {
		s.authLane = lane
	}
}
