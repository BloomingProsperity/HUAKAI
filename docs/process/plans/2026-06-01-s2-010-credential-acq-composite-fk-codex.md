# 2026-06-01 S2-010 credential acquisition composite FK - Codex plan

| Field | Value |
| --- | --- |
| Owner directive | "Fix audit finding S2-010 to PRODUCTION QUALITY." |
| Scope | In: harden `credential_acquisition_flow_sessions.provider_account_id` so the database enforces the row's `tenant_id` matches the referenced Provider Account; add a discriminating real-Postgres regression. Out: auth core, billing, quota, runtime credential finalization behavior, frozen package additions, reference-derived code. |
| Success criteria | Current code premise is verified from source; migration adds same-tenant composite FK; test fails if the FK is reverted; `go test` for `credentialacq` and `go build ./...` pass; mutation check is observed red; commit and push land only intended files. |
| Time estimate | 45-75 minutes wall clock; mostly test/migration verification. |
| Blast radius | Database schema only for credential acquisition flow session creation. Existing cross-tenant/orphan rows fail migration fast and require data cleanup. No credential bytes, billing rows, auth core, quota state, or production secrets are touched. |
| Failure modes | Missing existing parent unique key: rely on `provider_accounts_tenant_id_id_key` from migration 0040. Existing bad rows: migration fails instead of preserving unsafe data. Weak test: use two tenants and deliberately pair tenant A with tenant B's account so broken single-column FK would accept it. Historical migration ordering: add a new 0066 migration rather than editing 0019. |
| Decision points | Database schema is high-risk by rule, but this task is an explicit Owner-authorized fix and is limited to enforcing an existing DR-001 invariant. Owner park flag will be `risk:schema` in final output. |
| Pre-execution checklist | 1. Read audit row and HUAKAI source. 2. Read DR-001/CMB/spec context. 3. Read reference source for nearest acquisition/session patterns without copying. 4. Verify premise at HEAD. 5. Add failing test before production migration edit. 6. Implement minimal migration. 7. Run mutation, tests, build, review, commit, push. |
| Target files | `backend/sql/migrations/0066_credential_acq_session_composite_fk.up.sql` (new, not frozen); `backend/sql/migrations/0066_credential_acq_session_composite_fk.down.sql` (new, not frozen); `backend/internal/credentialacq/session_store_realpg_test.go` (existing test file, not frozen). |

## Root-Cause Statement

`credential_acquisition_flow_sessions` stores both `tenant_id` and `provider_account_id`, but migration 0019 only ties `provider_account_id` to `provider_accounts(id)`. `PostgresSessionStore.CreateFromStart` builds the session row from caller-provided `TenantID` and `ProviderAccountID`, and `Create` inserts those values directly. The later `credentialstore.ensureProviderAccountTenant` check protects credential finalization, but not flow creation or operator-visible flow state. DR-001 requires tenant-aware schema from day one, so this table needs a database invariant matching the later hardening pattern used for `account_credentials`.

## Reference Source

Nearest reference behavior is acquisition/session correlation, not multi-tenant composite FK enforcement. Sub2API creates provider OAuth session state and later checks callback state before exchange (`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/openai_oauth_service.go:45`, `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/openai_oauth_service.go:131`). New API's Codex OAuth path scopes browser session values by channel id and re-loads the channel before saving generated credential material (`new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/codex_oauth.go:75`, `new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/codex_oauth.go:126`). HUAKAI's delta is stricter: DR-001 tenant/account pairing is enforced in PostgreSQL before any finalizer path.
