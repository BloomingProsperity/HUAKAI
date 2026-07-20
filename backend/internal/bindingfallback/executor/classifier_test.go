package executor

import (
	"errors"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestSignalFromUpstreamUsesMachineCodesNotBroadStatus(t *testing.T) {
	decision := gateway.AttemptRetryDecision{RetryableBeforeDelivery: true}
	classification := gateway.Classification{}
	tests := []struct {
		name   string
		status int
		body   string
		want   bindingfallback.Signal
	}{
		{name: "精确上下文", status: http.StatusBadRequest, body: `{"error":{"code":"context_length_exceeded"}}`, want: bindingfallback.SignalUpstreamContextWindow},
		{name: "普通 413", status: http.StatusRequestEntityTooLarge, body: `{"error":{"message":"body too large"}}`, want: bindingfallback.SignalLocalConfigurationFailure},
		{name: "精确安全", status: http.StatusForbidden, body: `{"error":{"type":"content_policy_violation"}}`, want: bindingfallback.SignalUpstreamContentPolicy},
		{name: "普通 403", status: http.StatusForbidden, body: `{"error":{"message":"forbidden"}}`, want: bindingfallback.SignalLocalConfigurationFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SignalFromUpstream(tt.status, []byte(tt.body), classification, decision); got != tt.want {
				t.Fatalf("信号=%q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestObserveFailureReturnsOnlyExactConfiguredPhase(t *testing.T) {
	plan := router.RoutePlan{
		Attempts: []router.AttemptPlan{{PoolGroupID: 1}}, AttemptBudget: 1,
		FallbackPhases: []router.FallbackPhasePlan{{
			FallbackClass: bindingfallback.ClassQuota,
			Attempts:      []router.AttemptPlan{{PoolGroupID: 2, FallbackClass: bindingfallback.ClassQuota}},
			AttemptBudget: 1,
		}},
	}
	var coordinator bindingfallback.Coordinator
	decision, phase := ObserveFailure(&coordinator, PoolFailure(errors.New("unknown")), plan, false, false, true)
	if decision.Action != bindingfallback.ActionStop || len(phase.Attempts) != 0 {
		t.Fatalf("未知本地失败决策=%+v phase=%+v，期望终止", decision, phase)
	}

	coordinator = bindingfallback.Coordinator{}
	decision, phase = ObserveFailure(&coordinator, PoolFailure(pool.ErrBindingConcurrencyLimited), plan, false, false, true)
	if decision.Action != bindingfallback.ActionTransition || phase.FallbackClass != bindingfallback.ClassQuota {
		t.Fatalf("binding 并发决策=%+v phase=%+v，期望 quota", decision, phase)
	}
}

func TestPoolFailureGroupPolicyUnavailableIsTerminal503(t *testing.T) {
	failure := PoolFailure(errors.Join(errors.New("db unavailable"), pool.ErrGroupPolicyUnavailable))
	if failure.Status != http.StatusServiceUnavailable || failure.Code != "group_policy_unavailable" {
		t.Fatalf("failure=%+v want dedicated 503 group_policy_unavailable", failure)
	}
	if failure.RetryPermitted || !bindingfallback.IsTerminal(failure.Signal) {
		t.Fatalf("策略真相未知不得重试到其它池: %+v", failure)
	}
}

func TestDispatchFailureDistinguishesTransientAndLocal(t *testing.T) {
	transient := DispatchFailure(errors.New("dial tcp: connection refused"))
	if transient.Signal != bindingfallback.SignalTransientConnectionFailure || !transient.RetryPermitted {
		t.Fatalf("瞬态失败=%+v", transient)
	}
	local := DispatchFailure(errors.New("unsupported local adapter"))
	if !bindingfallback.IsTerminal(local.Signal) || local.RetryPermitted {
		t.Fatalf("本地失败=%+v", local)
	}
}
