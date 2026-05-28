# 2026-05-28 TRUST-B-5 docs closure Codex plan

| Owner directive | "TRUST-B-5 = 完整 docs 收尾" |
| Scope | Docs only: update `docs/specs/trust-chain-user-verifiable-ledger.md`, `docs/11_ACCEPTANCE_TEST_MATRIX.md`, `docs/10_RISK_REGISTER.md`, `docs/07_REFERENCE_EVIDENCE_LEDGER.md`, `docs/03_FEATURE_PARITY_MATRIX.md`, and optionally add `docs/process/reviews/TRUST-AB-summary.md`. No backend/frontend code, no schema, no dependency, no `LICENSE`. |
| Success criteria | TRUST-A+B decisions D-1..D-B-mismatch-priority are reflected; parity/risk/acceptance/reference docs cross-link; every reference-project capability or negative parity claim has a true `<repo>@<sha>:<file>:<line>` cite; no non-MIT source is read except allowed README/public-doc carve-outs; required OpenAPI consistency test runs from `backend/`. |
| Time estimate | 60-90 minutes wall clock; one Codex implementer session. |
| Blast radius | Documentation governance only. Bad edits can misstate release/parity status, contaminate clean-room posture, or create stale acceptance expectations, but cannot alter runtime behavior. |
| Failure modes | Unsupported reference claims -> mitigate by reading permissive/official/BSD source locally and citing exact lines; accidental LGPL/AGPL source read -> avoid source reads for Sub2API/New API/All API Hub and record README/public-doc basis only; stale TRUST spec keeps old Merkle endpoint scope -> explicitly mark full Merkle as Phase 2 Mandatory Roadmap while preserving nullable Merkle forward-compat fields; acceptance rows overclaim tests -> use PASS only where existing matrix already had concrete test paths or where current slice commits provide evidence, otherwise Planned/Mandatory Roadmap. |
| Decision points | None expected for docs-only closure. Owner confirmation would be required before code/schema changes, dependency additions, reading LGPL/AGPL source for new claims, or changing `LICENSE`. |
| Pre-execution checklist | 1. Read current doc formats and TRUST rows. 2. Inspect local reference checkout SHAs and exact source lines for permissible references. 3. Confirm no dirty worktree changes conflict with docs edits. 4. Apply docs patches only. 5. Run required `GOCACHE=/tmp/go-build go test ./cmd/gateway/ -count=1 -timeout 60s`. 6. Spot-check at least three citations with `nl`/`rg`. |

## Concrete execution order

1. Gather reference evidence from local checkouts: Rekor, Tessera, Trillian, OpenSSH (if present or package source available), WebPKI CRL source, LiteLLM, Portkey, Helicone AI gateway, Envoy AI Gateway. Record commit SHAs with `git rev-parse HEAD`.
2. Update the trust-chain spec to current TRUST-A+B semantics: five status vocabulary, three verify entrances, HUAKAI canonical receipt v1 fixed-order fields, signer key storage and rotation, CRL overlay, TOFU, dual-rail signature timing, four response headers, fail-open/DLQ policy, and three-dimension HUAKAI delta.
3. Update acceptance matrix with TRUST-A/B rows requested by Owner, avoiding overclaim where implementation evidence is not already explicit.
4. Add risk-register rows R-TRUST-001..007 using the existing table format.
5. Add reference evidence ledger rows with source cites and clean-room notes; keep Sub2API/New API claims to public docs/README or mark as historical ledger basis, not fresh source-derived assertions.
6. Update feature parity matrix row for signed response receipt with reference columns, HUAKAI delta, and architecture/algorithm/ecosystem dimensions.
7. Add TRUST-AB retrospective summary if it can be stated without inventing review data.
8. Run required verification and citation spot checks, then report changed files, validation, clean-room compliance, source files read, lane, agent ID, UTC timestamp, and Owner confirmation needs.
