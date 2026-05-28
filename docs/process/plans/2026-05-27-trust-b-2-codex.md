# 2026-05-27 TRUST-B-2 Codex Plan

| Owner directive | "TRUST-B-2 实施: signer integration + receipt 派生 + response inline signature" |
| Scope | Implement non-streaming provisional inline trust receipt signatures. In scope: new `trustreceipt` signer/builder helpers, trust header constants/status helper, existing non-streaming header writer wiring, discriminating tests. Out of scope: streaming signatures, receipt worker/final detached signatures, trust verify endpoint, well-known pubkey endpoint, schema/table changes, commits. |
| Success criteria | Required tests pass: `go test ./internal/trustreceipt/ ./internal/trust/ ./internal/gatewayhttp/ -count=1 -timeout 120s`; required build passes: `go build ./...`; signed non-streaming persisted responses emit `signed-only` plus signature/fingerprint/schema headers when signer is present, and remain `unverified` without signer. |
| Time estimate | 60-90 minutes wall clock; one inline Codex implementation session. |
| Blast radius | Response headers for non-streaming chat completions, `trust` header constants/status behavior, and new receipt derivation/signing helpers. No DB/schema/auth/billing/quota changes. |
| Failure modes | Weak tests that pass without signer/builder behavior: use discriminating fixtures and explicit mutation checks. Header status regression: preserve TRUST-A default by upgrading only persisted+signer success. Frozen package violation: modify existing `gatewayhttp` file only; do not add files under `gatewayhttp`, `gateway`, or `proto`. Cost overclaim: since `proto.Accounting` has no cost field, set `cost_cents=0` and keep validation provisional. |
| Decision points | None requiring new Owner confirmation under the provided D decisions. High-risk areas explicitly avoided: DB schema, billing ledger, quota enforcement, auth core, production secrets, deployment, `LICENSE`. |
| Pre-execution checklist | 1. Read CLAUDE/AGENTS/RULES excerpts. 2. Confirm target new files are not in frozen packages. 3. Inspect existing `trustreceipt`, `trust`, `gatewayhttp`, `proto`, and `auditledger` APIs. 4. Write failing tests before production code. 5. Run required package tests and full build before reporting. |

## File Scope

- Create: `backend/internal/trustreceipt/sign.go` in non-frozen `trustreceipt`.
- Create: `backend/internal/trustreceipt/builder.go` in non-frozen `trustreceipt`.
- Create: `backend/internal/trustreceipt/sign_test.go` in non-frozen `trustreceipt`.
- Create: `backend/internal/trustreceipt/builder_test.go` in non-frozen `trustreceipt`.
- Modify: `backend/internal/trust/status.go` in non-frozen `trust`.
- Modify: `backend/internal/trust/status_test.go` if helper coverage is needed.
- Modify: `backend/internal/gatewayhttp/chat_completions_handler_headers.go` existing file only; `gatewayhttp` is frozen, so no new file there.
- Modify: `backend/internal/gatewayhttp/chat_completions_billing.go` and same existing header file call sites only if required for the new signer parameter.
- Modify: `backend/internal/gatewayhttp/chat_completions_handler_headers_test.go` existing test file only; no new `gatewayhttp` test file.

## Concrete Execution Order

1. Add `trustreceipt` signer tests and run them to red for missing `SignReceipt`/`ErrSignerNil`.
2. Implement `SignReceipt` minimally using `Canonical`, `sign.Signer.Sign`, base64 encoding, and signer fingerprint.
3. Run `go test ./internal/trustreceipt/ -run 'TestSignReceipt' -count=1` to green.
4. Add builder tests and run them to red for missing `BuildProvisionalFromEnv`.
5. Implement builder mapping from HCSF request/accounting metadata, ledger fallback, tenant scope, model chain, token counts, empty price snapshot, and provisional state.
6. Run `go test ./internal/trustreceipt/ -run 'TestBuildProvisional' -count=1` to green.
7. Add/adjust trust and gatewayhttp header tests for signed-only upgrade and nil signer preservation, then run targeted tests to red.
8. Implement trust constants/helper and pass signer through `WriteHuakaiHeaders` callers.
9. Run targeted package tests to green, then required test command and full `go build ./...`.

