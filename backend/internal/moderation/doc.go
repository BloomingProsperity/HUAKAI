// Package moderation owns the content-moderation screener and its stores.
//
// Invariants:
//   - Chat dispatch may call the screener before billing reserve; nil wiring
//     remains a pass-through default.
//   - Screeners may inspect an in-memory request body, but audit events store
//     only payload hashes and match metadata.
//   - Fail-closed behavior is explicit per tenant config.
//   - Upstream platform-policy classification remains owned by internal/gateway.
package moderation
