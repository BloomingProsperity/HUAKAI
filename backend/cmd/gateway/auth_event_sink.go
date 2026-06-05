package main

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
)

// zapAuthEventSink 是生产装配下的 gatewayhttp.AuthEventSink 实现:把每条
// 认证安全事件(登录失败/密码重置/OAuth/social/session-refresh 等)落成结构化
// zap 日志。装配前 EventSink 为 nil → recordAuthEvent 直接静默丢弃这些安全审计
// 事件,生产环境无法追溯暴力破解 / 凭据填充 / 会话异常。本 sink 默认开箱即记,
// 不做 fail-open 静默吞:即便 logger 为 nil 也回落到 zap.NewNop(),保证调用安全。
type zapAuthEventSink struct {
	logger *zap.Logger
}

// newAuthEventSink 构造生产 auth 事件 sink。logger 为 nil 时回落 Nop,
// 保证调用方(handler 装配)永远拿到一个非 nil、可安全调用的 sink。
func newAuthEventSink(logger *zap.Logger) gatewayhttp.AuthEventSink {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &zapAuthEventSink{logger: logger.Named("auth_event")}
}

// RecordAuthEvent 把一条认证事件写成结构化日志。失败类事件(Outcome != "success")
// 用 Warn 级别以便告警/采集系统优先抓取,其余用 Info。所有字段都进结构化字段,
// 不拼字符串,便于下游索引。
func (s *zapAuthEventSink) RecordAuthEvent(_ context.Context, event gatewayhttp.AuthEvent) {
	if s == nil || s.logger == nil {
		return
	}
	fields := []zapcore.Field{
		zap.String("event_type", event.EventType),
		zap.String("outcome", event.Outcome),
	}
	if event.TenantID != 0 {
		fields = append(fields, zap.Int64("tenant_id", event.TenantID))
	}
	if event.UserID != 0 {
		fields = append(fields, zap.Int64("user_id", event.UserID))
	}
	if event.Provider != "" {
		fields = append(fields, zap.String("provider", event.Provider))
	}
	if event.ReasonClass != "" {
		fields = append(fields, zap.String("reason_class", event.ReasonClass))
	}
	if event.AuthMethod != "" {
		fields = append(fields, zap.String("auth_method", event.AuthMethod))
	}
	if event.SessionPolicy != "" {
		fields = append(fields, zap.String("session_policy", event.SessionPolicy))
	}
	if event.SessionsRevoked != 0 {
		fields = append(fields, zap.Int64("sessions_revoked", event.SessionsRevoked))
	}
	if event.Outcome == "success" {
		s.logger.Info("auth_event", fields...)
		return
	}
	s.logger.Warn("auth_event", fields...)
}
