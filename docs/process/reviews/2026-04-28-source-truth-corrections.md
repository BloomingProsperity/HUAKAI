# Source-Truth Corrections — F-POOL-001 + F-GW-002

| Field | Value |
| --- | --- |
| Status | Honest accounting; Owner-requested 2026-04-28 ("我想知道你们真的去分析，去拆解代码了吗") |
| Author | Claude PM-Orchestrator |
| Date | 2026-04-28 |
| Sub2API commit verified | `b0a2252ed19c3720e6adafde6083e64fbac2efa9` ("Merge pull request #2051 from DaydreamCoding/openai-fast-flex-policy") |
| Trigger | Owner asked whether Claude actually read source code. Honest answer: Codex did (15,665 raw-source URL references in 3.5MB artifact); Claude did NOT in cycles 1–2; Claude's "specifier passes" were paraphrases of pre-existing prose decompositions. This document catalogues every hallucinated claim that was NOT in source, paired with what source actually says. |

## Why This Matters

DR-008 strict path requires both Claude and Codex to do **independent source-verified specifier passes**. In Cycles 1 (Quota+Billing) and 2 (Pool Selection), Claude's pass was based on second-hand prose + evidence ledger rows — not on direct source reading. The pre-existing prose decompositions themselves had drift (Codex's first source dive was paraphrased and lossy). Both syntheses are therefore partially built on hallucinated structure.

This document corrects that. It does NOT cover one-api or other references; only Sub2API for F-POOL-001 and F-GW-002.

---

## F-POOL-001 — Hallucinations vs Source Truth

### H-1 ❌ "Three layers: continuation → sticky → fresh"

**Hallucinated** (in `pool-selection-claude.md` §2 and the pre-existing `layered-account-selection.md` §2):
> Layer 1 — Continuation Affinity: when a request carries an upstream-issued continuation marker (e.g. provider conversation id / session token), the gateway tries to send it to the same Provider Account that handled the prior turn.

**Source truth** (`backend/internal/service/gateway_service.go` lines 1376–1928): There is **no continuation-marker layer**. The actual layers are:

1. **Layer 1 — Model Routing** (lines 1528–1752): when the User's Group has a `ModelRouting` config mapping `requested_model → [account_ids]`, only those Accounts are eligible.
2. **Layer 1.5 — Sticky-within-routing** (lines 1589–1665): if sticky binding exists AND the bound Account is in the routing list, prefer it (with re-validation).
3. **Layer 1.5b — Sticky-standalone** (lines 1755–1803): when no Model Routing config, plain sticky session lookup.
4. **Layer 2 — Load-aware fresh** (lines 1805–1911): strict lexicographic sort on `(priority asc, load_rate asc, last_used_at asc)`, then `shuffleWithinSortGroups` randomizes within ties.
5. **Layer 3 — Fallback queue** (lines 1913–1927): if Layer 2 found candidates but no slot acquired, return `AccountWaitPlan` with `FallbackWaitTimeout` / `FallbackMaxWaiting`.

Grep confirmation: `grep -i 'continuation' gateway_service.go` returned no matches.

### H-2 ❌ "Top-K randomization with score formula"

**Hallucinated** (pool-selection-claude.md §2.3):
> `score(candidate) = w_priority * normalized(...) + w_balance * ... + w_latency * ...`
> `Pick: ... take top-K candidates (default K=3), then select uniformly at random.`

**Source truth** (lines 1691–1710):
```
sort.SliceStable(routingAvailable, func(i, j int) bool {
    a, b := routingAvailable[i], routingAvailable[j]
    if a.account.Priority != b.account.Priority {
        return a.account.Priority < b.account.Priority
    }
    if a.loadInfo.LoadRate != b.loadInfo.LoadRate {
        return a.loadInfo.LoadRate < b.loadInfo.LoadRate
    }
    // … LastUsedAt comparison …
})
shuffleWithinSortGroups(routingAvailable)
```

There is **no scoring formula**. The algorithm is **strict lexicographic sort** on three ordered fields, then `shuffleWithinSortGroups` randomizes ONLY within (priority, load_rate, last_used_at) tie groups. No `top-K`, no operator-tunable weights, no normalization.

### H-3 ❌ "Score signals: capability_confidence, fairness_debt, snapshot_freshness, ..."

**Hallucinated** (pool-selection-synthesis.md §The Synthesized HUAKAI Algorithm — Final / Phase B): I listed 11 score signals.

**Source truth**: Sub2API's selection has exactly **3 ordering keys**: `Priority` (operator-set int), `LoadRate` (computed by `concurrencyService.GetAccountsLoadBatch`), `LastUsedAt` (timestamp). That's it. No `capability_confidence`, no `fairness_debt`, no `snapshot_freshness` exists in source.

### H-4 ❌ "Per-Account exclusion list per request"

**Partially hallucinated**: I said per-request exclusion is built by appending failed Accounts.

**Source truth** is more nuanced. There are **two distinct exclusion mechanisms**:

- **`excludedIDs map[int64]struct{}`** parameter (caller-supplied): used by the outer retry-on-upstream-failover loop. The retry caller adds the failed Account ID and re-calls `SelectAccountWithLoadAwareness`. This is the upstream-error exclusion.
- **`localExcluded` map** (lines 1426–1452, inside the LoadBatchEnabled=false path): used when `checkAndRegisterSession` returns false (session-limit rejection). This is the session-limit exclusion, scoped to one selection call.

So my pass conflated two different exclusion paths.

### H-5 ❌ "Wait queue is a leased intent that re-enters admission on resume"

**Hallucinated** (synthesis Codex C4):
> A waiter resumed from queue re-enters Phase C and re-evaluates all hard gates.

**Source truth**: I cannot find evidence that resumed waiters re-validate. The source returns an `AccountWaitPlan` (line 1465–1470) and the caller honors the plan. The actual wait + acquire happens in `ConcurrencyHelper.AcquireAccountSlotWithWait` (file `internal/handler/gateway_helper.go` line 267) which I have not yet read. Need to verify whether re-validation happens there. **Until verified, this claim is unconfirmed**, not source-verified.

### H-6 ❌ "Capability shift / safe-equivalent as Route policy"

**Hallucinated** (synthesis C2 from Codex's pass):
> Sub2API has a capability-shift pattern (capability not natively supported → fall back to safe equivalent).

**Source truth**: I do NOT see a "capability shift" / "safe-equivalent" pattern in the selection code I've read. There IS model mapping (`account.GetMappedModel` line 64 in gateway_forward_as_chat_completions.go) but that is renaming a model id, not "fall back to a different capability". I cannot confirm Codex's claim from the code I read. **This may be in Codex's source dive but I have not located it.** Until verified, treat as unconfirmed.

### H-7 ❌ "Forced/administrative override (break-glass routing)"

**Partially correct** (synthesis Q1 / C3): there IS a `forcePlatform` mechanism (line 1328: `ctx.Value(ctxkey.ForcePlatform).(string)`). It's used by the `/antigravity` route to force a specific platform. **This is platform-level forcing**, not Account-level. My synthesis treated it as "Channel/Account override" which is too generous — source shows platform routing only.

### H-8 ❌ "Single-Account exemption (`allow_last_resort`) with three guards"

**Hallucinated**: I claimed Sub2API has logic where one Account in cooling_down can be exempt under specific conditions, traced to LiteLLM E-LM-DEEP-005. I did not verify this in source.

**Source truth**: Cannot find any "exemption" or "last-resort" logic in `gateway_service.go`. This is plausibly LiteLLM-specific (which I haven't read yet). My pass treated it as a Sub2API + LiteLLM convergence; that was unverified.

### H-9 ❌ "Cap_concurrency held under serializable txn during acquire"

**Hallucinated** (synthesis algorithm pseudo-code):
```
RevalidateAndAcquire:
    txn = begin_serializable()
    SELECT ... FOR UPDATE
    ... 8 hard-gate re-checks
    UPDATE provider_account SET in_flight_count = in_flight_count + 1 …
    commit(txn)
```

**Source truth** (line 2250–2255):
```go
func (s *GatewayService) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
    if s.concurrencyService == nil {
        return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
    }
    return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}
```

Sub2API's slot acquisition is delegated to `concurrencyService.AcquireAccountSlot` which is a **cache-backed primitive** (returns `(bool, error)` per stub interface in `testutil/stubs.go:24`). It is **not a serializable DB transaction**. Sub2API uses Redis or in-memory concurrency tracking, not row-locked PostgreSQL counter. The "lock 6 rows in serializable txn" pattern is HUAKAI's design, not inherited from Sub2API.

This is an important framing correction: Sub2API is NOT money-grade on this primitive (cache-only concurrency means crashes leak slots until TTL). HUAKAI's Pattern B with PostgreSQL row-locks is the fix, not the inheritance.

### H-10 ✅ Confirmed correct

These claims I verified DID match source:

- **Multiple sticky-cache-miss reasons enum**: source line 1656 logs `[StickyCacheMiss]` with reasons `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared`. Synthesis §4 listed `STICKY_BREAK_<reason>` enum — the spirit matches.
- **8 schedulability gates per layer**: `isAccountSchedulableForSelection` + `isAccountAllowedForPlatform` + `isModelSupportedByAccountWithContext` + `isAccountSchedulableForModelSelection` + `isAccountSchedulableForQuota` + `isAccountSchedulableForWindowCost` + `isAccountSchedulableForRPM` + (implicit `isExcluded`). My "Revalidation Gate at every layer" claim was directionally correct.
- **Per-request exclusion** via `excludedIDs` parameter — confirmed at the outer retry caller; internal `localExcluded` for session-limit retry — confirmed in source.
- **Sticky vs fallback wait limits**: `cfg.StickySessionMaxWaiting` vs `cfg.FallbackMaxWaiting` (lines 1456–1469) — confirmed; sticky is shorter.
- **Session hash derivation**: three-tier priority — `metadata.user_id` SessionID → cache_control ephemeral content hash → SessionContext+system+messages combined (lines 648–707) — my synthesis did not specify this; should add.

---

## F-GW-002 — Hallucinations vs Source Truth

### H-11 ❌ "Default scanner buffer = 1 MiB"

**Hallucinated** (streaming-forwarder-claude.md §2.1):
> Buffer size is per-Route policy, default 1 MiB.

**Source truth**: `gateway_service.go:46`
```go
defaultMaxLineSize = 500 * 1024 * 1024
```

**Default is 500 MiB**, not 1 MiB. Operator can override via `cfg.Gateway.MaxLineSize`. This is a 500x error in my pass.

### H-12 ❌ "Drain Mode (Phase C-bis) bounded by drain_max_bytes / drain_max_seconds / drain_max_estimated_cost"

**Hallucinated** (streaming-forwarder-claude.md §2.4):
> When the client disconnects but upstream is still emitting tokens, raw close-the-upstream-now produces a billing leak: ... HUAKAI bounds the drain.

**Source truth** (gateway_forward_as_chat_completions.go lines 397–456):
```go
writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
    // ...
    if _, err := fmt.Fprint(c.Writer, out); err != nil {
        return true // client disconnected
    }
    return false
}

processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent) bool {
    // ... merge usage, write chunks, flush ...
    return false
}

for scanner.Scan() {
    // ...
    if processAnthropicEvent(&event) {  // returns true on disconnect
        return resultWithUsage(), nil   // EXITS IMMEDIATELY
    }
}
```

When the downstream client disconnects, `writeChunk` returns true, `processAnthropicEvent` returns true, the for-loop **exits immediately at line 454**, and `defer resp.Body.Close()` (line 142) closes the upstream stream. **There is NO drain loop**. The "Phase C-bis bounded drain budget" is entirely fictional.

The pre-existing `streaming-forwarder.md` §4 claimed:
> ... some paths in the upstream code continue to drain upstream until usage data is collected before terminating — this is the controversial "billing-preserving drain" behavior.

The "billing-preserving drain" is partly real but indirectly: `detachStreamUpstreamContext(ctx, true)` (line 7781) returns `context.WithoutCancel(ctx)`, which means while *connecting* to upstream, request-context cancellation does not propagate. But once `resp` is acquired and processing begins, the for-loop exits cleanly on disconnect. There is no extended drain.

### H-13 ❌ "Eight-axis timeout policy"

**Hallucinated** (streaming-forwarder-claude.md §4):
> connect / TLS / request-write / response-header / first-token / inter-event / total-stream / downstream-write — eight Route policy fields.

**Source truth**: I read the forwarder file and gateway_service.go. The only stream-relevant config knob exposed is `cfg.Gateway.MaxLineSize`. Timeouts exist somewhere (the `httpUpstream.DoWithTLS` transport has them) but are not eight independent axes in any config struct I've seen. The eight-axis breakdown is HUAKAI's design proposal, not Sub2API's reality.

### H-14 ❌ "Usage source taxonomy: reported / normalized / inferred / partial / ambiguous"

**Hallucinated** (streaming-forwarder-claude.md §3): Four / five trust tiers with explicit reconciliation rules.

**Source truth** (`mergeAnthropicUsage` at gateway_forward_as_responses.go:200):
```go
func mergeAnthropicUsage(dst *ClaudeUsage, src apicompat.AnthropicUsage) {
    if dst == nil { return }
    if src.InputTokens > 0 { dst.InputTokens = src.InputTokens }
    if src.OutputTokens > 0 { dst.OutputTokens = src.OutputTokens }
    if src.CacheReadInputTokens > 0 { dst.CacheReadInputTokens = src.CacheReadInputTokens }
    if src.CacheCreationInputTokens > 0 { dst.CacheCreationInputTokens = src.CacheCreationInputTokens }
}
```

Sub2API's "merge" is **last-non-zero-wins per field**. There is no taxonomy. Mid-stream usage events overwrite previous; terminal frame overwrites mid-stream. There is no `inferred` source (no tokenizer fallback), no `ambiguous` source, no `pending_reconciliation` flag.

The taxonomy I designed for HUAKAI is a real **improvement**, but I should have framed it as "this is what HUAKAI adds beyond Sub2API", not "this is the taxonomy".

### H-15 ❌ "Multi-source usage reconciliation with conflict logging"

**Hallucinated** (streaming-forwarder-claude.md §3.2):
> If two sources report conflicting values at the same trust level, the terminal frame wins and the conflict is recorded in `rewrite_log`.

**Source truth**: There is no conflict detection; `mergeAnthropicUsage` silently overwrites. No `rewrite_log` exists.

### H-16 ❌ "Mid-stream rate-limit event handling with cooldown"

**Hallucinated** (streaming-forwarder-claude.md §6 taxonomy):
> `UPSTREAM_RATE_LIMIT` ... cooldown_account.

**Source truth**: rate-limit handling exists, but in `gateway_forward_as_chat_completions.go:163`:
```go
if s.rateLimitService != nil {
    s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
}
```

This fires only for **status >= 400 BEFORE the streaming body starts** (the failover branch at lines 144–174 is before the streaming handler at line 184). Once the upstream stream begins emitting events, mid-stream rate-limit detection is not triggered in this code path. My "mid-stream rate limit → cooldown account" was wrong about the timing.

### H-17 ❌ "Mid-stream failover blocked / `Idempotent-Stream-Replay` header"

**Hallucinated** (streaming-forwarder-claude.md S10 + AT-GW-002-09/10):
> Mid-stream failover after client-visible output is forbidden by default ... opt-in via `Idempotent-Stream-Replay: true` header.

**Source truth**: Mid-stream failover is **structurally impossible** in this code path because failover decision happens BEFORE streaming starts (line 153 `s.shouldFailoverUpstreamError(resp.StatusCode)` only runs for `resp.StatusCode >= 400` at line 145). Once `resp.StatusCode < 400` is reached and streaming begins (line 184), no failover hook exists.

So the "default-deny + opt-in" framing was wrong. The correct framing: **Sub2API has no mid-stream failover at all**. Whether HUAKAI should add it (with safety guards) is a HUAKAI design question, not a Sub2API behavior.

### H-18 ❌ "AMBIGUOUS_USAGE produces zero customer charge"

**Hallucinated** (streaming-forwarder-claude.md §6 + S7 invariant).

**Source truth**: Sub2API charges based on whatever `usage` accumulator contains. If the stream errors with no usage data, `usage` is zero and the charge is zero — but that's a side-effect, not a "no-charge gate". There is no `AMBIGUOUS_USAGE` enum; my taxonomy was a HUAKAI-specific design, not a Sub2API observation.

### H-19 ✅ Confirmed correct

- **Inline event processing with explicit flush per event**: confirmed (line 429 `c.Writer.Flush()`).
- **`bufio.Scanner` line-based parsing**: confirmed (line 222 `bufio.NewScanner(resp.Body)`).
- **Usage extraction from `message_start` AND `message_delta`**: confirmed (lines 415–417 and 411–413).
- **Tool-call delta accumulation across content_block_delta events**: confirmed (lines 270–281 buffered handler shows `Text/Thinking/Input` accumulation).
- **`detachStreamUpstreamContext` decouples upstream from request cancellation**: confirmed (`context.WithoutCancel(ctx)` line 7788) — but its behavior is "don't cancel", not "drain forever".
- **Stream-vs-buffered branch on `clientStream`**: confirmed (line 183/184).
- **No tokenizer fallback for missing usage**: confirmed (Sub2API has no inference path).

---

## What Stays True in the Synthesis

Even with the hallucinations corrected, the following synthesis decisions remain valid as **HUAKAI design choices** (clearly HUAKAI's, not inherited):

- **Pattern B (Pool acquire after Tx1 commit, not inside)**: this is a HUAKAI design reaction to lock-amplification risk. Sub2API's cache-only concurrency means the question doesn't even arise there. HUAKAI Pattern B is correct because HUAKAI uses PostgreSQL serializable txns (per Quota+Billing synthesis), which Sub2API does not.
- **Idempotency key composition (Provider Account excluded)**: HUAKAI design, correct.
- **Routing reason as structured payload (not free-form text)**: HUAKAI design, an improvement over Sub2API's `[StickyCacheMiss]` log lines.
- **Eight-axis timeout policy**: HUAKAI design improvement; should be re-framed as "HUAKAI splits Sub2API's coarse single-timeout into eight axes" rather than "Sub2API has eight axes".
- **Usage source taxonomy with `pending_reconciliation`**: HUAKAI design improvement; Sub2API has neither taxonomy nor reconciliation.
- **Mid-stream failover with `Idempotent-Stream-Replay` header**: HUAKAI design; Sub2API has no mid-stream failover at all.
- **Bounded drain budget (max_seconds / max_bytes / max_estimated_cost)**: HUAKAI design; Sub2API has no drain at all.

These are the **legitimate KEEP/IMPROVE/AVOID outputs**. The error was attribution: I framed HUAKAI design improvements as if they were Sub2API behaviors, which inflated the synthesis credibility.

---

## Action Items

1. **Mark `pool-selection-claude.md` and `streaming-forwarder-claude.md` as superseded** by source-verified rewrites (see next two files in this commit set).
2. **Rewrite both Claude passes** using source-verified facts only. HUAKAI design improvements clearly labeled as such.
3. **Update synthesis files**: re-mark Convergence vs Where-Codex-Sharpens-Claude vs Where-Claude-Sharpens-Codex distinction, since the Claude side was largely paraphrased.
4. **Owner answers Q1..Q4 stay valid** because they are HUAKAI policy decisions, not source extraction.
5. **Reviewer-lane sign-off MUST verify against source** — the stalled reviewer agent did partial work but did not catch these because it reviewed against synthesis-only.
6. **Methodology fix going forward**: every Claude specifier pass MUST cite specific file:line evidence from source. No more "behavior basis = existing prose".

This document is the truthful accounting Owner asked for.
