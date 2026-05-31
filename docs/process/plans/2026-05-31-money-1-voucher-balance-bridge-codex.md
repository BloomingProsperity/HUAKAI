# 2026-05-31 MONEY-1 voucher balance bridge

| Field | Value |
| --- | --- |
| Owner directive | "Do ONE task now... MONEY-1 Money-loop voucher->user_balances bridge" |
| Scope | In: add discriminating PostgreSQL integration coverage and update `backend/internal/voucher/store_postgres.go` so a successful USD voucher redemption credits/provisions `user_balances` in the same serializable transaction. Out: schema changes, billing ledger redesign, quota enforcement changes, multi-currency balance conversion, new dependencies, frozen package additions. |
| Success criteria | A no-balance user redeeming a 10000c USD voucher can reserve 100 USD, capture 100 USD to zero balance, and replaying the same idempotency key does not double-credit. Removing the voucher balance UPSERT makes the test fail. Non-USD vouchers must not be written into the currencyless `user_balances` table as USD-equivalent credit. |
| Time estimate | 1-2 hours wall clock; one Codex implementation pass plus local tests/self-review/push. |
| Blast radius | Money path: voucher redemption, `user_balances`, balance holds, and later settlement capture for users who redeemed vouchers. |
| Failure modes | Double credit on retry; partial voucher billing event without balance effect; decimal/cents conversion drift; breaking opt-in pass-through for users without voucher credit; mis-crediting a non-USD voucher into a currencyless USD balance row. Mitigation: update only the successful USD redemption transaction, keep idempotent replay read-only, assert reserve/capture/replay behavior, and add a non-USD guard test. |
| Decision points | Multi-currency voucher-to-balance support requires a later Owner decision because `user_balances` currently has no currency column and this task is explicitly out of schema scope. High-risk merge remains dispatcher/Owner-controlled per coordination protocol. |
| Pre-execution checklist | Read `.coordination/DISPATCH.md`, `CLAUDE.md` #8/#13/#14, voucher spec/matrix rows, local balance-hold code, and reference behavior evidence. Claim files through coordination. Write failing test before production code. Run targeted tests and time-bounded self-review before commit. |

Reference evidence used only as behavior evidence, not source design: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/redeem_service.go:317` shows same-transaction entitlement mutation after voucher state change; `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/redemption.go:129` shows row-locked redemption plus in-transaction quota credit; `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/antigravity_executor.go:117` shows an optimistic no-balance/unknown-balance default, which HUAKAI keeps for unprovisioned users while voucher redemption deliberately provisions the balance row.

## Clean-Room Provenance

Observed regions: 3 / Inferences: 1 / Open questions: 0.

Source files read: `backend/internal/service/redeem_service.go`; `model/redemption.go`; `internal/runtime/executor/antigravity_executor.go`
Lane: specifier
Agent: Codex GPT-5.5 server-b
UTC timestamp: 2026-05-31T14:52:28Z
