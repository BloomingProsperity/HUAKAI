# Sub2API — Layered Provider Account Selection

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Sub2API (LGPL-3.0, [E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | [F-POOL-001](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) |
| Evidence ledger rows | [E-S2A-DEEP-006, E-S2A-DEEP-007, E-S2A-DEEP-009](../../07_REFERENCE_EVIDENCE_LEDGER.md) |
| Specifier session | Codex (`omc ask codex --agent-prompt critic`, 2026-04-28T05:18 UTC) |
| Specifier date | 2026-04-28 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | Sub2API backend dispatch and selector files (paths verified by Codex; URLs withheld here in the public ledger but recorded in `.omc/artifacts/ask/`) |

## 1. WHY

Relay-station operators run with N upstream subscriptions of varying tier, balance, and rate-limit posture. The product promise is: a single platform-issued API Key gives a User predictable access to a logical capacity, while behind the scenes the gateway smooths over per-Account limits, mid-conversation account changes, and Account-level outages. The selection algorithm is the pivot point that converts a raw request into a choice of upstream Provider Account; bad selection produces customer-visible breakage (mid-conversation context loss, surprise rate-limit, drained balance on one Account while others sit idle). Sub2API's design pressure is "keep the multi-tenant relay running through provider weather" — and that pressure is also HUAKAI's pressure given the relay-station identity ([01 §Product Identity](../../01_PROJECT_BRIEF.md)).

## 2. WHAT

Selection runs in three layers; each layer can fail and fall through to the next, and every layer's candidate is **revalidated** against the same gates before being used.

**Layer 1 — Continuation affinity.** When a request carries a continuation marker (a multi-turn conversation continuation tied to an upstream provider's stateful session), the gateway tries to send it to the same Provider Account that handled the prior turn. Continuation affinity is a strong hint because providers may store conversation state inside their own session; switching mid-turn loses context.

**Layer 2 — Sticky session affinity.** When a request carries a session identifier (typically derived from an upstream-cookie / session-id header in the client request), the gateway maintains a TTL-refreshed mapping `(session-id, model) → Provider Account` and tries to honor it. Sticky exists because long multi-turn conversations work better when pinned to one Account, even when the provider does not strictly require it.

**Layer 3 — Fresh pooled selection.** When neither layer above produces a viable candidate, the gateway picks from the entire eligible Pool. Eligibility is filtered by Channel allow-list, Model support, transport mode, capability flags (e.g. tool-use, vision), and User-Group policy. Among eligible candidates a score is computed (Section 3); the gateway then **randomizes the final pick among the strong-scoring candidates**, not always taking the top-scored one.

Critically, **every layer's candidate goes through the same final gate** before use: lifecycle (enabled / not expired), schedulability (slot available, not over wait-queue limit), Model support, transport, capability, group policy, and per-request exclusion list (Accounts already failed in this request's failover loop). A continuation candidate that fails revalidation does not get a free pass; it falls through to layer 2, then layer 3.

## 3. INPUTS (signals)

- Per-request: requested Model id, capability requirements (tool-use, vision, reasoning effort), session id, continuation marker, User and User Group, request size / token estimate, exclusion list of Accounts already tried in this request.
- Per-Account: enabled flag, expiration timestamp, current concurrency slots in use vs cap, current wait-queue depth vs limit, recent error rate (sliding window), recent first-token latency (sliding window), Model allow-list, transport mode, capability flags, current quota / balance state, current temporary health state (operational / degraded / failed / error from probe runner), credential state (valid / refreshing / revoked).
- Per-Pool: operator-set priority weights per Account, group routing policy.
- Time: monotonic clock for sliding windows; wall clock for TTL.
- Randomness: tie-breaking among strong candidates.
- State mutated: Account concurrency slot acquired (later released), wait-queue counter, sticky binding TTL refresh.

## 4. FAILURE MODES HANDLED

- **Account becomes ineligible mid-flight.** Revalidation gate at every layer catches state changes (operator disabled, quota exhausted, credential revoke, probe-marked failed) before sending the upstream call.
- **Continuation Account is dead.** Falls through to sticky, then to fresh.
- **Sticky Account is overloaded.** Wait-queue limit on sticky path is shorter than on fallback path; over-limit triggers fallback rather than blocking the request indefinitely.
- **All Accounts in Pool failed during this request.** Per-request exclusion list prevents the same Account from being tried twice in one failover loop.
- **Top-scored Account starves under load.** Randomization among strong candidates spreads load instead of stampeding the highest-scored one.
- **Rate-limited Account.** Excluded by current temporary health state.
- **Provider session lost during continuation.** Continuation layer failure cleanly degrades to sticky / fresh; user sees a fresh-start completion instead of an error.

## 5. FAILURE MODES NOT HANDLED (gaps)

- **Multi-node deployment**: the algorithm above is described as in-process; running multiple gateway instances hitting one Pool requires distributed coordination (Account concurrency caps and wait queues are not single-node concerns when traffic exceeds one node's capacity).
- **Sticky-break attribution**: the algorithm decides to break a sticky binding when revalidation fails, but the **break reason** is not consistently propagated to the Usage Record / Audit Event. Operators investigating why a long conversation lost context have to reconstruct from logs.
- **Score weight tuning**: weights are operator-set scalar inputs; no automatic tuning, no A/B feedback. Bad weights starve low-priority Accounts or mask overload until escalation.
- **Cross-Account fairness over time**: random pick among strong candidates is fair in the short term but does not guarantee long-run balance across Accounts; persistent bias can develop if score weights skew.
- **Last-resort Account protection**: if every Account in a small Pool is in a temporary failed state at the same instant, the algorithm produces a clean error but does not implement LiteLLM's "single-Account exemption" pattern ([E-LM-DEEP-005](../../07_REFERENCE_EVIDENCE_LEDGER.md)).

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: the three-layer (continuation → sticky → fresh) structure with **revalidation gates at every layer**. This pattern is correct and must be preserved verbatim in HUAKAI's behavior, not its code.
- **KEEP**: randomized pick among strong candidates instead of strict top-1 selection. Top-1 routing in a relay-station produces stampedes on the strongest Account.
- **KEEP**: separate wait-queue limits for sticky-path vs fallback-path. Sticky path bound shorter; fallback path bound longer.
- **IMPROVE**: every selection emits an operator-visible "routing reason" field on the Usage Record stating which layer chose, which signals dominated, and (for sticky breaks) the break reason from a fixed taxonomy. This addresses gap §5 sticky-break attribution. ([F-OBS-001](../../03_FEATURE_PARITY_MATRIX.md) extension.)
- **IMPROVE**: import LiteLLM's single-Account exemption logic ([E-LM-DEEP-005](../../07_REFERENCE_EVIDENCE_LEDGER.md)) into HUAKAI's gate so that the last-resort Account in a small Pool is not cooled down on a single transient failure.
- **IMPROVE**: distributed concurrency back-end (Redis or PostgreSQL advisory lock) under DR-006 so per-Account caps work across multiple HUAKAI instances. In-process semaphores are insufficient for SaaS Edition ([E-LM-DEEP-012](../../07_REFERENCE_EVIDENCE_LEDGER.md) caveat).
- **IMPROVE**: long-run fairness telemetry — per-Account dispatch count and percentage per Pool exposed to operator; alert when distribution skews beyond an operator-set threshold over a window.
- **AVOID**: any fast-path that skips revalidation when the continuation or sticky candidate looks "obviously fine". Stale state is the single largest source of bad upstream calls.
- **AVOID**: in-process-only concurrency tracking for SaaS Edition deployments. (Personal Edition single-node is fine.)
- **AVOID**: silent score-weight defaults. Defaults must be explicit and documented in the operator UI; opaque weighting is unauditable.

## 7. ATTRIBUTION

- Source files read: Sub2API backend dispatch / selector files (specifier-lane session executed by Codex 2026-04-28T05:18 UTC; raw artifact in `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-deep-source-decom-2026-04-28T05-18-49-050Z.md`, retained gitignored). Behavior described in this file is paraphrased from that specifier-lane session output; no upstream function name, struct field, file path, or distinctive identifier appears here.
- Specifier-lane session: Codex (gpt-5.5 + xhigh, critic agent), 2026-04-28T05:18 UTC.
- Reviewer-lane session: TBD — must be a different agent session than the specifier above. Reviewer must verify the file against [_REVIEW_CHECKLIST.md](../specs/_REVIEW_CHECKLIST.md) CL-001..010 before Status moves to Reviewed.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending; CL-001..010 must all pass) |
| Notes | First decomposition file under [22_DEEP_MINING_MANDATE.md](../../22_DEEP_MINING_MANDATE.md). Establishes the format for subsequent files. |
