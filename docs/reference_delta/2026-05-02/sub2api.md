# Sub2API reference delta

## Repo snapshot

- Repo: `.omc/reference-src/sub2api`
- Branch: `main`
- Commit: `48912014a16e`
- Tag: `v0.1.121-1-g48912014`
- File count: `2042`
- State: source tree clean; local `.omc/` tool-state is untracked and ignored for this pass.

## Source areas read

- Gateway and protocol routes: `.omc/reference-src/sub2api/backend/internal/server/routes/gateway.go`
- Payment routes: `.omc/reference-src/sub2api/backend/internal/server/routes/payment.go`
- Admin routes: `.omc/reference-src/sub2api/backend/internal/server/routes/admin.go`
- User self-service routes: `.omc/reference-src/sub2api/backend/internal/server/routes/user.go`
- Ent schemas: `.omc/reference-src/sub2api/backend/ent/schema/*`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Multi-protocol gateway entrypoints for Claude Messages, OpenAI chat/images/responses, Gemini, Codex, and Antigravity. | `.omc/reference-src/sub2api/backend/internal/server/routes/gateway.go:44`, `:68`, `:84`, `:118`, `:140`, `:184` |
| source-confirmed | Gateway middleware includes request size limiting, request id normalization, endpoint normalization, API-key auth, and group resolution. | `.omc/reference-src/sub2api/backend/internal/server/routes/gateway.go:25`, `:36` |
| source-confirmed | Payment has user-facing config, checkout info, plans, provider channels, order create/verify/cancel/refund-request, resume-token verification, and provider webhooks. | `.omc/reference-src/sub2api/backend/internal/server/routes/payment.go:28`, `:36`, `:52`, `:60` |
| source-confirmed | Admin payment dashboard, order review, refund handling, plan CRUD, and provider instance CRUD exist. | `.omc/reference-src/sub2api/backend/internal/server/routes/payment.go:72`, `:79`, `:89` |
| source-confirmed | Admin has operations dashboards for concurrency, alerts, request errors, upstream errors, system logs, dashboard state, and cleanup. | `.omc/reference-src/sub2api/backend/internal/server/routes/admin.go:108`, `:132`, `:147`, `:163`, `:173`, `:184` |
| source-confirmed | Admin user and group workflows include balance, API keys, usage, RPM, capacity, rate multipliers, and overrides. | `.omc/reference-src/sub2api/backend/internal/server/routes/admin.go:217`, `:239` |
| source-confirmed | Provider account operations include check, sync, test, recover, refresh, stats, temporary unschedulable state, schedulable reset, batch import/export, and bulk actions. | `.omc/reference-src/sub2api/backend/internal/server/routes/admin.go:261`, `:281`, `:291`, `:305` |
| source-confirmed | User self-service includes TOTP, usage list/detail/stats/dashboard/API-key usage, redeem, subscription summary, and channel monitor view. | `.omc/reference-src/sub2api/backend/internal/server/routes/user.go:44`, `:80`, `:100`, `:107`, `:116` |
| source-confirmed | Data model includes channel monitors, monitor history/rollups/templates, payment audit/order/provider/plan, usage cleanup task, TLS fingerprint profile, subscription, redeem, and idempotency record. | `.omc/reference-src/sub2api/backend/ent/schema/channel_monitor.go`, `payment_order.go`, `payment_provider_instance.go`, `usage_cleanup_task.go`, `tls_fingerprint_profile.go`, `idempotency_record.go` |

## Inferred features

- inferred: The product has already accumulated production incident workflows around unhealthy upstream accounts, temporary account removal, channel monitor history, payment order recovery, and usage cleanup. Basis: admin and schema surfaces above, especially `.omc/reference-src/sub2api/backend/internal/server/routes/admin.go:281` and `.omc/reference-src/sub2api/backend/internal/server/routes/payment.go:36`.
- inferred: HUAKAI should not treat Sub2API as only "protocol conversion." Its real product weight is account-pool operations, payment lifecycle, and operator incident handling.

## Open questions

- open-question: Exact scheduler algorithms for account cooldown, channel monitoring, and usage cleanup still need deeper service-level reading before copying behavior into specs.
- open-question: OAuth account refresh and TLS fingerprint selection need clean-room behavior specs because they can encode provider-specific operational knowledge.

## HUAKAI delta

- `docs/03_FEATURE_PARITY_MATRIX.md` already has `F-RATE-001`, `F-AUTH-005`, `F-PAY-001`, `F-CH-002`, and `F-OPS-*`, but they are too coarse for Sub2API parity.
- `docs/17_FEATURE_LEVEL_MATRIX.md` says L1 defers advanced provider health and full billing, but Sub2API shows account health and user balance visibility are commercial survival features, not polish.
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` already admits sub2api async tasks are `0%` and L1 still needs multi-attempt fallback, real Anthropic upstream, payment/balance deduction, and Admin UI. This remains the biggest gap.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-ACC-HEALTH-001` | Provider account health operations | L1/L2 | Split from broad pool/rate features: check, sync, test, recover, temp-unschedulable, reset, bulk import/export. |
| `F-USER-SELF-001` | User self-service key/usage/balance console | L1/L2 | User must see keys, usage, balance, subscriptions, redeem history, and channel monitor state without admin help. |
| `F-PAY-ORDER-001` | Payment order lifecycle recovery | L2 | Expand `F-PAY-001`: create, verify, resume-token recover, cancel, refund request, webhook idempotency, audit log. |
| `F-CH-MON-001` | Channel monitor templates/history/rollups | L2 | Turn channel monitoring into a real operator workflow with templates, daily rollups, and failure history. |
| `F-LOG-RET-001` | Usage/system log retention and cleanup | L2 | Add retention policy, cleanup tasks, and admin search before logs become unbounded production debt. |
| `F-TLS-FP-001` | TLS fingerprint profile management | L3 | High-risk provider behavior; spec separately and keep implementation clean-room. |
