package channelhealth

import (
	"context"
	"log/slog"
	"time"
)

// 本文件负责渠道健康状态转换的两路可观测输出:
//   - DB 审计事件(AppendAudit)——权威审计源,全量落库,护城河;
//   - stdout 结构化运维日志(slog)——补 DB 审计只落库、运维实时看不见的观测盲区(缺口③)。
// 两者并存:审计不因日志而变,日志只在审计成功落库后追加。

// logComponent 是本包运维日志的 component 标识(惯例同 billing lease sweeper)。
const logComponent = "channelhealth"

// transitionLogLevel 按转换后的目标状态定运维日志级别:恢复上线(active)/开始放量(ramping)
// 是良性事件打 Info;降级/冷却/禁用/回滚/人工暂停都让账号退出正常选号、运营需即时知晓,打 Warn。
// 三镜对照:sub2api 按「预期短冷却=Info / 硬下线=Warn」分档,HUAKAI 取「离开可用池即 Warn」
// 更偏可见性(本功能目的正是让运营看见账号退池),按 reason 细分 rate_limit 冷却降 Info 留作后续。
func transitionLogLevel(state HealthState) slog.Level {
	switch state {
	case StateActive, StateRamping:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

// logTransition 在审计成功落库后补一条 stdout 结构化运维日志(每次真实转换恰一条,按目标状态分级)。
// 绝不写入原始上游文本 / token / 凭证材料——只带账号 id、凭证版本号等非敏感标识。这是补观测盲区,
// 不替代 DB 审计护城河(审计仍照旧全量落库)。
func (s *Service) logTransition(ctx context.Context, prev HealthState, rec Record, primary AuditEventType, requestID, actorID string) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []slog.Attr{
		slog.String("component", logComponent),
		slog.String("event_type", string(primary)),
		slog.Int64("tenant_id", rec.Key.TenantID),
		slog.Int64("provider_account_id", rec.Key.ProviderAccountID),
		slog.String("channel_id", rec.Key.StableChannelID()),
		slog.String("vendor", rec.Key.Vendor),
		slog.String("previous_state", string(prev)),
		slog.String("new_state", string(rec.State)),
		slog.String("reason_class", string(rec.ReasonClass)),
		slog.String("policy_version", rec.PolicyVersion),
	}
	if requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	if actorID != "" {
		attrs = append(attrs, slog.String("actor_id", actorID))
	}
	if rec.CooldownUntil != nil {
		attrs = append(attrs, slog.String("cooldown_until", rec.CooldownUntil.UTC().Format(time.RFC3339Nano)))
	}
	logger.LogAttrs(ctx, transitionLogLevel(rec.State), "channel health state transition", attrs...)
}

func (s *Service) emitTransitionEvents(ctx context.Context, prev HealthState, rec Record, requestID, actorID string, dec decision) error {
	events := dec.eventTypes
	if len(events) == 0 {
		events = defaultEvents(prev, rec.State)
	}
	var primary AuditEventType
	for _, typ := range events {
		if typ == "" {
			continue
		}
		ev := AuditEvent{
			Type:          typ,
			Key:           rec.Key,
			PreviousState: prev,
			NewState:      rec.State,
			ReasonClass:   rec.ReasonClass,
			PolicyVersion: rec.PolicyVersion,
			RequestID:     requestID,
			ActorID:       actorID,
			OccurredAt:    s.clock.Now(),
			Payload:       auditPayload(rec),
		}
		if err := s.store.AppendAudit(ctx, ev); err != nil {
			return err
		}
		// 末位非空事件作为运维日志主事件类型(事件列表按终态事件排在末位,如 [degraded,disabled]→disabled)。
		primary = typ
	}
	// 审计全部成功落库后再补运维日志:审计中途失败会在上面提前返回,不为已回滚的转换留误导性日志。
	if primary != "" {
		s.logTransition(ctx, prev, rec, primary, requestID, actorID)
	}
	return nil
}
