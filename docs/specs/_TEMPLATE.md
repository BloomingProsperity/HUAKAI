This is the canonical spec template. Copy to `<feature-id>-<slug>.md` and fill.

# <Feature ID>: <Short Behavior Title>

| Field | Value |
| --- | --- |
| Status | Draft / Reviewed / Released / Superseded |
| Feature ID | F-XX-NNN (must match a row in [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md)) |
| Specifier | <agent + session> |
| Specifier date | YYYY-MM-DD |
| Reviewer | <agent + session, set when Status moves to Reviewed> |
| Review date | YYYY-MM-DD |
| Released date | YYYY-MM-DD |
| Lane mode | Option B (default) / Option C (strict carve-out) |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by the specifier. Implementer lane MUST NOT open these.

- <reference-name> — <license tier from [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md)>
- <evidence-IDs> — `E-XXX-NNN` rows in [07](../07_REFERENCE_EVIDENCE_LEDGER.md)

## Capability

Which capability row in [02_CAPABILITY_CONTRACT.md](../02_CAPABILITY_CONTRACT.md) and [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) does this spec satisfy? Quote the local capability statement, not the reference's wording.

## Actor

Who triggers or observes this behavior (User, Operator, System, External Provider).

## Preconditions

State that must be true before the behavior runs.

## Normal Path

Numbered steps describing what happens when everything works. Use this project's vocabulary from [18_GLOSSARY.md](../18_GLOSSARY.md). Do not name reference functions, files, schema columns, or upstream UI components.

1. …
2. …
3. …

## Failure Path

What happens when each precondition fails or each step errors. One sub-section per material failure mode.

### Failure: <name>
- Trigger
- Observable outcome
- Operator-visible signal (log, status field, alert)

## Operator Recovery

How an operator detects and recovers from each failure mode without database surgery.

## Audit / Usage / Log Evidence

What rows are written, where, with which fields. Avoid naming reference table or column names; use this project's domain model from [19_DOMAIN_MODEL.md](../19_DOMAIN_MODEL.md).

## Acceptance Test Direction

Sketch of normal-path, failure-path, and recovery-path tests. The implementer lane will turn these into real tests in the local test framework. Reference test ID: `AT-XXX-NNN` in [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md).

## Open Questions

Anything the specifier could not resolve from the references or contracts. Implementer lane should flag these back to the specifier rather than guessing.

## Implementer Notes (added by implementer lane)

> This section is filled by the implementer after consuming the spec, NOT by the specifier. Notes here record local design choices, dependencies, and deviations.

- <date> — <implementer agent + session> — <note>
