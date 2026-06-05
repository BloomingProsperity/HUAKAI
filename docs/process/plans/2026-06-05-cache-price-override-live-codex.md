# 2026-06-05 cache price override live billing plan

| Owner directive | "缓存价格覆盖接进 LIVE 计费路径(MONEY)" |
| --- | --- |
| Scope | In: HUAKAI repo only; gatewayhttp live chat/messages/responses pricing path; existing cmd/gateway wiring; discriminating gatewayhttp test. Out: `/home/ubuntu/refs`, reference-source citations, schema changes, new runtime dependencies, new files under frozen packages, other endpoint families without cache-token pricing. |
| Success criteria | A tenant/model cache override multiplier changes only cache creation/read costs in live settlement; no override remains official pricing; mutation that removes `pricingeval.ApplyCacheCostOverride` makes the new test fail; requested build/vet/test gates pass; one local commit is created without push. |
| Time estimate | 45-75 minutes wall clock; one Codex worker session. |
| Blast radius | Money path for chat/messages/responses actual and predicted completion cost, because `completionCost` is shared. Default no-op behavior must remain unchanged when the store is nil or resolves to 1.0. |
| Failure modes | Wrong pricing point leaves override dormant: mitigate by testing through handler settlement, not direct helper only. Broad cost scaling changes non-cache cost: assert exact official vs overridden totals using non-cache and cache tokens. Missing wiring leaves production store nil: wire `d.cacheOverrideStore` in `chatHandlerDeps`. Frozen package structure violation: modify only existing gatewayhttp files. Clean-room risk: read only HUAKAI files and do not cite reference source. |
| Decision points | Block if more than one plausible gatewayhttp `pricingeval.Resolve` live settlement point exists and cannot be proven shared. Block before schema/auth/billing-ledger/payment logic changes beyond passing a read-only override resolver into pricing calculation. |
| Pre-execution checklist | 1. Confirm dirty tree and protect unrelated user files. 2. Grep HUAKAI gatewayhttp/cmd/billing/pricingeval for pricing and cache override APIs. 3. Add RED test in existing gatewayhttp pricing test file. 4. Implement minimal deps/wiring and result override. 5. Run gofmt/build/vet/tests. 6. Stage, run Codex review if CLI available per project rule. 7. Commit. 8. After commit, mutate out override application, verify test fails, restore with git checkout from HEAD. |

## Concrete execution order

- Modify `backend/internal/gatewayhttp/chat_completions_pricing_test.go` only after identifying the existing handler-driven pricing test fixture. Add a test that sets tenant/model override multiplier `2.0`, sends a live non-stream chat request with cache creation and cache read usage in the mocked canonical dispatcher, and asserts exact doubled cache costs while a sibling no-override case remains official price.
- Run the focused test and require RED caused by unchanged live path.
- Modify existing `backend/internal/gatewayhttp/chat_completions_handler.go` to add a narrow cache override resolver field to `ChatHandlerDeps`.
- Modify existing `backend/cmd/gateway/routes.go` to pass `d.cacheOverrideStore` into `chatHandlerDeps`.
- Modify existing `backend/internal/gatewayhttp/chat_completions_pricing.go` to resolve the multiplier by `tenantID` and requested/upstream model candidate, then apply `pricingeval.ApplyCacheCostOverride` only when non-identity.
- Run focused tests, then requested gates from `backend` with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`.
- Commit only intended files with required co-author trailer.
- After commit, perform the requested mutation by editing out the override application, run the new focused test and require failure, then restore the committed version with `git checkout -- <file>` and verify clean status except pre-existing unrelated files.

## Clean-room note

This is a clean-room implementer task. The plan and implementation use only HUAKAI-local code and the Owner-provided task statement. `/home/ubuntu/refs` is out of scope.
