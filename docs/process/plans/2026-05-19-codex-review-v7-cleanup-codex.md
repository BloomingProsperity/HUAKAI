# 2026-05-19 codex-review-v7-cleanup-codex

| Owner directive | "5 个都修 后 push"；本 lane 边界同时要求“不 push, Claude 主线 review 后统一 push 36+ commits”。 |
| Scope | In: Go backend pool SQL filters, sqlc regeneration, focused tests, provider registry default gating, gatewayhttp L2-cache ordering, gatewayhttp WaitPlan response handling. Out: reference reverse-proxy source, Rust, frontend, proto, schema migrations, new runtime dependencies, push. |
| Success criteria | F1/F2/F3/F4/F5 each has a focused code fix and regression test; sqlc generated files match SQL; modified packages build and race tests pass or any failure is reported honestly; commits follow Owner v2 naming. |
| Time estimate | 2-4 wall-clock hours depending on integration test database availability and sqlc/test runtime. |
| Blast radius | Pool route eligibility, provider adapter registration, non-streaming chat request ordering, billing claim abort reason on wait-plan saturation. A bad change could hide valid pool capacity, fail model dispatch earlier than intended, or change billing/pool concurrency behavior. |
| Failure modes | SQL filter could join wrong tenant or silently drop valid bindings; mitigate by joining tenant-scoped pool_groups/channels and testing disabled + soft-deleted parents. Env gating could make verified adapters unreachable; mitigate by limiting gate to the six session placeholder families named by the finding. L2 cache reorder could miss required cache key fields; mitigate by deriving upstream model/vendor immediately after route plan and before reserve/select. WaitPlan handling could invent queue semantics not present in code; mitigate by minimal 429 + Retry-After + `queue_wait` abort, without enqueueing. |
| Decision points | No high-risk schema/auth/billing-ledger migration changes planned. If tests require unavailable external PostgreSQL, record the blocker instead of faking green output. If sqlc is unavailable, stop and report because generated files must stay in sync. |
| Pre-execution checklist | Read AGENTS/RULES scope; read local HUAKAI files only; confirm all five findings against current code; write this plan before mutation; apply small patches; run sqlc; run gofmt; run per-module build/tests; stage/review/commit per requested commit boundaries. |

## Independent Finding Check

- F1 is valid. `backend/sql/queries/registry.sql` `ListModelPoolBindings` filters `model_pool_bindings.enabled/deleted_at` and effective window, but does not require the referenced `pool_groups` row to remain enabled and not soft-deleted. Current route planning can therefore keep returning bindings for disabled or drained pool groups.
- F2 is valid. `backend/sql/queries/pool_accounts.sql` joins `channels` only by `id`, then filters tenant and pool group in `WHERE`, but does not filter `channels.enabled` or `channels.deleted_at`. Disabled or soft-deleted channels can still supply provider accounts.
- F3 is valid. `backend/internal/provider/registrydefault/default.go` currently registers the six session reversal families unconditionally while the file comment says several endpoints remain placeholder/TODO. Default production registration can route live credentials into unverified adapters.
- F4 is valid. `NewChatCompletionsHandler` calls `prepareRouteAndAccount`, which reserves billing and selects/acquires a pool account, before `serveL2CacheIfAvailable`. A cacheable non-streaming replay can be blocked by pool capacity before checking cache.
- F5 is valid. `selectPoolAccount` treats `selRes == nil || selRes.AccountID == 0` as generic `no_capacity`, even though selector can legally return `SelectionResult{WaitPlan: ...}` with no account.

## F3 Gating Choice

Use an explicit env flag: `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=true`.

Reason: it is the smallest production-safe opt-in that avoids config/schema changes and does not add dependencies. A build tag would make runtime operations harder and a new config field would require broader wiring not requested in this lane. Tests can set/unset the env with `t.Setenv`.

## F5 Minimal Semantics

Implement gatewayhttp wait-plan handling as HTTP `429 Too Many Requests` with `Retry-After`. `WaitPlan` currently contains `TimeoutMS` and `MaxWaiting`, but no enqueue primitive appears in gatewayhttp. Therefore this lane should not pretend to enqueue. The claim should be aborted with reason `queue_wait`, distinct from `pool_no_capacity`, so billing/audit can distinguish admitted wait-policy saturation from hard no-capacity.

Retry-After derivation: use `ceil(TimeoutMS / 1000)` when positive; otherwise retain a conservative 1-second retry hint for a present wait plan.

## Verification Commands

Per commit, from `backend/`:

```bash
GOCACHE=/tmp/go-cache go build ./...
GOCACHE=/tmp/go-cache go test ./internal/<modified-package>/... ./internal/db/billing/... -race -count=1 -timeout 180s
```

Final cross-check:

```bash
GOCACHE=/tmp/go-cache go test ./internal/billing/... ./internal/credentialstore/... ./internal/db/billing/... ./internal/gatewayhttp/... ./internal/pool/... ./internal/provider/registrydefault/... ./cmd/gateway/... -race -count=1 -timeout 300s
```

## Source Files Read

- `AGENTS.md` instructions from prompt
- `docs/RULES.md`
- `backend/sql/queries/registry.sql`
- `backend/sql/queries/pool_accounts.sql`
- `backend/internal/db/registry/registry.sql.go`
- `backend/internal/db/billing/pool_accounts.sql.go`
- `backend/internal/db/billing/querier.go`
- `backend/internal/provider/registrydefault/default.go`
- `backend/internal/provider/registrydefault/default_test.go`
- `backend/internal/provider/registry.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_error.go`
- `backend/internal/pool/router/types.go`
- `backend/internal/pool/router/default_selector.go`
- `backend/internal/pool/dispatcher/account_source.go`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0008_model_registry.up.sql`
- `backend/Makefile`

Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-19T12:57:31Z
