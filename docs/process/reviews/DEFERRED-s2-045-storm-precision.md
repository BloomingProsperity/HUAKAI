# DEFERRED — S2-045 storm-precision refinements (codex R2, both S2)

These two codex round-2 findings on the S2-045 three-scope storm wiring are **precision
refinements, not regressions**, and are deferred to a follow-up slice per CLAUDE.md §8
("review should not discover the spec drip-by-drip — close the current slice at no-S0/S1
and write the next-slice spec"). Both verified factually correct against the code.

Neither is a regression: before this slice the endpoint/global scopes did NOT throttle at
all; after it they throttle admissions. Both refinements only make an already-new protection
*more precise*. Reference parity: none of sub2api@91da8159 / CLIProxyAPI@21fad9db /
new-api@20d3e73 implement endpoint or global refresh budgets at all, so the shipped slice is
already parity-or-stronger; these refinements are beyond every reference.

## D1 — Charge storm budget per outbound refresh attempt (codex R2 P2)

`scheduler.go` consumes one endpoint/global token per `processAccount`, but
`refreshWithBackoff` may retry a retryable failure up to `maxAttempts` (default 3), and some
adapters retry internally. So one admission can issue several token-endpoint POSTs; under a
transient 429/5xx storm actual vendor traffic ≈ admissions × retry-multiplier.

- Why S2 not S1: strictly stronger than the prior zero-limiting state; multiplier is bounded
  (×maxAttempts) and backoff-spaced; the account concurrency scope still bounds parallelism.
  Not an auth/billing/data-loss regression.
- Correct fix (separate slice): move the endpoint/global acquire inside the retry loop so each
  outbound attempt is charged, and define mid-sequence-denial semantics (stop retrying + audit
  storm-exhausted). This changes retry behavior and needs its own discriminating tests.

## D2 — Per-(provider, endpoint) bucket key instead of per-vendor (codex R2 P2)

The acquire passes `endpointFingerprint=""`, so the bucket key is `vendor|` — per-vendor, not
per-(provider, endpoint). A vendor with multiple refresh/auth-mode endpoints shares one bucket.

- Why S2 not S1: per-vendor grouping is the *conservative* direction (throttles a vendor's
  endpoints together — more aggressive, never under-protects). The refresh row
  (`ListAccountsForRefreshRow`: id/tenant/provider_id/vendor_name/expires_at) carries no OAuth
  endpoint URL, so a true fingerprint needs additional data plumbing.
- Correct fix (separate slice): plumb a stable endpoint/auth-mode fingerprint to the acquire
  key once the endpoint URL is available at this layer.
