This directory holds **per-reference × per-feature** prose-form decompositions, mandated by [22_DEEP_MINING_MANDATE.md](../22_DEEP_MINING_MANDATE.md) (Owner sharpening 2026-04-28: "必须对借鉴项目的功能一个一个的拆解！不能只读潜在的表象。").

# Decompositions

## Why this directory exists

A 100-character table cell in [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md) is the **index entry** for a piece of upstream behavior. It is not the work. The work is the prose decomposition file in this directory: ~600–1500 words per feature per reference, with the seven required fields fleshed out as paragraphs, including signals consumed, state transitions, race windows, edge cases, and a concrete KEEP / IMPROVE / AVOID for HUAKAI's design.

## File layout

```
decompositions/
  _TEMPLATE.md                         # canonical template
  <reference>/
    <feature-slug>.md                  # one file per (reference, feature)
```

Where `<reference>` is the project slug (e.g. `sub2api`, `one-api`, `litellm`, `new-api`, `portkey`, `helicone`, `envoy-ai-gateway`, `all-api-hub`) and `<feature-slug>` is a short kebab-cased identifier (e.g. `selection-algorithm`, `sticky-session`, `billing-claim-gate`).

## Authoritative rules

- File created from [_TEMPLATE.md](_TEMPLATE.md). Every section filled.
- The matching ledger row in [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md) MUST link to the decomposition file path under its `Observed Behavior Or Scenario` cell.
- The decomposition is reviewed (CL-001..010) by an agent session different from the writer; reviewer signature recorded at the bottom of the file.
- When a feature appears in multiple references, each reference gets its own decomposition file. HUAKAI's local design decision is then synthesized in [specs/](../specs/) (an Option C spec for carve-out areas, otherwise a regular contract doc).

## Lifecycle

```
Draft → Reviewed → Source-of-truth (linked from F-* row in docs/03)
```

A `Reviewed` decomposition is the load-bearing input for the corresponding [specs/](../specs/) Option C entry. An unreviewed `Draft` decomposition cannot be cited by an implementer-lane agent.

## Coverage tracker

The current per-reference coverage state is tracked in [22_DEEP_MINING_MANDATE.md §Per-Reference Coverage Tracking](../22_DEEP_MINING_MANDATE.md). Owner directive: every L1/L2 feature must reach `Reviewed` decomposition state before Phase 1 → Phase 2 transition.
