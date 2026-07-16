package bindingfallback

import "testing"

func TestCoordinatorRequiresExhaustionAndExactTarget(t *testing.T) {
	var c Coordinator
	first := c.Observe(FailureObservation{
		Signal: SignalBindingConcurrencyLimit, MorePrimary: true, TargetConfigured: true,
		LocalSafetyPassed: true,
	})
	if first.Action != ActionContinuePrimary {
		t.Fatalf("首个 normal 失败后的动作=%d，期望继续 normal", first.Action)
	}
	last := c.Observe(FailureObservation{
		Signal: SignalPoolCapacityExhausted, TargetConfigured: true, LocalSafetyPassed: true,
	})
	if last.Action != ActionTransition || last.Transition.To != ClassQuota {
		t.Fatalf("normal 耗尽后的决策=%+v，期望转移 quota", last)
	}
	if again := c.Observe(FailureObservation{
		Signal: SignalPoolCapacityExhausted, TargetConfigured: true, LocalSafetyPassed: true,
	}); again.Action != ActionStop {
		t.Fatalf("第二次转移动作=%d，期望终止", again.Action)
	}
}

func TestCoordinatorMixedOrDeliveredFailureStops(t *testing.T) {
	var mixed Coordinator
	if got := mixed.Observe(FailureObservation{
		Signal: SignalPoolCapacityExhausted, MorePrimary: true, TargetConfigured: true,
		LocalSafetyPassed: true,
	}); got.Action != ActionContinuePrimary {
		t.Fatalf("quota 首次失败动作=%d，期望继续", got.Action)
	}
	if got := mixed.Observe(FailureObservation{
		Signal: SignalUpstreamServerError, RetryPermitted: true, TargetConfigured: true,
		LocalSafetyPassed: true,
	}); got.Action != ActionStop {
		t.Fatalf("混合目标动作=%d，期望终止", got.Action)
	}

	var delivered Coordinator
	if got := delivered.Observe(FailureObservation{
		Signal: SignalUpstreamServerError, RetryPermitted: true, TargetConfigured: true,
		LocalSafetyPassed: true, DeliveryStarted: true,
	}); got.Action != ActionStop {
		t.Fatalf("已交付动作=%d，期望终止", got.Action)
	}
}

func TestAnnotateRoutingReasonRecordsStableEnums(t *testing.T) {
	raw := AnnotateRoutingReason([]byte(`{"reason":"test"}`), Transition{
		From: ClassNormal, To: ClassManual, Trigger: SignalConnectTimeout,
	})
	want := `"class_transition":{"from":"normal","to":"manual","trigger":"connect_timeout"}`
	if !containsJSONFragment(string(raw), want) {
		t.Fatalf("路由原因=%s，缺少 %s", raw, want)
	}
}

func containsJSONFragment(raw, fragment string) bool {
	for i := 0; i+len(fragment) <= len(raw); i++ {
		if raw[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
