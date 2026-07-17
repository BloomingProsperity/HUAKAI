package executor

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// NormalBudget 归一化 Router 的 normal attempt 上限。
func NormalBudget(plan router.RoutePlan) int {
	budget := plan.AttemptBudget
	if budget <= 0 || budget > len(plan.Attempts) {
		budget = len(plan.Attempts)
	}
	return budget
}

// ObserveFailure 把协议失败交给共享 Coordinator，并在获准时返回精确目标 phase。
func ObserveFailure(coordinator *bindingfallback.Coordinator, failure *Failure, plan router.RoutePlan, morePrimary, deliveryStarted, localSafetyPassed bool) (bindingfallback.StepDecision, router.FallbackPhasePlan) {
	if failure == nil {
		return bindingfallback.StepDecision{Action: bindingfallback.ActionStop}, router.FallbackPhasePlan{}
	}
	target, targetKnown := bindingfallback.TargetClass(failure.Signal)
	_, targetConfigured := router.FallbackPhaseForClass(plan, target)
	targetConfigured = targetKnown && targetConfigured
	decision := coordinator.Observe(bindingfallback.FailureObservation{
		Signal:            failure.Signal,
		RetryPermitted:    failure.RetryPermitted,
		MorePrimary:       morePrimary,
		TargetConfigured:  targetConfigured,
		LocalSafetyPassed: localSafetyPassed,
		DeliveryStarted:   deliveryStarted,
	})
	if decision.Action != bindingfallback.ActionTransition {
		return decision, router.FallbackPhasePlan{}
	}
	phase, ok := router.FallbackPhaseForClass(plan, decision.Transition.To)
	if !ok {
		return bindingfallback.StepDecision{Action: bindingfallback.ActionStop}, router.FallbackPhasePlan{}
	}
	return decision, phase
}
