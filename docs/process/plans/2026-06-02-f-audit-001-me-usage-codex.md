# 2026-06-02 F-AUDIT-001 Me Usage API Codex Plan

| Owner directive | "Implement an END-USER self-service consumption API so a user can query THEIR OWN real consumption" |
| Scope | Add `GET /v1/me/usage` for inbound API-key callers. Create a new non-frozen Go package `backend/internal/meusagehttp`. Modify route wiring only in `backend/cmd/gateway/routes.go`. No schema changes, no auth-core changes, no billing-ledger writes, no duplicated SQL. |
| Success criteria | Authenticated caller receives only records scoped by auth-derived `tenant_id` and `api_key_id`; response includes requested model, upstream model, real cost, provider/provider account id, ledger id, verify hint, created_at, and status; supports `from`, `to`, `cursor`, and `limit`; does not run exact history counts on the public endpoint; discriminating cross-tenant/key test passes and mutation removing the auth-derived scope makes it fail. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus review. |
| Blast radius | User-facing read API and route table. Security risk is cross-tenant or cross-key leakage if scoping is wrong. Availability risk is only usage-list read path returning 503 on auth/store failure. |
| Failure modes | Client-supplied tenant/user filters overriding auth scope; missing `api_key_id` filter; cursor from another endpoint accepted; missing ledger/model fields; leaking prompt/body fields; route wired without inbound auth; new files accidentally added to frozen packages. Mitigation: no tenant/user query params are accepted, store params are asserted in tests, cursor kind is endpoint-specific, response DTO is explicit, new package is non-frozen. |
| Decision points | Owner park: this is `risk:security` because it exposes billing/usage facts to end users. No high-risk Owner confirmation needed before implementation because it is a read-only API, does not touch auth core, billing ledger mutation, quota enforcement, schema, secrets, or deployment. If Owner wants "all user keys" instead of "current API key", a follow-up must add an auth-derived `user_id` filter to the existing sqlc query. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`; inspect admin usage handler and sqlc query; inspect inbound auth identity and chat auth mapping; claim coordination locks; write failing `meusagehttp` tests; run RED; implement minimal handler and route; run GREEN; run mutation RED; restore; run build/tests/review; commit and push. |

## Package / File Structure Check

- Create `backend/internal/meusagehttp/handler.go`: new package, not frozen, responsibility is end-user usage read HTTP only.
- Create `backend/internal/meusagehttp/handler_test.go`: new package tests, not frozen.
- Modify `backend/cmd/gateway/routes.go`: existing non-frozen route wiring.
- Do not add files to frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Concrete Execution Order

1. Write tests in `backend/internal/meusagehttp/handler_test.go` for:
   - authenticated user A receives A records and zero records from different tenant/key B;
   - store receives auth-derived `tenant_id` and `api_key_id`, ignoring client-supplied tenant/api key params;
   - response includes `requested_model`, `upstream_model`, `actual_cost`, `provider`, `provider_account_id`, `ledger_id`, `verify_hint`, `created_at`, and `status`.
2. Run `go test ./internal/meusagehttp/...` and confirm RED because package/handler does not exist.
3. Implement `backend/internal/meusagehttp/handler.go`:
   - dependencies: inbound auth resolver and existing `ListUsageRecords` store;
   - parse `from`, `to`, `limit`, and endpoint-specific cursor;
   - populate `ListUsageRecordsParams` from `auth.Identity`, not query params;
   - map explicit response records with no request body or prompt fields.
4. Wire `GET /v1/me/usage` in `backend/cmd/gateway/routes.go` with `d.inboundAuth` and `d.billingQueries`.
5. Run `go test ./internal/meusagehttp/... ./cmd/gateway/...`.
6. Mutation check: temporarily remove or override the auth-derived tenant/API-key scope, run the discriminating test, confirm RED, then restore.
7. Run `go build ./...` and required tests.
8. Stage intended diff and run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh < /dev/null`; normalize any findings.
9. Commit with root cause and `Rules touched: ...`, push to `origin HEAD:work/f-audit-me-usage`.
