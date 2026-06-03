package credentialacq

import "time"

const DefaultLongBootstrapTTL = 7 * 24 * time.Hour

func SelectBootstrapTTL(longLived bool) time.Duration {
	return SelectBootstrapTTLWithDurations(longLived, DefaultFlowTTL, DefaultLongBootstrapTTL)
}

func SelectBootstrapTTLWithDurations(longLived bool, shortTTL, longTTL time.Duration) time.Duration {
	if shortTTL <= 0 {
		shortTTL = DefaultFlowTTL
	}
	if longTTL <= 0 {
		longTTL = DefaultLongBootstrapTTL
	}
	if longLived {
		return longTTL
	}
	return shortTTL
}
