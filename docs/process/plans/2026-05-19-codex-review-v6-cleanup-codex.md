# 2026-05-19 codex review v6 cleanup codex
| Owner directive | "4 个都修, 不修好不允许 push" |
| Scope | In: HUAKAI Go backend generated pool SQL verification, `cmd/gateway` lifecycle error propagation, `internal/credentialstore` tenant consistency hardening, `internal/gatewayhttp` chat billing rate-table cost calculation and focused tests. Out: reference reverse-proxy source, Rust, frontend, proto, schema migrations, new dependencies, production secrets, push. |
| Success criteria | F1 generated SQL contains `credential_state` filter and commit exists; F3 bind/listen failures make `serveGateway` return non-nil error; F2 `ResolveActive` and credential writes cannot use mismatched `account_credentials.tenant_id` and `provider_accounts.tenant_id`; F4 removes all chat-path `decimal.NewFromFloat(0.01)` placeholders and uses public rate-table pricing or returns `pricing_unavailable`; requested build, targeted race tests, and codex reviews are run with actual results recorded. |
| Time estimate | 3-5h wall clock / one Codex executor lane. F4 is the longest slice because it touches reservation, settlement, stream, tests, and wiring. |
| Blast radius | Billing reservation/settlement values, successful chat completion responses, credential lookup/write safety, gateway process exit semantics. No schema/auth-core/quota-enforcement migration changes planned. |
| Failure modes | F1 prompt drift: current branch already has the pool sqlc regen commit (`939871c`), so duplicating it would create an empty or misleading commit; mitigation is to verify and treat that SHA as commit 1. F3 could return shutdown errors before listen errors if ordered incorrectly; mitigation is a one-shot server error channel checked after context cancellation. F2 tests may need a focused stub because package has no DB test harness; mitigation is a `pgx.Row`/`db.DBTX` stub that asserts SQL predicates and simulates inconsistent rows. F4 pricing JSON shape is only lightly specified; mitigation is to support the observed `pricing_data.models[model]` shape plus common key aliases, fail closed on miss, and add focused tests. F4 requested `ActualCost=nil/state=needs_reconcile` cannot be represented with current Go structs / NOT NULL DB columns without a migration; mitigation is normal-path true rate-table compute, reserve-time 503 on pricing miss, and schema-compatible `PendingReconciliation=true` with zero cost only when successful upstream usage is absent. If Owner requires nullable actual cost or a dedicated queue row, that is high-risk schema work and needs confirmation. |
| Decision points | No major disagreement with the prompt on F2/F3. F1 is already committed on this checkout, so I will not create a duplicate commit. F4 decision: implement actual-cost rate table compute for reported usage; use fail-closed `pricing_unavailable` before reserving/dispatching when source/table/model/rate is missing; keep L2 cache hit cost at zero by existing policy; for post-response missing usage, mark existing `PendingReconciliation` because nullable `ActualCost` / `needs_reconcile` state / queue insert needs schema or ledger contract changes that are out of bounds. |
| Pre-execution checklist | 1. Confirm worktree status and F1 generated SQL. 2. Patch lifecycle error propagation and add/adjust focused gateway test if available. 3. Build/test/review/commit F3. 4. Patch credentialstore `Create` and `ResolveActive`, add focused tenant-mismatch test. 5. Build/test/review/commit F2. 6. Add chat pricing helper, wire `ChatHandlerDeps.RateTables`, reserve predicted cost, non-streaming and streaming actual cost, tests and fixture rate tables. 7. Build/test/review/commit F4. 8. Run cross-commit race suite and base review. |
| Concrete execution order | Treat `939871c` as commit 1 after validation. Then create commit 2 `gateway lifecycle 捕 ListenAndServe 错误传给 main 防 bind 失败静默退 0`; commit 3 `credentialstore ResolveActive 加跨租户约束防 account_credentials.tenant_id 与 provider_accounts.tenant_id 不一致欧脱`; commit 4 `billing ActualCost 接 rate table compute 防 0.01 placeholder 与真实 usage 偏离`. |

## Independent Source Findings

- Current branch is `claude/phase-1` and is ahead of origin; worktree was clean at initial inspection.
- F1 context differs from the prompt: `939871c` already committed `backend/internal/db/billing/pool_accounts.sql.go` with the credential-state filter matching `backend/sql/queries/pool_accounts.sql`.
- `credentialstore.Store.Create` currently inserts `account_credentials` directly with caller-provided tenant/account IDs; `ResolveActive` joins provider accounts by account ID only. Both need tenant consistency hardening.
- `PostgresCredentialVault.Resolve` calls `ResolveActive(ctx, accountID)` without a tenant ID. Since runtime selection already used tenant-scoped pool selection, the minimal compatible defense is to make the store join enforce `ac.tenant_id = pa.tenant_id`; adding a tenant argument would ripple through the provider vault interface and is not necessary for this finding.
- `serveGateway` logs `ListenAndServe` errors and cancels context, but `shutdownGateway` can return nil, hiding bind failures from `main`.
- Chat billing has hard-coded `0.01` in reservation, non-streaming settlement, streaming settlement, and one cache-related handler path. L2 cache hit already uses zero and should remain zero unless Owner changes cache-hit pricing policy.

## Verification Commands

```bash
cd /home/codex/HUAKAI/backend
GOCACHE=/tmp/go-cache go build ./...
GOCACHE=/tmp/go-cache go test ./cmd/gateway/... -race -count=1 -timeout 180s
GOCACHE=/tmp/go-cache go test ./internal/credentialstore/... -race -count=1 -timeout 180s
GOCACHE=/tmp/go-cache go test ./internal/gatewayhttp/... -race -count=1 -timeout 180s
GOCACHE=/tmp/go-cache go test ./internal/billing/... ./internal/credentialstore/... ./internal/db/billing/... ./internal/gatewayhttp/... ./internal/pool/... ./cmd/gateway/... -race -count=1 -timeout 300s
codex exec review --uncommitted --full-auto -c model_reasoning_effort=xhigh --enable fast_mode < /dev/null
codex exec review --base main --full-auto -c model_reasoning_effort=xhigh --enable fast_mode < /dev/null
```

Source files read: backend/internal/db/billing/pool_accounts.sql.go; backend/sql/queries/pool_accounts.sql; backend/cmd/gateway/lifecycle.go; backend/internal/credentialstore/postgres_store.go; backend/internal/provider/postgres_vault.go; backend/internal/gatewayhttp/chat_completions_billing.go; backend/internal/gatewayhttp/chat_completions_dispatch.go; backend/internal/gatewayhttp/chat_completions_stream.go; backend/internal/gatewayhttp/chat_completions_handler_headers.go; backend/internal/gatewayhttp/chat_completions_handler.go; backend/internal/gatewayhttp/chat_completions_validate.go; backend/internal/billing/rate_table_source.go; backend/sql/migrations/0002_observability_billing.up.sql; backend/sql/migrations/0030_pricing_versions_public_scope.up.sql; backend/sql/migrations/0031_pricing_versions_public_scope_v2.up.sql; docs/specs/user-consumption-transparency.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-19T12:02:58Z
