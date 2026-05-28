# 2026-05-28 TRUST-B-4 Codex Implementation Plan

| Owner directive | "TRUST-B-4: pubkey well-known + /v1/trust/verify + huakai-verify CLI TOFU" |
| Scope | Implement public trust key distribution, detached trust receipt verification, receipt-id verification upgrade, and CLI TOFU. In: new `backend/internal/trusthttp` package, existing `backend/cmd/gateway/routes.go`, existing `backend/internal/gatewayhttp/cost_receipt_handler.go`, existing/new files under `backend/cmd/huakai-verify`. Out: schema migrations, audit ledger Merkle changes, streaming path changes, new files under frozen `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. |
| Success criteria | `/.well-known/huakai-pubkey.json` returns JWK Set-compatible keys plus HUAKAI revocation extensions; `/v1/trust/verify` publicly verifies detached `trust.receipt.v1` canonical payloads with 10KB body cap and 60/min anonymous IP limiter; `/v1/receipts/{id}/verify` verifies stored final trust receipt signatures from receipt facts and returns the B-4 status vocabulary; `huakai-verify` supports detached receipt verification with TOFU cache at `~/.huakai/known_keys/<host>.json`; required Go tests and `go build ./...` pass. |
| Time estimate | 2-4 hours wall clock in this session. |
| Blast radius | Public trust HTTP surface, route mounting, existing receipt verify handler behavior, and CLI verification flow. Frozen packages are only edited in existing files. No auth core, billing ledger, quota enforcement, DB schema, production secrets, deployment scripts, or `LICENSE` changes. |
| Failure modes | Weak verification tests could pass if signature verification is stubbed; mitigate with wrong-key/tamper fixtures where expected output differs from broken code. Revocation overlay could silently miss config; mitigate with tests that require revoked fingerprints to change key status in both `keys` and `revoked`. Receipt verification could confuse legacy hash signatures with B-3 final trust signatures; mitigate by tests using base64 final signatures and unsigned historical receipts. CLI TOFU could refetch on cache hit or accept key replacement; mitigate with request counters and mismatch tests. Public verify could become an oracle or DoS target; keep it stateless, unauthenticated by design, body-capped, and IP-limited. |
| Decision points | Stop for Owner confirmation if implementation requires a new database table/column, a new runtime dependency, changing auth/billing/quota core, changing `LICENSE`, touching secrets, or adding files to frozen packages. No new Owner decision is expected for D-2/D-3/D-B-1/D-B-3/D-B-pubkey-format/D-B-verify-authn/D-B-rate-limit because the dispatch fixes them. |
| Pre-execution checklist | Read CLAUDE.md / AGENTS.md / docs/RULES.md; confirm clean worktree; inspect existing signer, pubkey registry, trustreceipt canonical/signing, receipt worker B-3 storage behavior, route wiring, legacy CLI; create only non-frozen `trusthttp` files; write failing tests before production changes; run targeted red/green cycles; run required package tests and full build. |

## File Scope

- Create: `backend/internal/trusthttp/wellknown_handler.go` in non-frozen `trusthttp`.
- Create: `backend/internal/trusthttp/verify_handler.go` in non-frozen `trusthttp`.
- Create: `backend/internal/trusthttp/revocation.go` in non-frozen `trusthttp`.
- Create: `backend/internal/trusthttp/wellknown_handler_test.go` in non-frozen `trusthttp`.
- Create: `backend/internal/trusthttp/verify_handler_test.go` in non-frozen `trusthttp`.
- Create: `backend/internal/trusthttp/revocation_test.go` in non-frozen `trusthttp`.
- Modify: `backend/cmd/gateway/routes.go` existing route file to mount the new public endpoints.
- Modify: `backend/internal/gatewayhttp/cost_receipt_handler.go` existing frozen-package file only.
- Modify: `backend/internal/gatewayhttp/cost_receipt_handler_test.go` existing frozen-package test file only.
- Modify: `backend/cmd/huakai-verify/main.go` existing CLI file.
- Modify: `backend/cmd/huakai-verify/main_test.go` existing CLI test file.

## TDD Execution Order

1. Add `trusthttp` revocation and well-known tests, then run `go test ./internal/trusthttp/ -run 'Test(WellKnown|Revocation)' -count=1` to confirm red for the missing package.
2. Implement revocation overlay parsing plus JWK Set response shape, then rerun those tests to green.
3. Add `trusthttp` detached verify tests for valid, invalid, unknown, revoked, missing, and body cap/rate limiter cases; run them red.
4. Implement detached verify request parsing, canonical payload handling, ed25519 registry verification, revocation status mapping, 10KB body cap, and 60/min IP limiter; rerun to green.
5. Add route-wiring test if existing gateway route tests make this cheap; otherwise wire `routes.go` and rely on package/API build.
6. Add receipt-id verification tests in existing `gatewayhttp/cost_receipt_handler_test.go` for signed final trust receipt and unsigned historical receipt; run them red.
7. Upgrade existing receipt verify implementation to rebuild `trust.receipt.v1` from stored receipt facts and verify `signed_hash`/`signer_fingerprint`; rerun gatewayhttp tests to green.
8. Add CLI tests for first-use fetch/cache write, cache hit no-refetch, fingerprint mismatch, and revoked key failure; run them red.
9. Implement detached CLI flags `--receipt-file`, `--signature`, `--server`, `--refresh`, and `--json` while preserving the existing audit mode flags; rerun CLI tests to green.
10. Run required verification:
    `GOCACHE=/tmp/go-build go test ./internal/trusthttp/ ./internal/audit/ ./internal/trustreceipt/ ./internal/gatewayhttp/ ./internal/auditledger/ ./cmd/huakai-verify/ -count=1 -timeout 120s`
    and `GOCACHE=/tmp/go-build go build ./...`.
