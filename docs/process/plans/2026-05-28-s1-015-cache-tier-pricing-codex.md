# 2026-05-28 S1-015 cache-tier pricing Codex plan

| Owner directive | "IMPLEMENT audit fix S1-015 (cache-tier pricing: 5m-write / 1h-write / cache-read billed separately) in this worktree. Money/audit path -- be precise." |
| Scope | In scope: HUAKAI-internal Go backend changes under `backend/` for pricing breakdown, usage draft fields, gateway settle wiring, settler persistence, and discriminating unit/integration tests. Out of scope: DB migrations, sqlc regeneration, dependency changes, commits, pushes, reference-project source reads, production DB access. |
| Success criteria | `price()` returns total plus separate cache creation/read costs; 5m and 1h cache-write tokens use distinct rates with aggregate fallback preserved; gateway drafts carry split tokens and bucket costs; settler persists split token columns and combined cache creation/read cost columns; required Go checks pass; the new integration test fails when settler cache bucket persistence is temporarily reverted and passes after restore. |
| Time estimate | 1.5-3 wall-clock hours depending on full `go test ./...` and integration database availability. Agent time: one implementation pass plus verification and mutation proof. |
| Blast radius | Money/audit path: completion reserve/settle cost calculation, streaming/non-streaming usage drafts, L2 cache hit usage rows, and `usage_records` audit columns. Frozen packages `backend/internal/gatewayhttp` and `backend/internal/gateway` will be edited only in existing files; no new files there. |
| Failure modes | Incorrect multiplier/rounding order could break billing totals; missing fallback could break existing aggregate-only cache creation pricing; streaming drafts could persist costs without split tokens; cache hits could accidentally charge nonzero cost; integration test could be weak if it only checks nonzero values; full test suite may expose unrelated existing failures. Mitigation: keep existing total order `totalMicros * multiplier / 1e6`, use distinct rates in tests, assert exact persisted values, leave cache-hit actual cost at zero, and record any unrelated test failures verbatim. |
| Decision points | No Owner decision expected unless a required change crosses high-risk scope: schema migration, new runtime dependency, auth/billing ledger redesign, destructive DB operation, production DB access, or adding files inside frozen packages. |
| Pre-execution checklist | 1. Read `AGENTS.md` and `CLAUDE.md` rules #8/#13/#14. 2. Confirm branch/worktree. 3. Confirm no reference-project source reads. 4. Confirm target frozen packages are edit-existing-file only. 5. Add tests before production code. 6. Verify unit/integration tests are discriminating with RED before/after mutation. |

## Concrete execution order

1. Add unit tests in existing `backend/internal/gatewayhttp/chat_completions_pricing_test.go`:
   - exact bucket-cost test with rates input=1000, output=2000, 5m=1250, 1h=2000, read=100 and usage 5m=100, 1h=50, read=200;
   - split-sensitive test where 100/0 and 0/100 cache-creation split produce different costs.
2. Add integration test in existing `backend/internal/billing/settler_integration_test.go`:
   - set `SettleRequest.Draft.CacheCreation5mTokens=100`, `CacheCreation1hTokens=50`, `CacheReadTokens=200`, `CacheCreationCost=0.225`, `CacheReadCost=0.02`;
   - select the five `usage_records` columns by `claim_id` and assert exact equality.
3. Run the focused new tests before implementation and confirm they fail for the expected missing fields/signature/persistence behavior.
4. Implement pricing in existing `backend/internal/gatewayhttp/chat_completions_pricing.go`:
   - add split usage/rate fields;
   - parse 5m/1h rate keys;
   - return `completionCostBreakdown{Total, CacheCreationCost, CacheReadCost}`;
   - preserve aggregate-only fallback and existing missing/negative-rate errors.
5. Implement draft carrier in existing `backend/internal/gateway/forwarder_types.go`.
6. Wire non-streaming and L2 cache-hit drafts in existing `backend/internal/gatewayhttp/chat_completions_billing.go` and `backend/internal/gatewayhttp/chat_completions_handler_headers.go`.
7. Wire streaming completion cost breakdown in existing `backend/internal/gatewayhttp/chat_completions_stream.go`.
8. Persist settler cache-tier fields in existing `backend/internal/billing/settler.go`; leave abort zero-cost path unchanged.
9. Run focused unit and integration tests, then required verification:
   - `export PATH=/usr/local/go/bin:$PATH`
   - `cd backend`
   - `go build ./...`
   - `go vet ./internal/billing/... ./internal/gateway/... ./internal/gatewayhttp/...`
   - `go test ./...`
   - `export HUAKAI_DATABASE_URL=postgres://huakai:huakai@127.0.0.1:5432/huakai_dev?sslmode=disable`
   - `go test -tags integration_pg -count=1 ./internal/billing/...`
10. Mutation proof:
    - temporarily revert settler `Settle` cache split/cost lines to zeros;
    - run only the new integration test and capture RED;
    - restore implementation;
    - rerun only the new integration test and capture GREEN.
11. Final report includes changed files, diffs, required command outputs, mutation RED/GREEN output, deviations, and the Owner Chinese summary.
