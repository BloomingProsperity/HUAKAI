package channelhealth

import (
	"expvar"
	"sync"
)

// providerHealthMetrics 持有 provider 健康状态转换的 expvar 计数器。
// 该 map 命名为 "provider_health"；键为 "error_total" 与 "degraded_total"。
// 这些计数器在真实的状态机转换时递增，并经 otelbridge.bridgeCounters()
// 暴露给 OTel bridge。
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

// incProviderError 递增 error_total 计数器。在因错误决策而转入
// StateCoolingDown、StateDisabled 或 StateManualPaused 时调用。
func incProviderError() {
	getProviderHealthMetrics().Add("error_total", 1)
}

// incProviderDegraded 递增 degraded_total 计数器。在转入 StateDegraded 时调用。
func incProviderDegraded() {
	getProviderHealthMetrics().Add("degraded_total", 1)
}
