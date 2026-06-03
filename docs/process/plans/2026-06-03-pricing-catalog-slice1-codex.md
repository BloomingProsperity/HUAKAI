# 2026-06-03 pricing-catalog slice1 Codex plan

| Owner directive | "Implement the FIRST SLICE of gap \"pricing-catalog\". Read docs/process/gap-specs/pricing-catalog.md fully; implement ONLY its first slice." |
| Scope | In: first-slice pool-group pricing ratio CRUD, additive 0078 migration, new `internal/pricingcatalog` and `internal/pricingcataloghttp` packages, isolated route helper in `cmd/gateway/routes_pricing.go`, discriminating tests with mutation evidence. Out: upstream price fetching, user-facing effective catalog, billing ledger/settlement/quota changes, `routes.go` integration wiring. |
| Success criteria | `cd backend && sqlc generate && go build ./... && go vet ./internal/pricingcatalog/... ./internal/pricingcataloghttp/... && go test ./internal/pricingcatalog/... ./internal/pricingcataloghttp/...` pass; tests prove tenant isolation, exact decimal strings, admin authorization, validation, 404 mapping, and tenant query enforcement. |
| Time estimate | 2-4 hours wall clock in this session. |
| Blast radius | New table and isolated packages only. `cmd/gateway/routes_pricing.go` compiles but is not invoked until PM wires it. Existing pricing, billing ledger, settlement, quota, frozen packages, and `routes.go` remain untouched. |
| Failure modes | Migration number collision: use Owner-reserved `0078` and confirm current max before adding. SQL column mismatch: verify `tenants`, `pool_groups`, and existing pricing tables before writing queries. Weak tests: run mutation checks and record observed red output. Route integration risk: do not edit `routes.go`; helper only. Money precision regression: return numeric as text and assert no float/scientific notation. |
| Decision points | Owner/PM must wire `mountPricingCatalogRoutes` from `routes.go` later. Any auth-core, billing-ledger, quota-enforcement, or destructive schema change would require Owner confirmation and is out of scope. |
| Pre-execution checklist | Read full gap spec; confirm current migration max; confirm table/column names; inspect existing admin auth and route patterns; write RED tests first; implement minimal code; mutation-verify tests; run required commands. |

## Concrete Execution Order

1. Add `0078_pool_group_pricing_ratios.up.sql` and `.down.sql` with only additive create/drop for `pool_group_pricing_ratios`.
2. Add `internal/pricingcatalog` domain, validation, and Postgres store. Keep SQL SELECTs tenant-scoped and cast `ratio::numeric(20,8)::text` on reads/returns so Go receives exact decimal strings.
3. Add `internal/pricingcataloghttp` admin ratio handlers. Copy small admin HTTP helpers from `internal/adminhttp` with `// SYNC: copied from ... (frozen pkg)` comments because the dispatch requires a new HTTP package instead of adding adminhttp files.
4. Add `cmd/gateway/routes_pricing.go` with `mountPricingCatalogRoutes(r chi.Router, d *deps)` and no call from `routes.go`.
5. Add discriminating tests in the two new packages and verify RED before implementation.
6. Run mutation checks for each guarded defect, restore the code, then run the full required verification command set.

## False Premises / Dispatch Corrections

- The gap spec says to add admin handlers under `internal/adminhttp` and edit `cmd/gateway/routes.go`; the dispatch overrides this with `internal/pricingcataloghttp`, `cmd/gateway/routes_pricing.go`, and no `routes.go` edit.
- The spec says compute the next migration number; the dispatch reserves `0078`, so this slice uses `0078`.
- The first slice does not fetch upstream prices, so `internal/modelsync/http_fetcher.go` is read only for context and no fetcher code is added.
