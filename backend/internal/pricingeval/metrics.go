package pricingeval

import "expvar"

const (
	signalFlatChargedTotal    = "flat_charged_total"
	signalTieredChargedTotal  = "tiered_charged_total"
	signalTieredFallbackTotal = "tiered_fallback_total"
)

var pricingSignals = expvar.NewMap("billing_pricing_eval")

func init() {
	pricingSignals.Add(signalFlatChargedTotal, 0)
	pricingSignals.Add(signalTieredChargedTotal, 0)
	pricingSignals.Add(signalTieredFallbackTotal, 0)
}

type SignalSnapshot struct {
	FlatChargedTotal    int64
	TieredChargedTotal  int64
	TieredFallbackTotal int64
}

func SnapshotSignals() SignalSnapshot {
	return SignalSnapshot{
		FlatChargedTotal:    signalValue(signalFlatChargedTotal),
		TieredChargedTotal:  signalValue(signalTieredChargedTotal),
		TieredFallbackTotal: signalValue(signalTieredFallbackTotal),
	}
}

func observeFlatCharged() {
	pricingSignals.Add(signalFlatChargedTotal, 1)
}

func observeTieredCharged() {
	pricingSignals.Add(signalTieredChargedTotal, 1)
}

func observeTieredFallback() {
	pricingSignals.Add(signalTieredFallbackTotal, 1)
}

func signalValue(key string) int64 {
	v, ok := pricingSignals.Get(key).(*expvar.Int)
	if !ok {
		return 0
	}
	return v.Value()
}
