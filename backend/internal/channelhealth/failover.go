package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type PoolGate struct {
	store GateStore
	clock Clock
	// authLane 是独立于健康 FSM 的 auth 降级车道(nil=未接线,auth 检查短路,行为不变)。
	authLane AuthCooldownLane
}

type GateStore interface {
	LatestByProviderAccount(context.Context, int64, int64) (Record, error)
}

func NewPoolGate(store GateStore, clock Clock) *PoolGate {
	if clock == nil {
		clock = realClock{}
	}
	return &PoolGate{store: store, clock: clock}
}

func NewServicePoolGate(service *Service, clock Clock) *PoolGate {
	if service == nil {
		return NewPoolGate(nil, clock)
	}
	store, _ := service.Store().(GateStore)
	gate := NewPoolGate(store, clock)
	// auth 车道与 Service 共享同一实例:applySignal 写(Suspend/Clear)、PoolGate 读(Eligible),
	// 二者必须看同一份内存态。
	gate.authLane = service.authLane
	return gate
}

func (g *PoolGate) Allow(ctx context.Context, account *pool.AccountSnapshot, req pool.SelectionRequest) (bool, pool.GateFailureReason, error) {
	if g == nil || account == nil {
		return true, "", nil
	}
	// auth 降级车道检查:独立于健康 store(即使 store==nil 也生效),不读健康 State/Score。
	// 被 auth 车道移出选号(now<AuthUntil 或 HardDisabled)→ 返回独立的 GateFailureAuthCooldown,
	// 与 GateFailureHealth 区分(审计/计数可辨识)。
	if g.authLane != nil {
		ok, hardDisabled := g.authLane.Eligible(account.ID, g.clock.Now())
		if !ok {
			// DisableCooling 逃生阀只豁免软退避,不豁免 HardDisabled(否则给 revoked 号重开黑洞,修正5)。
			if !(account.DisableCooling && !hardDisabled) {
				return false, pool.GateFailureAuthCooldown, nil
			}
		}
	}
	if g.store == nil {
		return true, "", nil
	}
	rec, err := g.store.LatestByProviderAccount(ctx, req.TenantID, account.ID)
	if errors.Is(err, ErrNotFound) {
		return true, "", nil
	}
	if err != nil {
		return false, pool.GateFailureHealth, err
	}
	// disable_cooling 运维逃生阀(TOKLIFE-02):被 flag 的账号豁免"冷却/渐进放量"这类**流量抑制**,
	// 直接满流量放行。生产门链的 Health gate 被覆盖成本 channelhealth PoolGate(selector_wiring),
	// 而原本唯一读 DisableCooling 的 ProviderAccountHealthGate 在生产不跑——故该开关在生产此前是死的;
	// 这里补上读侧消费让它真生效。**只豁免 cooling_down/ramping**:disabled/manual_paused 这类硬停
	// (ban 信号即时禁用、运维手动暂停)必须仍然拦截,不能被 disable_cooling 绕过。默认 false → 行为不变。
	if account.DisableCooling {
		switch rec.State {
		case StateCoolingDown, StateRamping:
			return true, "", nil
		}
	}
	ok := IsEligible(rec, RampAdmissionKey(req, account.ID), g.clock.Now())
	if !ok {
		return false, pool.GateFailureHealth, nil
	}
	return true, "", nil
}

func (g *PoolGate) HealthStatus(ctx context.Context, account *pool.AccountSnapshot, req pool.SelectionRequest) (pool.HealthStatus, error) {
	if g == nil || g.store == nil || account == nil {
		return pool.HealthStatus{State: pool.HealthStateActive}, nil
	}
	rec, err := g.store.LatestByProviderAccount(ctx, req.TenantID, account.ID)
	if errors.Is(err, ErrNotFound) {
		return pool.HealthStatus{State: pool.HealthStateActive}, nil
	}
	if err != nil {
		return pool.HealthStatus{}, err
	}
	return pool.HealthStatus{State: string(rec.State), RampStagePct: rec.RampStagePct}, nil
}

func IsEligible(rec Record, admissionKey string, _ time.Time) bool {
	switch rec.State {
	case StateActive, StateDegraded:
		return true
	case StateRamping:
		return AdmitRamp(admissionKey, rec.RampStagePct)
	case StateCoolingDown:
		// 冷却状态由后台恢复协调器转成 ramping；请求热路径只读，因此即使
		// 截止时间已到，也要等状态转换完成后再按放量比例准入。
		return false
	case StateDisabled, StateManualPaused:
		return false
	default:
		return true
	}
}

func AdmitRamp(key string, pct int) bool {
	if pct >= 100 {
		return true
	}
	if pct <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()%100) < pct
}

func rampFailureRate(window WindowSummary) float64 {
	return rate(window.FailedAttempts, window.TotalAttempts)
}

func RampAdmissionKey(req pool.SelectionRequest, accountID int64) string {
	basis := "session:" + req.SessionHash
	if req.SessionHash == "" {
		basis = "continuation:" + req.ContinuationKey
	}
	if req.SessionHash == "" && req.ContinuationKey == "" {
		basis = "request:" + req.RequestID
	}
	if req.SessionHash == "" && req.ContinuationKey == "" && req.RequestID == "" && req.ClaimID > 0 {
		basis = fmt.Sprintf("claim:%d", req.ClaimID)
	}
	if req.SessionHash == "" && req.ContinuationKey == "" && req.RequestID == "" && req.ClaimID <= 0 {
		basis = "model:" + req.RequestedModel
	}
	return fmt.Sprintf("%d:%d:%s", req.TenantID, accountID, basis)
}

var _ pool.HealthGate = (*PoolGate)(nil)
