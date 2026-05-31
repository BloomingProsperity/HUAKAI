# DR-010: Auth Last-Used Telemetry Carve-Out

| Field | Value |
| --- | --- |
| Status | Implemented |
| Date opened | 2026-05-31 |
| Date decided | 2026-05-31 |
| Owner | HUAKAI Owner via S2-046 dispatch |
| Affected docs | docs/specs/_invariants/cross-module-boundaries.md, backend/sql/migrations/0007_l0_inbound_auth.up.sql |
| Supersedes | CMB-7 Auth read-only wording for `api_keys.last_used_at` only |
| Superseded by | — |

## Question

May inbound API-key authentication update `api_keys.last_used_at` after a successful bearer verification?

## Context

- S2-046 requires inbound auth to refresh `last_used_at` so operator/user telemetry no longer stays stale forever.
- Earlier N+4a plans intentionally kept Auth read-only to avoid hot-path write amplification and CMB-7 drift.
- The implemented carve-out is intentionally narrow: only successful API-key auth may touch `api_keys.last_used_at`; failed auth paths must not touch it; telemetry write failure or timeout must not alter the auth result.

## Codex View

- **Production / testability concerns:** A synchronous telemetry write can couple auth latency to row locks or slow writes. Mitigation: bound the touch with a short timeout and treat errors as log-only.
- **Security concerns:** Plaintext bearer and key hash must not be logged. The telemetry warning logs only tenant ID, API key ID, and error.
- **Recommendation:** Adopt the narrow carve-out because S2-046 makes freshness a required product behavior, but keep all non-telemetry writes out of Auth.
- **Confidence:** Medium
- **Updated:** 2026-05-31

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | Adopt bounded best-effort `api_keys.last_used_at` touch after successful inbound API-key authentication. |
| Decision date | 2026-05-31 |
| Reasoning | S2-046 dispatch requires this telemetry path and explicitly revises the prior read-only declaration. |
| Constraints attached | Touch is success-only, timeout-bounded, log-only on failure, and must not write routing, pool, billing, ledger, user, tenant, key status, key hash, or plaintext secret data. |

## Propagation Checklist

- [x] Update docs/specs/_invariants/cross-module-boundaries.md to document the telemetry-only Auth carve-out.
- [x] Update existing api_keys migration comments for fresh-install documentation consistency.
- [x] Update resolver comments and sqlc query comments to stop claiming Auth is fully read-only.
- [x] Add discriminating integration_pg tests for success and failed-auth paths.
- [ ] Independent dispatcher review signs off before landing branch merge.
