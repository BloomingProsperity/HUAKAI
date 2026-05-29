# S1-029 Round-2 [P1] — chunk-count provisional pricing — Claude independent position

Written without seeing codex's parallel draft (CLAUDE.md #10).

## The finding (codex Round-2, gpt-5.5 xhigh)
`streamingCompletionEvent` (chat_completions_stream.go:531-537) prices `draft.DeliveredTokenCount`
as output tokens when upstream usage is missing. But `draft.DeliveredTokenCount`
(forwarder.go:408 = `acc.DeliveredTokenCount()`) returns `acc.Usage.OutputTokens` only when
that is >0; otherwise it falls back to `acc.DeliveredChunkCount`
(forwarder_types.go:195-203). On the missing-usage path the first
`actualCompletionCost(usageFromDraft(draft))` errors precisely as
`pricingUnavailable("reported usage missing")` (pricing.go:63-67), which means
`draft.TokensOutput == 0` → so `DeliveredTokenCount` is the **chunk-count fallback**, not tokens.

`DeliveredChunkCount` = count of content-bearing canonical frames only — `canonicalDeliveredChunks`
(forwarder.go:533-544) returns 1 for `text_delta` / `tool_input_delta` / `reasoning_delta`,
0 otherwise (role / ping / finish frames excluded). One upstream SSE content frame → delivered=1
(forwarder.go:208-212). So frame granularity = provider SSE granularity.

## Severity analysis (S1 vs S2)
Direction of error depends on provider framing:
- Anthropic `text_delta`/`input_json_delta`: batches multiple tokens per frame → frames < tokens → **undercharge** (matches the slice's stated conservative philosophy, comment line 532).
- OpenAI chat deltas: ≈ 1 token/frame, occasional sub-token BPE splits → frames can slightly exceed tokens → **mild overcharge**.
- Tool-argument streaming (`tool_input_delta`): JSON streamed in fine fragments → frames can exceed tokens → **bounded overcharge**, magnitude bounded by tool-call size.

So there IS a real overcharge vector (OpenAI sub-token + tool-arg fragmentation), but it is:
- only on the **missing-usage** path (provider streamed content yet omitted the terminal usage frame — abnormal/rare),
- **output-only**, single model output rate, **bounded** magnitude,
- flagged `pending_reconciliation=true` + `usage_source='inferred'`.
BUT the S1-029 reconcile worker finalizes (zero-delta) after 5min grace → the provisional becomes
**permanent**, so "it's pending" is NOT a mitigant. Money-path + overcharge vector + #8
"归类不确定 → 提升 S1" → I classify **S1: fix before landing** (do not relabel a billing overcharge down).

## Options (with the cheap/conservative fix preferred)
- **(A) Keep chunk-count provisional.** Rejected: leaves the overcharge vector.
- **(B) Content-grounded token estimate (bytes/4 of delivered text).** Better estimate but
  needs the accumulator to retain delivered text length — the SAME deferred streaming-content-retention
  infra already deferred in DEFERRED-S2-163 (streaming cross-check). Out of scope per #8 (don't drip-expand);
  also reintroduces an input-style heuristic the slice deliberately avoided (line 532).
- **(C) [PREFERRED] On the chunk-count-only fallback, do NOT price a token-valued provisional;
  keep ActualCost=0 with pending_reconciliation=true + usage_source='inferred'.** Only price a
  provisional when a REAL output-token signal exists (acc.Usage.OutputTokens>0). This is
  strictly non-overcharging (eliminates the vector entirely), preserves the substantive S1-029
  deliverable (the reconcile worker + pending flag + audit trail), matches the slice's
  "never overcharge the user" philosophy, and needs no new infra. The accurate
  provisional estimate from delivered content is deferred to the content-retention slice
  (shared with S2-163 streaming cross-check). This is a "safe-equivalent / mandatory-roadmap"
  resolution, NOT a feature removal (Feature Preservation Rule) — the row, flag, and reconciliation
  all remain; only a possibly-wrong number is withheld.

NOTE on (C): on the pure missing-usage path acc.Usage.OutputTokens is ~always 0, so (C) means
the provisional charge is effectively $0-pending for usage-omitted streams. That is a
**reduction vs the Owner-accepted "bills a conservative provisional"**, in the safe direction.
It restores pre-S1-029 revenue behavior for that rare path while keeping the new reconcile/audit
machinery. Acceptable to decide autonomously (Owner delegated worker-policy; safe direction),
record here, and document as a follow-up. If codex's parallel draft surfaces a cheap
non-overcharging way to KEEP a positive provisional, prefer that.

## Decision
Lean (C). Confirm against codex parallel draft, then implement minimal fix + discriminating test
(RED: with chunk-count provisional, an OpenAI-style sub-token / tool-arg stream overcharges;
GREEN: fix yields $0-pending, no overcharge), one verification round (#8 Round-3 justified —
unresolved S1), land, then push.

## SYNTHESIS — codex parallel draft (gpt-5.5 xhigh, 2026-05-28; #10 cross-discuss)
codex could not write its .md (my `--sandbox read-only` flag blocked the write — analysis still
completed; verdict captured from stdout /tmp/s1029-p1-decide.txt). codex independently concluded:
1. Verdict: **HUAKAI S1, fix-now-then-land.**
2. Reason: "delivered chunk count is not token count and **can overcharge, not only undercharge**."
   → INDEPENDENTLY confirms my overcharge-vector concern (not just codex-review's undercharge framing).
3. Minimal fix: "remove pricing from DeliveredTokenCount; keep ActualCost=0 with pending
   reconciliation on missing usage." → IDENTICAL to my option (C).
4. Test: "draft with zero usage and DeliveredTokenCount=3 must keep ActualCost zero."

**Result: AGREE (both severity S1 + fix C). No conflict.** Per #10 surface = agree → proceed.

## Refinement beyond both drafts (config-failure nuance)
The error branch of `streamingCompletionEvent` fires for TWO reasons:
(a) reported-usage-missing (all token fields 0 → `pricingUnavailable("reported usage missing")`), and
(b) pricing-config failure (rate table missing/empty) even when real tokens are present.
The OLD provisional block set `usage_source=inferred` only on a SUCCESSFUL provisional retry —
in case (b) that retry ALSO errors (same broken rate table), so case (b) never became `inferred`.
That is correct and must be PRESERVED: marking a real-token request `inferred` would let the
settlementreconcile worker auto-finalize it at $0 → silent zero-charge of a real request.

So the fix sets `inferred` ONLY for genuine missing-usage (guard: `usageFromDraft(draft)` all-zero),
NOT unconditionally on the error branch. Two discriminating tests:
- **T1 (overcharge fix)**: missing usage + VALID rate table (output rate > 0) + DeliveredTokenCount=40
  → assert ActualCost==0, usage_source=inferred, pending=true. Mutation = reintroduce chunk-count
  provisional → ActualCost=0.1 (overcharge) → RED. (Valid rate table is the discriminating fixture;
  a broken table would make both old/new 0 = non-discriminating.)
- **T2 (inferred-gating)**: config failure (rate table missing) + real tokens present
  → assert ActualCost==0, usage_source != inferred (stays reported), pending=true.
  Mutation = set inferred unconditionally → usage_source=inferred → RED (would let worker zero-charge a real request).

T1 = rewrite of existing TestStreamingCompletionEvent_NoUsageUsesOutputOnlyProvisionalCost
(its 0.1 expectation now inverts to 0). T2 = new case.
