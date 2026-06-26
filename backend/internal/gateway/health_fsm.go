// A22 账号健康度滞回(Hysteresis)状态机。
// 规范:docs/specs/rate-limiting.md §A22 / DR-009 §6.6 / synthesis §1 A22。
//
// 硬底线(DR-009 §6.6):FSM 绝不能在 ambiguous-tier 的分类上自动转入
// StateDisabled。只有 TierIronClad 分类(带 FsmTransitionDisabled)或显式的
// operator 动作才能跨入 Disabled。由 isIronCladDisable() 在结构上强制保证 ——
// 每条产生 HealthStateDisabled 的代码路径都经过该谓词。
//
// 注:docs/process/plans/2026-05-04-a22-codeparallel-synthesis.md。
package gateway

import (
	"math"
	"time"
)

// HealthState 是 A22 的 6 态枚举。
type HealthState string

const (
	HealthStateNormal              HealthState = "normal"
	HealthStateDegraded            HealthState = "degraded"
	HealthStateCoolingDown         HealthState = "cooling_down"
	HealthStateNeedsRefresh        HealthState = "needs_refresh"
	HealthStateNeedsManualRecovery HealthState = "needs_manual_recovery"
	HealthStateDisabled            HealthState = "disabled"
)

// 滞回阈值 —— 按设计互不重叠(§A22 不变量)。
const (
	DefaultUpgradeStreak           = 10
	DefaultDegradedErrorThreshold  = 3
	DefaultManualRecoveryThreshold = 5
	DefaultScoreIncrement          = 0.05
	DefaultScoreDecrement          = 0.15
	DefaultHealthHalfLife          = 10 * time.Minute
	DefaultCooldownExitScore       = 0.40
	DefaultCooldownDuration        = 60 * time.Second
	DefaultErrorWindow             = 60 * time.Second
	DefaultInitialHealthScore      = 1.0
	neutralHealthScore             = 0.5
)

// EventType 区分输入到 FSM 的事件。
type EventType string

const (
	EventClassification EventType = "classification"
	EventClockTime      EventType = "clock_time"
	EventProbeResult    EventType = "probe_result"
)

// FSMClassification 是 A13 Classification 的 FSM 输入视图,额外补充了 FSM
// 所需的少量字段(severity、refresh 提示、可选的 cooldown override)。调用方
// 通过 ToFSMClassification() 从 gateway.Classification 构造它。
type FSMClassification struct {
	RuleID        string
	ErrorClass    string
	FsmTransition FsmTransition
	Tier          DisableTier
	RetryAfter    time.Duration
	CooldownUntil time.Time
	Severity      float64
	NeedsRefresh  bool
}

// ProbeResult 把带外 probe 的结果回传给 FSM。
type ProbeResult struct {
	Clean              bool
	RefreshSuccess     bool
	RefreshExhausted   bool
	OperatorClear      bool
	OperatorReenable   bool
	ObservedAt         time.Time
	HealthScore        float64
	HealthScoreKnown   bool
	CleanSuccessStreak int
}

// HealthSnapshot 是 FSM 消费的每账号持久化状态;
// FSM 是纯函数,绝不修改该 snapshot。
type HealthSnapshot struct {
	HealthScore         float64
	LastScoredAt        time.Time
	CleanSuccessStreak  int
	RecentErrorTimes    []time.Time
	AmbiguousErrorCount int
	CooldownUntil       time.Time
}

// Event 是 FSM Transition 的输入。
type Event struct {
	Type           EventType
	Classification FSMClassification
	ClockTime      time.Time
	ProbeResult    ProbeResult
	Health         HealthSnapshot
}

// SideEffectType 枚举 Transition 返回的延迟动作。
type SideEffectType string

const (
	SideEffectSetCooldownUntil SideEffectType = "set_cooldown_until"
	SideEffectEnqueueProbe     SideEffectType = "enqueue_probe"
	SideEffectEmitMetric       SideEffectType = "emit_metric"
	SideEffectNotifyOperator   SideEffectType = "notify_operator"
)

// SideEffect 是单个延迟动作;FSM 不执行 side effect。
type SideEffect struct {
	Type    SideEffectType
	Until   time.Time
	Metric  string
	Value   float64
	State   HealthState
	Reason  string
	RuleID  string
	Message string
}

// ToFSMClassification 把 gateway.Classification(R6 输出)桥接为 FSM 输入视图。
// 从 ErrorClass 推导 NeedsRefresh,从 Tier 推导 Severity。
func ToFSMClassification(c Classification) FSMClassification {
	severity := 1.0
	if c.Tier == TierIronClad {
		severity = 5.0
	}
	needsRefresh := false
	if c.Class == ErrorClassOAuthInvalidGrant && c.RetryAction == RetryActionCooldown {
		// OAuth refresh 路径:实际刷新由 A07 storm controller / F-AUTH-005 负责;
		// FSM 仅给出 NeedsRefresh 状态信号,让调用方路由到那条路径。
		needsRefresh = true
	}
	cooldown := time.Duration(0)
	if c.RetryAfterMs > 0 {
		cooldown = time.Duration(c.RetryAfterMs) * time.Millisecond
	}
	return FSMClassification{
		RuleID:        c.RuleID,
		ErrorClass:    string(c.Class),
		FsmTransition: c.FsmTransition,
		Tier:          c.Tier,
		RetryAfter:    cooldown,
		Severity:      severity,
		NeedsRefresh:  needsRefresh,
	}
}

// Transition 是纯粹的 FSM 步进函数。它绝不修改输入;
// 由调用方持久化新状态并执行 SideEffect。
func Transition(state HealthState, event Event, now time.Time) (HealthState, []SideEffect) {
	now = effectiveNow(event, now)
	score := healthScoreAfterEvent(event, now)

	next := state
	effects := []SideEffect{metricEffect("account_health_score", score, state, reasonForEvent(event), event.Classification.RuleID)}

	switch state {
	case HealthStateNormal:
		next, effects = transitionFromNormal(event, now, score, effects)
	case HealthStateDegraded:
		next, effects = transitionFromDegraded(event, now, score, effects)
	case HealthStateCoolingDown:
		next, effects = transitionFromCoolingDown(event, now, score, effects)
	case HealthStateNeedsRefresh:
		next, effects = transitionFromNeedsRefresh(event, now, score, effects)
	case HealthStateNeedsManualRecovery:
		next, effects = transitionFromNeedsManualRecovery(event, now, score, effects)
	case HealthStateDisabled:
		next, effects = transitionFromDisabled(event, now, score, effects)
	default:
		next = HealthStateNormal
		effects = append(effects, metricEffect("account_health_state_normalized", 1, next, "unknown_state", event.Classification.RuleID))
	}

	if next != state {
		effects = append(effects, metricEffect("account_health_state_transition", 1, next, string(state)+"->"+string(next), event.Classification.RuleID))
	}

	return next, effects
}

func transitionFromNormal(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventClassification {
		c := event.Classification
		if c.NeedsRefresh {
			return HealthStateNeedsRefresh, effects
		}
		if isIronCladDisable(c) {
			return HealthStateDisabled, append(effects, notifyOperator("iron_clad_disable", c.RuleID, HealthStateDisabled))
		}
		if c.FsmTransition == FsmTransitionCooling {
			return enterCoolingDown(now, withEscalatedCooldown(c, recentErrorCount(event.Health.RecentErrorTimes, now, true), now), effects)
		}
		if isAmbiguous(c) || c.FsmTransition == FsmTransitionDegraded || c.FsmTransition == FsmTransitionManualOnly {
			if manualRecoveryCount(event) >= DefaultManualRecoveryThreshold {
				return HealthStateNeedsManualRecovery, append(effects, notifyOperator("ambiguous_manual_threshold", c.RuleID, HealthStateNeedsManualRecovery))
			}
			if recentErrorCount(event.Health.RecentErrorTimes, now, true) >= DefaultDegradedErrorThreshold {
				return HealthStateDegraded, effects
			}
		}
	}
	if event.Type == EventProbeResult && event.ProbeResult.RefreshSuccess {
		return HealthStateNormal, effects
	}
	return HealthStateNormal, effects
}

func transitionFromDegraded(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventClassification {
		c := event.Classification
		if c.NeedsRefresh {
			return HealthStateNeedsRefresh, effects
		}
		if isIronCladDisable(c) {
			return HealthStateDisabled, append(effects, notifyOperator("iron_clad_disable", c.RuleID, HealthStateDisabled))
		}
		if c.FsmTransition == FsmTransitionCooling {
			return enterCoolingDown(now, withEscalatedCooldown(c, recentErrorCount(event.Health.RecentErrorTimes, now, true), now), effects)
		}
		if isAmbiguous(c) || c.FsmTransition == FsmTransitionManualOnly {
			if manualRecoveryCount(event) >= DefaultManualRecoveryThreshold {
				return HealthStateNeedsManualRecovery, append(effects, notifyOperator("ambiguous_manual_threshold", c.RuleID, HealthStateNeedsManualRecovery))
			}
			return HealthStateDegraded, effects
		}
	}
	if event.Type == EventProbeResult {
		if event.ProbeResult.RefreshExhausted {
			return HealthStateNeedsManualRecovery, append(effects, notifyOperator("refresh_exhausted", "", HealthStateNeedsManualRecovery))
		}
		if cleanSuccessStreak(event) >= DefaultUpgradeStreak && score >= 0.9 {
			return HealthStateNormal, effects
		}
	}
	return HealthStateDegraded, effects
}

func transitionFromCoolingDown(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventClassification {
		c := event.Classification
		if isIronCladDisable(c) {
			return HealthStateDisabled, append(effects, notifyOperator("iron_clad_disable", c.RuleID, HealthStateDisabled))
		}
		if c.FsmTransition == FsmTransitionCooling {
			return enterCoolingDown(now, withEscalatedCooldown(c, recentErrorCount(event.Health.RecentErrorTimes, now, true), now), effects)
		}
		if isAmbiguous(c) && manualRecoveryCount(event) >= DefaultManualRecoveryThreshold {
			return HealthStateNeedsManualRecovery, append(effects, notifyOperator("ambiguous_manual_threshold", c.RuleID, HealthStateNeedsManualRecovery))
		}
	}
	until := cooldownUntilFor(event, now)
	if event.Type == EventClockTime && !now.Before(until) {
		effects = append(effects, SideEffect{Type: SideEffectEnqueueProbe, State: HealthStateCoolingDown, Reason: "cooldown_expired"})
		return HealthStateCoolingDown, effects
	}
	if event.Type == EventProbeResult && event.ProbeResult.Clean && !now.Before(until) && score >= DefaultCooldownExitScore {
		return HealthStateDegraded, effects
	}
	return HealthStateCoolingDown, effects
}

func transitionFromNeedsRefresh(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventClassification {
		c := event.Classification
		if isIronCladDisable(c) {
			return HealthStateDisabled, append(effects, notifyOperator("iron_clad_disable", c.RuleID, HealthStateDisabled))
		}
		if c.FsmTransition == FsmTransitionCooling {
			return enterCoolingDown(now, withEscalatedCooldown(c, recentErrorCount(event.Health.RecentErrorTimes, now, true), now), effects)
		}
		if isAmbiguous(c) && manualRecoveryCount(event) >= DefaultManualRecoveryThreshold {
			return HealthStateNeedsManualRecovery, append(effects, notifyOperator("ambiguous_manual_threshold", c.RuleID, HealthStateNeedsManualRecovery))
		}
	}
	if event.Type == EventProbeResult {
		switch {
		case event.ProbeResult.RefreshSuccess:
			return HealthStateNormal, effects
		case event.ProbeResult.RefreshExhausted:
			return HealthStateNeedsManualRecovery, append(effects, notifyOperator("refresh_exhausted", "", HealthStateNeedsManualRecovery))
		}
	}
	return HealthStateNeedsRefresh, effects
}

func transitionFromNeedsManualRecovery(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventClassification && isIronCladDisable(event.Classification) {
		return HealthStateDisabled, append(effects, notifyOperator("iron_clad_disable", event.Classification.RuleID, HealthStateDisabled))
	}
	if event.Type == EventProbeResult && event.ProbeResult.OperatorClear {
		return HealthStateDegraded, effects
	}
	return HealthStateNeedsManualRecovery, effects
}

func transitionFromDisabled(event Event, now time.Time, score float64, effects []SideEffect) (HealthState, []SideEffect) {
	if event.Type == EventProbeResult && event.ProbeResult.OperatorReenable {
		return HealthStateNeedsManualRecovery, append(effects, notifyOperator("operator_reenable", "", HealthStateNeedsManualRecovery))
	}
	return HealthStateDisabled, effects
}

func enterCoolingDown(now time.Time, c FSMClassification, effects []SideEffect) (HealthState, []SideEffect) {
	until := c.CooldownUntil
	if until.IsZero() {
		if c.RetryAfter > 0 {
			until = now.Add(c.RetryAfter)
		} else {
			until = now.Add(DefaultCooldownDuration)
		}
	}
	effects = append(effects,
		SideEffect{Type: SideEffectSetCooldownUntil, Until: until, State: HealthStateCoolingDown, Reason: "cooldown_trigger", RuleID: c.RuleID},
		SideEffect{Type: SideEffectEnqueueProbe, Until: until, State: HealthStateCoolingDown, Reason: "cooldown_trigger", RuleID: c.RuleID},
	)
	return HealthStateCoolingDown, effects
}

// HealthScoreAfterEvent 导出给需要在不改变状态机的前提下预测某事件后
// 健康度分值的调用方使用。
func HealthScoreAfterEvent(event Event, now time.Time) float64 {
	return healthScoreAfterEvent(event, effectiveNow(event, now))
}

func healthScoreAfterEvent(event Event, now time.Time) float64 {
	if event.Type == EventProbeResult && event.ProbeResult.HealthScoreKnown {
		return clamp01(event.ProbeResult.HealthScore)
	}
	score := scoreFromSnapshot(event.Health, now)
	switch event.Type {
	case EventClassification:
		if isErrorClassification(event.Classification) {
			severity := event.Classification.Severity
			if severity <= 0 {
				severity = 1
			}
			if event.Classification.Tier == TierIronClad {
				severity = math.Max(severity, 5)
			}
			score = clamp01(score - DefaultScoreDecrement*severity)
		}
	case EventProbeResult:
		if event.ProbeResult.Clean || event.ProbeResult.RefreshSuccess {
			score = clamp01(score + DefaultScoreIncrement)
		}
	}
	return score
}

// DecayHealthScore 在配置的半衰期内,把分值按指数衰减松弛向中性值。
// 导出给维护 snapshot 的调用方使用。
func DecayHealthScore(score float64, lastScoredAt time.Time, now time.Time) float64 {
	score = clamp01(score)
	if lastScoredAt.IsZero() || !now.After(lastScoredAt) {
		return score
	}
	halfLives := now.Sub(lastScoredAt).Seconds() / DefaultHealthHalfLife.Seconds()
	factor := math.Pow(0.5, halfLives)
	return clamp01(neutralHealthScore + (score-neutralHealthScore)*factor)
}

func scoreFromSnapshot(snapshot HealthSnapshot, now time.Time) float64 {
	if snapshot.LastScoredAt.IsZero() && snapshot.HealthScore == 0 {
		return DefaultInitialHealthScore
	}
	return DecayHealthScore(snapshot.HealthScore, snapshot.LastScoredAt, now)
}

func isErrorClassification(c FSMClassification) bool {
	return c.FsmTransition == FsmTransitionDisabled ||
		c.FsmTransition == FsmTransitionDegraded ||
		c.FsmTransition == FsmTransitionCooling ||
		c.FsmTransition == FsmTransitionManualOnly ||
		c.Tier == TierAmbiguous ||
		c.Tier == TierIronClad ||
		c.NeedsRefresh
}

// isIronCladDisable 是 §6.6 的硬底线谓词:只有 TierIronClad +
// FsmTransitionDisabled 才能自动跨入 HealthStateDisabled。每条产生
// HealthStateDisabled 的代码路径都经过该谓词。
func isIronCladDisable(c FSMClassification) bool {
	return c.FsmTransition == FsmTransitionDisabled && c.Tier == TierIronClad
}

func isAmbiguous(c FSMClassification) bool {
	return c.Tier == TierAmbiguous || c.FsmTransition == FsmTransitionManualOnly
}

func manualRecoveryCount(event Event) int {
	if event.Type != EventClassification || !isAmbiguous(event.Classification) {
		return event.Health.AmbiguousErrorCount
	}
	return event.Health.AmbiguousErrorCount + 1
}

func cleanSuccessStreak(event Event) int {
	streak := event.Health.CleanSuccessStreak
	if event.ProbeResult.Clean || event.ProbeResult.RefreshSuccess {
		streak++
	}
	if event.ProbeResult.CleanSuccessStreak > streak {
		streak = event.ProbeResult.CleanSuccessStreak
	}
	return streak
}

func recentErrorCount(times []time.Time, now time.Time, includeCurrent bool) int {
	count := 0
	cutoff := now.Add(-DefaultErrorWindow)
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if !t.Before(cutoff) && !t.After(now) {
			count++
		}
	}
	if includeCurrent {
		count++
	}
	return count
}

func cooldownUntilFor(event Event, now time.Time) time.Time {
	if !event.Health.CooldownUntil.IsZero() {
		return event.Health.CooldownUntil
	}
	if !event.Classification.CooldownUntil.IsZero() {
		return event.Classification.CooldownUntil
	}
	return now.Add(DefaultCooldownDuration)
}

func effectiveNow(event Event, now time.Time) time.Time {
	if !now.IsZero() {
		return now
	}
	if event.Type == EventClockTime && !event.ClockTime.IsZero() {
		return event.ClockTime
	}
	if event.Type == EventProbeResult && !event.ProbeResult.ObservedAt.IsZero() {
		return event.ProbeResult.ObservedAt
	}
	return time.Unix(0, 0).UTC()
}

func reasonForEvent(event Event) string {
	switch event.Type {
	case EventClassification:
		if event.Classification.ErrorClass != "" {
			return event.Classification.ErrorClass
		}
		return string(event.Classification.FsmTransition)
	case EventClockTime:
		return "clock_time"
	case EventProbeResult:
		return "probe_result"
	default:
		return "unknown_event"
	}
}

func metricEffect(metric string, value float64, state HealthState, reason string, ruleID string) SideEffect {
	return SideEffect{Type: SideEffectEmitMetric, Metric: metric, Value: value, State: state, Reason: reason, RuleID: ruleID}
}

func notifyOperator(reason string, ruleID string, state HealthState) SideEffect {
	return SideEffect{Type: SideEffectNotifyOperator, State: state, Reason: reason, RuleID: ruleID, Message: reason}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// withEscalatedCooldown 实现 TOKLIFE-01 冷却指数退避:当上游没给 Retry-After /
// 显式 CooldownUntil override 时,按窗口内连续错误次数把 cooldown 翻倍
// (60s -> 2m -> 4m -> ... 上限 30m),避免一个反复 429 的账号被反复短冷却(60s)
// 空转抖动、反复打上游。streak 来自已持久化的 RecentErrorTimes(无需新 schema)。
// 只会【拉长】反复失败账号的冷却,不影响正常账号可用性。
func withEscalatedCooldown(c FSMClassification, errorStreak int, now time.Time) FSMClassification {
	if !c.CooldownUntil.IsZero() || c.RetryAfter > 0 {
		return c // provider / 显式 override 优先,不退避
	}
	d := DefaultCooldownDuration
	if errorStreak > 1 {
		shift := errorStreak - 1
		if shift > 30 {
			shift = 30 // 防极端 streak 上溢
		}
		d = DefaultCooldownDuration * time.Duration(int64(1)<<uint(shift))
	}
	const escalationCap = 30 * time.Minute
	if d > escalationCap || d <= 0 {
		d = escalationCap
	}
	c.CooldownUntil = now.Add(d)
	return c
}
