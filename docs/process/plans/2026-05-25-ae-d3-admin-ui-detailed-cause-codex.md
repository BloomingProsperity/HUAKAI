# 2026-05-25 AE-D3 admin UI detailed cause — Codex lane plan

| Owner directive | `CONTEXT: AE-D3 Owner 决策点 (auth_expired synthesis §F): last_refresh_outcome 镜像 - admin UI 是否需要直接显示 detailed cause` |
| Scope | 独立起草 Codex 视角决策计划；只读 HUAKAI current state、auth_expired synthesis、CLIProxyAPI MIT 与 LiteLLM MIT source snippets；不执行实现、不 staging、不 commit。 |
| Success criteria | Owner can choose whether the admin UI directly displays detailed refresh causes, and separately whether that display is backed by a `provider_accounts.last_refresh_outcome` mirror, audit-derived projection, or hybrid cache. |
| Time estimate | Decision plan: 1-2 hours wall clock. If approved later: API-only B path 0.5-1 day; A/C schema + API + UI 1-2 days depending on Panel 1/Panel 5 scope. |
| Blast radius | `provider_accounts.last_refresh_outcome`; `account_credentials.last_refresh_outcome`; `oauth_refresh_audit_events.outcome`; `/admin/v1/provider-accounts`; `/admin/v1/credentials/renew-status`; Admin Ops Provider Accounts and Renew Status panels. |
| Failure modes | A mirror can become stale or conflict with audit; B audit-only can delay UI if latest-audit projection is not built; C hybrid can confuse canonical vs cache; UI may show secret-bearing error messages if redaction rules are bypassed. Mitigation: audit remains canonical, UI default shows outcome class only, redacted message requires explicit detail surface and test coverage. |
| Decision points | §F AE-D3-D1..D9 below. |
| Pre-execution checklist | 1. Owner chooses A/B/C data-source strategy. 2. Owner chooses Panel 1, Panel 5, or both. 3. Confirm no new files in frozen packages. 4. If A/C, write migration/test for provider-account CHECK before writing mirror values. 5. If B/C, add latest-audit query or DTO projection with no raw-token fields. 6. Add discriminating handler/UI tests before implementation. |

Metadata:
- Observed regions: 31
- Inferences: 7
- Open questions: 9
- Lane independence note: a broad `rg` command accidentally surfaced snippets from the disallowed Claude lane file. I did not open it, did not cite it, and do not rely on it below; this plan uses primary HUAKAI files plus the reference reads listed in the provenance tail.

## A. Current HUAKAI Facts

1. `provider_accounts.last_refresh_outcome` was added as nullable `text` with a CHECK that only accepts the older refresh outcome set; the four S1 detailed causes are not in that original CHECK. `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:19`, `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:23`, `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:24`
2. `oauth_refresh_audit_events.outcome` has already been widened by S1 to accept `auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, and `account_disabled`. `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:1`, `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:17`, `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:20`
3. `account_credentials.last_refresh_outcome` exists as nullable `text` with no inspected enum CHECK in its table definition. `HUAKAI@local:backend/sql/migrations/0016_account_credentials.up.sql:39`, `HUAKAI@local:backend/sql/migrations/0016_account_credentials.up.sql:40`
4. Auth DB generated code reads and writes `provider_accounts.last_refresh_outcome` as `*string`; successful CAS refresh can persist an outcome there after schema allows it. `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:31`, `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:65`, `HUAKAI@local:backend/internal/db/auth/auth_credentials.sql.go:158`
5. Billing selection/revalidation generated rows also carry `provider_accounts.last_refresh_outcome`, so a mirror is visible to backend account-selection data structures even if routing should not use it as canonical evidence. `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:158`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:159`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:506`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:507`
6. The worker account-refresh list does not select `last_refresh_outcome`; it selects account identity/vendor/expiry only. Failure audit currently has enough account identity to write audit and health, but not a preloaded mirror value. `HUAKAI@local:backend/sql/queries/pool_accounts.sql:258`, `HUAKAI@local:backend/sql/queries/pool_accounts.sql:260`, `HUAKAI@local:backend/sql/queries/pool_accounts.sql:264`, `HUAKAI@local:backend/internal/db/billing/pool_accounts.sql.go:322`
7. The credential worker writes detailed failure outcomes into `oauth_refresh_audit_events` via `recordAuditString`, and maps selected outcomes into provider-account health transitions; it does not, in the inspected failure path, update `provider_accounts.last_refresh_outcome`. `HUAKAI@local:backend/internal/credentialworker/scheduler.go:200`, `HUAKAI@local:backend/internal/credentialworker/scheduler.go:202`, `HUAKAI@local:backend/internal/credentialworker/audit.go:56`, `HUAKAI@local:backend/internal/credentialworker/health_state.go:57`
8. Provider-account admin API already returns `last_refresh_outcome`; the SQL row and response DTO expose the field. `HUAKAI@local:backend/internal/db/admin/admin_provider_accounts.go:32`, `HUAKAI@local:backend/internal/db/admin/admin_provider_accounts.go:33`, `HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:144`, `HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:145`
9. Provider Accounts UI currently renders health, credential state, concurrency, priority, enabled, and operations; it does not render `last_refresh_at` or `last_refresh_outcome`. `HUAKAI@local:frontend/app/accounts/page.tsx:220`, `HUAKAI@local:frontend/app/accounts/page.tsx:227`, `HUAKAI@local:frontend/app/accounts/page.tsx:254`, `HUAKAI@local:frontend/app/accounts/page.tsx:261`
10. Renew Status UI already renders a "Last Result" column, but it reads `account_credentials.last_refresh_outcome`, `failure_class`, and `failure_count` through `/admin/v1/credentials/renew-status`, not the provider-account mirror. `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:453`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:462`, `HUAKAI@local:frontend/app/renew/page.tsx:40`, `HUAKAI@local:frontend/app/renew/page.tsx:175`
11. Credential refresh success updates `account_credentials.last_refresh_outcome` with the passed outcome, while credential refresh failure currently stores coarse `refresh_failed` plus `failure_class`. `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:709`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:724`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:766`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:772`
12. OpenAPI exposes `last_refresh_outcome` as loose string/null on provider accounts and credential renew status; it does not enumerate the four detailed causes. `HUAKAI@local:docs/openapi/openapi.yaml:3794`, `HUAKAI@local:docs/openapi/openapi.yaml:3795`, `HUAKAI@local:docs/openapi/openapi.yaml:3961`, `HUAKAI@local:docs/openapi/openapi.yaml:3962`

Inference 1: Changing only `provider_accounts.last_refresh_outcome` will help Panel 1 after UI work, but will not by itself change the current Panel 5 "Last Result" display, because Panel 5 is sourced from `account_credentials`.

Inference 2: If Owner wants a direct operator surface, the product decision is "show detailed cause in UI", while the technical decision is "which source backs that visible field." Those should not be collapsed.

## B. Reference Contrast

| Ref | Observed behavior | HUAKAI-fit reading |
|---|---|---|
| CLIProxyAPI MIT | Unauthorized refresh failure is persisted into the account's current state/error fields and disables further automatic refresh scheduling for that auth entry. `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor.go:4164`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor.go:4173`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:81`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:89` | Supports a current-state cache for operator triage. It does not prove the cache should be canonical when HUAKAI already has an append-only audit table. |
| CLIProxyAPI MIT docs | The project points operators to local dashboards/managers that track request/account/model/channel/status/token usage and account-pool anomaly cleanup. `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:81`, `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:85` | Supports showing actionable account-level cause in admin UI; operators should not need DB surgery to locate broken accounts. |
| LiteLLM MIT | Health checks return healthy/unhealthy endpoint lists and retain exception/status detail separately from the health-state map. `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:269`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:274`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:292`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:307` | Supports keeping operational detail queryable without overloading a single account row field as the sole truth. |
| LiteLLM MIT | Shared health-check coordination uses cached results and lock/TTL coordination across pods. `BerriAI/litellm@79b457867197:litellm/proxy/health_check_utils/shared_health_check_manager.py:16`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check_utils/shared_health_check_manager.py:30`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check_utils/shared_health_check_manager.py:190`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check_utils/shared_health_check_manager.py:205` | Supports a projection/cache layer for fast UI reads, but cache semantics must be explicit and non-canonical. |

Clean-room note: The references above are behavior evidence only. No upstream source names, schemas, comments, or UI implementation should flow into HUAKAI implementation.

## C. A/B/C Options

| Option | Shape | Pros | Risks | Required work |
|---|---|---|---|---|
| A. Mirror detailed cause into `provider_accounts.last_refresh_outcome` | Extend provider-account CHECK to accept the four detailed causes; on classified failure, update provider account mirror in the same transaction as refresh audit/ledger; Panel 1 renders the field directly. | Fastest account-list triage; matches existing provider-account API shape; aligns with current-state cache pattern observed in CLIProxyAPI. | Duplicate truth; stale cache if audit succeeds but mirror path is missed; schema change; Panel 5 still unchanged unless credential outcome is also updated or joined. | Migration + PG CHECK test; worker failure path mirror update; Provider Accounts UI column/detail; tests proving audit and mirror cannot diverge on tx failure. |
| B. Audit-canonical direct UI display | Do not mirror detailed cause into provider account. Add latest-audit projection for admin APIs, e.g. `last_refresh_detail_outcome`, `last_refresh_detail_at`, optional `last_refresh_error_class`; Panel 1 and/or Panel 5 render this projection. | Audit remains sole detailed truth; no provider-account CHECK change; same source can serve both panels; better provenance for operator trust. | More query/API work; needs latest-per-account indexing/query discipline; direct UI display is not a raw row-field read. | Latest audit query or service projection; handler DTO field; UI render; tests for missing audit, old audit, and redacted detail. |
| C. Hybrid cache + audit canonical | Allow provider-account mirror for a small current-state cache, but UI labels detailed cause as audit-backed when latest audit exists and cached when only mirror exists. Schedule cleanup/decision after production soak. | Fast Panel 1 triage plus canonical audit detail; tolerant of rollout gaps; useful when API latency matters. | Most moving parts; two fields can disagree; needs explicit reconciliation policy; highest test burden. | A work plus B projection or reconciliation; UI provenance label; periodic or query-time mismatch detection; cleanup decision date. |

## D. Codex Preference

Codex recommendation: choose **B for the data model**, while answering "yes" to direct admin UI display.

Reasoning:

1. Operators should see detailed cause directly in admin UI. CLIProxyAPI-style account triage and HUAKAI's own Renew Status panel both point toward visible account/credential failure state, not hidden DB-only evidence. `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:85`, `HUAKAI@local:frontend/app/renew/page.tsx:175`
2. The display should not make `provider_accounts.last_refresh_outcome` canonical for detailed failure cause. HUAKAI already has `oauth_refresh_audit_events` with the S1 four-class CHECK, request id, redacted error fields, and occurred time. `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:17`, `HUAKAI@local:backend/internal/db/auth/auth_audit.sql.go:22`, `HUAKAI@local:backend/internal/db/auth/auth_audit.sql.go:26`, `HUAKAI@local:backend/internal/db/auth/auth_audit.sql.go:27`
3. A pure mirror would not solve Panel 5 as observed, because Panel 5 reads `account_credentials.last_refresh_outcome`. A B-style audit projection can serve both Panel 1 and Panel 5 without creating two mirrors. `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:459`, `HUAKAI@local:backend/internal/credentialstore/postgres_store.go:462`, `HUAKAI@local:frontend/lib/api/types.ts:321`, `HUAKAI@local:frontend/lib/api/types.ts:334`
4. If Owner needs a very small immediate slice, A is acceptable only for Panel 1 and only if the mirror is documented as a current-state cache, not evidence. The migration must land before any detailed failure writes, because the current provider-account CHECK rejects the new detailed causes. `HUAKAI@local:backend/sql/migrations/0006_upstream_credential_management.up.sql:24`, `HUAKAI@local:backend/sql/migrations/0055_audit_outcome_check.up.sql:17`

## E. Execution Shape After Owner Decision

If Owner picks A:

1. Add migration extending `provider_accounts.last_refresh_outcome` CHECK to the old values plus the four detailed causes.
2. Add a PG migration test proving `auth_expired` is accepted and an unknown value is rejected.
3. Update credentialworker failure-audit transaction to update provider-account mirror with the same detailed outcome.
4. Add tests where audit insert/ledger append failure rolls back mirror update.
5. Render Panel 1 last refresh time/outcome with outcome-class coloring and no raw error message.

If Owner picks B:

1. Add or extend a non-frozen DB query/service projection for latest `oauth_refresh_audit_events` per provider account and/or credential.
2. Add DTO fields distinct from the legacy mirror, for example `last_refresh_detail_outcome`, `last_refresh_detail_at`, `last_refresh_detail_source`.
3. Render Panel 1 and/or Panel 5 from those fields; keep `last_refresh_outcome` display as legacy/coarse only if still useful.
4. Add tests for newest audit wins, missing audit shows neutral state, and redacted message is not shown unless explicitly approved.

If Owner picks C:

1. Implement A's schema and mirror under "cache" semantics.
2. Implement B's audit-derived projection where detailed provenance is required.
3. Add mismatch tests and UI source labeling.
4. Add a dated follow-up decision: keep mirror, clear mirror after display moves to audit projection, or split into a new cache field.

Package discipline:
- Do not add files under frozen `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Low-risk existing-file edits in `gatewayhttp` handlers are acceptable if Owner chooses UI/API changes there; new helper code should prefer non-frozen packages.
- Frontend file edits are low risk but must include discriminating tests or fixture review; UI must not display token, credential payload, raw upstream body, or unredacted OAuth error text.

## F. Owner Decision Checklist

| ID | Decision | Options | Codex recommendation |
|---|---|---|---|
| AE-D3-D1 | Should admin UI directly show detailed refresh cause? | Yes / No | **Yes**, because operators need account triage without DB queries. |
| AE-D3-D2 | Which panel is in scope? | Panel 1 Provider Accounts / Panel 5 Renew Status / Both | **Both eventually; first slice Panel 5 if credential renew ops is the target, Panel 1 if pool routing ops is the target.** Owner must choose first slice. |
| AE-D3-D3 | Data source for visible detailed cause | A mirror / B audit projection / C hybrid | **B audit projection**. A only if Owner prioritizes shortest Panel 1 path. |
| AE-D3-D4 | If A/C, should provider-account CHECK be widened? | Yes / No | **Yes before any write**; otherwise detailed failure writes can fail at DB. |
| AE-D3-D5 | Should `account_credentials.last_refresh_outcome` also carry detailed causes? | Yes / No / derive from audit only | **Derive from audit only for detail**; keep credential field as current credential-refresh summary unless Owner wants another mirror. |
| AE-D3-D6 | How much detail can UI show by default? | outcome class only / class + redacted error class / class + redacted message | **Outcome class + timestamp by default; redacted error class in detail drawer; no redacted message until Owner approves UX/security posture.** |
| AE-D3-D7 | Backfill policy | No backfill / audit-derived backfill / synthetic inference | **No synthetic backfill**. Only audit-derived projection from observed audit rows. |
| AE-D3-D8 | Test gate | UI fixture only / handler tests / PG + handler + UI | **PG required for A/C; handler + UI required for B; all tests must be mutation-discriminating.** |
| AE-D3-D9 | OpenAPI enum tightening | Keep loose strings / enumerate detailed causes / add separate typed detail field | **Separate typed detail field if B/C; avoid tightening legacy `last_refresh_outcome` until source semantics are settled.** |

## G. Acceptance Tests To Require Later

1. A/B/C common: classified `auth_expired` audit appears in admin response as the selected detailed cause; a fixture with only coarse `refresh_failed` must not pass.
2. A/C: transaction failure after mirror update attempt rolls back both mirror and audit/ledger; test must assert exact previous mirror value remains.
3. B/C: two audit rows for same account return the newest `occurred_at`; older detailed cause must not win.
4. Panel 5: provider-account mirror alone must not satisfy the test if the endpoint is supposed to show credential renew cause.
5. UI: detail cell must render `auth_expired` exactly and must not render credential bytes, refresh token, raw upstream body, or unredacted OAuth message.

## H. Risks And Assumptions

- Assumption: S1 four-class outcome set remains `auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, `account_disabled`.
- Assumption: `oauth_refresh_audit_events` is the canonical evidence trail for refresh failure details.
- Risk: Option A can create false confidence if mirror write succeeds in one path but not every failure path.
- Risk: Option B can grow query complexity; mitigate with latest-per-account index review before implementation.
- Risk: Existing frontend `HealthState` still lists older values while 0056 changes provider-account health values to `healthy`, `throttled`, `revoked`, `cooldown`; AE-D3 UI work should not hide that mismatch if it blocks rendering. `HUAKAI@local:frontend/lib/api/types.ts:16`, `HUAKAI@local:backend/sql/migrations/0056_provider_account_health_state_check.up.sql:17`, `HUAKAI@local:backend/sql/migrations/0056_provider_account_health_state_check.up.sql:18`
- Clean-room risk: Low if references remain behavior-only and no implementation names/layouts/tests are copied.
- Security risk: Medium if UI expands from outcome class into error detail; default must stay sanitized and token-leakage-safe.

## I. Open Questions

1. Does AE-D3 mean "direct display somewhere in admin UI" or specifically "Panel 1 reads provider-account row field directly"?
2. Is Panel 5 Renew Status the primary operator surface for credential refresh failures?
3. Should `rate_limit_exceeded` be visible in the same column as auth failures, or separated from auth-cause display?
4. Should `risk_control_triggered` trigger a stronger visual severity than `auth_expired`?
5. Should `account_disabled` be treated as a permanent state requiring manual re-enable copy?
6. Should admin API include audit event id/request id for click-through traceability?
7. Should redacted error message be visible to tenant operators or platform admins only?
8. Should OpenAPI define a new enum for detailed cause now, or stay loose until the source strategy is chosen?
9. Should the provider-account mirror be cleared on refresh success if Owner chooses B?

## Clean-room Provenance Tail

Source files read:
- `backend/internal/db/auth/auth_credentials.sql.go`
- `backend/internal/db/auth/auth_audit.sql.go`
- `backend/internal/db/billing/pool_accounts.sql.go`
- `backend/sql/queries/pool_accounts.sql`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0006_upstream_credential_management.up.sql`
- `backend/sql/migrations/0016_account_credentials.up.sql`
- `backend/sql/migrations/0055_audit_outcome_check.up.sql`
- `backend/sql/migrations/0056_provider_account_health_state_check.up.sql`
- `backend/internal/db/admin/admin_provider_accounts.go`
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`
- `backend/internal/gatewayhttp/admin_credentials_handler.go`
- `backend/internal/credentialstore/postgres_store.go`
- `backend/internal/credentialworker/audit.go`
- `backend/internal/credentialworker/health_state.go`
- `backend/internal/credentialworker/refresher.go`
- `backend/internal/credentialworker/scheduler.go`
- `backend/internal/auth/audit.go`
- `frontend/app/accounts/page.tsx`
- `frontend/app/renew/page.tsx`
- `frontend/lib/api/types.ts`
- `frontend/lib/api/providerAccounts.ts`
- `frontend/lib/api/renew.ts`
- `docs/openapi/openapi.yaml`
- `docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md`
- `docs/process/plans/2026-05-24-auth-expired-schema-gate-codex.md`
- `/home/codex/refs/CLIProxyAPI/LICENSE`
- `/home/codex/refs/CLIProxyAPI/README_CN.md`
- `/home/codex/refs/CLIProxyAPI/sdk/cliproxy/auth/conductor.go`
- `/home/codex/refs/CLIProxyAPI/sdk/cliproxy/auth/conductor_scheduler_refresh_test.go`
- `/home/codex/refs/litellm/LICENSE`
- `/home/codex/refs/litellm/litellm/proxy/health_check.py`
- `/home/codex/refs/litellm/litellm/proxy/health_check_utils/shared_health_check_manager.py`

Process incident:
- Disallowed file snippets surfaced accidentally via broad search output: `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md`. Not cited; not used as evidence.

Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-25T00:51:50Z
