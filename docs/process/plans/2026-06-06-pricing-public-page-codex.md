# 2026-06-06 pricing public page endpoint
| Owner directive | "Add UNAUTH public pricing-page endpoint to HUAKAI (branch fix/pricing-public)." |
| Scope | In: new unauth `GET /v1/pricing/page` handler in `backend/internal/pricingpublichttp`, route mount in `backend/cmd/gateway/routes.go`, OpenAPI path/schema, focused httptest coverage. Out: migrations, commits, money ledger/auth/quota/billing mutations, reading `/home/ubuntu/refs`, new files in frozen packages. |
| Success criteria | No-auth request returns a curated enabled-model pricing list without `?version`; response exposes only model identity, public unit prices, and context metadata; disabled catalog entries are omitted by the registry source; OpenAPI consistency remains green; required build/vet/tests are run or any blocker is reported. |
| Time estimate | 45-75 minutes wall clock, one Codex work unit. |
| Blast radius | Public read-only HTTP surface, route registration, OpenAPI contract, and new package tests. Existing auth-gated `/v1/models` and raw `/v1/pricing/rate-table` behavior should remain unchanged. |
| Failure modes | Accidentally adding auth would break unauth marketing access, mitigated by `TestPublicPricingNoAuth`. Leaking internal cost/identity fields would expose sensitive data, mitigated by projection-negative assertions. Requiring `?version` would duplicate raw rate-table behavior, mitigated by no-param test. Adding files to frozen packages would violate structure rules, mitigated by new package only and route import edit. |
| Decision points | None expected. Stop for Owner only if implementation would require schema migration, auth core, billing ledger, quota enforcement, new dependency, real secrets, or files under `/home/ubuntu/refs`. |
| Pre-execution checklist | 1. Confirm default public pricing scope from billing code and migrations. 2. Reuse registry `ListedModel` projection shape without auth. 3. Write discriminating tests before production handler. 4. Mount route top-level without middleware. 5. Add OpenAPI path and schemas. 6. Run requested verification as far as local environment permits. 7. Stage `backend/` and `docs/` only; do not commit. |

## Concrete Execution Order

1. Create `backend/internal/pricingpublichttp/handler_test.go` with the four requested httptest cases and mutation comments.
2. Run the new package test and confirm it fails because the package/handler does not exist yet.
3. Create `backend/internal/pricingpublichttp/handler.go` with small local interfaces for model catalog and public pricing table.
4. Run the new package tests and fix only the minimal implementation needed.
5. Modify `backend/cmd/gateway/routes.go` to import `pricingpublichttp` and mount `GET /v1/pricing/page` top-level with `Catalog: d.modelRegistry` and `Pricing: d.rateTableSource`.
6. Update `docs/openapi/openapi.yaml` with the unauth path and public response schemas.
7. Run targeted route/OpenAPI/package checks, then requested build/vet/unit commands.
8. Run `git add backend/ docs/` and report status without committing.
