# 2026-05-28 TRUST-B-4 R1 Fix Codex Plan

| Owner directive | "TRUST-B-4 R1 fix: 3 S1 必修" |
| Scope | In: narrow implementation-lane fix for S1-1 receipt-by-id CRL revocation overlay, S1-2 well-known normal Cache-Control header, and S1-3 revocation file size cap. Out: S2 informational findings, rotation/incident dynamic `max-age=60`, schema/database changes, auth/billing/quota core, `LICENSE`, production secrets, new runtime dependencies, and commits. |
| Success criteria | Add discriminating tests for the three S1 regressions; each test would fail if the named guard is removed. `/.well-known/huakai-pubkey.json` returns the normal cache header. `LoadRevocationsFromEnv` rejects files larger than 1 MiB. `/v1/receipts/{id}/verify` returns `status=unverified` and `reason=key_revoked` for a valid signature made by a revoked fingerprint. Required package tests and `go build ./...` pass. |
| Time estimate | 45-90 minutes wall clock in this Codex session. |
| Blast radius | Public trust metadata response headers, trust revocation config loading, and stored receipt signature verification. Frozen package `backend/internal/gatewayhttp` is edited only in existing files; no new files are added there. |
| Failure modes | Revocation test could be weak if it only checks invalid signature behavior; mitigation: use a valid signed receipt and a revoked overlay so broken code returns `signed-only`. Cache header test could be weak if it only checks Content-Type; mitigation: exact header equality. Size-cap test could be weak if it accepts parse errors from invalid JSON; mitigation: write a syntactically simple oversized JSON-like file and assert error mentions size/too large before parsing. |
| Decision points | Stop for Owner confirmation if the fix requires database schema changes, auth/billing/quota edits, a new runtime dependency, production secret handling, deleting files, or adding files to frozen packages. None are expected. |
| Pre-execution checklist | Read `docs/RULES.md`, `CLAUDE.md`, and `AGENTS.md`; inspect current dirty worktree without reverting user/Claude changes; confirm `trusthttp` is a non-frozen package; write tests before production code; verify red failures; implement minimal changes; run required tests and build. |

## File Scope

- Modify: `backend/internal/gatewayhttp/cost_receipt_handler.go` existing frozen-package file only, to apply the revocation overlay after signature verification.
- Modify: `backend/internal/gatewayhttp/cost_receipt_handler_test.go` existing frozen-package test file only, to add `TestReceiptVerifyMarksRevokedKeyAsUnverified`.
- Modify: `backend/internal/trusthttp/wellknown_handler.go` non-frozen package, to add the normal Cache-Control header.
- Modify: `backend/internal/trusthttp/wellknown_handler_test.go` non-frozen package test file, to add `TestWellKnownReturnsCacheControl`.
- Modify: `backend/internal/trusthttp/revocation.go` non-frozen package, to enforce a 1 MiB revocation file cap before reading.
- Modify: `backend/internal/trusthttp/revocation_test.go` non-frozen package test file, to add `TestLoadRevocationsFromEnvRejectsOversizeFile`.

## Execution Order

1. Add `TestWellKnownReturnsCacheControl`, `TestLoadRevocationsFromEnvRejectsOversizeFile`, and `TestReceiptVerifyMarksRevokedKeyAsUnverified`.
2. Run targeted tests and confirm they fail for the expected missing guards.
3. Add the well-known `Cache-Control` header.
4. Replace unbounded revocation-file read with a 1 MiB pre-read size check and bounded read path.
5. Add revocation-overlay dependency to receipt verify deps and map valid revoked signatures to `unverified`/`key_revoked`.
6. Run targeted tests to green.
7. Run required verification:
   `GOCACHE=/tmp/go-build go test ./internal/trusthttp/ ./internal/gatewayhttp/ ./internal/audit/ ./internal/trustreceipt/ ./internal/auditledger/ ./cmd/huakai-verify/ -count=1 -timeout 120s`
   and `GOCACHE=/tmp/go-build go build ./...`.
