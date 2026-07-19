package channelhealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct {
	store       Store
	policy      Policy
	clock       Clock
	cooldowns   StatusCooldownSource
	alertOutbox obsdlq.Outbox
	// authLane 是独立于健康 FSM 的 auth 降级车道(nil=未接线,SignalAuthChallenge 变 no-op)。
	authLane AuthCooldownLane
	// logger 记渠道健康状态转换的 stdout 结构化运维日志——补 DB 审计(AppendAudit)只落库、
	// 运维实时看不见的观测盲区。nil→slog.Default();可经 WithLogger 注入(测试用收集型 handler)。
	logger *slog.Logger
	// pendingTransitionLogs 仅在事务闭包内(withMutation 里的 tx service)非 nil:emitTransitionEvents
	// 把待打的转换日志攒在这里,由 withMutation 在事务 Commit 成功后才真正打出——避免事务回滚
	// (Serializable Commit 抛 40001 / emitAlert 失败)后残留与 DB 权威审计矛盾的幽灵日志(审查 S2)。
	pendingTransitionLogs *[]transitionLogRecord
}

type transactionalStore interface {
	WithTx(context.Context, func(Store) error) error
}

type ServiceOption func(*Service)

func WithAlertOutbox(outbox obsdlq.Outbox) ServiceOption {
	return func(s *Service) {
		s.alertOutbox = outbox
	}
}

// WithLogger 注入渠道健康状态转换运维日志的 logger;nil 时构造器兜底 slog.Default()。
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		s.logger = logger
	}
}

func NewService(store Store, policy Policy, clock Clock, opts ...ServiceOption) *Service {
	if clock == nil {
		clock = realClock{}
	}
	s := &Service{store: store, policy: policy.normalized(), clock: clock}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

func (s *Service) EnsureDefaultActive(ctx context.Context, key ChannelKey) (Record, error) {
	if s == nil || s.store == nil {
		return Record{}, errors.New("channelhealth: service not configured")
	}
	if err := key.Validate(); err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	rec, err := s.store.Get(ctx, key)
	if err == nil {
		return rec, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	rec = newRecord(key, s.policy, now)
	return s.store.UpsertRecord(ctx, rec)
}

func (s *Service) ApplySignal(ctx context.Context, sig Signal) (Record, error) {
	// auth 降级车道:auth 失败(SignalAuthChallenge)独立于健康 FSM 处理——只把账号临时移出选号
	//(authcooldown 纯内存),完全不改 rec.State/Score、不写健康窗口(防 auth blip 污染健康分,
	// 保留既有「令牌问题不写健康降级」意图)。刻意在 withMutation 之前短路:该路径不碰健康存储,
	// 进 Serializable 事务只会白开空事务——401 风暴(恰是车道目标场景)下每失败一次多两趟 DB
	// 往返(审查 S3);lane 未接线(knob 关)时同样短路,与基底「auth 类直接跳过」逐字节等价。
	if normalizeSignalClass(sig.Class) == SignalAuthChallenge {
		if s == nil || s.store == nil {
			return Record{}, errors.New("channelhealth: service not configured")
		}
		if s.authLane != nil && sig.Key.ProviderAccountID != 0 {
			s.authLane.Suspend(ctx, sig.Key.ProviderAccountID, sig.AuthFailureClass, sig.Key.CredentialVersion, s.clock.Now())
		}
		return Record{}, nil
	}
	return s.withMutation(ctx, func(tx *Service) (Record, error) {
		return tx.applySignal(ctx, sig)
	})
}

func (s *Service) applySignal(ctx context.Context, sig Signal) (Record, error) {
	if s == nil || s.store == nil {
		return Record{}, errors.New("channelhealth: service not configured")
	}
	if sig.Key.ChannelID == "" {
		sig.Key.ChannelID = sig.Key.StableChannelID()
	}
	if err := sig.Key.Validate(); err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	if sig.At.IsZero() {
		sig.At = now
	}
	class := normalizeSignalClass(sig.Class)
	// 成功信号顺带清 auth 车道(等价 CLIProxy self-heal:一次成功即解除冷却、strike 归零)。
	if class == SignalSuccess && s.authLane != nil && sig.Key.ProviderAccountID != 0 {
		s.authLane.Clear(ctx, sig.Key.ProviderAccountID, authcooldown.ClearReasonSuccess)
	}
	rec, err := s.EnsureDefaultActive(ctx, sig.Key)
	if err != nil {
		return Record{}, err
	}
	prev := rec.State
	rec.SampleWindow = addSignalToWindow(rec.SampleWindow, s.policy, sig, now)
	rec.LastSignalClass = normalizeSignalClass(sig.Class)
	rec.LastSignalAt = &sig.At
	rec.UpdatedAt = now
	decision := s.evaluate(ctx, rec, sig, now)
	if decision.state != "" && decision.state != rec.State {
		if decision.state == StateCoolingDown && hasEvent(decision.eventTypes, EventRampRolledBack) {
			s.rollbackRamp(&rec, now, decision.reason)
			if rec.RampFailureCount >= s.policy.RepeatedRampRollbackAlertThreshold {
				decision.alertType = AlertRepeatedRampRollback
				decision.alertSeverity = "high"
				decision.alertPayload = map[string]any{"ramp_failure_count": rec.RampFailureCount}
			}
		} else {
			applyDecision(&rec, decision, now, s.policy)
		}
		switch rec.State {
		case StateDegraded:
			incProviderDegraded()
		case StateCoolingDown, StateDisabled:
			incProviderError()
		}
	}
	rec, err = s.store.UpsertRecord(ctx, rec)
	if err != nil {
		return Record{}, err
	}
	if prev != rec.State || decision.auditEvenWithoutStateChange {
		if err := s.emitTransitionEvents(ctx, prev, rec, sig.RequestID, "", decision); err != nil {
			return Record{}, err
		}
	}
	if decision.alertType != "" {
		if err := s.emitAlert(ctx, rec, decision.alertType, decision.alertSeverity, decision.alertPayload); err != nil {
			return Record{}, err
		}
	}
	return rec, nil
}

func (s *Service) MaybeStartRamp(ctx context.Context, key ChannelKey) (Record, error) {
	return s.withMutation(ctx, func(tx *Service) (Record, error) {
		return tx.maybeStartRamp(ctx, key)
	})
}

func (s *Service) maybeStartRamp(ctx context.Context, key ChannelKey) (Record, error) {
	rec, err := s.recordForMutation(ctx, key)
	if err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	if rec.State != StateCoolingDown {
		return rec, nil
	}
	if rec.CooldownUntil == nil || rec.CooldownUntil.After(now) {
		return rec, nil
	}
	prev := rec.State
	rec.State = StateRamping
	rec.ReasonClass = SignalNone
	rec.RampStagePct = 1
	rec.RampStartedAt = &now
	rec.CooldownUntil = nil
	rec.StateEnteredAt = now
	rec.LastTransitionAt = now
	rec.UpdatedAt = now
	rec.PolicyVersion = s.policy.Version
	rec, err = s.store.UpsertRecord(ctx, rec)
	if err != nil {
		return Record{}, err
	}
	return rec, s.emitTransitionEvents(ctx, prev, rec, "", "", decision{eventTypes: []AuditEventType{EventRampStarted}})
}

func (s *Service) AdvanceRamp(ctx context.Context, key ChannelKey) (Record, error) {
	return s.withMutation(ctx, func(tx *Service) (Record, error) {
		return tx.advanceRamp(ctx, key)
	})
}

func (s *Service) advanceRamp(ctx context.Context, key ChannelKey) (Record, error) {
	rec, err := s.recordForMutation(ctx, key)
	if err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	if rec.State != StateRamping {
		return rec, nil
	}
	if rec.RampStartedAt != nil && now.Sub(*rec.RampStartedAt) < s.policy.RampStageMinDuration {
		return rec, nil
	}
	recent := windowFor(rec.SampleWindow, s.policy.MinObservation, now)
	if recent.TotalAttempts < s.policy.RampStageMinSamples {
		return rec, nil
	}
	if rampFailureRate(recent) > s.policy.RampErrorThresholdPct || recent.BanSignals > 0 {
		prev := rec.State
		s.rollbackRamp(&rec, now, SignalChannelError)
		rec, err = s.store.UpsertRecord(ctx, rec)
		if err != nil {
			return Record{}, err
		}
		dec := decision{
			eventTypes: []AuditEventType{EventRampRolledBack, EventDisabled},
		}
		if rec.RampFailureCount >= s.policy.RepeatedRampRollbackAlertThreshold {
			dec.alertType = AlertRepeatedRampRollback
			dec.alertSeverity = "high"
			dec.alertPayload = map[string]any{"ramp_failure_count": rec.RampFailureCount}
		}
		if err := s.emitTransitionEvents(ctx, prev, rec, "", "", dec); err != nil {
			return Record{}, err
		}
		if dec.alertType != "" {
			if err := s.emitAlert(ctx, rec, dec.alertType, dec.alertSeverity, dec.alertPayload); err != nil {
				return Record{}, err
			}
		}
		return rec, nil
	}
	prev := rec.State
	switch rec.RampStagePct {
	case 0:
		rec.RampStagePct = 1
	case 1:
		rec.RampStagePct = 10
	case 10:
		rec.RampStagePct = 50
	case 50:
		rec.RampStagePct = 100
	default:
		rec.State = StateActive
		rec.RampStagePct = 0
		rec.RampStartedAt = nil
		rec.ReasonClass = SignalNone
		// ramp 完全恢复 = 连续失败 streak 结束;清零,使下一轮回滚从 d*factor
		// 重新起步,而非沿用账号终生累计失败数永久卡在最大 cooldown。
		rec.RampFailureCount = 0
	}
	if rec.State == StateRamping {
		rec.RampStartedAt = &now
	}
	rec.LastTransitionAt = now
	rec.UpdatedAt = now
	rec.PolicyVersion = s.policy.Version
	rec, err = s.store.UpsertRecord(ctx, rec)
	if err != nil {
		return Record{}, err
	}
	events := []AuditEventType{EventRampStarted}
	if rec.State == StateActive {
		events = []AuditEventType{EventRecovered}
	}
	return rec, s.emitTransitionEvents(ctx, prev, rec, "", "", decision{eventTypes: events})
}

func (s *Service) ManualPause(ctx context.Context, key ChannelKey, actorID, reason string) (Record, error) {
	return s.manualTransition(ctx, key, actorID, reason, StateManualPaused, 0, []AuditEventType{EventManualOverride, EventDisabled}, "")
}

func (s *Service) ManualResume(ctx context.Context, key ChannelKey, actorID, reason string) (Record, error) {
	return s.manualTransition(ctx, key, actorID, reason, StateRamping, 1, []AuditEventType{EventManualOverride, EventRampStarted}, "")
}

func (s *Service) ForceActive(ctx context.Context, key ChannelKey, actorID, reason string) (Record, error) {
	return s.manualTransition(ctx, key, actorID, reason, StateActive, 0, []AuditEventType{EventManualOverride, EventRecovered}, "security")
}

func (s *Service) ForceCooldown(ctx context.Context, key ChannelKey, until time.Time, reason string) (Record, error) {
	return s.withMutation(ctx, func(tx *Service) (Record, error) {
		return tx.forceCooldownLocked(ctx, key, until, reason)
	})
}

func (s *Service) forceCooldownLocked(ctx context.Context, key ChannelKey, until time.Time, reason string) (Record, error) {
	if until.IsZero() {
		return Record{}, errors.New("cooldown_until is required")
	}
	rec, err := s.recordForMutation(ctx, key)
	if err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	until = until.UTC()
	if !until.After(now) {
		return Record{}, errors.New("cooldown_until must be in the future")
	}
	prev := rec.State
	reasonClass := SignalClass(strings.TrimSpace(reason))
	if reasonClass == "" {
		reasonClass = SignalRateLimit
	}
	changed := prev != StateCoolingDown || rec.ReasonClass != reasonClass
	if rec.CooldownUntil == nil || until.After(*rec.CooldownUntil) {
		cooldownUntil := until
		rec.CooldownUntil = &cooldownUntil
		changed = true
	}
	if !changed {
		return rec, nil
	}
	rec.State = StateCoolingDown
	rec.ReasonClass = reasonClass
	rec.Confidence = ConfidenceObserved
	rec.RampStagePct = 0
	rec.RampStartedAt = nil
	rec.RecoveryBlockedReason = ""
	rec.StateEnteredAt = now
	rec.LastTransitionAt = now
	rec.PolicyVersion = s.policy.Version
	rec.UpdatedAt = now
	rec, err = s.store.UpsertRecord(ctx, rec)
	if err != nil {
		return Record{}, err
	}
	return rec, s.emitTransitionEvents(ctx, prev, rec, "", "", decision{eventTypes: []AuditEventType{EventDisabled}})
}

func (s *Service) ListChannelHealth(ctx context.Context, tenantID int64, limit, offset int) ([]ChannelHealthState, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("channelhealth: service not configured")
	}
	if tenantID <= 0 {
		return nil, errors.New("tenant_id must be positive")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	return s.store.ListChannelHealth(ctx, tenantID, limit, offset)
}

func (s *Service) GetChannelHealth(ctx context.Context, tenantID int64, channelID string) (ChannelHealthState, []AuditEvent, error) {
	if s == nil || s.store == nil {
		return ChannelHealthState{}, nil, errors.New("channelhealth: service not configured")
	}
	if tenantID <= 0 {
		return ChannelHealthState{}, nil, errors.New("tenant_id must be positive")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ChannelHealthState{}, nil, errors.New("channel_id is required")
	}
	return s.store.GetChannelHealth(ctx, tenantID, channelID)
}

func (s *Service) SummarizeChannelHealth(ctx context.Context, tenantID int64) (ChannelHealthSummary, error) {
	if s == nil || s.store == nil {
		return ChannelHealthSummary{}, errors.New("channelhealth: service not configured")
	}
	if tenantID <= 0 {
		return ChannelHealthSummary{}, errors.New("tenant_id must be positive")
	}
	summary, err := s.store.SummarizeChannelHealth(ctx, tenantID)
	if err != nil {
		return ChannelHealthSummary{}, err
	}
	return normalizeChannelHealthSummary(summary), nil
}

func (s *Service) recordForMutation(ctx context.Context, key ChannelKey) (Record, error) {
	if s == nil || s.store == nil {
		return Record{}, errors.New("channelhealth: service not configured")
	}
	if key.ChannelID == "" {
		key.ChannelID = key.StableChannelID()
	}
	return s.EnsureDefaultActive(ctx, key)
}

func (s *Service) manualTransition(ctx context.Context, key ChannelKey, actorID, reason string, state HealthState, rampPct int, events []AuditEventType, alertSeverity string) (Record, error) {
	return s.withMutation(ctx, func(tx *Service) (Record, error) {
		return tx.manualTransitionLocked(ctx, key, actorID, reason, state, rampPct, events, alertSeverity)
	})
}

func (s *Service) manualTransitionLocked(ctx context.Context, key ChannelKey, actorID, reason string, state HealthState, rampPct int, events []AuditEventType, alertSeverity string) (Record, error) {
	reason = strings.TrimSpace(reason)
	if s.policy.ManualOverrideRequiresReason && reason == "" {
		return Record{}, errors.New("manual override reason is required")
	}
	rec, err := s.recordForMutation(ctx, key)
	if err != nil {
		return Record{}, err
	}
	now := s.clock.Now()
	prev := rec.State
	rec.State = state
	rec.ReasonClass = SignalManualOverride
	rec.Confidence = ConfidenceOperatorOverride
	rec.ManualOverrideActorID = actorID
	rec.ManualOverrideReason = reason
	rec.ManualPauseReason = ""
	rec.CooldownUntil = nil
	rec.RampStagePct = rampPct
	rec.RampStartedAt = nil
	if state == StateManualPaused {
		rec.ManualPauseReason = reason
	}
	if state == StateRamping {
		rec.RampStartedAt = &now
	}
	rec.StateEnteredAt = now
	rec.LastTransitionAt = now
	rec.PolicyVersion = s.policy.Version
	rec.UpdatedAt = now
	rec, err = s.store.UpsertRecord(ctx, rec)
	if err != nil {
		return Record{}, err
	}
	// 运营 resume(ForceActive→active / ManualResume→ramping)一并清 auth 降级车道(含 HardDisabled),
	// 否则被 auth 车道硬禁的账号运营者救不回(§17 修正2)。ManualPause(→manual_paused)不清。
	if s.authLane != nil && key.ProviderAccountID != 0 && (state == StateActive || state == StateRamping) {
		s.authLane.Clear(ctx, key.ProviderAccountID, authcooldown.ClearReasonOperatorResume)
	}
	if err := s.emitTransitionEvents(ctx, prev, rec, "", actorID, decision{eventTypes: events}); err != nil {
		return Record{}, err
	}
	if alertSeverity != "" {
		if err := s.emitAlert(ctx, rec, AlertManualForceActive, alertSeverity, map[string]any{"actor_id": actorID}); err != nil {
			return Record{}, err
		}
	}
	return rec, nil
}

// withMutation 的 Serializable 冲突重试预算。健康信号写(applySignal 在同一
// SERIALIZABLE 事务里 read-then-UpsertRecord channel_health_state)在同账号并发突发
// (429/5xx 风暴——恰是应触发冷却的场景)下会互撞,败者 Commit 抛 40001;不重试则该样本
// 被静默丢弃(生产唯一调用方 chat_completions_error.go 丢弃返回),延迟 CoolingDown/Disabled
// 生效(审查 B9[S3])。PostgreSQL 官方立场:Serializable 下 40001/40P01 是预期的、应由
// 应用层重试的错误。base 取小(常态无争用),decorrelated jitter 把并发惊群沿时间轴打散
// 避免重试再撞,cap 封顶长尾;退避 sleep 发生在 WithTx 之外(连接已归还池,不占连接睡眠)。
const (
	healthMutationRetryMax    = 5
	healthMutationBackoffBase = time.Millisecond
	healthMutationBackoffCap  = 25 * time.Millisecond
)

func (s *Service) withMutation(ctx context.Context, fn func(*Service) (Record, error)) (Record, error) {
	if s == nil || s.store == nil {
		return Record{}, errors.New("channelhealth: service not configured")
	}
	txs, ok := s.store.(transactionalStore)
	if !ok {
		// 无事务边界的 store:无回滚风险,emitTransitionEvents 里 pending 为 nil 走立即打(行为不变)。
		return fn(s)
	}
	var out Record
	backoff := healthMutationBackoffBase
	for attempt := 0; ; attempt++ {
		// 每次重试都跑一整个干净事务;转换日志先攒进本轮 pending,只有本轮 WithTx 返回 nil
		//(Commit 成功)后才 flush——失败/回滚轮次的 pending 丢弃,不留幽灵日志(审查 S2)。
		var pending []transitionLogRecord
		err := txs.WithTx(ctx, func(store Store) error {
			txService := *s
			txService.store = store
			txService.pendingTransitionLogs = &pending
			var innerErr error
			out, innerErr = fn(&txService)
			return innerErr
		})
		if err == nil {
			for i := range pending {
				s.logTransition(ctx, pending[i])
			}
			return out, nil
		}
		// 只重试瞬时序列化冲突;业务错误/其它错误立即原样返回,不吞不改。
		if !isHealthSerializationConflict(err) || attempt >= healthMutationRetryMax {
			return out, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, ctxErr
		}
		backoff = healthMutationNextBackoff(backoff)
		if !healthMutationSleep(ctx, backoff) {
			return out, ctx.Err()
		}
	}
}

// isHealthSerializationConflict 判定错误是否为 Serializable 事务的瞬时冲突
// (40001 序列化失败 / 40P01 死锁)——可安全重试;业务哨兵是 Go error 值而非
// *pgconn.PgError,天然不命中,确定性结果立即返回。
func isHealthSerializationConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

// healthMutationNextBackoff 按 decorrelated jitter 计算下一次退避:
// min(cap, rand[base, prev*3])。相邻重试睡眠去相关,把同账号并发惊群沿时间轴打散。
func healthMutationNextBackoff(prev time.Duration) time.Duration {
	hi := prev * 3
	if hi < healthMutationBackoffBase {
		hi = healthMutationBackoffBase
	}
	d := healthMutationBackoffBase
	if span := int64(hi - healthMutationBackoffBase); span > 0 {
		d += time.Duration(rand.Int63n(span))
	}
	if d > healthMutationBackoffCap {
		d = healthMutationBackoffCap
	}
	return d
}

// healthMutationSleep 睡眠 d,或在 ctx 结束时提前返回。true=睡满,false=ctx 已取消。
func healthMutationSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type decision struct {
	state                       HealthState
	reason                      SignalClass
	cooldown                    time.Duration
	cooldownUntil               *time.Time
	recoveryBlockedReason       string
	confidence                  ConfidenceTier
	eventTypes                  []AuditEventType
	alertType                   AlertType
	alertSeverity               string
	alertPayload                map[string]any
	auditEvenWithoutStateChange bool
}

func (s *Service) evaluate(ctx context.Context, rec Record, sig Signal, now time.Time) decision {
	class := normalizeSignalClass(sig.Class)
	if rec.State == StateManualPaused {
		return decision{}
	}
	if isBanSignal(class) {
		until := now.Add(s.policy.BanSignalMinCooldown)
		dec := decision{
			state:         StateDisabled,
			reason:        class,
			cooldownUntil: &until,
			confidence:    ConfidenceObserved,
			eventTypes:    []AuditEventType{EventDisabled},
			alertType:     AlertBanSignal,
			alertSeverity: "high",
			alertPayload:  map[string]any{"cooldown_hours": int(s.policy.BanSignalMinCooldown.Hours())},
		}
		if !s.policy.AutomaticPostBanRamp {
			dec.recoveryBlockedReason = "operator_ack_required"
		}
		return dec
	}
	if rec.State == StateRamping {
		recent := windowFor(rec.SampleWindow, s.policy.MinObservation, now)
		if recent.TotalAttempts >= s.policy.RampStageMinSamples && rampFailureRate(recent) > s.policy.RampErrorThresholdPct {
			return decision{
				state:      StateCoolingDown,
				reason:     class,
				cooldown:   s.policy.ErrorRateCooldown,
				confidence: ConfidenceObserved,
				eventTypes: []AuditEventType{EventRampRolledBack, EventDisabled},
			}
		}
	}
	if dec := s.rateLimitDecision(ctx, rec, sig, now); dec.state != "" {
		return dec
	}
	if dec := s.upstream5xxDecision(ctx, rec, sig, now); dec.state != "" {
		return dec
	}
	if dec := s.latencyDecision(rec, now); dec.state != "" {
		return dec
	}
	if dec := s.errorRateDecision(rec, now); dec.state != "" {
		return dec
	}
	return decision{}
}

func (s *Service) rateLimitDecision(ctx context.Context, rec Record, sig Signal, now time.Time) decision {
	w := windowFor(rec.SampleWindow, s.policy.RateLimitWindow, now)
	if w.TotalAttempts < s.policy.MinSampleCount {
		return decision{}
	}
	if rate(w.RateLimitHits, w.TotalAttempts) <= s.policy.RateLimitHitRateThresholdPct {
		return decision{}
	}
	var until time.Time
	if sig.RateLimitResetAt != nil && sig.RateLimitResetAt.After(now) {
		until = sig.RateLimitResetAt.UTC()
	} else {
		statusCode := sig.StatusCode
		if statusCode == 0 {
			statusCode = 429
		}
		until = now.Add(s.statusCooldown(ctx, statusCode, s.policy.DefaultRateLimitCooldown))
	}
	return decision{
		state:         StateCoolingDown,
		reason:        SignalRateLimit,
		cooldownUntil: &until,
		confidence:    ConfidenceObserved,
		eventTypes:    []AuditEventType{EventDegraded, EventDisabled},
	}
}

func (s *Service) errorRateDecision(rec Record, now time.Time) decision {
	w := windowFor(rec.SampleWindow, s.policy.ErrorRateWindow, now)
	attempts := w.TotalAttempts - w.LocalGateway5xxHits
	if attempts < s.policy.MinSampleCount {
		return decision{}
	}
	if rate(w.FailedAttempts, attempts) <= s.policy.ErrorRateThresholdPct {
		return decision{}
	}
	return decision{
		state:      StateCoolingDown,
		reason:     SignalChannelError,
		cooldown:   s.policy.ErrorRateCooldown,
		confidence: ConfidenceObserved,
		eventTypes: []AuditEventType{EventDegraded, EventDisabled},
	}
}

func (s *Service) upstream5xxDecision(ctx context.Context, rec Record, sig Signal, now time.Time) decision {
	w := windowFor(rec.SampleWindow, s.policy.Upstream5xxWindow, now)
	attempts := w.TotalAttempts - w.LocalGateway5xxHits
	if attempts < s.policy.MinSampleCount {
		return decision{}
	}
	if rate(w.Upstream5xxHits, attempts) <= s.policy.Upstream5xxRateThresholdPct {
		return decision{}
	}
	if rec.State == StateDegraded && rec.ReasonClass == SignalUpstream5xx {
		cooldown := s.policy.Upstream5xxCooldown
		if sig.StatusCode == 529 {
			cooldown = s.statusCooldown(ctx, 529, cooldown)
		}
		return decision{
			state:      StateCoolingDown,
			reason:     SignalUpstream5xx,
			cooldown:   cooldown,
			confidence: ConfidenceObserved,
			eventTypes: []AuditEventType{EventDisabled},
		}
	}
	return decision{
		state:      StateDegraded,
		reason:     SignalUpstream5xx,
		confidence: ConfidenceObserved,
		eventTypes: []AuditEventType{EventDegraded},
	}
}

func (s *Service) latencyDecision(rec Record, now time.Time) decision {
	w := windowFor(rec.SampleWindow, s.policy.LatencyWindow, now)
	if w.TotalAttempts < s.policy.MinSampleCount || w.LatencyP99MS <= s.policy.LatencyP99ThresholdMS {
		return decision{}
	}
	if rec.State == StateDegraded && rec.ReasonClass == SignalLatencyP99 {
		return decision{
			state:      StateCoolingDown,
			reason:     SignalLatencyP99,
			cooldown:   s.policy.LatencyCooldown,
			confidence: ConfidenceObserved,
			eventTypes: []AuditEventType{EventDisabled},
		}
	}
	return decision{
		state:      StateDegraded,
		reason:     SignalLatencyP99,
		confidence: ConfidenceObserved,
		eventTypes: []AuditEventType{EventDegraded},
	}
}

func applyDecision(rec *Record, dec decision, now time.Time, p Policy) {
	rec.State = dec.state
	if dec.reason != "" {
		rec.ReasonClass = dec.reason
	}
	if dec.confidence != "" {
		rec.Confidence = dec.confidence
	}
	if dec.cooldownUntil != nil {
		c := dec.cooldownUntil.UTC()
		rec.CooldownUntil = &c
	} else if dec.cooldown > 0 {
		c := now.Add(dec.cooldown)
		rec.CooldownUntil = &c
	}
	if rec.State == StateCoolingDown && rec.CooldownUntil == nil {
		c := now.Add(p.ErrorRateCooldown)
		rec.CooldownUntil = &c
	}
	if rec.State != StateRamping {
		rec.RampStagePct = 0
		rec.RampStartedAt = nil
	}
	rec.RecoveryBlockedReason = dec.recoveryBlockedReason
	rec.StateEnteredAt = now
	rec.LastTransitionAt = now
	rec.PolicyVersion = p.Version
	rec.UpdatedAt = now
}

// maxRampBackoffLevel 封顶连续 ramp 回滚的指数退避级数,防 factor^n 无界增长。
// factor=2(默认)时最大 2^5=32x ErrorRateCooldown。
const maxRampBackoffLevel = 5

func (s *Service) rollbackRamp(rec *Record, now time.Time, reason SignalClass) {
	rec.State = StateCoolingDown
	rec.ReasonClass = reason
	rec.RampFailureCount++
	rec.RampStagePct = 0
	rec.RampStartedAt = nil
	d := s.policy.ErrorRateCooldown
	if d <= 0 {
		d = DefaultPolicy().ErrorRateCooldown
	}
	// 连续 ramp 回滚指数升级 cooldown:第 n 次连续回滚 backoff = d * factor^n。
	// 单次回滚(RampFailureCount==1)= d*factor,与历史行为逐字一致;之后随连续
	// 失败次数升级,level 封顶 maxRampBackoffLevel 防 factor^n 无界增长。streak 在
	// ramp 完全恢复(AdvanceRamp 推进到 StateActive)时清零。
	factor := s.policy.RampBackoffFactor
	if factor < 1 {
		factor = 1
	}
	level := rec.RampFailureCount
	if level < 1 {
		level = 1
	}
	if level > maxRampBackoffLevel {
		level = maxRampBackoffLevel
	}
	backoff := time.Duration(float64(d) * math.Pow(factor, float64(level)))
	c := now.Add(backoff)
	rec.CooldownUntil = &c
	rec.StateEnteredAt = now
	rec.LastTransitionAt = now
	rec.UpdatedAt = now
	rec.PolicyVersion = s.policy.Version
}

func (s *Service) emitAlert(ctx context.Context, rec Record, typ AlertType, severity string, payload map[string]any) error {
	if severity == "" {
		severity = "high"
	}
	if payload == nil {
		payload = map[string]any{}
	}
	alert := Alert{
		Type:        typ,
		Key:         rec.Key,
		Severity:    severity,
		ReasonClass: rec.ReasonClass,
		Payload:     payload,
		CreatedAt:   s.clock.Now(),
	}
	if s.alertOutbox != nil {
		alert.Payload = sanitizePayloadMap(ctx, alert.Payload)
		raw, err := json.Marshal(alert)
		if err != nil {
			return err
		}
		_, err = s.alertOutbox.Enqueue(ctx, obsdlq.OutboxEvent{
			TenantID:  rec.Key.TenantID,
			EventType: obsdlq.EventTypeChannelAlert,
			Priority:  obsdlq.PriorityDefault,
			Payload:   raw,
		})
		return err
	}
	return s.store.AppendAlert(ctx, alert)
}

func defaultEvents(prev, next HealthState) []AuditEventType {
	switch next {
	case StateDegraded:
		return []AuditEventType{EventDegraded}
	case StateCoolingDown, StateDisabled, StateManualPaused:
		return []AuditEventType{EventDisabled}
	case StateRamping:
		return []AuditEventType{EventRampStarted}
	case StateActive:
		if prev != "" && prev != StateActive {
			return []AuditEventType{EventRecovered}
		}
	}
	return nil
}

func hasEvent(events []AuditEventType, want AuditEventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func auditPayload(rec Record) map[string]any {
	payload := map[string]any{
		"tenant_id":             rec.Key.TenantID,
		"channel_id":            rec.Key.StableChannelID(),
		"vendor":                rec.Key.Vendor,
		"provider_account_id":   rec.Key.ProviderAccountID,
		"account_credential_id": rec.Key.AccountCredentialID,
		"credential_version":    rec.Key.CredentialVersion,
		"state":                 rec.State,
		"reason_class":          rec.ReasonClass,
		"policy_version":        rec.PolicyVersion,
		"score":                 rec.Score,
		"window_summary": map[string]any{
			"total_attempts":    rec.SampleWindow.TotalAttempts,
			"failed_attempts":   rec.SampleWindow.FailedAttempts,
			"rate_limit_hits":   rec.SampleWindow.RateLimitHits,
			"upstream_5xx_hits": rec.SampleWindow.Upstream5xxHits,
			"latency_p99_ms":    rec.SampleWindow.LatencyP99MS,
			"ban_signals":       rec.SampleWindow.BanSignals,
		},
		"ramp_stage_pct":     rec.RampStagePct,
		"ramp_failure_count": rec.RampFailureCount,
	}
	if rec.CooldownUntil != nil {
		payload["cooldown_until"] = rec.CooldownUntil.UTC().Format(time.RFC3339Nano)
	}
	if rec.ManualOverrideActorID != "" {
		payload["manual_override_actor_id"] = rec.ManualOverrideActorID
	}
	return payload
}

func newRecord(key ChannelKey, p Policy, now time.Time) Record {
	key.ChannelID = key.StableChannelID()
	return Record{
		Key:              key,
		State:            StateActive,
		Score:            100,
		ReasonClass:      SignalNone,
		Confidence:       ConfidenceObserved,
		StateEnteredAt:   now,
		LastTransitionAt: now,
		PolicyVersion:    p.Version,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func rampFailureRate(w WindowSummary) float64 {
	return rate(w.FailedAttempts, w.TotalAttempts)
}

func (s *Service) Policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	return s.policy
}

func (s *Service) Store() Store {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *Service) String() string {
	if s == nil {
		return "channelhealth.Service<nil>"
	}
	return fmt.Sprintf("channelhealth.Service{policy=%s}", s.policy.Version)
}
