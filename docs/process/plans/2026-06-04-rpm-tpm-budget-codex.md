# 2026-06-04 RPM/TPM Budget Codex Plan

| Owner directive | "实现+验证...给 HUAKAI 加 RPM/TPM 预算追踪...Redis 快路...front 现有 quota 不替换...不要 git commit...严禁读 /home/ubuntu/refs 或任何外部参考" |
| Scope | In: HUAKAI-only source read, new `backend/internal/budget` package, new `backend/internal/budgetenforce` package, existing `backend/internal/config`, existing `backend/cmd/gateway/wiring.go`, existing config example/tests. Out: `/home/ubuntu/refs`, external reference source, commits, DB schema, auth/billing ledger/quota durable schema changes, and any edits to frozen `backend/internal/{gatewayhttp,gateway,proto}`. |
| Success criteria | Unit tests cover RPM deny, TPM accumulation, reserve/settle/release reconciliation, fail modes, claim idempotency, user hard cap, all-or-nothing refund, and key encoding. Redis integration test proves Lua server-time/minute behavior and concurrent limit exactness. Required gate: `cd backend && go build ./... && go vet ./... && go test ./internal/budget/... ./internal/budgetenforce/... ./cmd/gateway/...`; Redis gate: `go test -tags=integration_redis ./internal/budget/...`. |
| Time estimate | 3-5 hours wall clock for implementation plus verification, depending on existing test fallout. |
| Blast radius | Gateway hot path reserve/settle path, Redis availability behavior, quota reserve composition, config parsing, and platform settings allow-list. Budget is opt-in and must not replace durable quota cost enforcement. |
| Failure modes | Redis unavailable could block traffic: mitigate with default `memory_fallback` and breaker. Lua bug could over-admit under concurrency: mitigate with integration Redis concurrent test. Claim retries could double count: store claim reservations and skip repeated claim increments. Scope encoding could allow key injection: encode IDs to restricted base64url/int strings and test `:{}`/newline. Later-scope deny could leak earlier-scope increments: refund prior scopes and test. |
| Decision points | The prompt forbids editing `gatewayhttp`, but current `quotaenforce.BuildReserveRequest` receives no raw request body/max token field, so exact PM formula `estimateInputTokens(body)+min(max_tokens,4096)` cannot be computed on the gateway path without Owner/PM allowing an existing-file `gatewayhttp` change or a new token-estimate field in the reserve call. I will implement the budget API with explicit estimated-token input and wire best-effort through the existing reserve interface; exact hot-path TPM pre-reserve remains a PM review blocker if frozen-file edits stay forbidden. |
| Pre-execution checklist | Read `CLAUDE.md`; read `AGENTS.md`; check coordination board; inspect existing quota/quotaenforce/wiring/gatewayhttp/retrybudget/circuitbreaker/platformsettings/config code; write this plan; claim implementation files before editing; write tests before production code; run gates; release coordination lock. |

## Clean-Room And Structure Notes

- Clean-room: this task uses only HUAKAI source and the PM-provided spec. I will not read `/home/ubuntu/refs`, clone external repositories, or make reference-project behavior claims.
- Frozen packages: no new files and no edits in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- New package files are outside frozen packages:
  - `backend/internal/budget/*.go` and tests: budget domain engine, Redis store, memory fallback, limits/settings, claim reconciliation.
  - `backend/internal/budgetenforce/*.go` and tests: thin adapters around quota reserver and billing settler.
- Existing files to modify:
  - `backend/internal/config/config.go` / tests: opt-in budget config and Redis URL/fail mode/default limits.
  - `backend/internal/platformsettings/types.go` / tests if needed: allow non-secret `budget_limits` JSON setting.
  - `backend/cmd/gateway/wiring.go` / tests: compose budget outside quota and preserve existing quota behavior.
  - `backend/config.example.yaml`: document non-secret budget config.

## Concrete Execution Order

1. Write failing `internal/budget` tests for fixed-window checks, all-or-nothing refund, claim idempotency, reconciliation, fail modes, and scope encoding.
2. Write failing `internal/budgetenforce` tests for budget-before-quota, deny short-circuit, settle delta, and abort release.
3. Add Redis integration tests behind `integration_redis` for server-time minute behavior and 100 concurrent limit50 exact admission.
4. Implement `internal/budget` in small files: types/options, scope encoding, limits resolver, memory store, Redis Lua store, service orchestration, errors/metrics.
5. Implement `internal/budgetenforce` adapters.
6. Add config/platform setting parsing and gateway wiring.
7. Run the required gates and record exact results.
