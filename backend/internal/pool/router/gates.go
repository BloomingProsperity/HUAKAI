package router

import (
	"context"
	"time"
)

type GateFailureReason string

const (
	GateFailureTenantFilter   GateFailureReason = "tenant_filter"
	GateFailureLifecycle      GateFailureReason = "lifecycle"
	GateFailureChannel        GateFailureReason = "channel"
	GateFailureProtocolFamily GateFailureReason = "protocol_family"
	GateFailureModel          GateFailureReason = "model"
	GateFailureModelCooldown  GateFailureReason = "model_cooldown"
	GateFailureCapability     GateFailureReason = "capability"
	GateFailureCredential     GateFailureReason = "credential"
	GateFailureHealth         GateFailureReason = "health"
	// GateFailureAuthCooldown 是 auth 降级车道(authcooldown)专用的不合格原因,与 GateFailureHealth
	// 区分:auth 失败不写健康分,单独临时排除,便于审计/计数辨识「因坏 key 被移出选号」。
	GateFailureAuthCooldown        GateFailureReason = "auth_cooldown"
	GateFailureGroupPolicy         GateFailureReason = "group_policy"
	GateFailurePerRequestExclusion GateFailureReason = "per_request_exclusion"
	GateFailurePinnedAccount       GateFailureReason = "pinned_account"
	GateFailureScoredBand          GateFailureReason = "scored_band"
	GateFailureSlotCapacity        GateFailureReason = "slot_capacity"
	GateFailureSlotManager         GateFailureReason = "slot_manager"
)

type Gate interface {
	Allow(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error)
}

const (
	HealthStateActive      = "active"
	HealthStateDegraded    = "degraded"
	HealthStateCoolingDown = "cooling_down"
	HealthStateRamping     = "ramping"
	HealthStateDisabled    = "disabled"
	HealthStatePaused      = "manual_paused"
)

type HealthStatus struct {
	State        string
	RampStagePct int
}

type HealthStatusGate interface {
	HealthStatus(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (HealthStatus, error)
}

type TenantGate interface{ Gate }
type LifecycleGate interface{ Gate }
type ChannelGate interface{ Gate }
type ProtocolFamilyGate interface{ Gate }
type ModelSupportGate interface{ Gate }
type CapabilityGate interface{ Gate }
type CredentialGate interface{ Gate }
type HealthGate interface{ Gate }
type GroupPolicyGate interface{ Gate }
type ExclusionGate interface{ Gate }
type PinnedAccountGate interface{ Gate }
type WindowCostGateIface interface{ Gate }
type SessionCountGateIface interface{ Gate }

type GateChain struct {
	Tenant        TenantGate
	Lifecycle     LifecycleGate
	Channel       ChannelGate
	Protocol      ProtocolFamilyGate
	Model         ModelSupportGate
	Capability    CapabilityGate
	Credential    CredentialGate
	Health        HealthGate
	GroupPolicy   GroupPolicyGate
	Exclusion     ExclusionGate
	Pinned        PinnedAccountGate
	WindowCost    WindowCostGateIface
	SessionCount  SessionCountGateIface
	ContextWindow ContextWindowGateIface
	RatePrecheck  RatePrecheckGateIface
}

func DefaultGateChain() GateChain {
	g := AllowAllGate{}
	return GateChain{
		Tenant: g, Lifecycle: g, Channel: g, Protocol: protocolFamilyGate{}, Model: modelRateLimitGate{}, Capability: g,
		Credential: g, Health: ProviderAccountHealthGate{}, GroupPolicy: g, Exclusion: exclusionGate{}, Pinned: pinnedAccountGate{},
		// WindowCost 默认为 nil;由 WithWindowCostGate 设置。nil == AllowAll(fail-open)。
		WindowCost: WindowCostGate{},
		// SessionCount 默认为 nil registry;fail-open。
		SessionCount: SessionCountGate{},
		// ContextWindow 零值即 fail-open(除非同时满足
		// ModelContextWindow>0 且 EstimatedInputTokens>0 且发生溢出,否则放行)。
		ContextWindow: ContextWindowGate{},
		// RatePrecheck 默认是一个 nil-counter 的 gate(fail-open);由 wiring
		// 层注入 precheck.Counter 以激活 ROUTE-121。
		RatePrecheck: RatePrecheckGate{},
	}
}

func (c GateChain) Allow(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	for _, gate := range c.ordered() {
		ok, reason, err := gate.gate.Allow(ctx, account, req)
		if err != nil || !ok {
			return ok, gate.reason(reason), err
		}
	}
	return true, "", nil
}

// SelectionGatePreparer 由"决策只依赖 SelectionRequest、与候选账号无关"的 gate 实现:
// 允许其在一次 Select 内只做一次准备(典型是查库), 返回一个本次 Select 专用、
// 后续 Allow 不再查库的 gate。未实现该接口的 gate 在 ForSelection 中原样保留。
type SelectionGatePreparer interface {
	PrepareForSelection(ctx context.Context, req SelectionRequest) Gate
}

// ForSelection 返回本次 Select 专用的 GateChain: 先把 nil 槽补成默认 gate, 再对
// 实现了 SelectionGatePreparer 的 gate 调一次 PrepareForSelection, 用其返回的
// prepared gate 替换该槽; 其余 gate 原样保留。
//
// 值接收 + 返回局部副本, 绝不修改接收者(selector 实例上的 gates 字段), 因此多
// goroutine 并发 Select 各自持有自己的局部链, 无竞态。AllowAllGate 等不实现
// preparer 的 gate → 链不变 → 行为保持(接线前 ForSelection 是恒等变换)。
func (c GateChain) ForSelection(ctx context.Context, req SelectionRequest) GateChain {
	c = c.withDefaults()
	c.Tenant = prepareGate(ctx, c.Tenant, req)
	c.Lifecycle = prepareGate(ctx, c.Lifecycle, req)
	c.Channel = prepareGate(ctx, c.Channel, req)
	c.Model = prepareGate(ctx, c.Model, req)
	c.Capability = prepareGate(ctx, c.Capability, req)
	c.Credential = prepareGate(ctx, c.Credential, req)
	c.Health = prepareGate(ctx, c.Health, req)
	c.GroupPolicy = prepareGate(ctx, c.GroupPolicy, req)
	c.Exclusion = prepareGate(ctx, c.Exclusion, req)
	c.Pinned = prepareGate(ctx, c.Pinned, req)
	c.WindowCost = prepareGate(ctx, c.WindowCost, req)
	c.SessionCount = prepareGate(ctx, c.SessionCount, req)
	c.ContextWindow = prepareGate(ctx, c.ContextWindow, req)
	c.RatePrecheck = prepareGate(ctx, c.RatePrecheck, req)
	return c
}

// prepareGate: g 实现 SelectionGatePreparer 则返回其准备后的 gate, 否则原样返回。
func prepareGate(ctx context.Context, g Gate, req SelectionRequest) Gate {
	if p, ok := g.(SelectionGatePreparer); ok {
		return p.PrepareForSelection(ctx, req)
	}
	return g
}

// withDefaults 把 nil 槽补成 DefaultGateChain 的默认 gate, 返回补齐后的副本(值接收, 不改原链)。
func (c GateChain) withDefaults() GateChain {
	d := DefaultGateChain()
	if c.Tenant == nil {
		c.Tenant = d.Tenant
	}
	if c.Lifecycle == nil {
		c.Lifecycle = d.Lifecycle
	}
	if c.Channel == nil {
		c.Channel = d.Channel
	}
	if c.Protocol == nil {
		c.Protocol = d.Protocol
	}
	if c.Model == nil {
		c.Model = d.Model
	}
	if c.Capability == nil {
		c.Capability = d.Capability
	}
	if c.Credential == nil {
		c.Credential = d.Credential
	}
	if c.Health == nil {
		c.Health = d.Health
	}
	if c.GroupPolicy == nil {
		c.GroupPolicy = d.GroupPolicy
	}
	if c.Exclusion == nil {
		c.Exclusion = d.Exclusion
	}
	if c.Pinned == nil {
		c.Pinned = d.Pinned
	}
	if c.WindowCost == nil {
		c.WindowCost = d.WindowCost
	}
	if c.SessionCount == nil {
		c.SessionCount = d.SessionCount
	}
	if c.ContextWindow == nil {
		c.ContextWindow = d.ContextWindow
	}
	if c.RatePrecheck == nil {
		c.RatePrecheck = d.RatePrecheck
	}
	return c
}

func (c GateChain) ordered() []namedGate {
	c = c.withDefaults()
	return []namedGate{
		{c.Tenant, GateFailureTenantFilter},
		{c.Lifecycle, GateFailureLifecycle},
		{c.Channel, GateFailureChannel},
		{c.Protocol, GateFailureProtocolFamily},
		{c.Model, GateFailureModel},
		{c.Capability, GateFailureCapability},
		{c.Credential, GateFailureCredential},
		{c.Health, GateFailureHealth},
		{c.GroupPolicy, GateFailureGroupPolicy},
		{c.Exclusion, GateFailurePerRequestExclusion},
		{c.Pinned, GateFailurePinnedAccount},
		{c.WindowCost, GateFailureWindowCost},
		{c.SessionCount, GateFailureSessionCount},
		{c.ContextWindow, GateFailureContextWindow},
		{c.RatePrecheck, GateFailureRatePrecheck},
	}
}

type namedGate struct {
	gate     Gate
	fallback GateFailureReason
}

func (g namedGate) reason(reason GateFailureReason) GateFailureReason {
	if reason != "" {
		return reason
	}
	return g.fallback
}

type AllowAllGate struct{}

func (AllowAllGate) Allow(context.Context, *AccountSnapshot, SelectionRequest) (bool, GateFailureReason, error) {
	return true, "", nil
}

type protocolFamilyGate struct{}

func (protocolFamilyGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if req.ProtocolFamily == "" {
		return true, "", nil
	}
	if account == nil || account.ProtocolFamily != req.ProtocolFamily {
		return false, GateFailureProtocolFamily, nil
	}
	return true, "", nil
}

type modelRateLimitGate struct {
	Now func() time.Time
}

func (g modelRateLimitGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return false, GateFailureModel, nil
	}
	key := req.ModelCooldownKey
	if key == "" {
		key = req.RequestedModel
	}
	if key == "" || len(account.ModelRateLimits) == 0 {
		return true, "", nil
	}
	limit, ok := account.ModelRateLimits[key]
	if !ok || limit.RateLimitResetAt.IsZero() {
		return true, "", nil
	}
	if limit.RateLimitResetAt.After(g.now()) {
		return false, GateFailureModelCooldown, nil
	}
	return true, "", nil
}

func (g modelRateLimitGate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

type ProviderAccountHealthGate struct {
	Now func() time.Time
}

func (g ProviderAccountHealthGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return false, GateFailureHealth, nil
	}
	// TOKLIFE-02:operator 的紧急豁免口 —— 对打了标记的账号跳过 health/cooldown 检查。
	// 仅当 disable_cooling=TRUE 时生效;默认 false,完全保持原有行为。
	if account.DisableCooling {
		return true, "", nil
	}
	if ProviderAccountHealthEligible(account.HealthState, account.HealthStateUntil, g.now()) {
		return true, "", nil
	}
	return false, GateFailureHealth, nil
}

func (g ProviderAccountHealthGate) HealthStatus(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (HealthStatus, error) {
	if account == nil {
		return HealthStatus{State: HealthStateDisabled}, nil
	}
	state := account.HealthState
	if ProviderAccountHealthEligible(state, account.HealthStateUntil, g.now()) {
		return HealthStatus{State: HealthStateActive}, nil
	}
	switch state {
	case "throttled", "cooldown":
		return HealthStatus{State: HealthStateCoolingDown}, nil
	case "revoked":
		return HealthStatus{State: HealthStateDisabled}, nil
	default:
		return HealthStatus{State: HealthStateDisabled}, nil
	}
}

func (g ProviderAccountHealthGate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// ProviderAccountHealthEligible 是 provider_accounts 健康字段的统一时间判定。
// selector 与只读管理诊断共用该函数，避免过期冷却在两个视图中得出相反结论。
func ProviderAccountHealthEligible(state string, until time.Time, now time.Time) bool {
	switch state {
	case "", "healthy":
		return true
	case "throttled", "revoked", "cooldown":
		return !until.IsZero() && !until.After(now)
	default:
		return false
	}
}

type exclusionGate struct{}

func (exclusionGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account != nil && req.ExcludedAccounts != nil {
		if _, ok := req.ExcludedAccounts[account.ID]; ok {
			return false, GateFailurePerRequestExclusion, nil
		}
	}
	return true, "", nil
}

type pinnedAccountGate struct{}

func (pinnedAccountGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if req.PinnedAccountID == 0 {
		return true, "", nil
	}
	if account == nil || account.ID != req.PinnedAccountID {
		return false, GateFailurePinnedAccount, nil
	}
	return true, "", nil
}
