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
	ramp  interface {
		MaybeStartRamp(context.Context, ChannelKey) (Record, error)
	}
	clock Clock
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
	gate.ramp = service
	return gate
}

func (g *PoolGate) Allow(ctx context.Context, account *pool.AccountSnapshot, req pool.SelectionRequest) (bool, pool.GateFailureReason, error) {
	if g == nil || g.store == nil || account == nil {
		return true, "", nil
	}
	rec, err := g.store.LatestByProviderAccount(ctx, req.TenantID, account.ID)
	if errors.Is(err, ErrNotFound) {
		return true, "", nil
	}
	if err != nil {
		return false, pool.GateFailureHealth, err
	}
	rec, err = g.maybeStartExpiredRamp(ctx, rec)
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
	rec, err = g.maybeStartExpiredRamp(ctx, rec)
	if err != nil {
		return pool.HealthStatus{}, err
	}
	return pool.HealthStatus{State: string(rec.State), RampStagePct: rec.RampStagePct}, nil
}

func (g *PoolGate) maybeStartExpiredRamp(ctx context.Context, rec Record) (Record, error) {
	if rec.State != StateCoolingDown || rec.CooldownUntil == nil || rec.CooldownUntil.After(g.clock.Now()) {
		return rec, nil
	}
	if g.ramp == nil {
		return rec, nil
	}
	return g.ramp.MaybeStartRamp(ctx, rec.Key)
}

func IsEligible(rec Record, admissionKey string, now time.Time) bool {
	switch rec.State {
	case StateActive, StateDegraded:
		return true
	case StateRamping:
		return AdmitRamp(admissionKey, rec.RampStagePct)
	case StateCoolingDown:
		// 冷却已到期 → 放行,让通道在冷却结束后自动恢复(否则一旦进 cooling_down 即永久卡死)。
		// 主流 Allow()/HealthStatus 会先由 maybeStartExpiredRamp 把"非 nil 且已过期"的记录转成
		// ramping;但当 ramp 未接线(NewPoolGate,g.ramp==nil)或本函数被其它路径直达时,这里是
		// 唯一的恢复闸门——必须放行已到期冷却。与 maybeStartExpiredRamp 的 guard(同样只对"非 nil
		// 且已过期"动作)语义对齐:未到期 → 拒绝;无截止时间(nil)→ 保守拒绝(行为不变)。
		if rec.CooldownUntil != nil && !rec.CooldownUntil.After(now) {
			return true
		}
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

func RampAdmissionKey(req pool.SelectionRequest, accountID int64) string {
	basis := req.SessionHash
	if basis == "" {
		basis = req.ContinuationKey
	}
	if basis == "" {
		basis = req.RequestedModel
	}
	return fmt.Sprintf("%d:%d:%s:%d", req.TenantID, accountID, basis, req.AttemptSeq)
}

var _ pool.HealthGate = (*PoolGate)(nil)
