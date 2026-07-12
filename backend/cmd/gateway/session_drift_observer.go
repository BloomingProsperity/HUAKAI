// HUAKAI · iKun

package main

import (
	"context"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// zapSessionDriftObserver 把会话上下文漂移事件落成结构化日志 (与 zapAuthEventSink 同纪律):
// Medium(仅 IP 变)/Low(仅 UA 变)是 token 盗用/重放最常见形态的弱信号, 不落日志则检测全盲;
// High 同流记录 (撤销行为在 usersession 内已发生)。IP/UA 均为 class, 无 PII。
type zapSessionDriftObserver struct {
	logger *zap.Logger
}

func newSessionDriftObserver(logger *zap.Logger) usersession.DriftObserver {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &zapSessionDriftObserver{logger: logger.Named("session_drift")}
}

// ObserveSessionDrift 纯观测: low 用 Info, medium/high 用 Warn 便于告警侧优先抓取。
func (o *zapSessionDriftObserver) ObserveSessionDrift(_ context.Context, ev usersession.SessionDriftEvent) {
	if o == nil || o.logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("level", string(ev.Level)),
		zap.String("reason", ev.Reason),
		zap.String("source", ev.Source),
		zap.Int64("tenant_id", ev.TenantID),
		zap.Int64("user_id", ev.UserID),
		zap.String("family_id", ev.FamilyID),
		zap.String("ip_class", ev.IPClass),
		zap.String("ua_class", ev.UAClass),
		zap.String("baseline_ip", ev.BaselineIP),
		zap.String("baseline_ua", ev.BaselineUA),
	}
	if ev.Level == usersession.DriftLow {
		o.logger.Info("session_drift", fields...)
		return
	}
	o.logger.Warn("session_drift", fields...)
}
