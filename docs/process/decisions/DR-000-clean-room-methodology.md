# DR-000: Clean-Room Methodology For HUAKAI

| Field | Value |
| --- | --- |
| Status | Implemented |
| Date opened | 2026-04-27 |
| Date decided | 2026-04-28 |
| Date implemented | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/05_CLEAN_ROOM_POLICY.md, docs/06_REFERENCE_PROJECTS.md, docs/10_RISK_REGISTER.md, docs/12_AGENT_WORKFLOW.md, docs/20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md |
| Supersedes | — |
| Superseded by | — |

## Question

Which clean-room methodology does HUAKAI adopt: **A** (single-agent behavior-only discipline), **B** (two-lane separation: specifier reads references, implementer reads only specs), or **C** (textbook two-team strict)? See full option text in [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](../../20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md).

## Context

- License verification (E-LIC-001..004 in [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md)) found:
  - Sub2API: LGPL-3.0
  - New API: AGPL-3.0-or-later
  - All API Hub: AGPL-3.0
  - one-api: MIT (anchor reference)
- AGPL is triggered by **network distribution**, which is exactly how this gateway will be deployed.
- [05_CLEAN_ROOM_POLICY.md](../../05_CLEAN_ROOM_POLICY.md) declares a clean-room rule but did not previously specify how isolation between reading-references and writing-implementation is enforced.
- Until this DR is Decided, agents operate as if Option B were chosen.

## Claude (PM-Orchestrator) view

> Edited only by Claude.

- **Analysis:** Two of three primary references are AGPL, which is the strongest practical copyleft for a network service. Option A ("be careful") leaves no structural defense if a contamination claim is ever made. Option C is the gold standard but is heavy: it forbids the implementer from even reading public docs, which is hard to sustain across many features. Option B preserves an inspectable artifact (the spec file) at every reference-to-implementation boundary, which both reduces contamination risk and produces a durable design record. The cost of B over A is one extra file per non-trivial feature, which is small relative to the legal exposure reduction.
- **Recommendation:** **Option B** as the project default. Allow Option C selectively for features judged highest-risk (e.g. anything where the AGPL reference is the only realistic source of behavior knowledge — billing ledger, weighted routing edge cases, account pool optimization).
- **Risks if adopted:** Coordination overhead per feature; agents need explicit tracking of which lane they are in for each session; specifier-lane sessions must be marked "contaminated" and not reused for implementer work.
- **Risks if rejected:** Under Option A, future legal review may classify the project as a derivative; under Option C, velocity drops and the implementer cannot read even safe MIT references like one-api, which is wasteful given that one-api is a viable safe-reading source.
- **Confidence:** High
- **Updated:** 2026-04-27

## Codex (Reviewer) view

> Edited only by Codex.

- **Critique of Claude's view:** I agree with Claude's Option B recommendation, but the success condition is stricter than "one extra spec file." Option B only reduces contamination risk if spec files are treated as the sole implementation input for non-MIT references and are reviewed for leakage before implementers or test writers use them. A weak spec that copies upstream names, schemas, UI structure, or algorithmic detail would simply move contamination from code into docs.
- **Production / testability concerns:** Option B improves testability if every spec includes actors, preconditions, normal path, failure path, operator recovery, audit/log evidence, and acceptance-test direction. It can hurt delivery if specs are vague, because implementers will fill gaps from memory or external reference exposure. Specifier-lane agents should not write implementation tests directly; they should write behavior scenarios that the clean implementer lane converts into local tests.
- **License / dependency concerns:** Option A is too weak for AGPL-adjacent gateway work because this product is explicitly a network service. Option C is strongest but likely too slow as the default. Option B is the right baseline, with Option C reserved for high-risk areas where AGPL references are the main source of behavior knowledge, especially billing ledger behavior, account-pool routing edge cases, and provider failover/account-health heuristics. MIT/BSD/Apache references may be read more freely, but still should not override local contracts or attribution requirements.
- **Recommendation:** Option B as the default methodology, plus an explicit Option C carve-out for highest-risk AGPL-derived feature areas. Add a `docs/specs/` template before Phase 1 evidence mining becomes implementation-facing.
- **Confidence:** High
- **Updated:** 2026-04-28

## Gemini (UI / Ops) view

> Edited only by Gemini. **Section deferred by Owner (2026-04-28).**

- **Status:** No Gemini input collected for this DR. Owner has elected to skip Gemini's view because admin UI work is deferred to Phase 7+ per [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md), and the clean-room methodology choice has limited immediate UI/Ops impact at this phase. If a later DR materially intersects UI clean-room concerns (e.g. how much UI evidence may be borrowed from AGPL admin dashboards), this DR may be revisited or a follow-up DR opened.
- **Updated:** 2026-04-28

## Conflicts

> Synthesized by Claude PM after the three views are in.

No material conflicts. Claude and Codex both recommend **Option B** as the default. Codex's section sharpens — not opposes — the recommendation with two refinements:

1. **Option C carve-out for highest-risk AGPL-derived feature areas.** Codex names billing ledger behavior, account-pool routing edge cases, and provider failover / account-health heuristics. Claude's original framing already allowed selective Option C use; Codex's contribution is making the carve-out list explicit instead of left to per-feature judgment.
2. **Spec-leakage review is a hard prerequisite.** Claude's "one extra spec file" framing was too lightweight. A spec that copies upstream names, schemas, UI structure, or algorithmic detail just moves contamination from code into docs. A spec-review step (checklist or [.agents/skills/](../../../.agents/skills/) skill) must exist before any spec is released from the specifier lane to the implementer lane.

Gemini view deferred by Owner (see above) — no UI/Ops dimension to reconcile in this DR.

**Synthesized recommendation entering the Owner Decision:** Option B as default, with Option C carve-out for the three named high-risk areas, and a mandatory spec-leakage review step that must be in place before Phase 1 evidence work becomes implementation-facing.

## Owner Decision

> Owner only.

| Field | Value |
| --- | --- |
| Decision | Option B (two-lane separation) as default + Option C carve-out for billing ledger, account-pool routing, and provider failover/account-health heuristics. |
| Decision date | 2026-04-28 |
| Reasoning | AGPL exposure (2 of 3 primary references) makes Option A too weak as a defense if a contamination claim ever arises, and is incompatible with the SaaS Distribution Edition planned in [DR-002](DR-002-product-editions.md). Option C across-the-board would block reading even MIT one-api which is wasteful given one-api is the safe anchor. Codex's carve-out + spec-leakage review addresses the "spec becomes contamination vector" failure mode without crippling velocity. |
| Constraints attached | A spec-leakage review checklist or skill must exist before specs leave the specifier lane and enter implementer-facing work. Lane definitions must be reflected in [12_AGENT_WORKFLOW.md](../../12_AGENT_WORKFLOW.md). |

## Propagation Checklist

- [x] Update [05_CLEAN_ROOM_POLICY.md](../../05_CLEAN_ROOM_POLICY.md) — Methodology section replaced; lane definitions, carve-out list, and spec-leakage review pointer added.
- [x] Update [12_AGENT_WORKFLOW.md](../../12_AGENT_WORKFLOW.md) — Clean-Room Lanes section added with role-to-lane mapping.
- [x] Update [10_RISK_REGISTER.md](../../10_RISK_REGISTER.md) R-LIC-001 — Mitigation sharpened to reference DR-000; status flipped to Mitigated.
- [x] Update [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](../../20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md) — Decided header added at top pointing to DR-000.
- [x] Add `docs/specs/` directory with [README](../../specs/README.md), [_TEMPLATE.md](../../specs/_TEMPLATE.md), and [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md) (10 checks CL-001..010).
- [x] Mark Status = Implemented (header above).
