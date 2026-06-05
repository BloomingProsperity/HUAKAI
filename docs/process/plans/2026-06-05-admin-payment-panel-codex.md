# 2026-06-05 admin-payment-panel-codex
| Owner directive | "admin 支付运营面板后端接口" |
| Scope | In: HUAKAI-only backend payment admin HTTP endpoints, payment service/store read methods, platform-settings-backed non-secret provider runtime config, OpenAPI declarations, focused tests. Out: `/home/ubuntu/refs`, new payment schema/table, payment state-machine redesign, auth core, billing ledger changes, frontend work. |
| Success criteria | Admin can filter/list orders by tenant/status/time/page, read single-order audit events, read/update manual/taobao provider runtime config, read tenant-scoped dashboard stats, and retry fulfillment idempotently. Required Go build/vet/tests and OpenAPI consistency pass. |
| Time estimate | 2-4 wall-clock hours; one Codex implementation session. |
| Blast radius | Payment admin API, payment store read paths, runtime provider registry, OpenAPI path contract. Money writes remain inside existing `Fulfill` and existing store transactions. |
| Failure modes | Weak filters leak wrong tenant/status rows; dashboard counts wrong order set; retry double-credits; provider PUT persists but does not affect runtime; OpenAPI drifts from chi routes. Mitigation: discriminating handler/service tests, money idempotency test, route consistency test, no new money write path. |
| Decision points | No high-risk schema change will be made. If platform settings cannot safely store the provider JSON key, runtime-only provider config becomes roadmap with explicit limitation. |
| Pre-execution checklist | 1. Do not read `/home/ubuntu/refs`. 2. Keep frozen packages `internal/gatewayhttp`, `internal/gateway`, `internal/proto` unchanged except existing files if unavoidable. 3. Keep new/edited files under 500 lines and functions under 80 lines. 4. Write failing tests before production code. 5. Run required gates before commit. 6. Commit before mutation checks; restore mutation changes with checkout. |

## Concrete Execution Order

1. Add focused red tests:
   - `paymenthttp` handler test: `status=pending` list only returns pending and passes tenant/status/time/limit/offset to service.
   - `paymenthttp` handler test: `GET /{id}/audit` returns only audit events from service.
   - `payment` service/store tests: dashboard totals and daily series are computed from tenant-scoped orders.
   - `payment` service test: retry on recharging/paid completes once; retry on completed returns idempotent and does not add a second credit.
   - `paymenthttp` handler test: provider config GET/PUT wires provider kind, enabled flag, checkout URL, actor, and runtime update.
2. Implement payment domain additions:
   - Add filter/stat/provider config DTOs to `internal/payment`.
   - Add store read methods for admin filtered order list and dashboard stats in memory and Postgres stores.
   - Add service methods `AdminListOrders`, `DashboardStats`, `RetryFulfillment`, `GetProviderRuntimeConfig`, and `SetProviderRuntimeConfig`.
3. Implement HTTP additions in new focused `paymenthttp` files:
   - Parse `status`, `tenant_id`, `created_from`, `created_to`, `limit`, `offset`.
   - Add `/dashboard`, `/{id}/audit`, `/{id}/retry`, and `/providers/{provider}/config`.
   - Keep existing `GET /{id}` behavior intact.
4. Persist provider config through existing `platform_settings`:
   - Add one non-secret JSON key to `internal/platformsettings`.
   - Add a small `cmd/gateway` adapter that reads/writes that key and prewarms `payment.Service` from DB settings when present.
5. Update `docs/openapi/openapi.yaml` for the new routes and query parameters.
6. Run gates from `backend`:
   - `/usr/local/go/bin/go fmt` via `gofmt -w` on touched Go files.
   - `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./internal/paymenthttp/... ./cmd/gateway/...`
   - `GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/payment ./internal/paymenthttp ./cmd/gateway -count=1`
7. Stage intended diff and run per-commit review if CLI supports the required command.
8. Commit with the required co-author trailer.
9. Mutation checks after commit:
   - Mutate status filter to ignore status; confirm list test fails; restore.
   - Mutate retry idempotency path to duplicate credit if feasible without destructive changes; confirm money test fails; restore.

## Clean-Room Notes

- Implementer lane: only `/home/ubuntu/decomp-payment-panel-portal.md` and HUAKAI repository files are behavior sources.
- No reference-source citations or reference identifiers will appear in commit text.
- Provider runtime config stores only non-secret `enabled` and `checkout_url`; credentials remain outside this slice.
