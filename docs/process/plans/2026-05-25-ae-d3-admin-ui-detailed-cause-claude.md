# 2026-05-25 AE-D3 Admin UI Detailed Cause Claude Lane Plan

| Field | Value |
| --- | --- |
| Owner directive | `AE-D3 Owner 决策点 (auth_expired synthesis §F): last_refresh_outcome 镜像 - admin UI 是否需要直接显示 detailed cause` |
| Lane | Claude perspective parallel-draft; written before execution; no git add/commit/push. |
| Scope | In: decide whether current admin surfaces should read detailed refresh cause from a materialized provider-account field or from refresh audit events. Out: implementing schema/UI/backend changes in this pass. |
| Success criteria | Owner can choose AE-D3 A/B/C with explicit schema, UI, audit, backfill, and cleanup consequences. |
| Blast radius | `provider_accounts.last_refresh_outcome`, `oauth_refresh_audit_events.outcome`, `/admin/v1/provider-accounts`, `/admin/v1/credentials/renew-status`, Admin Ops account and renew panels. |
| Failure modes | Weak decision could hide auth expiry from operators, duplicate audit truth without reconciliation, or force an expensive audit join into a frequently refreshed UI. |
| Metadata | Observed regions: 21 / Inferences: 7 / Open questions: 5 |

## §A 共识

1. `provider_accounts.last_refresh_outcome` is physically `text` with a CHECK created by 0006; the 0006 list does not include the four S1 detailed causes selected by auth-expired synthesis. `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:19`, `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:23`, `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:24`
2. The S1 commit updated only the refresh-audit outcome CHECK to accept the four detailed causes; it did not update the provider-account mirror CHECK. `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:1`, `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:17`, `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:20`
3. The generated auth refresh query reads and writes `provider_accounts.last_refresh_outcome` as `*string`, so option A/C is mechanically feasible but requires schema alignment before detailed causes can be persisted there. `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:31`, `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:65`, `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:158`
4. The generated billing account queries also select `provider_accounts.last_refresh_outcome` into account-selection/revalidation rows, so a detailed mirror would be visible to backend routing code even if routing does not currently use it. `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:87`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:159`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:429`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:507`
5. The provider-account admin API already returns `last_refresh_outcome`, but the current accounts page does not render it. `HUAKAI@local:backend/internal/db/admin/admin_provider_accounts.go:33`, `HUAKAI@local:backend/internal/db/admin/admin_provider_accounts.go:106`, `HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:145`, `HUAKAI@local:frontend/app/accounts/page.tsx:220`
6. The current renew-status page is not reading provider-account rows; it reads `account_credentials.last_refresh_outcome` through `/admin/v1/credentials/renew-status` and renders it in "Last Result". `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:453`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:462`, `HUAKAI@local:frontend/app/renew/page.tsx:40`, `HUAKAI@local:frontend/app/renew/page.tsx:175`, `HUAKAI@local:frontend/app/renew/page.tsx:222`
7. `account_credentials.last_refresh_outcome` is also `text`, but the inspected table definition does not constrain it to a fixed enum. `HUAKAI@local:backend/sql/migrations/0016_account_credentials.up.sql:39`, `HUAKAI@local:backend/sql/migrations/0016_account_credentials.up.sql:40`
8. Refresh audit rows already store detailed outcome plus sanitized error context as append-only evidence. `HUAKAI@local:backend/sql/queries/auth_audit.sql:2`, `HUAKAI@local:backend/sql/queries/auth_audit.sql:5`, `HUAKAI@local:backend/sql/queries/auth_audit.sql:14`, `HUAKAI@local:backend/sql/queries/auth_audit.sql:15`
9. Observability already maps the four detailed audit outcomes into warning/error severity; the audit table is usable for operator filtering today. `HUAKAI@local:backend/sql/queries/observability.sql:114`, `HUAKAI@local:backend/sql/queries/observability.sql:115`, `HUAKAI@local:backend/sql/queries/observability.sql:116`
10. Current credentialworker failure path records detailed sidecar audit outcomes when the provider error carries one; otherwise it falls back to a permanent-disable outcome. `HUAKAI@local:backend/internal/credentialworker/scheduler.go:200`, `HUAKAI@local:backend/internal/credentialworker/scheduler.go:201`, `HUAKAI@local:backend/internal/credentialworker/scheduler.go:204`

## §B 冲突

| Option | What changes | Strength | Cost / Risk | Claude read |
| --- | --- | --- | --- | --- |
| A. Mirror detailed cause | Extend `provider_accounts.last_refresh_outcome` to accept `auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, `account_disabled`; write those values when refresh fails; UI reads the field directly. | Fastest admin UX and matches existing API shape. | Schema change, duplicate audit truth, historical rows before 0055 need explicit "unknown/not backfilled" treatment. | Good if Owner prioritizes immediate account-list visibility. |
| B. Do not mirror | Keep provider-account field coarse/current-success only; detailed cause comes from `oauth_refresh_audit_events`. | Cleaner normalized model; audit remains sole detailed evidence. | UI needs latest-audit query or join; append-only stream can lag display unless a query materializes latest per account. | Good long-term if Owner accepts slower UI implementation. |
| C. Mirror now, clean/reconcile after 30 days | Use provider-account field as a bounded current-state cache; audit table remains canonical; after audit latest-cause endpoint is stable, Owner decides whether to clear/migrate the mirror. | Ships operator clarity while keeping a path back to a cleaner model. | Still requires schema change now and a dated cleanup gate. | Claude recommendation. |

Primary conflict: A/C optimize the operator's first screen; B optimizes data-model purity. The current codebase already has both direct fields and append-only audit, so the decision is not "field vs table"; it is whether the direct field is allowed to carry detailed failure class.

## §C Claude 补充

1. The AE-D3 question names `provider_accounts.last_refresh_outcome`, but the currently visible renew panel uses `account_credentials.last_refresh_outcome`. Owner should decide whether "admin UI" means Panel 1 provider-account list, Panel 5 renew-status, or both. `HUAKAI@local:frontend/app/accounts/page.tsx:224`, `HUAKAI@local:frontend/app/renew/page.tsx:175`
2. If Owner chooses A/C for Panel 5, implementation must either also write detailed causes into `account_credentials.last_refresh_outcome` or add a latest-audit lookup to the renew-status endpoint; changing only provider_accounts will not update the inspected renew UI. `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:457`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:462`, `HUAKAI@local:frontend/lib/api/types.ts:321`, `HUAKAI@local:frontend/lib/api/types.ts:334`
3. If Owner chooses A/C for Panel 1 only, the accounts page needs a display column or details drawer; the backend already serializes the field. `HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`, `HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:145`, `HUAKAI@local:frontend/app/accounts/page.tsx:223`
4. Backfill should be conservative: do not synthesize detailed causes for historical rows without a same-account audit row after 0055. Mark pre-decision field values as existing state, not proof of a specific cause. This follows Truth-First: no detailed cause without an observed audit event.
5. The mirror should be documented as "current operator summary", not audit evidence. The audit event remains canonical for forensic timeline and severity filters. `HUAKAI@local:backend/internal/credentialworker/audit.go:56`, `HUAKAI@local:backend/internal/credentialworker/audit.go:63`, `HUAKAI@local:backend/internal/credentialworker/audit.go:66`
6. If B is selected, the plan must include a dedicated query for latest refresh-audit event per provider account and must prove tenant scoping, pagination stability, and no token leakage in payload.
7. If C is selected, set the cleanup review date to 2026-06-24 UTC and record it in the implementation commit body or a follow-up review file.

## §D 执行序

Decision points before implementation:

1. D-1 UI target: provider-account list, credential renew-status list, or both.
2. D-2 Persistence choice: A mirror, B audit-only, or C temporary mirror.
3. D-3 Mirror column scope: `provider_accounts` only, `account_credentials` only, or both.
4. D-4 Physical values: use S1 DB spellings exactly (`auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, `account_disabled`) or introduce a separate UI label layer only.
5. D-5 Backfill policy: no backfill, audit-derived latest backfill, or one-time best-effort with explicit "derived" provenance.
6. D-6 Audit canonicality: whether UI must link from mirror to latest audit event for proof.
7. D-7 Cleanup gate for C: exact date, owner, and removal criteria.
8. D-8 Test gate: whether this AE-D3 slice requires real PostgreSQL migration tests or can land as focused handler/UI tests plus existing 0055 PG evidence.

Concrete execution order after Owner approval:

1. Re-read newest migration head and confirm no later migration already changed provider-account CHECK.
2. Add failing tests first:
   - provider-account update rejects/accepts selected detailed mirror values under real PG if A/C touches `provider_accounts`;
   - renew-status endpoint returns detailed cause if Panel 5 is in scope;
   - accounts or renew UI fixture renders detailed cause, not only `!= bad`.
3. If A/C: add a new migration that extends `provider_accounts.last_refresh_outcome` CHECK; do not create files in frozen packages.
4. If A/C and Panel 1: wire writer path so detailed failure outcome updates provider-account mirror only after/with the same successful audit transaction.
5. If A/C and Panel 5: write or derive detailed cause for `account_credentials.last_refresh_outcome` with an explicit provenance rule.
6. If B: add a tenant-scoped latest-audit query for the admin endpoint, using `oauth_refresh_audit_events(provider_account_id, occurred_at DESC)` and preserving pagination determinism.
7. Update frontend display in the chosen panel(s): direct field display for A/C, latest audit display for B.
8. Update OpenAPI/types only if API response shape changes; if only string values expand, document but avoid unnecessary schema churn.
9. Run focused backend tests, frontend typecheck/build if UI changes, and migration round-trip if schema changes.
10. Stage intended diff only and run mandatory per-commit Codex review before any commit.

## §E 借鉴对照

| Reference | Observed behavior | HUAKAI implication |
| --- | --- | --- |
| Sub2API | The inspected monitor flow writes per-check history with status, latency, ping latency, message, and checked time, then marks the monitor checked; latest-list queries return the latest status/latency without the verbose message. `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:269`, `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:280`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:258`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:263` | Supports a split model: concise current status for list UI, detailed message in history/audit. This leans toward C if the list needs immediate clarity without loading full audit text. |
| Sub2API | The inspected ops path sanitizes upstream error text before storage/display. `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:210`, `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:220`, `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:283` | HUAKAI mirror must carry only categorical cause, not raw provider message; redacted message belongs in audit payload. |
| New-API | The inspected channel path can disable a channel from error policy, stores a reason/time on the channel record, and also notifies operators. `QuantumNous/new-api@20d3e7373452:service/channel.go:18`, `QuantumNous/new-api@20d3e7373452:service/channel.go:28`, `QuantumNous/new-api@20d3e7373452:model/channel.go:734`, `QuantumNous/new-api@20d3e7373452:model/channel.go:736` | Supports current-state materialization for operator recovery. HUAKAI should avoid copying the shape, but a categorical current mirror is a safe equivalent. |
| LiteLLM | The inspected health-check storage has a table with status, counts, error message, response time, details, checker, and timestamp; latest/history endpoints expose current and historical views separately. `BerriAI/litellm@79b457867197:litellm/proxy/schema.prisma:1045`, `BerriAI/litellm@79b457867197:litellm/proxy/schema.prisma:1049`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:455`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:1179` | Supports C: keep a current operator summary while retaining historical evidence. |
| Portkey Gateway | The inspected local gateway UI streams request logs with HTTP status and a details view showing request/response data. `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:75`, `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:80`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:1980`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:2033` | Operator UI benefits from quick status on the list plus a drilldown. For HUAKAI, mirror should be summary-only and link to audit for details. |

Reference comparison count: 5.

## §F Owner 决策清单

Recommended by Claude lane: choose C, "mirror detailed cause now as a current-state cache, keep audit table canonical, and schedule a 30-day cleanup/reconciliation decision."

Owner choices:

1. AE-D3-A: Mirror detailed outcome into `provider_accounts.last_refresh_outcome`; update provider-account CHECK and UI direct-read path.
2. AE-D3-B: Do not mirror; keep provider-account outcome coarse; add latest-audit query/API/UI path for detailed cause.
3. AE-D3-C: Mirror detailed outcome now; document audit as canonical; on 2026-06-24 decide whether to keep, clear, or replace mirror with audit-derived latest-cause endpoint.
4. AE-D3-D: Apply chosen detailed cause to Panel 1 provider accounts only.
5. AE-D3-E: Apply chosen detailed cause to Panel 5 credential renew-status only.
6. AE-D3-F: Apply chosen detailed cause to both panels, with separate write/query rules for `provider_accounts` and `account_credentials`.
7. AE-D3-G: No historical backfill; only new refresh attempts after migration show detailed mirror.
8. AE-D3-H: Audit-derived backfill allowed only when latest same-account audit row has one of the four S1 detailed outcomes.

Open questions:

1. Does Owner require detailed cause in the account list, renew panel, or both?
2. Should `rate_limit_exceeded` be displayed as a warning while auth/risk/disabled display as error?
3. Should a mirror value link to a specific audit `request_id` in the UI?
4. If C is selected, who owns the 2026-06-24 cleanup gate?
5. Should `account_credentials.last_refresh_outcome` remain unconstrained text or receive its own CHECK in a later schema gate?

Clean-room risk: low. This plan uses reference projects only for behavior-level operator-surface patterns and cites source lines as evidence anchors. It does not copy non-MIT source code, comments, schemas, UI source, internal names, tests, or file structures.

Security risk: medium. Detailed categorical cause is safe for UI, but raw provider text must stay sanitized/redacted in audit payloads; no token bytes or raw OAuth response bodies should be mirrored.

功能缩水: none. All options preserve detailed cause; the choice only determines whether the first-screen UI reads it from a mirror, an audit query, or a temporary mirror plus cleanup gate.

Owner 中文摘要：本 Claude lane 计划基于真实代码读取确认，HUAKAI 当前 `provider_accounts.last_refresh_outcome` 已被后端和 API 暴露但 schema CHECK 未包含 S1 四类详细原因，`oauth_refresh_audit_events` 已包含这些详细原因且 observability 可过滤；同时当前 renew UI 实际读的是 `account_credentials.last_refresh_outcome`，所以 AE-D3 必须先确认目标是账号列表、renew 面板还是两者。Claude 推荐 C：先镜像详细 cause 作为当前状态缓存，audit table 仍是 canonical forensic evidence，并在 2026-06-24 做清理/保留决策；没有功能缩水，clean-room 风险低，安全风险集中在不得把 raw OAuth 错误/secret 镜像到 UI。

Source files read: backend/internal/db/auth/auth_credentials.sql.go; backend/internal/db/billing/pool_accounts.sql.go; backend/sql/queries/auth_credentials.sql; backend/sql/queries/pool_accounts.sql; backend/sql/migrations/0006_upstream_credential_management.up.sql; backend/sql/migrations/0016_account_credentials.up.sql; backend/sql/migrations/0055_audit_outcome_check.up.sql; backend/sql/queries/auth_audit.sql; backend/sql/queries/observability.sql; backend/internal/db/auth/auth_audit.sql.go; backend/internal/credentialworker/audit.go; backend/internal/credentialworker/scheduler.go; backend/internal/auth/audit.go; backend/internal/credentialstore/postgres_store.go; backend/internal/db/admin/admin_provider_accounts.go; backend/internal/gatewayhttp/admin_pool_accounts_handler.go; frontend/app/accounts/page.tsx; frontend/app/renew/page.tsx; frontend/lib/api/types.ts; /home/codex/refs/sub2api/backend/internal/service/channel_monitor_service.go; /home/codex/refs/sub2api/backend/internal/repository/channel_monitor_repo.go; /home/codex/refs/sub2api/backend/internal/service/ops_service.go; /home/codex/refs/new-api/service/channel.go; /home/codex/refs/new-api/model/channel.go; /home/codex/refs/litellm/litellm/proxy/schema.prisma; /home/codex/refs/litellm/litellm/proxy/health_endpoints/_health_endpoints.py; /home/codex/refs/portkey-gateway/src/middlewares/log/index.ts; /home/codex/refs/portkey-gateway/src/public/index.html

Lane: specifier

Agent: Codex GPT-5 coding agent, Claude-lane draft requested by Owner

UTC timestamp: 2026-05-25T00:35:35Z
