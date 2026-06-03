// Package moderation owns the content-moderation screener and its stores.
//
// Invariants:
//   - It is not wired into request dispatch in slice 1.
//   - Screeners may inspect an in-memory request body, but audit events store
//     only payload hashes and match metadata.
//   - Fail-closed behavior is explicit per tenant config.
//   - Upstream platform-policy classification remains owned by internal/gateway.
package moderation
