# 2026-05-20 Case C Billing Setting P2 Fixes - Codex

| Field | Value |
| --- | --- |
| Owner directive | "HUAKAI Go backend — 修复 codex review 在 case C 计费设置 Phase 1A+1B 发现的 2 个 P2。" |
| Scope | Fix HUAKAI internal migration/check constraint and billing policy resolver cache race. No reference-project source reads. No git operations. Do not touch billing/state.go, forwardSSEAndSettle, or admin API. |
| Success criteria | 0046 migration constrains `stream_input_only_interrupted_policy` values to `no_bill` / `no_bill_record`; resolver invalidation cannot be overwritten by stale cache fill; unit test covers the race; requested build and race tests report 0 FAIL. |
| Time estimate | 30-60 minutes wall clock; one Codex work unit. |
| Blast radius | Billing settings persistence and in-process billing policy cache only. Migration is stated unapplied/uncommitted by Owner, so direct up.sql edit is acceptable. |
| Failure modes | Over-constraining future keys; holding resolver mutex during DB reads; masking cold-read fallback behavior; introducing race-test flake. Mitigation: key-specific SQL CHECK, capture generation under lock then release before store.Get, keep existing fallback paths, deterministic store callback. |
| Decision points | None expected. Owner already authorized direct 0046 up migration edit and generation-based race fix. |
| Pre-execution checklist | Read target migration, resolver, policy and existing tests; patch only scoped files; run `go build ./...`; run requested `go test ... -race`. |

## Execution Order

1. Add the key-specific table CHECK to `backend/sql/migrations/0046_billing_settings.up.sql`.
2. Add a per-tenant generation map to `PolicyResolver`; increment it in `Invalidate`; only cache-set if the observed generation did not change during the store read.
3. Extend the fake policy store with a deterministic Get callback and add a race-regression unit test.
4. Run the exact requested backend build and race-test commands with local cache directories.

## Notes

No clean-room/reference-project source is involved. No feature is removed or narrowed; unsupported `bill_input` remains rejected consistently by Go validation and SQL persistence.
