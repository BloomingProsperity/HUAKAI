package pool

import (
	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type Selector = router.Selector
type SelectionRequest = router.SelectionRequest
type SelectionResult = router.SelectionResult
type RateAccountingScope = router.RateAccountingScope

const (
	RateAccountingAll         = router.RateAccountingAll
	RateAccountingLogicalOnly = router.RateAccountingLogicalOnly
	RateAccountingAccountOnly = router.RateAccountingAccountOnly
)

// StickyState 别名(DM-07):让 gatewayhttp 等消费方不必直接 import pool/router。
type StickyState = router.StickyState

const (
	StickyStateNone = router.StickyStateNone
	StickyStateHit  = router.StickyStateHit
	StickyStateMiss = router.StickyStateMiss
)

type WaitPlan = router.WaitPlan

type AccountSnapshot = router.AccountSnapshot
type RoutingPolicy = router.RoutingPolicy
type AccountSource = router.AccountSource
type RoutingPolicySource = router.RoutingPolicySource
type StickyStore = router.StickyStore
type ClaimGate = router.ClaimGate
type BindingConcurrencyReader = router.BindingConcurrencyReader

// SelectionMode 与其常量 re-export,供 cmd/gateway 装配生产 RoutingPolicySource 时
// 按 binding selection_mode 返回对应策略,无需直引 pool/router 内部包。
type SelectionMode = router.SelectionMode

const (
	SelectionModeStrictPriority   = router.SelectionModeStrictPriority
	SelectionModePriorityWeighted = router.SelectionModePriorityWeighted
)

type GateFailureReason = router.GateFailureReason
type ExhaustionFamily = router.ExhaustionFamily
type Exhaustion = router.Exhaustion

const (
	GateFailureTenantFilter        = router.GateFailureTenantFilter
	GateFailureLifecycle           = router.GateFailureLifecycle
	GateFailureChannel             = router.GateFailureChannel
	GateFailureProtocolFamily      = router.GateFailureProtocolFamily
	GateFailureModel               = router.GateFailureModel
	GateFailureModelCooldown       = router.GateFailureModelCooldown
	GateFailureCapability          = router.GateFailureCapability
	GateFailureCredential          = router.GateFailureCredential
	GateFailureHealth              = router.GateFailureHealth
	GateFailureAuthCooldown        = router.GateFailureAuthCooldown
	GateFailureGroupPolicy         = router.GateFailureGroupPolicy
	GateFailurePerRequestExclusion = router.GateFailurePerRequestExclusion
	GateFailureScoredBand          = router.GateFailureScoredBand
	GateFailureWindowCost          = router.GateFailureWindowCost
	GateFailureContextWindow       = router.GateFailureContextWindow
	GateFailureRatePrecheck        = router.GateFailureRatePrecheck
	GateFailureSlotCapacity        = router.GateFailureSlotCapacity
	GateFailureSlotManager         = router.GateFailureSlotManager
)

const (
	ExhaustionFamilyUnknown        = router.ExhaustionFamilyUnknown
	ExhaustionFamilyCapacity       = router.ExhaustionFamilyCapacity
	ExhaustionFamilyStaticMismatch = router.ExhaustionFamilyStaticMismatch
	ExhaustionFamilyContextWindow  = router.ExhaustionFamilyContextWindow
	ExhaustionFamilyMixed          = router.ExhaustionFamilyMixed
)

type Gate = router.Gate
type HealthStatus = router.HealthStatus
type HealthStatusGate = router.HealthStatusGate
type TenantGate = router.TenantGate
type LifecycleGate = router.LifecycleGate
type ChannelGate = router.ChannelGate
type ProtocolFamilyGate = router.ProtocolFamilyGate
type ModelSupportGate = router.ModelSupportGate
type CapabilityGate = router.CapabilityGate
type CredentialGate = router.CredentialGate
type HealthGate = router.HealthGate
type GroupPolicyGate = router.GroupPolicyGate
type ExclusionGate = router.ExclusionGate
type GateChain = router.GateChain
type AllowAllGate = router.AllowAllGate
type WindowCostGate = router.WindowCostGate
type SessionCountGate = router.SessionCountGate
type ContextWindowGate = router.ContextWindowGate
type RatePrecheckGate = router.RatePrecheckGate

const (
	HealthStateActive      = router.HealthStateActive
	HealthStateDegraded    = router.HealthStateDegraded
	HealthStateCoolingDown = router.HealthStateCoolingDown
	HealthStateRamping     = router.HealthStateRamping
	HealthStateDisabled    = router.HealthStateDisabled
	HealthStatePaused      = router.HealthStatePaused
)

type SlotManager = router.SlotManager
type ReleaseFunc = router.ReleaseFunc
type AcquireResult = router.AcquireResult

// NoCapacityError 透传 router 的无容量错误类型,供 HTTP 层 errors.As 取最早恢复时刻算 Retry-After。
type NoCapacityError = router.NoCapacityError

var (
	ErrNoEligibleAccount         = router.ErrNoEligibleAccount
	ErrKeyRateLimited            = router.ErrKeyRateLimited
	ErrBindingRateLimited        = router.ErrBindingRateLimited
	ErrBindingConcurrencyLimited = router.ErrBindingConcurrencyLimited
	ErrAllChannelsDegraded       = router.ErrAllChannelsDegraded
	ErrGroupPolicyUnavailable    = router.ErrGroupPolicyUnavailable
	ErrClaimRace                 = router.ErrClaimRace
	ErrSlotManagerUnavailable    = router.ErrSlotManagerUnavailable
	ErrNoSlotAvailable           = router.ErrNoSlotAvailable
	ErrPASRPreMutationFail       = router.ErrPASRPreMutationFail
	ErrPASRPostMutationFail      = router.ErrPASRPostMutationFail
)
