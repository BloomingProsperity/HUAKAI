This file is the canonical decision record template. Copy to `DR-NNN-<slug>.md` and fill in.

# DR-NNN: <Short Title>

| Field | Value |
| --- | --- |
| Status | Open / Discussion / Decided / Implemented / Superseded |
| Date opened | YYYY-MM-DD |
| Date decided | YYYY-MM-DD or — |
| Owner | <name> |
| Affected docs | <comma-separated paths> |
| Supersedes | — |
| Superseded by | — |

## Question

<One paragraph. Ideally one sentence. State the choice precisely.>

## Context

- Why this came up.
- Trigger (an issue, a license fact, a phase gate, a stakeholder ask).
- Links to relevant evidence rows ([07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md)), risks ([10_RISK_REGISTER.md](../10_RISK_REGISTER.md)), or contracts.

## Claude (PM-Orchestrator) view

> Edited only by Claude.

- **Analysis:** <…>
- **Recommendation:** <…>
- **Risks if adopted:** <…>
- **Risks if rejected:** <…>
- **Confidence:** Low / Medium / High
- **Updated:** YYYY-MM-DD

## Codex (Reviewer) view

> Edited only by Codex.

- **Critique of Claude's view:** <…>
- **Production / testability concerns:** <…>
- **License / dependency concerns:** <…>
- **Recommendation:** <…>
- **Confidence:** Low / Medium / High
- **Updated:** YYYY-MM-DD

## Gemini (UI / Ops) view

> Edited only by Gemini.

- **UI / operability impact:** <…>
- **API-shape impact:** <…>
- **Operator workflow concerns:** <…>
- **Recommendation:** <…>
- **Confidence:** Low / Medium / High
- **Updated:** YYYY-MM-DD

## Conflicts

> Synthesized by Claude PM after the three views are in. Cosmetic disagreements omitted.

- [Topic]: <Claude says X> vs <Codex says Y> — material reason: <one line>
- (none) if all three views align.

## Owner Decision

> Owner only.

| Field | Value |
| --- | --- |
| Decision | <chosen option> |
| Decision date | YYYY-MM-DD |
| Reasoning | <short> |
| Constraints attached | <e.g. "with Codex's mitigation X also adopted"> |

## Propagation Checklist

- [ ] Update <doc-1> to reference DR-NNN.
- [ ] Update <doc-2>.
- [ ] Update [10_RISK_REGISTER.md](../10_RISK_REGISTER.md) if applicable.
- [ ] Update [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) if applicable.
- [ ] Update affected skill files under [.agents/skills/](../../.agents/skills/) if applicable.
- [ ] Mark Status = Implemented when all above are done.

## Supersession Note

If a later DR replaces this one, fill the `Superseded by` header field. Do not delete or rewrite Decided content; the trail is the artifact.
