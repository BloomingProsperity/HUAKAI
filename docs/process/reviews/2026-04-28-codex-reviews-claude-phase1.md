# Review: Codex reviewing Claude Phase 1 outputs

| Field | Value |
| --- | --- |
| Reviewer | Codex (gpt-5.5 + xhigh, critic agent) |
| Date | 2026-04-28 |
| Subject | Claude's Phase 1 prose decompositions + 5 inventories |

## Severity Legend
🔴 CRITICAL / 🟠 MAJOR / 🟡 MINOR

## Subject 1 — layered-account-selection.md
### Findings
- 🔴 **CL-007 / DR-000 failure: account-pool routing is an Option C carve-out, but no lane mode or strict spec is present.** The file targets `F-POOL-001` at line 7 and describes account selection in detail at lines 21-29. DR-000 makes account-pool routing an Option C carve-out; this artifact has no `Lane mode` field and remains `Status | Draft` at line 5. Future implementers must not treat this as released design input. Fix: add `Lane mode | Option C`, create the strict `docs/specs/` spec, and review it before implementation use.
- 🟠 **CL-005 borderline algorithm leakage.** Lines 21-29 specify the exact layer order and revalidation flow; line 34 enumerates scoring inputs. This is useful analysis but too close to implementable pseudocode for an LGPL-derived feature. Fix: rewrite the downstream spec in guarantee form, grouping signal classes rather than preserving observed ordering.
- 🟠 **Provenance contradicts the assignment.** Lines 9 and 73-75 say Codex authored/spec-mined this file, not Claude. That makes this a poor subject for "Codex reviews Claude" unless the metadata is wrong. Fix the attribution or exclude from Claude-output review.

## Subject 2 — streaming-forwarder.md
### Findings
- 🟠 **Lane mode is undeclared despite billing/accounting semantics.** Line 7 scopes this as "streaming + accounting"; lines 27, 31, 53-54, and 66-67 define usage extraction and settlement behavior. Billing reconciliation is Option C-adjacent at minimum, but the header has no `Lane mode`. Fix: split pure forwarding from billing settlement, or mark the settlement spec Option C.
- 🟠 **Identifier-looking names bleed into spec prose.** `ResponseWriter` at line 28 and proposed fields like `drain_max_bytes`, `drain_max_seconds`, `drain_max_estimated_cost`, and `disconnect_reason` at line 66 read like implementation/schema names. Even if local, they should move to a HUAKAI schema contract after clean-room review. Fix: use behavioral prose here.
- 🟡 **Drain budget improvement is incomplete.** Line 66 adds byte/time/cost caps, but does not say who records partial settlement, how retries are suppressed after client disconnect, or what operator event is emitted.

## Subject 3 — protocol-translation.md
### Findings
- 🟠 **Pseudo-schema is embedded before a HUAKAI contract exists.** Lines 38, 63, and 66 name `compatibility_notes`, `losses[]`, `downgrades[]`, `compatibility_mode`, and `reasoning_downgraded`. These are not glossary-level behavior terms; they are field names. Fix: move field naming to a local HUAKAI contract/spec and keep this decomposition behavior-only.
- 🟠 **Broken dependency link.** Line 70 links `upstream-transport.md`, but that file does not exist under `docs/decompositions/sub2api/`. A future implementer following this hits a dead handoff. Fix: create the referenced decomposition or mark it explicitly `TBD, no link`.
- 🟡 **Dangerous behavior phrasing around vision downgrade.** Line 44 says images may be "flattened to text descriptions"; a gateway cannot safely synthesize image descriptions without an OCR/model pass. Line 64 correctly prefers fail-closed, so line 44 should be softened to "dropped or rejected per policy."

## Subject 4 — docs/decompositions/sub2api/_INVENTORY.md
### Findings
- 🟠 **The status legend contradicts the table.** Lines 16-17 say README rows do not count as `shallow-evidence`; rows 51, 53, 63-64, 73-76, and 86 still label README-only items as `shallow-evidence`. This inflates coverage. Fix: use `unmined (README only)` until source evidence exists.
- 🟠 **CL-006 failure: nonexistent evidence ID.** Line 87 cites `E-S2A-012`, which is not present in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`. Fix: add the ledger row or remove the citation.
- 🟠 **Draft files are counted as deep-decomposed.** Line 18 defines `deep-decomposed` as a Draft file, but `docs/decompositions/README.md` says unreviewed Draft decompositions cannot be cited by implementers. Fix: separate `draft-decomposed` from `reviewed-decomposed`.

## Subject 5 — docs/decompositions/one-api/_INVENTORY.md
### Findings
- 🟠 **Contradictory quota story.** Line 32 says one-api does not reserve before upstream call; lines 50 and 34 say it uses pre-deduct / reservation formula. That will mislead the billing design. Fix: reconcile the old `E-OAI-DEEP-004` conclusion with later `E-OAI-DEEP-013/015`.
- 🟠 **README rows mislabeled as `shallow-evidence`.** The legend at line 17 requires an `E-X-DEEP` row, but rows 40-41, 55, 70-72, 79-81, and 87-88 use README evidence under the same status. Fix statuses.
- 🟡 **Top-level directory claim is stale/incomplete.** Line 9 says verified dirs exclude `docs`; GitHub API during this review shows `docs` exists. Fix the verified list or state non-code dirs were intentionally omitted.

## Subject 6 — docs/decompositions/new-api/_INVENTORY.md
### Findings
- 🟠 **AGPL file-structure leakage.** Line 9 copies top-level AGPL repo structure, and later sections name source dirs (`relay/`, `model/`, `service/`, `setting/`, etc.). For an AGPL reference, this is not implementer-safe inventory text. Fix: keep source paths in specifier-private notes; public inventory should group by behavior.
- 🟠 **CL-006 failure: nonexistent evidence ID.** Line 29 cites `E-NAI-010`, absent from the ledger. Fix it before using Realtime as a mapped feature.
- 🟠 **Phase blocker list undercounts L1/L2 unmined work.** Lines 99-103 list only four blockers, omitting line 47 `F-BILL-001`, line 82 `F-CONFIG-001`, line 90 `F-GW-001`, and line 91 auth/security middleware. Fix the blocker list from the table, not memory.
- 🟡 **Top-level directory claim is incomplete.** Line 9 omits `.agents` and `docs`, both present in the GitHub API response during review.

## Subject 7 — docs/decompositions/litellm/_INVENTORY.md
### Findings
- 🟠 **Phase blocker summary is materially incomplete.** Lines 128-132 name four critical unmined L1/L2 areas but omit request/response compression at line 111, endpoint definitions at line 114, and response normalization at line 115. Fix the blocker rule: every L1/L2 `unmined` row blocks, unless explicitly deferred with Owner rationale.
- 🟠 **Status inflation continues.** Lines 25-32 use `shallow-evidence` for deep rows with no prose decomposition; per the Phase 1 mandate, that is not enough for implementation-facing coverage. Fix by making "source evidence" and "reviewed decomposition" separate columns.
- 🟡 **PASS on license quarantine.** Line 145 correctly quarantines LiteLLM `enterprise/`.

## Subject 8 — docs/decompositions/portkey/_INVENTORY.md
### Findings
- 🟠 **CL-006 failure: nonexistent evidence ID.** Line 96 cites `E-PK-011`, absent from the evidence ledger. Fix or remove.
- 🟠 **Phase blocker count is wrong and the bullet list is incomplete.** Line 105 says seven critical L1/L2 blockers, but lines 106-112 list six; the table also omits L1/L2 unmined auth, retry/fallback, timeout, provider config, cost tracking, logging, typed errors, and provider-error normalization from the blocker list. Fix the summary from table data.
- 🟡 **Top-level dirs pass.** Line 9 matches the current GitHub API `src/` directory list.

## Cross-cutting findings
- 🟠 **Inventories misuse `shallow-evidence` as a catch-all.** Across Sub2API, one-api, New API, LiteLLM, and Portkey, README-only rows and source-evidence-without-prose rows collapse into the same status. That destroys the Phase 1 gate.
- 🟠 **Future-file links often point to `.` instead of the intended file.** Examples: one-api lines 102-108, new-api lines 107-113, litellm lines 136-141, and portkey lines 115-122. Link text names files, href goes to the directory.
- 🟠 **Reviewer/provenance metadata is not audit-grade.** The three prose decompositions claim Codex authorship while this task frames them as Claude outputs. Clean-room review depends on exact lane provenance.

## Summary of Action Items
| # | Severity | Action |
| --- | --- | --- |
| 1 | 🔴 | Add Option C lane handling and strict spec for `F-POOL-001` before implementation use. |
| 2 | 🟠 | Replace README-only `shallow-evidence` statuses with `unmined (README only)`. |
| 3 | 🟠 | Fix missing evidence IDs: `E-S2A-012`, `E-NAI-010`, `E-PK-011`. |
| 4 | 🟠 | Recompute all Phase 1 blocker summaries from inventory rows. |
| 5 | 🟠 | Remove AGPL source directory structure from public New API inventory. |
| 6 | 🟠 | Reconcile one-api quota reservation claims. |
| 7 | 🟡 | Fix future-file links that currently target `.`. |

## Reviewer Verdict
REVISE. The decompositions contain useful behavioral understanding, but they are not cleanly releasable: the most important routing artifact lacks Option C handling, several inventories cite nonexistent evidence IDs, and the status summaries overstate coverage. The main risk is not that Claude missed every feature; it is that future agents would trust these inventories as gate-quality when they are still mixed planning notes, README hints, and unreviewed drafts.
