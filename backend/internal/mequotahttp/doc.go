// Package mequotahttp exposes the authenticated user's read-only quota status.
//
// The read returns the window-shaped quota dimensions — requests, cost_usd, and
// tokens_estimated — each as its own window with cap/consumed/remaining, tagged by
// metric. Concurrency is intentionally excluded: it is a slot-based metric, not a
// window-accumulation counter, so it has no window row to project; a concurrency
// status read is tracked as a separate follow-up.
package mequotahttp
