package router

import (
	"context"
	"time"
)

type GateFailureReason string

const (
	GateFailureTenantFilter        GateFailureReason = "tenant_filter"
	GateFailureLifecycle           GateFailureReason = "lifecycle"
	GateFailureChannel             GateFailureReason = "channel"
	GateFailureProtocolFamily      GateFailureReason = "protocol_family"
	GateFailureModel               GateFailureReason = "model"
	GateFailureCapability          GateFailureReason = "capability"
	GateFailureCredential          GateFailureReason = "credential"
	GateFailureHealth              GateFailureReason = "health"
	GateFailureGroupPolicy         GateFailureReason = "group_policy"
	GateFailurePerRequestExclusion GateFailureReason = "per_request_exclusion"
	GateFailureScoredBand          GateFailureReason = "scored_band"
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

type GateChain struct {
	Tenant      TenantGate
	Lifecycle   LifecycleGate
	Channel     ChannelGate
	Protocol    ProtocolFamilyGate
	Model       ModelSupportGate
	Capability  CapabilityGate
	Credential  CredentialGate
	Health      HealthGate
	GroupPolicy GroupPolicyGate
	Exclusion   ExclusionGate
}

func DefaultGateChain() GateChain {
	g := AllowAllGate{}
	return GateChain{
		Tenant: g, Lifecycle: g, Channel: g, Protocol: protocolFamilyGate{}, Model: modelRateLimitGate{}, Capability: g,
		Credential: g, Health: ProviderAccountHealthGate{}, GroupPolicy: g, Exclusion: exclusionGate{},
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

func (c GateChain) ordered() []namedGate {
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
		return false, GateFailureModel, nil
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
	if providerAccountHealthEligible(account.HealthState, account.HealthStateUntil, g.now()) {
		return true, "", nil
	}
	return false, GateFailureHealth, nil
}

func (g ProviderAccountHealthGate) HealthStatus(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (HealthStatus, error) {
	if account == nil {
		return HealthStatus{State: HealthStateDisabled}, nil
	}
	state := account.HealthState
	if providerAccountHealthEligible(state, account.HealthStateUntil, g.now()) {
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

func providerAccountHealthEligible(state string, until time.Time, now time.Time) bool {
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
