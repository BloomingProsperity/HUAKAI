This directory holds spec files produced by the **specifier lane** under the clean-room methodology decided in [DR-000](../decisions/DR-000-clean-room-methodology.md).

# Specs

A spec describes one feature's behavior in this project's own terms. It is written by an agent that has read non-MIT reference material and exists so the implementer lane can build the feature **without** ever reading the references itself.

## Authoritative Rules

- The methodology and lane definitions live in [05_CLEAN_ROOM_POLICY.md §Methodology: Decided](../05_CLEAN_ROOM_POLICY.md).
- The spec-leakage review checklist is [`_REVIEW_CHECKLIST.md`](_REVIEW_CHECKLIST.md). Every spec must pass it before the implementer lane reads it.
- The spec template is [`_TEMPLATE.md`](_TEMPLATE.md). Copy it to `<feature-id>-<slug>.md`.

## Naming

`<feature-id>-<slug>.md` where `<feature-id>` matches the row in [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) (e.g. `F-GW-001-route-eligibility.md`). One spec per feature.

## Lifecycle

```
Draft → Reviewed → Released → (Superseded by <new spec>)
```

| State | Meaning |
| --- | --- |
| Draft | Specifier has written the spec. Not yet reviewable by implementer lane. |
| Reviewed | Passed [`_REVIEW_CHECKLIST.md`](_REVIEW_CHECKLIST.md). Reviewer name + date recorded in the spec header. |
| Released | Implementer lane is permitted to read it. |
| Superseded | A newer spec replaces this one. Old file stays in repo for history; header points to the replacement. |

## Implementer-Lane Discipline

When you (implementer agent) read a spec, you must:

1. Verify the spec header says `Status: Released` and shows a Reviewer.
2. **Not** open any reference URL, repository, or doc cited in the spec's `Sources` field.
3. Treat the spec as the only ground truth for behavior; treat this repo's contracts as the only ground truth for architecture.

If a spec is `Draft` or `Reviewed` (not yet `Released`) — close the file and switch tasks until it reaches `Released`.
