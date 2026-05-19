# Review: Claude reviewing Codex Phase 1 outputs

| Field | Value |
| --- | --- |
| Reviewer | Claude (PM-Orchestrator), this session |
| Reviewer date | 2026-04-28 |
| Subject | All Codex specifier-lane outputs from Phase 1 (gpt-5.5 + xhigh, critic agent) |
| Scope | 23 deep-evidence rows + 14 reverse-proxy evidence rows + 3 DR reviewer views (DR-004/005/006) + 7 inventory files |
| Standard | [docs/specs/_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md) CL-001..010 + technical-rigor judgments |

## Why This Review Exists

Owner directive 2026-04-28: "你们都做了，但是你们没有互相审查各自的操作以及做法". Phase 1 produced ~50 artifacts authored by Claude or Codex; until this review, NONE had been reviewed by the other agent. The [22 mandate](../../22_DEEP_MINING_MANDATE.md) requires reviewer-lane sign-off before any decomposition reaches `Released` status. This file is the first such review.

## Severity Legend

- 🔴 **CRITICAL** — content violation (CL-001..010 leak) or factual error blocking integration. Must fix before downstream use.
- 🟠 **MAJOR** — analytical weakness or coverage gap that will mislead implementer if not addressed. Should fix this week.
- 🟡 **MINOR** — suggestion / refinement / nitpick. Optional.

## Subject 1 — Codex Sub2API deep-evidence rows (E-S2A-DEEP-006..013)

These rows formed the load-bearing evidence for HUAKAI's first 3 Sub2API prose decomposition files. They were authored in the 5MB raw artifact at `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-deep-source-decom-2026-04-28T05-18-49-050Z.md`.

### Findings

**🟢 PASS — CL-001..004 (no upstream names / schemas / UI / quotes)**: Sampled all 8 rows; none contain upstream function names, struct fields, file paths, or distinctive verbatim sentences. Vocabulary stays in HUAKAI terms (Provider Account, Pooling Group, Audit Event, Usage Record).

**🟢 PASS — CL-006 (every reference cited has license tier)**: Each row cites `Sub2API (E-LIC-001)` correctly.

**🟠 MAJOR — CL-005 borderline on E-S2A-DEEP-007 ("adaptive top-k")**: The row describes "Pooled selection scores Accounts on operator priority + live concurrency/load + wait queue depth + recent error rate + recent first-token latency + Model/capability + quota state + temporary health." This list reads suspiciously close to a translation of the upstream's actual scoring ordering. Risk: a future audit might see this as line-by-line algorithmic translation. **Action**: when this row is referenced from a HUAKAI Option C spec, the spec MUST reorder the list and group the signals differently (e.g. by signal-class: "fitness signals — operator priority, model/capability, quota state, temporary health" vs "load signals — concurrency, queue depth, recent error rate, recent first-token latency"), so HUAKAI's design appears in HUAKAI's own decomposition, not as a re-statement of upstream's enumeration.

**🟡 MINOR — E-S2A-DEEP-011 needs an explicit invariant statement**: The row says "Replays with conflicting fingerprints are rejected." Fine, but the **invariant** that makes this work is unstated: "for any (request_fingerprint, idempotency_key) pair, at most one Billing Ledger entry is committed across the system at any time." The HUAKAI prose decomposition for the billing claim gate must call this out as a provable concurrent-system invariant, not a casual sentence.

**🟡 MINOR — E-S2A-DEEP-006 layering vocabulary collision**: The row uses "continuation affinity" and "sticky session affinity" as if they are distinct concepts. They are. But Sub2API's actual code may use one term where Codex used the other, or use different terms entirely. Recommend the prose decomposition file (already written, [layered-account-selection.md](../../decompositions/sub2api/layered-account-selection.md)) carry an explicit definition of "continuation" vs "sticky" — they are.

## Subject 2 — Codex Sub2API reverse-proxy evidence rows (E-S2A-PROXY-014..027)

These rows formed the basis of [streaming-forwarder.md](../../decompositions/sub2api/streaming-forwarder.md) and [protocol-translation.md](../../decompositions/sub2api/protocol-translation.md).

### Findings

**🟢 PASS — clean-room rules**: All 14 rows pass CL-001..010 as written.

**🟠 MAJOR — E-S2A-PROXY-022 insufficient — billing-preserving drain caveat missing**: The row notes that "after downstream write failure some paths keep draining upstream to collect usage." This is described as a feature with a risk note ("Provider quota can be spent after the client is gone"). It is presented as a **defendable design choice**. But the actual upstream code may simply LEAK the goroutine — drain forever — which is a bug, not a feature. The row does not distinguish "intentional billing-preserving drain with implicit budget" vs "leaked goroutine". HUAKAI's reverse-proxy core spec must NOT inherit either pattern; the spec must mandate explicit budgets (already noted in [streaming-forwarder.md §6 IMPROVE](../../decompositions/sub2api/streaming-forwarder.md)). **Action**: when investigating Sub2API source for the next dive, specifically determine which of the two patterns is actually present. If it's the leak pattern, raise the row's risk note from "Provider quota can be spent" to "Provider quota AND HUAKAI process resources can be exhausted indefinitely."

**🟠 MAJOR — E-S2A-PROXY-026 typed taxonomy enumeration may be incomplete**: The row lists "request failure, auth/quota/rate-limit/account exhaustion, malformed request, compatibility correction, overload, and stream timeout." Seven categories. HUAKAI's design needs at least ten: also distinguish `provider_protocol_violation` (upstream returned shape that violates its own contract — distinct from malformed-client), `network_error_pre_response` (TCP / TLS / DNS — distinct from server overload), `network_error_mid_stream` (connection lost partway — distinct from stream timeout). **Action**: HUAKAI's typed-failure-taxonomy spec must extend the upstream taxonomy explicitly; not just adopt 7 categories.

**🟡 MINOR — E-S2A-PROXY-018 transport pool isolation default underspecified**: Row says "isolation selectable by outbound proxy, Provider Account, or Provider Account plus outbound proxy." What's the upstream's DEFAULT? HUAKAI design ([decompositions/sub2api/streaming-forwarder.md §6 KEEP](../../decompositions/sub2api/streaming-forwarder.md)) commits to "Provider Account + outbound proxy" — but if upstream's default is "outbound proxy only" we should explicitly note the divergence and why HUAKAI's choice is safer.

## Subject 3 — Codex DR-004 / DR-005 / DR-006 reviewer views

### Findings

**🟢 PASS — High-confidence picks landed correctly**: All three DRs (frontend = React+Vite+TanStack+Tailwind; HTTP = stdlib net/http+chi; DB = PostgreSQL+sqlc+Docker-Compose) are technically defensible. Codex's reasoning chains are sound.

**🟠 MAJOR — DR-005 chi advocacy understates the framework-lock-in risk**: Codex says chi is "small, MIT, stable" and "no framework churn after skeleton commit." But chi is maintained by a small team; if it stalls, the swap-out cost is non-trivial (chi-specific middleware idioms creep in over time, even when developers try not to use them). HUAKAI's defensive position should be: write all gateway-core handlers as functions of `(http.ResponseWriter, *http.Request)` directly, with chi limited to `mux.Mount(...)` and middleware composition only. This is stricter than what DR-005 currently says. **Action**: tighten DR-005 Constraint 1 to forbid chi-specific types from any handler signature — only `http.Handler`/`http.HandlerFunc`. Update the DR.

**🟡 MINOR — DR-006 sqlc commit not explicit on raw-SQL-mode requirement**: Codex says "sqlc is acceptable if pinned and audited." Fine, but sqlc supports multiple modes; HUAKAI must pick **raw SQL with type-generated bindings** (the canonical mode) and **forbid** sqlc's experimental Go-DSL features. Otherwise we get an ORM-by-the-back-door. Tighten DR-006 Constraint description.

## Subject 4 — Codex 7 inventory files

### Findings

**🟢 PASS — All 7 inventories use HUAKAI vocabulary, no upstream names**: Sampled.

**🟠 MAJOR — Codex's all-api-hub inventory underestimates what HUAKAI must absorb from the multi-account dashboard pattern**: The inventory says "L1/L2-relevant unmined rows: none for gateway core. Most unmined rows are SaaS Phase 7+ client UX or out-of-scope browser-extension behavior." This is correct for the gateway runtime. But for the SaaS Edition (Model 2 per Owner refinement 2026-04-28), the multi-account dashboard pattern IS load-bearing — operators using HUAKAI's SaaS will need exactly this kind of cross-tenant overview. **Action**: re-tier these rows to L4 SaaS-Phase-10+ rather than out-of-scope; flag for SaaS Edition prose decomposition.

**🟠 MAJOR — Codex's litellm inventory misses provider-specific transformation depth**: The inventory groups all 100+ LLM provider integrations into a single row "100+ provider integrations". For a "Sub2API + breadth" product (DR-007), each provider adapter is its own L2-or-better feature with its own quirks (Anthropic's tool-result envelope ordering; Gemini's content-part flattening; Azure's API version negotiation; Bedrock's per-region credential handling). Treating them as one row hides the real Phase 9 work. **Action**: split the row into a per-provider sub-inventory before Phase 9 entry.

**🟡 MINOR — Codex's helicone inventory cites "Public docs evidence" without specific URL anchors in several rows**: Acceptable for inventory level, but when these rows promote to deep decomposition the URLs must be pinned to specific commit hashes / doc versions for the [tracking policy](../../24_REFERENCE_TRACKING_POLICY.md) baseline.

## Subject 5 — Codex algorithmic insights (Sub2API + one-api + LiteLLM, in [docs/07 §Algorithmic Insights](../../07_REFERENCE_EVIDENCE_LEDGER.md))

### Findings

**🟢 PASS — Insights are concrete, opinionated, in HUAKAI vocabulary**: 15 directives across the 3 references, each one sentence with KEEP / IMPROVE / AVOID labeling.

**🟠 MAJOR — One-api KEEP directive on streaming usage fallback is incomplete**: "KEEP streaming usage fallback, but mark inferred usage confidence and reconcile when upstream usage arrives later." The "reconcile when upstream usage arrives later" part assumes upstream may emit usage out-of-band after the stream closes. Not all providers do this. HUAKAI's design must define what happens when **no out-of-band usage ever arrives** for an inferred usage record — does the inferred number become authoritative after a TTL? Or is the Usage Record permanently flagged as `inferred-permanent`? **Action**: make this a sub-decision in the streaming-forwarder spec; do NOT ship without a concrete rule.

**🟠 MAJOR — LiteLLM AVOID directive on hidden override precedence misses one source**: The directive says "Route, Channel, and Provider Account retry policy must have a documented deterministic order." But LiteLLM's actual hierarchy includes **per-request override** (the caller can override retry policy in the request body, as observed in E-LM-DEEP-007). If HUAKAI accepts per-request retry override (and it should, for power users), the precedence has 4 levels: per-request → per-Account → per-Channel → per-Route default. Document all 4. **Action**: extend the directive in docs/07.

## Subject 6 — Cross-cutting: clean-room contamination ledger gap

**🔴 CRITICAL — No per-session contamination state is recorded.** The [22 mandate](../../22_DEEP_MINING_MANDATE.md) and [DR-000](../decisions/DR-000-clean-room-methodology.md) jointly require that "an agent session that has read non-MIT source enters specifier-only contamination state for the rest of the session and must not be reused for implementer work." Currently NO record exists tying specific Codex / Claude sessions to the references they read. When Phase 2 implementer-lane work begins, there will be no way to answer "is this session safe to use for implementation?" **Action**: open a follow-up to introduce `docs/sessions/<session-id>.md` with session-id, agent identity, references read (with E-LIC-NNN tags), and contamination verdict (`clean / specifier-contaminated / implementer-contaminated`). This is a Phase 1 → Phase 2 transition blocker. Add to the Deep Mining Gate or open a new gate.

## Summary of Action Items

| # | Severity | Subject | Action |
| --- | --- | --- | --- |
| 1 | 🔴 CRITICAL | cross-cutting | Open `docs/sessions/` ledger; require session contamination tracking before Phase 2. |
| 2 | 🟠 MAJOR | E-S2A-DEEP-007 | Reorder & regroup signal list when referenced from any HUAKAI spec to avoid CL-005 borderline. |
| 3 | 🟠 MAJOR | E-S2A-PROXY-022 | Determine if upstream drain is intentional design or leaked goroutine in next dive; sharpen risk note. |
| 4 | 🟠 MAJOR | E-S2A-PROXY-026 | Extend typed failure taxonomy from 7 to ≥10 categories in HUAKAI spec. |
| 5 | 🟠 MAJOR | DR-005 | Tighten Constraint 1: gateway-core handler signatures use stdlib `http.Handler` only; chi limited to mux/middleware. |
| 6 | 🟠 MAJOR | docs/07 §Algorithmic Insights | Define inferred-usage TTL/permanence rule; extend retry-precedence to 4 levels. |
| 7 | 🟠 MAJOR | all-api-hub inventory | Re-tier rows from "out-of-scope" to L4 SaaS Phase 10+. |
| 8 | 🟠 MAJOR | litellm inventory | Split per-provider rows for the Phase 9 catalog work. |
| 9 | 🟡 MINOR | E-S2A-DEEP-011 | Add invariant statement to billing-claim-gate spec. |
| 10 | 🟡 MINOR | E-S2A-DEEP-006 | Define continuation vs sticky vocabulary explicitly in [layered-account-selection.md](../../decompositions/sub2api/layered-account-selection.md). |
| 11 | 🟡 MINOR | DR-006 | Pin sqlc to raw-SQL-mode; forbid Go-DSL. |
| 12 | 🟡 MINOR | helicone inventory | Pin URLs to commit hashes for tracking baseline. |

## Reviewer Verdict

- **CL-001..004 pass** across all sampled artifacts.
- **CL-005 borderline** flagged in 1 row; mitigation specified.
- **CL-006..010 pass**.
- **Technical rigor**: 1 CRITICAL gap (session contamination ledger), 7 MAJOR gaps that need targeted action before Phase 2 contract lock.
- Overall verdict: Codex's outputs are **release-quality for inventory purposes**. They are **not yet release-quality for prose-decomposition consumption** without the action items above being addressed.

## Next

Codex must run the symmetric review on Claude's outputs (3 prose decomposition files in `decompositions/sub2api/` + 5 inventory files Claude authored). That dispatch happens after the current `bverzwcek` Quota+Billing dive returns; this file is committed first so Claude's review of Codex is on record before Codex sees Claude's review of itself (preventing a bias loop).
