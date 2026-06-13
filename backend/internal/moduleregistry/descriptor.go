// Package moduleregistry holds the in-process, runtime module-knowledge spine
// that the (admin-gated) ops assistant queries for fast root-cause: each HUAKAI
// subsystem registers a ModuleDescriptor describing its identity, capabilities,
// and an OPTIONAL read-only liveness probe.
//
// Boundaries (intentional, enforced by tests):
//   - ADDITIVE: this package is off every request hot path. Probes run only when
//     an operator asks for a Snapshot; nothing here is touched per-request.
//   - PRIVACY: probe results carry system-diagnostic enums/counts ONLY — never
//     secrets, tokens, or user data. ProbeResult.Detail is for operator triage,
//     not for echoing inputs. Callers must respect the privacy redaction
//     boundary that governs the rest of the gateway.
//   - NON-BLOCKING: a probe must never block startup and must never panic the
//     caller. Snapshot runs probes concurrently with a per-probe timeout; a slow
//     or erroring probe degrades to a status, it does not hang the snapshot.
package moduleregistry

import "context"

// ProbeStatus is the closed enum a HealthProbe may report. It is deliberately
// small so an operator (or assistant) can reason about it without parsing free
// text. Anything a probe cannot determine is "unknown", which is distinct from
// an actively failing "error".
type ProbeStatus string

const (
	// StatusOK — the module is wired and its cheap read-only check passed.
	StatusOK ProbeStatus = "ok"
	// StatusDegraded — wired but a non-fatal check is unhappy (e.g. pool empty).
	StatusDegraded ProbeStatus = "degraded"
	// StatusUnknown — no probe registered, or the probe timed out / was skipped.
	// Unknown is NOT an error: it means "we could not determine", which is the
	// correct, non-alarming state for a slow probe under timeout.
	StatusUnknown ProbeStatus = "unknown"
	// StatusError — the probe ran and reported an actual failure.
	StatusError ProbeStatus = "error"
)

// ProbeResult is the read-only outcome of a HealthProbe. Detail is a short,
// operator-facing diagnostic string built from enums/counts — never secrets or
// user data.
type ProbeResult struct {
	Status ProbeStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// HealthProbe is an OPTIONAL, read-only liveness check. It must be cheap and
// side-effect-free, must honor ctx cancellation/deadline, and must never panic.
// If a probe panics, Snapshot recovers it into a StatusError result so one bad
// probe cannot take down the operator view.
type HealthProbe func(ctx context.Context) ProbeResult

// ModuleDescriptor is the static identity of a HUAKAI subsystem plus an optional
// live probe. IDs are stable, dotted strings (e.g. "billing.service") so they
// survive refactors and can be referenced from docs, the static catalog, and the
// assistant's context.
type ModuleDescriptor struct {
	// ID is the stable identifier (dotted, lower-case). Required; Register
	// rejects an empty ID.
	ID string `json:"id"`
	// Category groups modules for operator filtering, e.g. "money-path",
	// "routing", "credentials", "observability".
	Category string `json:"category"`
	// Title is a short human-readable name.
	Title string `json:"title"`
	// Capabilities is a short list of what the module does, in operator terms.
	Capabilities []string `json:"capabilities,omitempty"`
	// HealthProbe is optional. When nil, Snapshot reports StatusUnknown for this
	// module (no probe is not an error).
	HealthProbe HealthProbe `json:"-"`
}
