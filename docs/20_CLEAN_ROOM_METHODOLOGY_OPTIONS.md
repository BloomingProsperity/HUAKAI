This file is agent-facing and authoritative.

> **Decided 2026-04-28 in [DR-000](process/decisions/DR-000-clean-room-methodology.md).** This document is retained as the option catalog and analysis record. The active methodology is **Option B (default) + Option C carve-out + spec-leakage review**, codified inline in [05_CLEAN_ROOM_POLICY.md §Methodology: Decided](05_CLEAN_ROOM_POLICY.md).

# Clean-Room Methodology Options

## Why this document exists

[05_CLEAN_ROOM_POLICY.md](05_CLEAN_ROOM_POLICY.md) declares a clean-room rule but does not specify **how** isolation is enforced between the agents that read reference projects and the agents that write local implementation.

License verification ([06_REFERENCE_PROJECTS.md](06_REFERENCE_PROJECTS.md)) found that the three named primary references are all **strong copyleft** (AGPL or LGPL). This sharpens the clean-room obligation. The current methodology in this repository — one agent (Claude) acts as both reference miner and lead architect — is closer to "license-aware reimplementation" than to a textbook clean-room.

Owner must pick a methodology. Three options follow, ordered by isolation strength.

## Option A — Single Agent, Behavior-Only Discipline (current de facto)

Claude reads public reference docs / issues / behavior, captures evidence as user-outcome statements, then designs and writes the local implementation. Codex audits for contamination. Gemini builds UI from the same behavior evidence.

**Pros**

- Lowest coordination overhead.
- Smallest agent count.
- Already what the documents implicitly assume.

**Cons**

- Same agent context holds both upstream behavior and downstream implementation. "Did not consciously copy" is hard to prove later.
- AGPL and LGPL contamination risk is non-trivial; "we only read public docs" is a weaker defense than "we never read source".
- No structural barrier — relies on agent self-discipline and reviewer audit.

**When acceptable**

- References are MIT/Apache/BSD (non-copyleft).
- The scope is small enough that contamination risk per feature is low.

## Option B — Two-Lane Separation (recommended)

Two distinct agent lanes:

- **Specifier lane (dirty)** — may read reference public material, public source, public issues. Produces only abstract specs in `docs/specs/<feature>.spec.md`. **Never writes implementation, schema, UI, or test code.**
- **Implementer lane (clean)** — never reads reference projects. Reads only the specs from the dirty lane plus this repo's own docs. Produces all code, schema, UI, and tests.

A specifier-lane spec is a behavior contract: actors, preconditions, triggers, expected results, failure modes, recovery, edge cases. It must not name reference functions, reference files, reference schema column names, or reference internal terms.

**Pros**

- Real structural isolation. Stronger legal posture for AGPL-adjacent references.
- Specs become a durable artifact that outlives any specific agent run.
- Reviewer (Codex) can independently check that specs leak no protected detail.

**Cons**

- Higher coordination cost.
- Specifier lane needs explicit Owner authorization to read each reference (per-session contamination tracking).
- Slower per-feature.

**When acceptable**

- Default for this project given the AGPL/LGPL reference exposure.

## Option C — Strict Two-Team Clean-Room (textbook)

Same as Option B, but the "implementer lane" agent is also forbidden from ever reading any reference project, even non-source artifacts (no public docs, no demos, no UI screenshots). All knowledge transfer goes through written specs reviewed by a third arbiter.

**Pros**

- Strongest legal posture (matches IBM-PC-clone clean-room precedent).
- Implementer truly cannot copy because it has nothing to copy from.

**Cons**

- Very slow.
- Hard to handle features driven by emergent operator knowledge that lives in the implementer's head from prior projects.

**When acceptable**

- If commercial release is anticipated and Owner wants the strongest defense.

## Owner Decision Required

| Option | Recommended for HUAKAI? |
| --- | --- |
| A | Only if Owner accepts that AGPL-adjacent contamination defense is "best effort". |
| B | **Recommended default.** Aligns with the AGPL/LGPL reference reality without crippling velocity. |
| C | If commercial release is planned and contamination tolerance is zero. |

Owner: please pick A, B, or C. The choice will be reflected in:

- [05_CLEAN_ROOM_POLICY.md](05_CLEAN_ROOM_POLICY.md) — methodology section.
- [12_AGENT_WORKFLOW.md](12_AGENT_WORKFLOW.md) — agent lane definitions.
- [10_RISK_REGISTER.md](10_RISK_REGISTER.md) — R-LIC-001 mitigation downgraded or sharpened accordingly.

Until the Owner picks, agents must operate **as if Option B were chosen** — i.e. avoid reading non-MIT reference source code; restrict reference exposure to public behavior, docs, issues.
