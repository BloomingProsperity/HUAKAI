This file is agent-facing and authoritative.

# Deep Mining Mandate

> **Owner directive 2026-04-28:** "必须每个项目每个功能单独的拆解，学习，优化。" — Every reference, every feature, decomposed individually; learned; optimized for HUAKAI.

## Why This Exists

Phase 1 first pass mined READMEs only — 8–15 high-level behaviors per reference, grouped at the feature-area level. Owner judged this too shallow. Phase 1 second pass began deep source reads (E-OAI-DEEP-*, E-LM-DEEP-*, E-S2A-DEEP-*) but the discipline was uneven — some features had source-verified evidence, others only README-derived.

This mandate fixes the unevenness. **No feature shall enter implementation without a per-reference × per-feature deep decomposition that meets the acceptance bar below.**

## Owner Sharpening 2026-04-28

> "必须对借鉴项目的功能一个一个的拆解！不能只读潜在的表象。"

A row in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md) is **not enough**. A 100-character table cell, even if marked `Source code (deep read)`, is still surface-form. The mandate now requires:

- **One file per `(reference, feature)` pair** under [decompositions/](decompositions/), prose-form, ~600–1500 words.
- The decomposition file expands the seven fields below into paragraphs with concrete edge cases, signals, state transitions, race windows, and operator-visible artifacts.
- The ledger row in `docs/07` becomes a one-line index entry pointing at the decomposition file. The decomposition file is where the work lives.
- A reference's features are **enumerated first** (full feature inventory) before any deep dive begins — no "I dove into the 3 features I happened to find"; the inventory is owned by the specifier-lane agent assigned to that reference, and the inventory is auditable.
- **The inventory lives at [`docs/decompositions/<reference>/_INVENTORY.md`](decompositions/)** and lists every feature area in the reference, each with a Status column: `unmined / shallow-evidence / deep-decomposed (linked file)`. Owner directive 2026-04-28: "整体代码和逻辑都读完" — the inventory is the audit instrument that proves comprehensive coverage. A reference whose inventory has any L1/L2-relevant feature in `unmined` state cannot be cited as Phase-1-complete.

## What Is Mandatory

For every feature row in [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) that targets **L1 MVP** or **L2 Production Usable**:

1. **Source-code-verified evidence** from at least one cited reference. README-only evidence is insufficient; the row must cite at least one `E-X-DEEP-NNN` ID whose `Source Type` is `Source code (deep read)` or `Source code (Codex deep read)`.
2. **Per-reference decomposition** when the feature appears in multiple references. If F-XXX cites three references, the deep decomposition must cover each reference's implementation separately (one E-X-DEEP-NNN row per reference). Common ground is then synthesized in HUAKAI's local design.
3. **Algorithmic insight**: a `KEEP / IMPROVE / AVOID` analysis recorded in [07_REFERENCE_EVIDENCE_LEDGER.md §Algorithmic Insights for HUAKAI Core](07_REFERENCE_EVIDENCE_LEDGER.md) for every L1/L2 feature.
4. **Reviewer sign-off**: a different agent session (or a fresh session of the same agent with no prior context) reviews the decomposition for clean-room compliance against [_REVIEW_CHECKLIST.md](specs/_REVIEW_CHECKLIST.md) (CL-001..010).

For features targeting **L3 / L4**: shallow (README-derived) evidence is acceptable until the feature is promoted to active scope. When promoted, the deep-decomposition mandate applies.

## Decomposition Record Format

Every E-X-DEEP-NNN row that satisfies this mandate must implicitly answer the seven fields below. The fields may be carried in the row's `Observed Behavior` and `Feature Candidate` cells if compact, or expanded in a per-feature note when needed:

1. **WHY** the upstream chose this design (motivation / context).
2. **WHAT** the upstream actually does, in HUAKAI vocabulary (algorithm steps, signals consumed, state mutated).
3. **INPUTS** consumed by the algorithm (per-request data, per-Account state, per-User state, time, randomness).
4. **FAILURE MODES HANDLED** by the upstream and how.
5. **FAILURE MODES NOT HANDLED** (gaps the upstream leaves; HUAKAI's chance to do better).
6. **HUAKAI's KEEP / IMPROVE / AVOID** decision, justified.
7. **ATTRIBUTION**: the verified URL of the source file read; the agent and session that read it.

## Acceptance Criteria (Per Feature)

A feature row in [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) is **deep-decomposed** when ALL of these hold:

- [ ] The row's `Evidence ID` column cites at least one `E-X-DEEP-NNN` row.
- [ ] If the feature is multi-source (cites multiple references), each cited reference contributes at least one `E-X-DEEP-NNN` row.
- [ ] The corresponding `E-X-DEEP-NNN` row(s) carry source-code-verified attribution (verified URL of the source file read, in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md)).
- [ ] **One prose-form decomposition file exists at `docs/decompositions/<reference>/<feature-slug>.md`** (Owner Sharpening 2026-04-28). The file is ~600–1500 words and answers all seven fields (WHY / WHAT / INPUTS / FAILURES HANDLED / FAILURES NOT HANDLED / KEEP-IMPROVE-AVOID / ATTRIBUTION).
- [ ] At least one HUAKAI `KEEP / IMPROVE / AVOID` directive in [07 §Algorithmic Insights](07_REFERENCE_EVIDENCE_LEDGER.md) addresses this feature.
- [ ] If the feature is in the [Option C carve-out list](process/decisions/DR-000-clean-room-methodology.md) (billing reconciliation, pool-aware routing, provider failover/account-health), an Option C strict spec exists in [specs/](specs/) for it AND that spec passed CL-001..010 review.
- [ ] No upstream function name, schema column name, file path, or distinctive identifier appears anywhere in the deep-evidence rows, decomposition files, or downstream specs.

## Phase Exit Gate

**Phase 1 cannot exit** while any L1 MVP feature lacks deep-decomposition coverage. Specifically:

- Every L1 MVP row in [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) must satisfy the acceptance criteria above.
- The audit is performed at Phase 1 → Phase 2 transition by Codex via [.agents/skills/feature-parity-auditor/SKILL.md](../.agents/skills/feature-parity-auditor/SKILL.md), with new check items added to that skill: (a) every L1/L2 row cites at least one E-X-DEEP-NNN, (b) every cited reference for a multi-source row contributes a deep row, (c) the Algorithmic Insights section has a corresponding directive.
- Failures block Phase 2 contract lock per [15_RELEASE_GATES.md §Acceptance Gate](15_RELEASE_GATES.md).

**Phase 9 (parity closure)** extends the mandate to all L3 features. **Phase 10+ (SaaS Edition)** extends to all L4.

## Per-Reference Coverage Tracking

Each reference project tracked in [06_REFERENCE_PROJECTS.md](06_REFERENCE_PROJECTS.md) carries a coverage state in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md):

| Reference | Source-code reads to date | Mandated next dives |
| --- | --- | --- |
| one-api (MIT) | controller/relay.go, model/token.go, plus Codex deep dive on relay/forwarder/quota/streaming | Streaming forwarder edge cases (partial-stream failure mid-flush) |
| LiteLLM (MIT) | router_utils/cooldown_handlers.py, plus Codex deep dive on retry / fallback chain / concurrency / TPM | Provider-specific adapters (anthropic, gemini) for transformation pipeline |
| Sub2API (LGPL-3.0) | service/channel_monitor_service.go, plus Codex deep dive on dispatch / billing / health / availability | **Reverse-proxy hot path** (request transform / streaming / cancellation) — Codex dispatched 2026-04-28 |
| New API (AGPL-3.0) | README only | Cache-aware billing logic; protocol-translation matrix; reasoning-effort handling |
| All API Hub (AGPL-3.0) | README only (browser-extension, limited gateway value) | Multi-source admin UX patterns when SaaS Edition admin work begins |
| Portkey (MIT) | README only | Guardrail engine; semantic cache; WebSocket realtime |
| Helicone (GPL-3.0) | README only | Performance-aware routing signal aggregation; OpenTelemetry surface |
| Envoy AI Gateway (Apache-2.0) | README only | Two-tier topology; endpoint-picker policy; K8s CRD shape |

## Workflow

When mining a new feature or reaching Phase 1 → Phase 2 audit:

1. List the features (F-* IDs) at the target L-level that lack deep coverage.
2. For each, identify the cited references and read each reference's source for that specific feature (specifier-lane).
3. Write the E-X-DEEP-NNN row to [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md).
4. Update the [Algorithmic Insights for HUAKAI Core](07_REFERENCE_EVIDENCE_LEDGER.md) with KEEP / IMPROVE / AVOID for the feature.
5. If the feature is in DR-000 Option C carve-out, write the Option C spec under [specs/](specs/) and submit it for CL-001..010 review.
6. Mark the feature row's Status in [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) with `Open (deep-decomposed)` once the acceptance criteria above are satisfied.
7. The Phase 1 → Phase 2 audit verifies every L1/L2 row carries the `(deep-decomposed)` marker.

## Discipline Reminder

The point of this mandate is **algorithm quality**, not paperwork. Owner directive: "算法要优化，尤其是核心" — the core algorithms must be optimized, especially the core. Reading source deeply is the only way to know what the upstream actually does, so HUAKAI can decide whether to keep, improve, or avoid each piece. Skipping the deep read and writing a generic spec produces a generic gateway, not a relay-station product the user signed up to build.
