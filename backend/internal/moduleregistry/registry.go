package moduleregistry

import (
	"context"
	"sort"
	"sync"
	"time"
)

// DefaultProbeTimeout bounds each health probe inside Snapshot. A probe that
// does not return within this window is reported as StatusUnknown ("could not
// determine") rather than being allowed to stall the operator view.
const DefaultProbeTimeout = 750 * time.Millisecond

// Registry is the thread-safe, in-process module-knowledge spine. It is
// constructed once in wiring and modules register into it during runtime build.
type Registry struct {
	mu      sync.RWMutex
	byID    map[string]ModuleDescriptor
	timeout time.Duration
}

// New returns an empty Registry using DefaultProbeTimeout. It delegates to
// NewWithProbeTimeout so the configurable constructor stays on the live path.
func New() *Registry {
	return NewWithProbeTimeout(DefaultProbeTimeout)
}

// NewWithProbeTimeout returns an empty Registry with a custom per-probe timeout.
// A non-positive timeout falls back to DefaultProbeTimeout. This is the single
// underlying constructor (New wraps it with the default), so future wiring can
// tune the probe budget without a new constructor.
func NewWithProbeTimeout(d time.Duration) *Registry {
	if d <= 0 {
		d = DefaultProbeTimeout
	}
	return &Registry{
		byID:    make(map[string]ModuleDescriptor),
		timeout: d,
	}
}

// Register adds (or replaces) a descriptor.
//
// Dup-ID policy: LAST-WINS, idempotent. Re-registering the same ID overwrites
// the prior descriptor and returns nil. This is deliberate — wiring runs once
// per process, and a last-wins contract keeps a re-run / test re-init from
// erroring while still being deterministic. An empty ID is the only rejected
// input (returns ErrEmptyID) because an unaddressable module is a programming
// error, not a legitimate overwrite.
func (r *Registry) Register(d ModuleDescriptor) error {
	if d.ID == "" {
		return ErrEmptyID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[d.ID] = d
	return nil
}

// Get returns the descriptor for id and whether it was found.
func (r *Registry) Get(id string) (ModuleDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.byID[id]
	return d, ok
}

// List returns all descriptors sorted by ID (stable order for deterministic
// operator views and tests).
func (r *Registry) List() []ModuleDescriptor {
	r.mu.RLock()
	out := make([]ModuleDescriptor, 0, len(r.byID))
	for _, d := range r.byID {
		out = append(out, d)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListByCategory returns descriptors whose Category equals cat, sorted by ID.
func (r *Registry) ListByCategory(cat string) []ModuleDescriptor {
	all := r.List()
	out := make([]ModuleDescriptor, 0, len(all))
	for _, d := range all {
		if d.Category == cat {
			out = append(out, d)
		}
	}
	return out
}

// ModuleSnapshot is one module's descriptor merged with its live probe result.
type ModuleSnapshot struct {
	Descriptor ModuleDescriptor `json:"descriptor"`
	Probe      ProbeResult      `json:"probe"`
}

// Snapshot runs every registered module's health probe CONCURRENTLY, bounded by
// the registry's per-probe timeout, and returns the merged view sorted by ID.
//
// Guarantees:
//   - A module with no probe yields StatusUnknown (no probe != error).
//   - A probe that exceeds the timeout yields StatusUnknown — the snapshot does
//     not wait for it; the goroutine is abandoned (it observes ctx cancellation)
//     so a single slow probe cannot stall the whole operator view.
//   - A probe that panics is recovered into StatusError (one bad probe can't
//     crash the operator path).
//
// Snapshot honors the caller's ctx: if ctx is cancelled, probes still in flight
// resolve to StatusUnknown and the function returns promptly.
func (r *Registry) Snapshot(ctx context.Context) []ModuleSnapshot {
	descs := r.List()
	out := make([]ModuleSnapshot, len(descs))

	var wg sync.WaitGroup
	for i := range descs {
		d := descs[i]
		out[i] = ModuleSnapshot{Descriptor: d, Probe: ProbeResult{Status: StatusUnknown}}
		if d.HealthProbe == nil {
			continue // no probe → leave as unknown
		}
		wg.Add(1)
		go func(idx int, probe HealthProbe) {
			defer wg.Done()
			out[idx].Probe = runProbe(ctx, probe, r.timeout)
		}(i, d.HealthProbe)
	}
	wg.Wait()
	return out
}

// runProbe executes a single probe with a per-probe timeout. It returns the
// probe's result if it completes in time, StatusUnknown if it times out or ctx
// is cancelled first, and StatusError if the probe panics. The probe runs in its
// own goroutine so a hung probe never blocks the bounded wait — the goroutine is
// left to observe the derived ctx's cancellation and exit on its own.
func runProbe(parent context.Context, probe HealthProbe, timeout time.Duration) ProbeResult {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	resCh := make(chan ProbeResult, 1) // buffered: a late probe send never blocks
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				resCh <- ProbeResult{Status: StatusError, Detail: "probe panic"}
			}
		}()
		resCh <- probe(ctx)
	}()

	select {
	case res := <-resCh:
		if res.Status == "" {
			res.Status = StatusUnknown
		}
		return res
	case <-ctx.Done():
		// Timed out (or parent cancelled): we do NOT wait for the probe. Report
		// "unknown" — we could not determine — never "error", and never a hang.
		return ProbeResult{Status: StatusUnknown, Detail: "probe timeout"}
	}
}
