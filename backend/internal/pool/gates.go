package pool

import "context"

type GateFailureReason string

const (
	GateFailureTenantFilter        GateFailureReason = "tenant_filter"
	GateFailureLifecycle           GateFailureReason = "lifecycle"
	GateFailureChannel             GateFailureReason = "channel"
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
		Tenant: g, Lifecycle: g, Channel: g, Model: g, Capability: g,
		Credential: g, Health: g, GroupPolicy: g, Exclusion: exclusionGate{},
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

type exclusionGate struct{}

func (exclusionGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account != nil && req.ExcludedAccounts != nil {
		if _, ok := req.ExcludedAccounts[account.ID]; ok {
			return false, GateFailurePerRequestExclusion, nil
		}
	}
	return true, "", nil
}
