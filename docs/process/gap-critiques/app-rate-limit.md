# Gap Critique: Application-Level Per-User / Per-Group Rate Limiting (F-RL-APP-001)

**Reviewer:** Adversarial Principal Review  
**Date:** 2026-06-03  
**Design file:** `docs/process/gap-designs/app-rate-limit.md`  
**Codebase snapshot:** `C:\HUAKAI\repo\backend` — migration max 0076 confirmed

---

## Verdict

**needs-work**

The design is structurally sound and the money-path and CMB invariants are largely
respected. However it contains one architecturally incorrect claim (the two-phase
RPM-before-body / TPM-inside-prepareRoute split) that cannot be implemented as
written, a wiring-guard gap that is acknowledged but unresolved, a post-settle
feedback path for `success_window.go` that is described but never mechanically
designed, and several missing discriminating tests covering retry, multi-endpoint,
and eviction paths. None are blocking security issues, but collectively they will
cause the implementer to produce either dead code or a materially different design
than what is reviewed here. Must-fix count: 7.

---

## Holes

### H-1 — Two-phase RPM/TPM split is architecturally incorrect

The design states:

> "The chat handler checks AppRateLimiter **after** `auth.Resolve` succeeds and
> **before** `validateChatCompletionsRequest`" for RPM, and "TPM is enforced after
> body parse (in `prepareRoute`)."

**Reality (from `chat_completions_handler.go`):**

```
Auth.Resolve(ctx, r)                   ← ident available
validateChatCompletionsRequest(w,r,ctx) ← reads + unmarshals body; chatRequest (incl. MaxTokens) available HERE
newChatExecution(...)
prepareRoute(w)                        ← registry lookup + router.Plan only; NO token estimation here
reserveClaim(w)                        ← ClaimGate.Reserve (Tx1)
```

`validateChatCompletionsRequest` reads the full body and populates `chatRequest`
including `MaxTokens` / `MaxCompletionTokens`. **Token estimation is available
immediately after this call.** `prepareRoute` does only model registry resolution
and router planning — it performs zero token estimation. There is no place in
`prepareRoute` to inject a TPM check with a meaningful `TokenEstimate`.

Consequence: the two-phase split (RPM pre-body, TPM in-prepareRoute) **cannot be
implemented as specified**. Both metrics are available at the same point: after
`validateChatCompletionsRequest`. The correct injection point for a unified
RPM+TPM check is between `validateChatCompletionsRequest` and `prepareRoute` (or
at the top of `prepareRoute` with `ex.req` available). The design must be revised
to reflect a single-phase gate call at the correct location.

### H-2 — `success_window.go` post-settle feedback path is undefined

The design states:

> "`success_window.go` tracks actual token counts post-settle (from
> `UsageRecordDraft`) for observe-mode accuracy metrics."

No mechanism is designed to deliver post-settle data to the in-process
`success_window`. After Tx2 settlement, actual usage is committed inside
`Settler.Settle` (called from `settleCompletion` / `settleCompletionWithRecovery`
in `chat_completions_billing.go`). The `SuccessWindow` in `userratelimit` has no
callback hook, no event-bus subscriber, and no direct dependency on `billing`.

Either:
- The window only ever receives pre-dispatch `TokenEstimate` (making the
  "actual token counts post-settle" claim false), or
- A post-settle callback must be designed and wired (which adds new
  cross-package dependencies not shown).

The design must choose one and specify the mechanism explicitly.

### H-3 — Gate not applied to `/v1/messages` and `/v1/responses`

`NewChatCompletionsHandler`, `NewMessagesHandler`, and `NewResponsesHandler` all
share `ChatHandlerDeps` and the same handler body (via `NewChatCompletionsHandler`
internally). The design's wiring section shows `AppRateLimiter` added to
`ChatHandlerDeps` and the gate called inside `NewChatCompletionsHandler`, which
means it **will** fire for all three endpoints — but the design never acknowledges
this. There are no tests covering the `/v1/messages` or `/v1/responses` paths,
no documentation of the shared-deps pattern, and no discussion of whether the same
rate limit should apply equally across all three endpoints.

If operator intent is "chat only", the injection must be endpoint-specific (not
in the shared handler body). If intent is "all three", it must be documented and
tested.

### H-4 — Retry loop interaction with gate is unspecified

`NewChatCompletionsHandler` contains a retry/failover loop:

```go
for i := 0; i < budget; i++ {
    outcome := exec.runAttempt(...)
    ...
    exec.prepareNextAttemptAfterAbort()
    continue
}
```

The gate fires once at handler entry. On a retry after an upstream 429/5xx abort,
the RPM bucket is **not re-checked**. A burst of attempts that each fail with
upstream errors will never be rate-limited on the 2nd, 3rd… attempt even though
they each represent a real inbound request moment consuming provider quota. The
design should explicitly specify whether re-checking is intended (and if so, where
in the retry path) or document the accepted gap.

### H-5 — Eviction fail-open is worse for user-keyed buckets than for IP-keyed

The design copies the `ipBucketRegistry` reset-on-cap pattern. For the IP-keyed
front-door limiter, the fail-open moment is acceptable because the IP-spoof
attacker pays for generating the flood with their own denied requests first.

For user-keyed buckets the threat model is different: a single compromised
tenant with many valid user_id values (e.g., a leaked key driving many synthetic
user IDs via a multi-user platform) could continuously trigger registry resets,
giving every user a fresh full bucket on every `maxEntries`-th request. The
design acknowledges this under R4 but does not quantify `maxEntries` or analyse
the reset-cycle time at realistic tenant scales. With a small `maxEntries` this
is an amplified fail-open, not a rare hiccup. The value must be documented and
the reset-cycle analysis included.

---

## Money / Schema / Auth / CMB Risks

### MS-1 — Schema: migration 0077 number is correct

Confirmed: `sql/migrations/` currently ends at `0076_user_role`. Migration 0077
is the correct next number. No collision.

### MS-2 — Down-migration is safe

`DROP TABLE IF EXISTS user_rate_limit_policies` correctly cascades to the three
indexes (they are owned by the table). No orphaned objects. Safe for rollback.

### MS-3 — Money path: gate fires before Tx1 — correct

Gate fires after `validateChatCompletionsRequest` and before `reserveClaim`
(which calls `ClaimGate.Reserve`). A denied request never enters Tx1/Tx2. No
double-charge, no orphaned claim, no refund needed. This invariant is sound.

### MS-4 — No shopspring/decimal concern

RPM/TPM are integer counters; token-bucket arithmetic uses `float64` (same as
existing `gateway.TokenBucket`). No money arithmetic involved. Correct.

### MS-5 — CMB-1: no credential material in GateInput — correct

`GateInput` carries `TenantID` (int64), `UserID` (int64), `UserGroup` (string),
`TokenEstimate` (int). No bearer token, no raw API key, no session material.
Log output on deny uses only these same fields. CMB-1 honored.

### MS-6 — CMB: upstream payload never logged — correct

Gate runs before any upstream dispatch. No upstream payload exists at check time.
Correct.

### MS-7 — Router reads no credentials — correct

Gate is called in `ChatHandlerDeps` (handler layer), not in pool router. The
gate has no write path and performs no credential lookup. Correct.

### MS-8 — Tenant isolation in SQL enforced — correct

The unique partial index is `(tenant_id, user_group)`. Policy queries must be
parameterised by `tenant_id`. A tenant cannot read or affect another tenant's
policy row. sqlc parameterisation prevents SQL injection. Correct.

### MS-9 — `UserGroup = ""` (empty string) vs `"default"` sentinel undocumented

`auth.Identity.UserGroup` defaults to `"default"` (confirmed in
`api_key_resolver_integration_test.go` line 174). Legacy code paths where
`UserGroup` is `""` (empty) could occur if old callers do not populate the
field. The design does not specify gate behaviour when `GateInput.UserGroup == ""`.
If the gate key is `(TenantID, "")` and no policy row with `user_group = ''`
exists, it correctly falls back to the tenant-default NULL row — but the design
should document this sentinel explicitly to prevent future regressions.

---

## Parity Gaps

### PG-1 — Retry-After computation method is underspecified

The design says `middleware.go` computes `ceil(1/rate_per_sec)` "mirroring
`rate_limit.go:retryAfterForRatePerSec`". The existing `retryAfterForRatePerSec`
in `cmd/gateway/rate_limit.go` is keyed on per-second rates derived from the
IP-bucket configuration. For RPM-based user buckets, the rate is
`rpm_limit / 60.0`. A user at RPM=600 gets a `Retry-After` of 1 second
(ceil(1/(600/60)) = ceil(0.1) = 1). That is technically correct but misleadingly
short for the burst=1 case. No parity gap per se, but worth documenting the RPM
→ per-second conversion in `middleware.go`.

### PG-2 — `observe` mode does not emit a metric — it only sets a flag

The design says `mode=observe` emits metrics. `GateDecision.ObservedExceed=true`
is set, but the design provides no mechanism to turn that flag into an actual
metric emission (counter, histogram, log line). The middleware layer is described
as "writes 429 JSON" — it says nothing about emitting the observe-mode signal
to any metrics backend. Parity with "allow + emit metric" reference behavior is
**not achieved** by merely setting a boolean on a struct; a caller must consume
`ObservedExceed` and emit the metric. This is not shown anywhere in the design.

### PG-3 — Success-count window is tumbling, not sliding

The design acknowledges: "tumbling window is cheaper and sufficient for
RPM-scale detection." Reference implementations use a true sliding window (or
a ring-buffer approximation). A tumbling window can allow up to 2× the limit
in a worst-case boundary crossing (N requests at T=59s + N requests at T=61s
all count against different windows). This is documented as an accepted
trade-off, but the word "Better" in the parity table is inaccurate — it is a
cheaper approximation, not a behavioral improvement.

---

## Maintainability (God-File Check)

All proposed files are under 500 lines as budgeted in the design:

| File | Budgeted lines | Verdict |
|---|---|---|
| `gate.go` | ~200 | OK |
| `bucket_registry.go` | ~160 | OK |
| `policy_loader.go` | ~130 | OK |
| `middleware.go` | ~130 | OK |
| `success_window.go` | ~110 | OK |
| `policy_store.go` | ~90 | OK |
| `gate_test.go` | ~300 | OK |

No god-file violations in the line budgets. All new code lives in
`internal/userratelimit` — the frozen-package rule is respected.

**One function-scope concern:** `gate.go` at ~200 lines with two metric paths
(RPM + TPM) and three policy modes (`enforce`, `observe`, `disabled`) in a
single `Gate.Check` method risks exceeding the 80-line function limit if not
carefully structured. The design does not show the function signature or
internal helper decomposition for `Gate.Check`. The implementer must verify
`Gate.Check` delegates sub-concerns to private helpers and stays within 80 lines.

---

## Must-Fix Before Implementation

1. **[ARCH] Revise the two-phase RPM/TPM injection point (H-1).** The design
   incorrectly places RPM before body-parse and TPM inside `prepareRoute`. The
   correct injection is a **single gate call** after `validateChatCompletionsRequest`
   (body+MaxTokens available) and before `prepareRoute` (or at the top of
   `prepareRoute`). Rewrite §Endpoints to reflect the actual handler call sequence
   and provide a single `GateInput` construction site that includes both
   `MaxTokens`-derived `TokenEstimate` and `UserGroup`.

2. **[ARCH] Define or remove the post-settle `success_window` feedback path (H-2).**
   Either: (a) remove the claim that `success_window` receives actual post-settle
   token counts and document it as input-estimate only, or (b) design the
   callback/hook mechanism (e.g., a `Gate.RecordSettled(ctx, userID, actualTokens)`
   method called from `settleCompletion`) and add a discriminating test for it.

3. **[TEST] Add discriminating tests for `/v1/messages` and `/v1/responses` (H-3).**
   Add at least one test that exercises rate limiting via `NewMessagesHandler`
   and one via `NewResponsesHandler`, confirming the shared `ChatHandlerDeps`
   path fires the gate equally. Alternatively, document explicitly that the gate
   is intentionally shared and add a wiring assertion.

4. **[WIRE] Add `AppRateLimiter != nil` to `chatHandlerConfigured()` or add a
   startup assertion in `wiring.go` (R5 + H-3).** The design acknowledges R5
   (nil-skip silently disables the gate) but leaves it as a future smoke-test
   concern. Before implementation ships, either: (a) add `AppRateLimiter != nil`
   to `chatHandlerConfigured` guarded behind a release flag, or (b) add a
   `wiring_test.go` assertion that verifies `chatHandlerDeps(d).AppRateLimiter != nil`
   when the feature is not explicitly disabled. The current design leaves a
   permanently silentable defect with no CI catch.

5. **[TEST] Add discriminating test for retry-loop gate interaction (H-4).**
   A test must verify: fire N requests (exhausting the RPM bucket), then trigger
   a retry scenario where `prepareNextAttemptAfterAbort` is called. Assert that
   the gate does **not** double-deduct on retries within the same handler
   invocation (or assert that it does, if that is the intended design). Without
   this test, a mutation that adds a second gate call inside the retry loop (or
   removes it) is invisible.

6. **[PARITY] Instrument `ObservedExceed` flag into a real metric emission (PG-2).**
   `GateDecision.ObservedExceed = true` is not itself an emitted metric. The
   design must specify: what consumes this flag? Either `middleware.go` emits a
   structured log line / counter increment on `ObservedExceed`, or
   `gate.go` calls an injected metrics reporter. A test must assert that an
   observe-mode excess produces an observable side-effect (log line, counter
   increment on a test sink), not just a field value on a return struct.

7. **[DOC/TEST] Document and test the `UserGroup = ""` empty-string fallback
   (MS-9).** Add a test `TestGate_EmptyUserGroupFallsBackToTenantDefault` that
   passes `GateInput.UserGroup = ""` with a tenant-default policy and asserts
   correct enforcement. Document in `types.go` that empty string is treated
   identically to "no group" (falls back to tenant default) so future callers
   are not surprised.
