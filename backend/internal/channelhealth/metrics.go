package channelhealth

import (
	"expvar"
	"sync"
)

// providerHealthMetrics holds expvar counters for provider health state transitions.
// The map is named "provider_health"; keys are "error_total" and "degraded_total".
// These counters are incremented on real state-machine transitions and exposed to
// the OTel bridge via otelbridge.bridgeCounters().
var (
	providerHealthOnce    sync.Once
	providerHealthMetrics *expvar.Map
)

func getProviderHealthMetrics() *expvar.Map {
	providerHealthOnce.Do(func() {
		m := expvar.NewMap("provider_health")
		m.Add("error_total", 0)
		m.Add("degraded_total", 0)
		providerHealthMetrics = m
	})
	return providerHealthMetrics
}

// incProviderError increments the error_total counter. Called on transitions to
// StateCoolingDown, StateDisabled, or StateManualPaused caused by an error decision.
func incProviderError() {
	getProviderHealthMetrics().Add("error_total", 1)
}

// incProviderDegraded increments the degraded_total counter. Called on transitions
// to StateDegraded.
func incProviderDegraded() {
	getProviderHealthMetrics().Add("degraded_total", 1)
}
