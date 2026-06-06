# 2026-06-06 payment provider PSP reconcile contract
| Owner directive | "fix147b 扩展 payment.Provider 接口加 PSP 对账方法(report 147, MONEY 接口, 极谨慎)" |
| Scope | In: `backend/internal/payment/provider.go`, focused payment package tests, optional `backend/internal/payment/PROVIDERS.md` wording. Out: service/handler wiring, refund/cancel flow behavior, frozen packages `backend/internal/{gatewayhttp,gateway,proto}`, `/home/ubuntu/refs`, commits. |
| Success criteria | `Provider` exposes `QueryOrder`, `Refund`, and `Cancel`; minimal result types and `ErrProviderOperationNotSupported` exist; every current `Provider` implementer returns zero value plus not-supported for the new methods; no existing payment flow calls the new methods; requested build/vet/test commands pass or failures are reported honestly. |
| Time estimate | 20-35 minutes wall clock, one Codex session. |
| Blast radius | MONEY contract compilation surface in `internal/payment`; downstream code that assigns concrete providers to `Provider` must compile with the new methods. Runtime blast radius should be zero because service/HTTP flows remain untouched. |
| Failure modes | Missing an implementer causes compile failure; mitigate with `rg` plus `go build ./...`. Weak tests could pass without checking typed errors; mitigate with direct `errors.Is` assertions on all built-in providers. Type overdesign could imply PSP semantics not specified; mitigate by minimal string fields only. Accidental behavior change in create/refund/cancel flows; mitigate by diff review and confirming no new method call sites outside tests. |
| Decision points | BLOCKED if a richer status enum or PSP refund schema is required; BLOCKED before any payment service/handler wiring; no Owner confirmation needed for the minimal contract because the task directly authorizes it. |
| Pre-execution checklist | 1. Read `provider.go` and payment types. 2. Identify all `Provider` implementers and distinguish `paymenthttp.PaymentProvider`. 3. Add failing focused tests for unsupported PSP operations. 4. Implement only the minimal contract and unsupported defaults. 5. Run gofmt and requested build/vet/test commands with `/usr/local/go/bin/go GOCACHE=/tmp/go-build`. 6. Review diff for clean-room and behavior scope. |
| Target packages | Modify existing non-frozen package `backend/internal/payment`; do not add files to frozen packages. |

## Concrete execution order

1. Add payment-package tests that call `QueryOrder`, `Refund`, and `Cancel` on `manual`, `taobao`, `hmac`, and `test` providers and assert `errors.Is(err, ErrProviderOperationNotSupported)` plus zero-value results where applicable.
2. Run the focused new test to observe the expected compile failure before production changes.
3. Add `ErrProviderOperationNotSupported`, `ProviderOrderState`, `ProviderRefundResult`, and the three methods to `Provider`.
4. Implement the three methods on `manualProvider`, `taobaoProvider`, `hmacProvider`, and `testProvider`, all returning not-supported.
5. Update `PROVIDERS.md` only if wording is stale after the interface change.
6. Run `gofmt -w` on touched Go files.
7. Run:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./internal/payment/... ./internal/paymenthttp/...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/payment/... ./internal/paymenthttp/... -count=1`
8. Inspect `git diff` and `rg` for unintended calls to the new methods.
