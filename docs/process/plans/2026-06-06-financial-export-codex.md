# 2026-06-06 financial export CSV endpoints

| Owner directive | "Add financial/usage CSV EXPORT endpoints to HUAKAI (branch fix/financial-export). Verified absent (no csv/export endpoints). Reach CLOSURE: admin export routes + CSV streaming + tests + gate-ready." |
| Scope | Add read-only CSV export handlers in new package `backend/internal/exporthttp`; wire routes in `backend/cmd/gateway/routes.go`; add narrow payment export service/store support in existing `backend/internal/payment` files; reuse generated `internal/db/billing` usage list query; add focused unit and wiring tests. Out of scope: migrations, `/home/ubuntu/refs`, integration_pg/socket tests, commits, new runtime dependencies. |
| Success criteria | `GET /v1/admin/payments/export.csv?from=&to=&status=` and `GET /v1/admin/usage/export.csv?from=&to=` return `text/csv; charset=utf-8`, attachment disposition, header rows, tenant-scoped rows, date validation, row cap/truncation signal, and CSV formula-injection escaping for every string cell. Tests cover shape, tenant isolation, CSV-injection guard, date validation, usage shape, and forbidden non-admin access. |
| Time estimate | 60-90 minutes wall clock / one Codex session. |
| Blast radius | New HTTP package plus small existing payment service/store extension and route wiring. No frozen-package new files. Existing frozen package edits limited to `cmd/gateway/routes.go` import/wiring only; no new files in `gatewayhttp`, `gateway`, or `proto`. |
| Failure modes | Missing tenant filter could leak finance data; mitigate with identity-derived `ScopeTenantID` and discriminating tenant test. CSV injection could produce spreadsheet formula execution; mitigate with shared `SafeCSVCell` and mutation test. Existing payment list cap could truncate too early; mitigate with separate export list normalization and explicit 100k cap. Usage query could be global if tenant pointer nil; mitigate by always passing a non-nil tenant ID. Large exports could be unbounded; mitigate by 366-day date cap, 100k row cap, periodic flush, and `X-Truncated: true` plus trailing notice row. |
| Decision points | Platform admins in HUAKAI are normally global and have `ScopeTenantID=0`; export endpoints will require a tenant-scoped admin identity (`tenant_operator` or any future admin identity with positive `ScopeTenantID`) and reject unscoped platform admins with 400 to avoid accidental global finance export. Owner can later add an explicit `tenant_id` query variant if global platform export is desired. |
| Pre-execution checklist | Confirm linked worktree and branch; read payment admin list/store/query contracts; read billing usage list contract; create `internal/exporthttp` instead of adding files to frozen packages; write failing handler tests before production code; implement minimal export code; wire routes; run unit tests for `internal/exporthttp`, targeted payment tests if changed, `cmd/gateway`, then `go build ./...` and `go vet ./...` where sandbox permits. |

## Concrete execution order

1. Add failing `backend/internal/exporthttp` tests for payments CSV shape, tenant scope, injection guard, date validation, usage CSV shape, and forbidden non-admin role.
2. Run `go test ./internal/exporthttp` and confirm the package is missing/failing for the expected reason.
3. Add payment export support in existing `internal/payment` files:
   - `OrderExportFilter` and `ExportOrders` on `Service`.
   - Store capability `AdminExportOrders`.
   - Existing memory/postgres store methods that reuse existing filter normalization and `adminOrderWhere`.
4. Implement `backend/internal/exporthttp` with:
   - `Deps`, `Auth`, `PaymentExporter`, `UsageExporter`.
   - `MountRoutes`.
   - date parsing for `from`/`to` using RFC3339, inclusive lower bound, exclusive upper bound, and 366-day cap.
   - `SafeCSVCell`, decimal money formatting, CSV headers, row cap, periodic flush.
5. Wire `exporthttp.MountRoutes` into `mountAdminRoutes` in `backend/cmd/gateway/routes.go`.
6. Add or adjust `cmd/gateway` wiring tests proving the routes are mounted behind admin auth.
7. Run required verification commands possible in sandbox:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/exporthttp ./internal/payment ./cmd/gateway`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`

## Assumptions

- No reference source will be read; implementation is HUAKAI-native and based only on the PM-provided behavior summary.
- Generated sqlc files are reused as-is; no migration or sqlc regeneration is planned.
- Usage export will use `ListUsageRecords` because it already provides tenant/date filters and request ID/model/cost fields suitable for reconciliation.
- Payment export needs a new payment service/store method only because the existing admin list endpoint intentionally caps UI pages at 200 rows.
