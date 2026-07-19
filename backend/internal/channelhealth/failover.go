package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type PoolGate struct {
	store GateStore
	ramp  interface {
		MaybeStartRamp(context.Context, ChannelKey) (Record, error)
	}
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
	gate.ramp = service
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
	ramped, err := g.ramp.MaybeStartRamp(ctx, rec.Key)
	if err != nil {
		// 读路径上的机会性 ramp-start 是无 40001 重试的 SERIALIZABLE 写。并发选号同时评估同一
		// 到期冷却账号时,只有一个能提交 CoolingDown→Ramping,其余竞争落败者收到 40001/40P01。
		// 这是良性竞争:胜者已把记录翻成 ramping。落败者绝不能因此把正在恢复的账号误判为不可用
		// ——否则若它是唯一可选账号,调用方拿到 spurious NoCapacity,账号在负载下持续抖动。
		// 吞掉序列化冲突并返回原(到期冷却)记录,交由 IsEligible 的"到期冷却→放行"闸门恢复。
		// 非序列化错误(真实 DB 故障)仍上抛,保持既有保守语义。
		if isRampContentionError(err) {
			return rec, nil
		}
		return Record{}, err
	}
	return ramped, nil
}

// isRampContentionError 判定 err 是否为 PostgreSQL 序列化失败(40001)或死锁(40P01)——
// 即 Serializable 事务下的良性并发争抢,与业务/连接错误区分。与 internal/billing、
// internal/mediatask 等处的判定约定一致。
func isRampContentionError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
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
