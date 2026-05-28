# 2026-05-28 TRUST-B-3 Codex Implementation Plan

| Owner directive | "TRUST-B-3: final billing detached signature + 复用 user_cost_receipts.signed_hash" |
| Scope | Implement settle-hook final trust receipt signing. In: `backend/internal/trustreceipt/final.go`, existing audit worker/storage files, wiring/tests. Out: new DB columns, pubkey/verify endpoint, docs/acceptance B-5, new files under frozen `gatewayhttp`, `gateway`, or `proto`. |
| Success criteria | Settled receipts with a trust signer store a final `trust.receipt.v1` detached signature in `user_cost_receipts.signed_hash`; nil signer appends the receipt with empty signature fields and reports a warning path; final canonical bytes include real cost, tokens, price snapshot, and `validation_state=valid` for normal receipts; requested Go tests and `go build ./...` pass. |
| Time estimate | 60-90 minutes wall clock in this session. |
| Blast radius | Settlement receipt hook, receipt storage validation, receipt JSON signature formatting, gateway settlement wiring, focused tests. Money ledger, quota enforcement, auth core, migrations, and deployment scripts are not touched. |
| Failure modes | Legacy receipt signature path could be double-base64 encoded; mitigate with response helper that preserves already-base64 ed25519 signatures. `signed_hash BYTEA NOT NULL` could reject nil fail-open values; mitigate by writing non-nil empty byte slices and allowing both signature fields empty together. Tests could be weak; use ed25519 verification and cost mutation assertions. |
| Decision points | No Owner sign-off needed for low/medium-risk implementation support. Stop if a schema migration, new runtime dependency, auth/billing ledger mutation, or frozen-package new file becomes necessary. |
| Pre-execution checklist | Read CLAUDE.md/AGENTS.md rules; confirm clean worktree; inspect trustreceipt, audit worker/storage, billing SettleRequest, HCSF usage, gateway wiring; write failing tests first; run red test; implement minimal code; run required verification. |

## Concrete Execution Order

1. Add focused tests in `backend/internal/audit/receipt_worker_test.go` for signer success, nil-signer fail-open, and mutation sensitivity.
2. Run the new tests and confirm they fail for missing B-3 behavior.
3. Add `backend/internal/trustreceipt/final.go` in non-frozen `trustreceipt` package. Target package is not frozen.
4. Update existing `backend/internal/audit/receipt_worker.go` to inject `*sign.Signer`, build final receipt after `DeriveReceipt`, sign canonical bytes, and overwrite legacy receipt signature fields only on success.
5. Relax existing storage validation in `backend/internal/audit/receipt_storage.go` so both signature fields may be empty together, while one-sided partial signatures still fail.
6. Keep `backend/internal/audit/receipt_storage_pgx.go` and SQL storage writes on existing columns, using non-nil byte slices for empty signatures.
7. Update existing wiring/tests to pass the audit signer into the hook where final signatures are expected.
8. Run the required test/build commands from `backend/`.
