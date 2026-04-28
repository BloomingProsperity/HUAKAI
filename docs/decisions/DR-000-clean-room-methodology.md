# DR-000: Clean-Room Methodology For HUAKAI

| Field | Value |
| --- | --- |
| Status | Discussion |
| Date opened | 2026-04-27 |
| Date decided | — |
| Owner | Owner |
| Affected docs | docs/05_CLEAN_ROOM_POLICY.md, docs/06_REFERENCE_PROJECTS.md, docs/10_RISK_REGISTER.md, docs/12_AGENT_WORKFLOW.md, docs/20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md |
| Supersedes | — |
| Superseded by | — |

## Question

Which clean-room methodology does HUAKAI adopt: **A** (single-agent behavior-only discipline), **B** (two-lane separation: specifier reads references, implementer reads only specs), or **C** (textbook two-team strict)? See full option text in [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](../20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md).

## Context

- License verification (E-LIC-001..004 in [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md)) found:
  - Sub2API: LGPL-3.0
  - New API: AGPL-3.0-or-later
  - All API Hub: AGPL-3.0
  - one-api: MIT (anchor reference)
- AGPL is triggered by **network distribution**, which is exactly how this gateway will be deployed.
- [05_CLEAN_ROOM_POLICY.md](../05_CLEAN_ROOM_POLICY.md) declares a clean-room rule but did not previously specify how isolation between reading-references and writing-implementation is enforced.
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

- **Critique of Claude's view:** _(Codex to fill)_
- **Production / testability concerns:** _(Codex to fill — e.g. does Option B make scenario tests easier or harder; can spec files be turned into acceptance test inputs)_
- **License / dependency concerns:** _(Codex to fill)_
- **Recommendation:** _(A / B / C / custom — Codex to fill)_
- **Confidence:** _(Codex to fill)_
- **Updated:** _(Codex to fill)_

## Gemini (UI / Ops) view

> Edited only by Gemini.

- **UI / operability impact:** _(Gemini to fill — e.g. how much UI evidence is needed from AGPL UIs vs how much can be designed from product contracts)_
- **API-shape impact:** _(Gemini to fill)_
- **Operator workflow concerns:** _(Gemini to fill)_
- **Recommendation:** _(A / B / C / custom — Gemini to fill)_
- **Confidence:** _(Gemini to fill)_
- **Updated:** _(Gemini to fill)_

## Conflicts

> Synthesized by Claude PM after the three views are in.

- _(none yet — Codex and Gemini have not filled their sections)_

## Owner Decision

> Owner only.

| Field | Value |
| --- | --- |
| Decision | _(Owner to write: A / B / C / custom)_ |
| Decision date | _(YYYY-MM-DD)_ |
| Reasoning | _(short)_ |
| Constraints attached | _(e.g. "Option B with Option C carve-out for billing ledger work")_ |

## Propagation Checklist

- [ ] Update [05_CLEAN_ROOM_POLICY.md](../05_CLEAN_ROOM_POLICY.md) — replace "methodology decision pending" with the chosen option, inline.
- [ ] Update [12_AGENT_WORKFLOW.md](../12_AGENT_WORKFLOW.md) — add lane definitions if Option B or C.
- [ ] Update [10_RISK_REGISTER.md](../10_RISK_REGISTER.md) R-LIC-001 — sharpen mitigation to the chosen option.
- [ ] Update [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](../20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md) — add a "Decided" header pointing to this DR.
- [ ] If Option B or C: add `docs/specs/` directory and a spec template.
- [ ] Mark Status = Implemented when all above are done.
