Copy this template to `<reference>/<feature-slug>.md` and fill every section. Word target: 600–1500.

# `<reference>` — `<Feature title>`

| Field | Value |
| --- | --- |
| Status | Draft / Reviewed / Source-of-truth |
| Reference | <name + license + E-LIC-NNN row> |
| Feature in HUAKAI matrix | <F-XXX-NNN> |
| Evidence ledger row | <E-X-DEEP-NNN> |
| Specifier session | <agent + session id> |
| Specifier date | YYYY-MM-DD |
| Reviewer session | <agent + session id, different from specifier> |
| Reviewer date | YYYY-MM-DD |
| Source files read | <verified URLs, one per line> |

## 1. WHY (motivation / context)

Why does the upstream solve this feature this way? What real-world pressure (operator pain point, performance budget, user expectation, abuse pattern) drove the design? Cite public discussion / issues / design notes if available; otherwise infer carefully and mark inference explicitly.

## 2. WHAT (algorithm in HUAKAI vocabulary)

Describe the algorithm step-by-step in HUAKAI vocabulary only ([18_GLOSSARY.md](../18_GLOSSARY.md)). Use prose, not pseudocode. State transitions belong here. **Do not** quote upstream function names, struct fields, or file paths. **Do not** translate code line-by-line — that is leakage (CL-005). Aim to describe the same behavior in different sentences than the upstream's code shape.

## 3. INPUTS (signals consumed, state mutated)

Per-request data, per-Account state, per-User state, time, randomness, configuration. Be exhaustive — each input is a potential source of subtle behavior change.

## 4. FAILURE MODES HANDLED

What does the upstream defend against? Enumerate. For each: trigger, detection, response, observable artifact (log, status field, user-visible message).

## 5. FAILURE MODES NOT HANDLED (gaps)

What does the upstream miss? Concurrency races, partial-state recovery, abusive client patterns, multi-node distribution, cross-tenant leakage, rounding errors, etc. Each gap is a HUAKAI improvement opportunity.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

The local design directive. Each bullet is one sentence, opinionated.

- **KEEP**: <upstream behavior worth retaining as-is>
- **IMPROVE**: <upstream behavior HUAKAI strengthens, with the strengthening described>
- **AVOID**: <upstream behavior HUAKAI rejects, with the safer alternative described>

## 7. ATTRIBUTION

- Source files read: <verified URLs>
- Specifier-lane session: <agent + session id>
- Reviewer-lane session: <agent + session id; must be different>
- Verified clean-room compliance: <CL-001..010 sign-off note>

## Review Sign-Off

```markdown
## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | <agent + session> |
| Review date | YYYY-MM-DD |
| Checks passed | CL-001 through CL-010 |
| Notes | <e.g. "CL-005 had a borderline pseudocode block; rewritten."> |
```

After review, set `Status: Reviewed` and link this file from the corresponding `F-XXX-NNN` row in [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) and from the `E-X-DEEP-NNN` ledger row in [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md).
