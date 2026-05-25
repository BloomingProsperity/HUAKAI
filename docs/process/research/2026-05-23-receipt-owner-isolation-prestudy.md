# 2026-05-23 Receipt Owner Isolation Prestudy

Metadata: Observed regions: 35 / Inferences: 8 / Open questions: 2

Recency note: `api.github.com` direct shell checks were blocked by sandbox networking. Browser access to `https://api.github.com/repos/QuantumNous/new-api` showed `updated_at=2026-05-23T12:31:49Z` and `pushed_at=2026-05-23T05:24:56Z`; the other three API endpoints could not be fetched here. Local/source fallback: Sub2API HEAD `91da815` is 2026-05-20; one-api HEAD `8df4a26` is 2025-02-21 but public GitHub search showed activity/PRs in May 2026; CLIProxyAPI local snapshot records `21fad9d`, and public GitHub search showed updated 2026-05-08. Owner should re-run the four `api.github.com/repos/<owner>/<repo>` checks outside this sandbox before treating first-cite recency as closed.

## §A Reference Observations

### Sub2API (LGPL, paraphrase only)

- Schema owner fields: API key rows and usage-log rows both carry a user-owner dimension; usage logs also bind API key/account and optional group/subscription dimensions. Cites: `sub2api@91da815:backend/ent/schema/api_key.go:34`, `sub2api@91da815:backend/ent/schema/api_key.go:121`, `sub2api@91da815:backend/ent/schema/usage_log.go:32`, `sub2api@91da815:backend/ent/schema/usage_log.go:164`.
- Handler/session check: API-key auth loads the key's user and places that owner into request context; usage listing/stat/detail handlers filter by current subject and reject a requested key whose owner differs from the session subject. Cites: `sub2api@91da815:backend/internal/server/middleware/api_key_auth.go:101`, `sub2api@91da815:backend/internal/server/middleware/api_key_auth.go:211`, `sub2api@91da815:backend/internal/handler/usage_handler.go:35`, `sub2api@91da815:backend/internal/handler/usage_handler.go:128`, `sub2api@91da815:backend/internal/handler/usage_handler.go:154`, `sub2api@91da815:backend/internal/handler/usage_handler.go:191`.
- Tenant(group) vs user: user-to-group access is N:N through a join table; an API key may be pinned to one group, and usage records snapshot the selected group. Cites: `sub2api@91da815:backend/ent/schema/user.go:118`, `sub2api@91da815:backend/ent/schema/user_allowed_group.go:15`, `sub2api@91da815:backend/ent/schema/user_allowed_group.go:40`.
- Legacy backfill: observed migration converts a legacy per-user group array into the join table, then a later migration drops the legacy column. Cites: `sub2api@91da815:backend/migrations/007_add_user_allowed_groups.sql:1`, `sub2api@91da815:backend/migrations/007_add_user_allowed_groups.sql:14`, `sub2api@91da815:backend/migrations/014_drop_legacy_allowed_groups.sql:1`.

### New-API (AGPL, paraphrase only)

- Schema owner fields: token, request log, and quota/usage rollup records all carry a user-owner dimension; log/rollup query helpers include user-scoped read paths. Cites: `new-api@20d3e73:model/token.go:14`, `new-api@20d3e73:model/token.go:81`, `new-api@20d3e73:model/log.go:20`, `new-api@20d3e73:model/log.go:193`, `new-api@20d3e73:model/log.go:387`, `new-api@20d3e73:model/usedata.go:12`, `new-api@20d3e73:model/usedata.go:111`.
- Handler/session check: session auth explicitly compares the caller's declared user id with the active session user; token CRUD uses current session id for list/get/create/delete/update; token-authenticated gateway requests derive the request user from token owner and validate group usability. Cites: `new-api@20d3e73:middleware/auth.go:95`, `new-api@20d3e73:middleware/auth.go:214`, `new-api@20d3e73:middleware/auth.go:276`, `new-api@20d3e73:middleware/auth.go:409`, `new-api@20d3e73:controller/token.go:34`, `new-api@20d3e73:controller/token.go:167`, `new-api@20d3e73:controller/token.go:236`.
- Tenant(group) vs user: each user has a primary group; configured policy expands that into usable groups for token routing, so this is closer to RBAC/policy expansion than a direct N:N membership table in the regions read. Cites: `new-api@20d3e73:model/user.go:35`, `new-api@20d3e73:controller/group.go:26`, `new-api@20d3e73:middleware/auth.go:344`.
- Legacy backfill: no owner backfill for token/log/usage was observed; observed migration machinery is AutoMigrate plus column/type migration helpers. Cites: `new-api@20d3e73:model/main.go:250`, `new-api@20d3e73:model/main.go:454`.

### One-API (MIT)

- Schema owner fields: `Token.UserId` and `Log.UserId` are the owner fields; token/log query helpers filter by `user_id`, and per-day stats also constrain by `user_id`. Cites: `one-api@8df4a26:model/token.go:23`, `one-api@8df4a26:model/token.go:39`, `one-api@8df4a26:model/token.go:106`, `one-api@8df4a26:model/log.go:15`, `one-api@8df4a26:model/log.go:125`, `one-api@8df4a26:model/log.go:225`.
- Handler/session check: dashboard token handlers read `ctxkey.Id`, create tokens with that user id, and update/delete through owner-scoped lookups; gateway token auth sets context user id from `token.UserId` after token/user status checks. Cites: `one-api@8df4a26:controller/token.go:16`, `one-api@8df4a26:controller/token.go:60`, `one-api@8df4a26:controller/token.go:123`, `one-api@8df4a26:controller/token.go:170`, `one-api@8df4a26:controller/token.go:188`, `one-api@8df4a26:middleware/auth.go:91`, `one-api@8df4a26:middleware/auth.go:110`, `one-api@8df4a26:middleware/auth.go:132`.
- Tenant(group) vs user: user has one group string; distributor resolves the user's group and routes channel selection by group/model, so observed relation is 1 user -> 1 group, group -> many users/channels. Cites: `one-api@8df4a26:model/user.go:48`, `one-api@8df4a26:middleware/distributor.go:23`, `one-api@8df4a26:middleware/distributor.go:47`.
- Legacy backfill: observed SQL backfills user quota by summing each user's token quota through the token owner relation. Cite: `one-api@8df4a26:bin/migration_v0.2-v0.3.sql:1`.

### CLIProxyAPI (MIT)

- Schema owner fields: no account-hub user owner table was observed for receipts/usage. Config auth uses an API-key list; Postgres storage creates config/auth blobs with only id/content/timestamps; usage records carry provider/model/client API key/auth account/source fields. Cites: `CLIProxyAPI@21fad9d:config.example.yaml:38`, `CLIProxyAPI@21fad9d:internal/access/config_access/provider.go:19`, `CLIProxyAPI@21fad9d:internal/store/postgresstore.go:121`, `CLIProxyAPI@21fad9d:sdk/cliproxy/usage/manager.go:13`, `CLIProxyAPI@21fad9d:internal/runtime/executor/helps/usage_helpers.go:156`.
- Handler/session check: config access authenticates that the supplied credential is in the configured key set; management exposes API key/usage queue endpoints, but there is no observed token.user_id == session.user_id check because no session-user model is present in the regions read. Cites: `CLIProxyAPI@21fad9d:internal/access/config_access/provider.go:55`, `CLIProxyAPI@21fad9d:internal/access/config_access/provider.go:88`, `CLIProxyAPI@21fad9d:internal/api/server.go:599`, `CLIProxyAPI@21fad9d:internal/api/handlers/management/usage.go:23`.
- Tenant(group) vs user: no tenant/group layer observed; isolation is by deployment/configured client key and upstream auth/account identity. Cites: `CLIProxyAPI@21fad9d:internal/runtime/executor/helps/usage_helpers.go:227`, `CLIProxyAPI@21fad9d:internal/runtime/executor/helps/usage_helpers.go:265`.
- Legacy backfill: README states built-in usage stats were removed and external keepers/dashboards persist queue data by account/model/status/token usage; no in-repo user-owner backfill pattern observed. Cite: `CLIProxyAPI@21fad9d:README.md:73`.

## §B Candidate Patterns

- Sidecar mapping: add a receipt-owner mapping table keyed by tenant/request/sequence -> user; lowest append-only risk, good for legacy partial backfill, extra join on read.
- Column add: add nullable `user_id` to receipt rows and write it on all new receipts; best long-term query shape, but backfilling old rows conflicts with append-only unless treated as a signed migration exception.
- Composite key/index: keep receipt primary identity but add `(tenant_id,user_id,request_id,receipt_sequence)` index/constraint for user-scoped reads; too disruptive as a primary key change.
- RBAC upper layer: keep receipt schema tenant-only and authorize via ledger/claim lookup; avoids receipt mutation but couples every read to billing tables and weakens self-contained receipt snapshots.
- No user-level isolation: acceptable only for single-user/local proxy patterns like CLIProxyAPI, not for HUAKAI account hub.

## §C HUAKAI Recommendation

- Recommended pattern: Column add for new data plus optional sidecar for legacy; do not rely on tenant-only receipt reads. HUAKAI already has `SessionIdentity.UserID`, while current receipt read path uses tenant/request only. Cites: `HUAKAI:backend/internal/auth/session_middleware.go:15`, `HUAKAI:backend/internal/audit/receipt_storage_pgx.go:109`, `HUAKAI:backend/internal/gatewayhttp/cost_receipt_handler.go:98`.
- Source of truth: propagate owner from billing claim/usage, not from user input. `billing_ledger_claims` and `usage_records` already carry `user_id`, and settler rejects mismatch against claim user. Cites: `HUAKAI:backend/sql/migrations/0002_observability_billing.up.sql:19`, `HUAKAI:backend/sql/migrations/0002_observability_billing.up.sql:121`, `HUAKAI:backend/internal/billing/settler.go:101`, `HUAKAI:backend/internal/billing/settler.go:117`.
- Read rule: user receipt endpoint should require tenant match and, for non-legacy receipts, user match. Legacy NULL-owner receipts should be admin-only or hidden from user endpoint unless a deterministic sidecar owner exists.
- Append-only constraint: receipt rows reject UPDATE/DELETE, and sequence support makes later corrections append new rows. Any direct historical column backfill needs explicit Owner-approved migration exception. Cites: `HUAKAI:backend/sql/migrations/0028_user_cost_receipts.up.sql:24`, `HUAKAI:backend/sql/migrations/0028_user_cost_receipts.up.sql:30`, `HUAKAI:backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:3`.

## §D Legacy Backfill Strategy Candidates

1. Deterministic join: infer owner from `billing_ledger_claims`/`usage_records` through request/audit/claim joins; use only where the join is 1:1 and auditable. HUAKAI already joins receipts inputs through claim/usage tables. Cite: `HUAKAI:backend/internal/audit/receipt_formatter.go:475`.
2. Permanent NULL: leave old receipts ownerless; user endpoint denies or hides them, admin/audit endpoint can still read by tenant. This avoids rewriting append-only rows.
3. Re-issue receipts: append a new receipt sequence with owner embedded after recalculation/resigning; highest audit clarity but requires product messaging and migration controls.

Recommended backfill posture: new writes non-null; deterministic legacy mapping in sidecar where provable; permanent NULL for ambiguous rows; no trigger bypass or destructive rewrite without Owner confirmation.

## §E Clean-Room

- LGPL/AGPL references were used only as behavior evidence with file:line anchors; no source code, comments, struct names beyond generic owner categories, schema layouts, or implementation algorithms are vendored into HUAKAI.
- MIT references can inform implementation more freely, but HUAKAI should still use its existing SessionIdentity + billing claim design rather than importing foreign package structure.
- Open questions: complete `api.github.com` recency for Sub2API/one-api/CLIProxyAPI outside sandbox; decide whether legacy NULL user receipts are hidden from user endpoint or shown with an explicit legacy state.

5-line Chinese summary:
1. 四个参考项目里，账号中心型项目普遍把 token/log/usage 绑定到 user owner；CLIProxyAPI 是本地代理型例外。
2. HUAKAI receipt 现在只有 tenant 隔离，但 session、claim、usage 已有 user 维度，推荐新 receipt 写入 user_id。
3. 没有功能缩水：tenant 隔离保留，新增用户隔离；legacy 通过 sidecar/NULL/重签三种路径处理。
4. clean-room 风险可控：LGPL/AGPL 只做行为级 paraphrase，不 vendor 代码/结构。
5. 需 Owner 确认：三项 GitHub API recency 复核、DB schema 变更、legacy NULL receipt 的用户端策略。

Source files read:
`sub2api`: `backend/ent/schema/api_key.go`, `backend/ent/schema/usage_log.go`, `backend/ent/schema/user.go`, `backend/ent/schema/user_allowed_group.go`, `backend/internal/server/middleware/api_key_auth.go`, `backend/internal/handler/usage_handler.go`, `backend/migrations/001_init.sql`, `backend/migrations/007_add_user_allowed_groups.sql`, `backend/migrations/014_drop_legacy_allowed_groups.sql`, `backend/migrations/027_usage_billing_consistency.sql`.
`new-api`: `model/token.go`, `model/log.go`, `model/usedata.go`, `middleware/auth.go`, `controller/token.go`, `controller/log.go`, `model/user.go`, `controller/group.go`, `model/main.go`.
`one-api`: `model/token.go`, `model/log.go`, `middleware/auth.go`, `controller/token.go`, `model/user.go`, `middleware/distributor.go`, `model/main.go`, `bin/migration_v0.2-v0.3.sql`.
`CLIProxyAPI`: `README.md`, `config.example.yaml`, `internal/store/postgresstore.go`, `internal/access/config_access/provider.go`, `internal/api/handlers/management/usage.go`, `sdk/cliproxy/usage/manager.go`, `internal/runtime/executor/helps/usage_helpers.go`, `internal/api/server.go`, `.huakai-head-sha`.
`HUAKAI`: `backend/internal/audit/receipt_storage_pgx.go`, `backend/internal/auth/session_middleware.go`, `backend/internal/gatewayhttp/cost_receipt_handler.go`, `backend/sql/migrations/0002_observability_billing.up.sql`, `backend/internal/billing/settler.go`, `backend/sql/migrations/0028_user_cost_receipts.up.sql`, `backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql`, `backend/internal/audit/receipt_formatter.go`.

Lane: prestudy
Agent: Codex GPT-5
UTC timestamp: 2026-05-23T14:44:28Z
