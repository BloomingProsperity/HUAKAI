package pool

import (
	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type Selector = router.Selector
type SelectionRequest = router.SelectionRequest
type SelectionResult = router.SelectionResult
type WaitPlan = router.WaitPlan

type AccountSnapshot = router.AccountSnapshot
type RoutingPolicy = router.RoutingPolicy
type AccountSource = router.AccountSource
type RoutingPolicySource = router.RoutingPolicySource
type StickyStore = router.StickyStore
type ClaimGate = router.ClaimGate

type GateFailureReason = router.GateFailureReason

const (
	GateFailureTenantFilter        = router.GateFailureTenantFilter
	GateFailureLifecycle           = router.GateFailureLifecycle
	GateFailureChannel             = router.GateFailureChannel
	GateFailureProtocolFamily      = router.GateFailureProtocolFamily
	GateFailureModel               = router.GateFailureModel
	GateFailureCapability          = router.GateFailureCapability
	GateFailureCredential          = router.GateFailureCredential
	GateFailureHealth              = router.GateFailureHealth
	GateFailureGroupPolicy         = router.GateFailureGroupPolicy
	GateFailurePerRequestExclusion = router.GateFailurePerRequestExclusion
	GateFailureScoredBand          = router.GateFailureScoredBand
	GateFailureWindowCost          = router.GateFailureWindowCost
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

type RoutingLayer = router.RoutingLayer

const (
	RoutingLayerRoutingAffinity   = router.RoutingLayerRoutingAffinity
	RoutingLayerStickyWithinRoute = router.RoutingLayerStickyWithinRoute
	RoutingLayerStickyStandalone  = router.RoutingLayerStickyStandalone
	RoutingLayerFresh             = router.RoutingLayerFresh
	RoutingLayerForced            = router.RoutingLayerForced
	RoutingLayerFallbackQueue     = router.RoutingLayerFallbackQueue
)

type RoutingReasonWaitAction = router.RoutingReasonWaitAction
type RoutingReasonExclusionItem = router.RoutingReasonExclusionItem
type RoutingReasonBuilder = router.RoutingReasonBuilder

var (
	ErrNoEligibleAccount      = router.ErrNoEligibleAccount
	ErrAllChannelsDegraded    = router.ErrAllChannelsDegraded
	ErrClaimRace              = router.ErrClaimRace
	ErrSlotManagerUnavailable = router.ErrSlotManagerUnavailable
	ErrNoSlotAvailable        = router.ErrNoSlotAvailable
	ErrPASRPreMutationFail    = router.ErrPASRPreMutationFail
	ErrPASRPostMutationFail   = router.ErrPASRPostMutationFail
)
