This file is agent-facing and authoritative.

# Clean-Room Policy

Full feature parity or better remains mandatory; clean-room constraints change implementation method, not feature scope.

## Core Rule

Reference projects are empirical evidence, not source-code providers.

## Allowed From References

- Publicly observable behavior.
- Product workflows.
- Feature lists.
- Configuration concepts at the user-outcome level.
- Error and risk patterns.
- Public issue scenarios.
- Public documentation facts.
- Test ideas and acceptance expectations expressed independently.

## Prohibited Outputs (Owner reaffirmed 2026-05-08: 必读源码 + 不抄)

Reading borrowed reference source code is **mandatory** for behavior fidelity
(see specifier-lane permissions §Workflow Methodology below). The clean-room
boundary is on what flows OUT into this repository, not on what gets read.

The forbidden outputs (do not write into HUAKAI):

- Verbatim source code (function bodies, struct field listings copy/paste).
- Distinctive file structures preserved across rename.
- Verbatim comments.
- Database schema names or column names.
- API implementation details copied without rewording in HUAKAI vocabulary.
- UI source.
- Unique layout or styling lifted from upstream.
- Internal naming conventions (function/method/constant names) — see
  CL-001 / CL-001a in [specs/_REVIEW_CHECKLIST.md](specs/_REVIEW_CHECKLIST.md).
- Algorithms expressed as line-by-line translation of upstream code (CL-005).
- Copied test code (verbatim test bodies / fixture data).

## Required Clean-Room Method

1. Record reference evidence as behavior or scenario.
2. Convert behavior into an independent requirement.
3. Design local architecture from first principles and project contracts.
4. Implement without viewing or copying protected implementation details.
5. Validate against local acceptance tests.

## License Risk Rule

License risk can change implementation method, isolation boundary, rollout strategy, or documentation requirements. It cannot delete a feature.

## Methodology: Decided

**Decided 2026-04-28 in [DR-000](decisions/DR-000-clean-room-methodology.md).**

HUAKAI operates under **Option B (two-lane separation)** as the project default, with an **Option C carve-out** for the highest-risk AGPL-derived feature areas. All specifier-lane outputs must pass a spec-leakage review before being released to the implementer lane.

### Lane Definitions

- **Specifier lane (dirty).** May read public reference material — docs, issues, public source code from non-MIT references. Produces only abstract specs in [`specs/`](specs/). Never writes implementation, schema, UI, or test code. Once an agent session has read non-MIT source, that session enters specifier-only contamination state for the rest of the session and must not be reused for implementer work; open a new session for the other lane.
- **Implementer lane (clean).** Reads only this repository's own docs, specs from [`specs/`](specs/), and MIT-licensed anchor references (currently [songquanpeng/one-api](https://github.com/songquanpeng/one-api)). Produces all code, schema, UI, and tests. Never reads non-MIT reference source.

### Option C Carve-out (Strict Mode)

The following feature areas use Option C: the implementer lane reads only the spec, not even MIT-licensed analogues, because the AGPL reference is the dominant behavior source:

- Billing ledger reconciliation logic.
- Account-pool routing edge cases (cross-provider failover, balance-aware account selection).
- Provider failover and account-health heuristics.

Other features default to Option B.

### Spec-Leakage Review

Every spec produced in the specifier lane must pass review against [`specs/_REVIEW_CHECKLIST.md`](specs/_REVIEW_CHECKLIST.md) before the implementer lane is allowed to consume it. A spec that copies upstream names, schemas, UI structure, or algorithmic detail simply moves contamination from code to docs, defeating the methodology.

### What the Specifier Lane Is Allowed To Do (Owner Clarification, 2026-04-28)

The specifier lane is **explicitly allowed and encouraged** to:

- Read non-MIT reference source code, public docs, public issues, and public discussions.
- Borrow ideas, design rationale, failure-mode learnings, edge-case inventories, and behavioral logic at the conceptual level.
- Extract empirical operator wisdom (what works, what fails, why) and record it as evidence.

The line is **verbatim copying**, not "reading source". Reading and learning from upstream is the entire point of mining; specs are the mechanism for converting reading into legally-defensible local knowledge.

What remains forbidden, regardless of how casually the source was read:

- Pasting upstream source code, comments, schema, UI source, or test code into any file in this repository.
- Translating an upstream algorithm line-by-line into local words; the leakage checklist (CL-005 in [specs/_REVIEW_CHECKLIST.md](specs/_REVIEW_CHECKLIST.md)) catches this.
- Carrying upstream function names, file structure, schema column names, or distinctive UI component names into specs or implementation.

Stand on the shoulders of giants — but write the standing pose in your own anatomy. The specifier lane reads; the implementer lane builds; specs are the only handover surface.
