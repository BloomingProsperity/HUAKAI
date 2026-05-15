package cachemetrics

import (
	"expvar"
	"fmt"
	"sync"
)

const (
	StreamStateAcquired = "acquired"
	StreamStateInFlight = "inflight"
	StreamStatePartial  = "partial"
	StreamStateFailed   = "failed"
)

var (
	streamStateOnce     sync.Once
	streamStateCounters map[string]*expvar.Map
)

func initStreamStateCounters() {
	streamStateOnce.Do(func() {
		streamStateCounters = map[string]*expvar.Map{
			StreamStateAcquired: expvar.NewMap("huakai_stream_state_acquired_total"),
			StreamStateInFlight: expvar.NewMap("huakai_stream_state_inflight_total"),
			StreamStatePartial:  expvar.NewMap("huakai_stream_state_partial_total"),
			StreamStateFailed:   expvar.NewMap("huakai_stream_state_failed_total"),
		}
	})
}

func ObserveStreamState(state, vendor, model string) {
	initStreamStateCounters()
	counter := streamStateCounters[state]
	if counter == nil {
		counter = streamStateCounters[StreamStateFailed]
	}
	counter.Add(streamStateLabelKey(vendor, model), 1)
}

func SnapshotStreamState(state string) map[string]int64 {
	initStreamStateCounters()
	out := map[string]int64{}
	counter := streamStateCounters[state]
	if counter == nil {
		return out
	}
	counter.Do(func(kv expvar.KeyValue) {
		if v, ok := kv.Value.(*expvar.Int); ok {
			out[kv.Key] = v.Value()
		}
	})
	return out
}

func streamStateLabelKey(vendor, model string) string {
	if vendor == "" {
		vendor = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	return fmt.Sprintf("vendor=%s,model=%s", vendor, model)
}
