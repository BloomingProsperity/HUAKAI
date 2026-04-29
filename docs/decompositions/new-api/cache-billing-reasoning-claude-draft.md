# `new-api` — Cache-Aware Billing + Reasoning-Effort Pass-Through (Claude draft)

| Field | Value |
| --- | --- |
| Status | Draft (Claude lane parallel viewpoint to Codex T5 specifier) |
| Reference | New API (AGPL-3.0, [E-LIC-002]) |
| Feature in HUAKAI matrix | F-BILL-003 (L3 Phase 6+) + F-MODEL-001 (L2) |
| Evidence ledger anchors | E-NAI-001 (cache-aware billing), E-NAI-004 (reasoning-effort) |
| Specifier session | Claude PM-Orchestrator, parallel viewpoint, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD (will pair with Codex T5 source-verified output for synthesis) |
| Companion artifact | docs/decompositions/new-api/cache-billing-reasoning-source-verified.md (Codex T5 — source-verified) |
| Source files read | NONE directly. This draft reasons from the project's public README, the existing inventory at `docs/decompositions/new-api/_INVENTORY.md`, the parent project's source-verified decomposition at `docs/decompositions/one-api/quota-billing-source-verified.md`, and HUAKAI's own [F-BILL spec column](../../specs/observability-billing.md). Codex T5 will produce the source-verified counterpart; synthesis combines. |

> **Lane discipline**: This file is intentionally NOT source-verified. It exists because Owner directive 2026-04-29 (`所有的动作和行为都要和codex进行交叉处理`) requires Claude to operate as an independent specifier-lane viewpoint, not just an orchestrator. Where this draft and the Codex source-verified version disagree on structural facts, the Codex version wins. Where they disagree on HUAKAI-fit framing, this draft contributes. The synthesis step reconciles.

## 1. WHY (motivation in HUAKAI vocabulary)

Two pressures converge on this surface:

1. **Cache-pricing reality**. Modern providers (Anthropic prompt caching, Google implicit cache, OpenAI cached-tier) charge cached-input tokens at a fraction of fresh-input pricing — often 10–25% of the fresh rate. A relay-station that bills the User as "fresh tokens × fresh rate" while paying the upstream "cached tokens × cached rate" turns systematic operator profit. Conversely, billing the User at the upstream's cached rate but settling against fresh rate exhausts operator margin under a popular-prompt workload. Either misalignment surfaces only when an operator's accounts age and prompt caches warm; both directions silently corrupt the Owner's two business models in [01_PROJECT_BRIEF.md].

2. **Reasoning-effort fairness**. Newer reasoning models (o-series, Claude extended-thinking, Gemini deep-think) consume thinking tokens whose cost is real but whose user-visible value is a *signal* rather than a *substring* of the answer. Charging an end User the full reasoning budget when the User asked for "low effort" is theft; charging only output tokens when the User asked for "high effort" is freight. The relay must propagate User intent to the upstream and faithfully reflect upstream's reasoning cost back into the Usage Record.

new-api is the reference because its parent project (one-api) lacks both surfaces; new-api's distinctive contribution is **adding cache-tier and reasoning-tier dimensions to the existing pricing model without breaking parent compatibility**.

## 2. WHAT (algorithm in HUAKAI vocabulary — structural; algorithm details await Codex T5)

### 2.1 Cache-aware billing

The billing event row carries **token-count fields that split a single upstream call's input into at least three buckets**:

- **fresh input tokens** (priced at full input rate)
- **cache-creation tokens** (priced typically at parent rate × cache-creation multiplier; a one-time premium because the upstream paid to compute and persist the cache breakpoint)
- **cache-read tokens** (priced at the cached rate, typically a small fraction of fresh)

A fourth bucket may exist depending on the upstream's billing surface:

- **cache-eviction or cache-warm tokens** (some providers expose a dedicated cost line for cache state changes)

The pricing table is per-(provider, model) with **per-bucket multipliers**, not a single price-per-input-token. The settler in F-OBS-001 §Tx2 must:

1. Read the upstream's response and identify which token-count fields apply to which bucket (a per-provider mapping).
2. Look up the active billing-policy version's bucket multipliers.
3. Compute fresh / cache-create / cache-read contributions separately.
4. Sum into `actual_cost` with full decimal precision (numeric(20,8)).
5. Persist the bucketed split in the Usage Record so analytics can reconstruct margin per bucket.

### 2.2 Reasoning-effort pass-through

Three independent dimensions:

- **End-User intent dimension**: User can request reasoning effort (none / low / medium / high) via a parameter in the canonical request shape.
- **Provider acceptance dimension**: Each upstream supports a different effort vocabulary (some accept "high/medium/low", some accept a token-budget integer, some require a model-tier variant, some ignore effort entirely).
- **Cost / settlement dimension**: Reasoning tokens consumed appear separately in the upstream usage report and must be priced separately from output tokens (typically at a higher rate, because reasoning is uncached compute).

The relay translates User intent into the provider-acceptable shape during F-PROTO-002 protocol translation; the streaming forwarder (F-GW-002) extracts the reasoning-token count from the per-event usage stream into the same accumulator that already tracks input/output; the settler treats reasoning tokens as a separate billing bucket per §2.1.

## 3. INPUTS (HUAKAI signals)

- Per-request: requested effort level, model id (some models *imply* effort), tenant policy on reasoning visibility (whether to pass-through reasoning_summary or strip it).
- Per-Provider: bucket multipliers, effort vocabulary mapping, reasoning-supported flag.
- Per-Account: optional account-level pricing override for tenants with negotiated provider rates.
- Per-Tenant: cache-billing policy version, effort-billing policy version, currency code.
- Time: billing-policy effective_at (so historical Usage Records reconstruct the rate that was active when settled, not the rate currently configured).

## 4. FAILURE MODES HANDLED

- **Upstream returns ambiguous bucket counts**: usage_source = `partial`; pending_reconciliation = true; Usage Record committed with best-effort split; reconciliation worker corrects from authoritative provider-billing API later.
- **Provider doesn't expose cache breakdown**: bucket all input as fresh; record `cache_breakdown_unavailable=true` in Usage Record metadata; surface on operator dashboard so operator can choose to pull cache breakdown from a different signal (e.g. response headers).
- **User-supplied effort exceeds account tier limit**: clamp to the maximum the tier allows; surface a clamp event on Usage Record so end-User can be billed correctly and the abuse pattern (always-max-effort accounts) is visible.
- **Effort-supported model migrated mid-stream**: stream forwarder must propagate the model-id-actually-used into the Usage Record (not the requested model-id); settler reads from the actually-used field.
- **Cache breakpoint expired silently**: upstream reports zero cache-read; relay handles as fresh-input billing without operator surprise.

## 5. INTERFACES TO HUAKAI

- **F-BILL-003**: bucket-multiplier pricing table is a new schema table (`pricing_buckets`) keyed by (provider_id, model_id, billing_policy_version, bucket_kind).
- **F-MODEL-001**: capability matrix in F-PROTO-002 already has `reasoning_summary` cell — add a parallel `reasoning_effort_levels` cell that lists the levels each (client, upstream) pair supports.
- **F-OBS-001 Usage Record**: add columns `cache_creation_tokens`, `cache_read_tokens`, `reasoning_tokens` (all NOT NULL DEFAULT 0). Already present in current schema (verified via `docs/schema/observability-billing.sql`); confirms HUAKAI's schema anticipates this surface.
- **Operator UI** (F-OPS-003 future): bucket-margin dashboard so operator can see "fresh input margin = X%", "cache-read margin = Y%" per tenant, per model, over a window.

## 6. RISKS HUAKAI MUST GUARD AGAINST

- **Margin erosion via miscategorization**: a single mis-mapped bucket (e.g. counting cache-read as fresh) inverts the operator's margin. AT-OBS-014 money-precision test alone is insufficient; need an AT that asserts **bucket categorization correctness** when upstream returns cache-aware response shape.
- **Reasoning-effort downgrade attack**: User claims "high effort" to upstream (paying for it), then operator silently translates to "low" (paying upstream low, billing User high). Audit must record both requested and actually-used effort levels.
- **Pricing-policy version drift**: per-Tenant policy version vs per-billing-event policy version vs operator's current configured version can diverge across migrations. Usage Record MUST reference the version active at settlement time; never assume "current".
- **Cache-creation cost double-counting** if the same prompt is sent twice within the cache TTL: only the first call should pay cache-creation; second pays cache-read. Settler must distinguish via upstream's `cache_creation_input_tokens` field being zero on the second call.

## 7. SAFE ADAPTATION FOR HUAKAI (clean-room divergences from new-api)

- new-api operates as a single-tenant gateway by default; HUAKAI is multi-tenant (DR-001). Bucket multipliers MUST be per-tenant-overridable, not global.
- new-api's settler likely runs synchronously inline; HUAKAI's F-OBS-001 Tx2 is transactional with outbox semantics. Bucket categorization happens inside Tx2 — do NOT defer to async worker (loses durability).
- new-api may not expose pricing-policy version at the Usage Record level; HUAKAI MUST (per spec line 8 of observability-billing.md) so historical bills are reconstructible.
- AGPL constraint: HUAKAI implements the *behavior* (bucket-multiplier pricing + reasoning-effort propagation) using a structurally different ER design (PostgreSQL `pricing_buckets` keyed table) rather than translating new-api's internal types.

## 8. EVIDENCE LEDGER ROWS NEEDED

- E-NAI-001 (existing): cache-aware billing — promote from shallow to deep when Codex T5 source-verified lands.
- E-NAI-004 (existing): reasoning-effort handling — promote similarly.
- E-NAI-NEW: bucket-categorization correctness AT (Codex T5 + this draft together justify a new ledger row).

## 9. OPEN QUESTIONS (for Codex T5 to resolve from source)

1. Does new-api hold a per-bucket multiplier or a flat percent-of-fresh discount? Synthesis depends on this.
2. Does new-api source allow per-channel pricing override or only per-provider?
3. How does new-api detect cache-read vs cache-creation when the upstream provides only one combined `cache_input_tokens` number?
4. What does new-api do when reasoning-effort is requested but the model doesn't support it — error, silently downgrade, or pass through unchanged?
5. Is new-api's Usage Record schema already migration-versioned, or does it rely on field nullability?

## Owner Chinese summary

本 draft 是 Claude lane 对 new-api **缓存差价计费 + 推理强度透传**两个亮点的独立结构化拆解，**没读源码**，靠 inventory + 一-api 父项目源拆 + HUAKAI 既有 spec 推理。和 Codex T5 完成的 source-verified 文件配对后做 synthesis，结构事实以 Codex 版为准；HUAKAI-fit 风险框架（多租户分摊、margin 蒸发、effort 降级攻击、policy 版本漂移）以本 draft 为初稿。最大未决问题：bucket 分类是 multiplier 还是 percent，以及 cache-read vs cache-creation 的检测信号——等 T5 源核验回来再合成。
