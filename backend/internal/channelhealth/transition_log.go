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
// HUAKAI 选择「离开可用池即 Warn」以优先保证退池可见性；按 reason 把预期的
// rate_limit 短冷却细分为 Info 留作后续。
func transitionLogLevel(state HealthState) slog.Level {
	switch state {
	case StateActive, StateRamping:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

// transitionLogRecord 是一条待打的转换运维日志的完整快照:在事务闭包内产生、Commit 成功后才打,
// 因此把打日志所需字段全部快照下来,不持有会随事务回滚变化的活引用。
type transitionLogRecord struct {
	prev      HealthState
	rec       Record
	primary   AuditEventType
	requestID string
	actorID   string
}

// recordTransitionLog 决定「攒起来延迟打」还是「立即打」:
//   - 事务路径(pendingTransitionLogs 非 nil):攒进 pending,由 withMutation 在 Commit 成功后 flush,
//     事务回滚则整批丢弃——绝不为已回滚的转换留幽灵日志(审查 S2)。
//   - 无事务边界的 store(pending 为 nil):无回滚风险,立即打(行为与旧版一致)。
func (s *Service) recordTransitionLog(ctx context.Context, r transitionLogRecord) {
	if s.pendingTransitionLogs != nil {
		*s.pendingTransitionLogs = append(*s.pendingTransitionLogs, r)
		return
	}
	s.logTransition(ctx, r)
}

// logTransition 打一条 stdout 结构化运维日志(每次真实转换恰一条,按目标状态分级)。
// 绝不写入原始上游文本 / token / 凭证材料——只带账号 id、凭证版本号等非敏感标识。这是补观测盲区,
// 不替代 DB 审计护城河(审计仍照旧全量落库)。仅由 withMutation 在事务 Commit 成功后调用。
func (s *Service) logTransition(ctx context.Context, r transitionLogRecord) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []slog.Attr{
		slog.String("component", logComponent),
		slog.String("event_type", string(r.primary)),
		slog.Int64("tenant_id", r.rec.Key.TenantID),
		slog.Int64("provider_account_id", r.rec.Key.ProviderAccountID),
		slog.String("channel_id", r.rec.Key.StableChannelID()),
		slog.String("vendor", r.rec.Key.Vendor),
		slog.String("previous_state", string(r.prev)),
		slog.String("new_state", string(r.rec.State)),
		slog.String("reason_class", string(r.rec.ReasonClass)),
		slog.String("policy_version", r.rec.PolicyVersion),
	}
	if r.requestID != "" {
		attrs = append(attrs, slog.String("request_id", r.requestID))
	}
	if r.actorID != "" {
		attrs = append(attrs, slog.String("actor_id", r.actorID))
	}
	if r.rec.CooldownUntil != nil {
		attrs = append(attrs, slog.String("cooldown_until", r.rec.CooldownUntil.UTC().Format(time.RFC3339Nano)))
	}
	logger.LogAttrs(ctx, transitionLogLevel(r.rec.State), "channel health state transition", attrs...)
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
	// 审计全部成功落库后再登记运维日志(审计中途失败上面已提前返回);登记只是攒进 pending,
	// 真正打出推迟到 withMutation 确认事务 Commit 成功之后——事务回滚则整批丢弃,不留幽灵日志。
	if primary != "" {
		s.recordTransitionLog(ctx, transitionLogRecord{
			prev:      prev,
			rec:       rec,
			primary:   primary,
			requestID: requestID,
			actorID:   actorID,
		})
	}
	return nil
}
