This file is agent-facing and authoritative.

# Reference Tracking and Continuous Learning Policy

> **Owner directive 2026-04-28:** "我们后续的维护也主要看借鉴平台的更新，他们更新后我们吸取问题，然后自查，更新我们的产品。"

## Why This Policy Exists

Phase 1 mined the 8 reference projects at one point in time. Those projects keep evolving — they ship new features, fix bugs, change algorithms, and respond to security advisories. HUAKAI's competitive position and operational quality depend on **tracking those updates and incorporating their lessons**, not on a one-time mining pass.

This policy operationalizes that maintenance discipline as a binding rule: every release window of every tracked reference triggers a HUAKAI self-audit cycle.

## Tracked References

The actively-maintained references tracked (one-api retired 2026-05-28 — see Reference Eligibility below):

| Reference | License | GitHub repo | Release feed | Issue feed | Tracking owner |
| --- | --- | --- | --- | --- | --- |
| Sub2API | LGPL-3.0 | [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) | `/releases.atom` | `/issues.atom` | Claude PM |
| New API | AGPL-3.0 | [QuantumNous/new-api](https://github.com/QuantumNous/new-api) | `/releases.atom` | `/issues.atom` | Claude PM |
| LiteLLM | MIT | [BerriAI/litellm](https://github.com/BerriAI/litellm) | `/releases.atom` | `/issues.atom` | Claude PM |
| Portkey | MIT | [Portkey-AI/gateway](https://github.com/Portkey-AI/gateway) | `/releases.atom` | `/issues.atom` | Claude PM |
| Helicone | GPL-3.0 | [Helicone/ai-gateway](https://github.com/Helicone/ai-gateway) | `/releases.atom` | `/issues.atom` | Claude PM |
| Envoy AI Gateway | Apache-2.0 | [envoyproxy/ai-gateway](https://github.com/envoyproxy/ai-gateway) | `/releases.atom` | `/issues.atom` | Claude PM |
| All API Hub | AGPL-3.0 | [qixing-jk/all-api-hub](https://github.com/qixing-jk/all-api-hub) | `/releases.atom` | `/issues.atom` | Gemini (UI ops) |

## Reference Eligibility (added 2026-05-28 Owner directive)

Tracked references MUST be actively maintained. A reference that becomes abandoned or is superseded by an active fork is RETIRED from this list (its historical evidence is preserved in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md) as provenance, but it is no longer tracked, cited for new decisions, or used as a clean-room anchor). **Retired:** one-api (last upstream commit 2025-02-21, ~15 months stale; superseded by its active fork New API). Re-examine all references for maintenance status periodically; prune the abandoned ones.

## Cadence

Three review cycles, each with a different scope:

### Per-Release Review (event-driven, within 7 days of release)

When ANY tracked reference cuts a new release (major / minor / patch):

1. Read the release notes / CHANGELOG / closed-issue list since prior version.
2. For each material change, classify it into one of:
   - **Bug fix** (upstream fixed a bug) — does HUAKAI have the same bug? Self-audit our corresponding decomposition + spec + code.
   - **Algorithmic change** (upstream changed an algorithm) — re-read our prose decomposition for that algorithm; update if upstream's new design is better; explicitly reject if HUAKAI's design is already better.
   - **New feature** (upstream added a feature) — does HUAKAI care? If yes, add as a candidate row to [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) and queue for Mandated Next Dives.
   - **Security advisory** (CVE or equivalent) — fast-path. Same-day audit of HUAKAI for the same vulnerability shape.
   - **License change** — re-verify [E-LIC-NNN](07_REFERENCE_EVIDENCE_LEDGER.md) row; update [docs/06](06_REFERENCE_PROJECTS.md) if changed.
3. Record findings in `docs/tracking/<reference>/YYYY-MM-DD-vXX.md` (one file per reviewed release; format below).
4. Open follow-up issues / spec updates / matrix promotions as needed.

### Monthly Sweep (calendar-driven, last business day of month)

Every month, regardless of release activity:

1. Walk every tracked reference's commit log since last sweep.
2. Identify behavioral changes that did not ship as a release (work-in-progress on main).
3. Identify recurring issue clusters (e.g. multiple users reporting the same problem upstream).
4. Update [22 §Per-Reference Coverage Tracking](22_DEEP_MINING_MANDATE.md) with any drift between current decomposition state and current upstream behavior.
5. Record in `docs/tracking/<reference>/YYYY-MM-monthly.md`.

### Quarterly Strategic Review (calendar-driven, end of quarter)

Every quarter:

1. Compare HUAKAI's feature parity matrix against each reference's current feature surface.
2. Identify features that have appeared upstream but are NOT in HUAKAI's matrix — propose F-* rows.
3. Identify HUAKAI features that are no longer aligned with the upstream intent (drift).
4. Update [DR-007](process/decisions/DR-007-product-positioning-and-breadth.md) success criteria 2 (catalog comparison) with current numbers.
5. Owner review of strategic direction relative to reference movement.
6. Record in `docs/tracking/_quarterly-YYYYqN.md`.

## Tracking Entry Format

Every per-release / monthly / quarterly review writes a markdown entry. Template:

```markdown
# <reference> v<version> review (YYYY-MM-DD)

| Field | Value |
| --- | --- |
| Reference | <name + license + commit/tag> |
| Reviewer | <agent + session> |
| Cadence | per-release / monthly / quarterly |
| Trigger | <release notes URL or commit range> |

## Material changes

### <change category>: <change summary>
- **What changed**: <one-paragraph behavior diff in HUAKAI vocabulary>
- **HUAKAI impact**: <which prose decomposition / spec / matrix row is affected>
- **Action**: <Promote / Demote / Patch / Ignore (with reason)>
- **Owner**: <agent assigned to follow-up>
- **Followup link**: <docs/decompositions/...md or specs/...md or matrix row>

(repeat per material change)

## Self-audit

For each upstream bug fix in this release, the corresponding HUAKAI prose decomposition or spec was checked. Findings:

- <vulnerability shape> — HUAKAI status: VULNERABLE / SAFE-BY-DESIGN / SAFE-BY-CODE / UNKNOWN-NEED-INVESTIGATION

## Open questions for Owner
- <list>
```

## Roles

| Cadence | Driver | Reviewer | Approver |
| --- | --- | --- | --- |
| Per-release | Claude PM | Codex (parity + clean-room) | Owner (only when action requires Owner-confirm per [docs/00 risk model](00_PM_OPERATING_SYSTEM.md)) |
| Monthly sweep | Claude PM | Codex | Claude PM (for non-strategic adjustments) |
| Quarterly strategic | Claude PM | Codex + Gemini (if UI-relevant) | Owner |

## Integration With Existing Gates

This policy is **post-Phase-1**. Integration with the Phase 1 → 2 transition: the Phase 1 baseline tracking entry (`docs/tracking/<reference>/2026-04-28-baseline.md`) MUST exist for every reference before Phase 1 can exit. The baseline freezes "what we mined and when" so the per-release diff has a starting point.

This policy is referenced from [15_RELEASE_GATES.md](15_RELEASE_GATES.md) as a **Continuous Gate** — it never closes, every release of HUAKAI requires the tracking ledger to be current within its cadence windows.

## What "Self-Audit" Means

When upstream fixes a bug, HUAKAI must answer one of these for each tracked algorithm:

1. **VULNERABLE**: HUAKAI has the same bug shape. Action: open follow-up, fix, ship patch.
2. **SAFE-BY-DESIGN**: HUAKAI's algorithm structurally cannot have this bug because of an explicit design choice. Cite the design choice from the relevant prose decomposition.
3. **SAFE-BY-CODE**: HUAKAI does not currently have the bug because of the specific code path, but the design does not preclude it. Action: add an invariant or test that prevents future regression.
4. **UNKNOWN-NEED-INVESTIGATION**: cannot determine without source-reading the relevant HUAKAI module. Action: assign to a specifier-lane agent for review.

The self-audit verdict is recorded in the tracking entry's `## Self-audit` section.

## Anti-Pattern: Silent Drift

The failure mode this policy prevents is **silent drift**: HUAKAI's decomposition reflects upstream's state-at-Phase-1, upstream evolves over months, HUAKAI's behavior eventually diverges in ways no one notices until a customer complains. The cadence + audit format makes the divergence auditable in real time.
