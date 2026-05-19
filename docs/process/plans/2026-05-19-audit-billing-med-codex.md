# 2026-05-19 audit billing MED codex

| Owner directive | "修 audit list MED 集中 (7 项 audit + billing)" |
| --- | --- |
| Scope | In: Go audit, billing, gateway HTTP fixes and focused tests for AT-AUDIT-001-060 through AT-AUDIT-001-064. Out: reference reverse-proxy source, frontend, Rust, vendor/boring, proto, pool, community, and `backend/cmd/gateway/main.go` route splitting. |
| Success criteria | The seven listed MED defects are fixed; named tests cover zero refund, refund receipt sequence increment, cost overflow, unsupported schema graceful verdict, and internal verify error redaction; requested build and race tests pass. |
| Time estimate | 60-90 minutes wall clock; one Codex executor lane. |
| Blast radius | Billing refund error semantics, audit receipt formatting/storage, and audit verification HTTP responses. |
| Failure modes | Sequence query may not match storage abstractions; mitigation: follow existing pgx repository patterns and keep interface changes minimal. Error redaction may break callers expecting raw errors; mitigation: preserve status shape while replacing only public message. Overflow check may affect parsing edge cases; mitigation: add focused tests. |
| Decision points | High-risk changes are not expected. Stop before schema, auth core, billing ledger design, quota enforcement, deployment, secrets, or dependency changes. |
| Pre-execution checklist | 1. Confirm clean worktree. 2. Read specified Go files and adjacent tests. 3. Patch implementation. 4. Add focused acceptance-style Go tests. 5. Run requested build and race test commands. |
