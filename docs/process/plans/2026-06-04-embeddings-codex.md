# 2026-06-04 embeddings-codex

| Owner directive | "新增 POST /v1/embeddings -- OpenAI 兼容的向量 embeddings 中转,复用 HUAKAI 现有 路由/选号/计费 部件,直通(passthrough)转发到运营方上游账号的 /v1/embeddings,按 input token 计费。" |
| Scope | In: backend `POST /v1/embeddings`, new non-frozen `backend/internal/embeddingshttp` handler/tests, minimal existing-file wiring in `cmd/gateway`, OpenAPI route declaration, minimal adapter passthrough only if HUAKAI dispatcher lacks embeddings endpoint selection. Out: schema changes, new runtime dependencies, external reference source reading, commits. |
| Success criteria | Request validates OpenAI embeddings body, rejects empty input before reserve, routes/selects account via existing HUAKAI components, reserves estimated input tokens, dispatches passthrough body to upstream embeddings endpoint, reads non-stream response, settles exactly once from `usage.prompt_tokens` on 2xx, aborts exactly once on upstream/dispatch/read/parse failures, returns upstream embeddings JSON, and passes requested build/vet/test gate. |
| Time estimate | 1-2 hours wall clock; 1 Codex implementation session. |
| Blast radius | Money/hot path: incorrect reserve/settle/abort can leak quota, double charge, or leave hanging claims; adapter changes can regress chat dispatch if not narrowly keyed; route wiring can break gateway build/OpenAPI tests. |
| Failure modes | Missing settle after reserve -> hanging claim; mitigation: discriminating tests assert settled claim and no pending reserve. Missing abort on upstream failure -> hanging claim; mitigation: failure-path test asserts abort exactly once and no settle. Wrong endpoint -> upstream receives chat path; mitigation: stub dispatcher/adapter test captures embeddings path or dispatch inputs. Empty input reserved -> abuse/billing noise; mitigation: validation test asserts no reserve. Usage parse missing -> unsafe settlement; mitigation: fail closed to abort instead of guessing post-response tokens. |
| Decision points | Owner confirmation already present for money-path feature. Stop for Owner only if implementation requires database schema, billing ledger contract changes, auth core changes, quota-enforcement contract changes, new runtime dependency, or reading external reference source. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Coordinate edits through `.coordination/`. 3. Do not read `/home/ubuntu/refs` or external reference source. 4. Inspect HUAKAI chat relay, dispatcher, token estimator, claim/settler interfaces, gateway wiring, and OpenAPI test expectations. 5. Write failing tests first in non-frozen package. 6. Implement minimal handler/wiring. 7. Run requested gate: `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/embeddingshttp/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -18`. |
| Package/file structure | New files target `backend/internal/embeddingshttp`, which is not listed as frozen. Existing frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto` may be read and only edited in existing files if needed; no new files there. |
| Clean-room note | Owner explicitly forbids reading `/home/ubuntu/refs` or any external reference source for this task. This plan therefore does not perform the default triple-mirror research step and makes no reference-project capability/mechanism claims. Implementation will be based only on HUAKAI-owned code and public OpenAI-compatible request/response shape stated in the Owner directive. |

## Concrete Execution Order

1. Inspect HUAKAI chat handler/attempt orchestration and quote local file:line evidence in final.
2. Inspect dispatcher and existing protocol/adapter endpoint handling to find the smallest safe passthrough hook.
3. Inspect token estimator and quota/billing interfaces used by chat.
4. Inspect gateway route wiring and OpenAPI test/doc location.
5. Write `internal/embeddingshttp` tests first for success, upstream failure abort, empty input no reserve, and settlement/abort hanging-claim mutations.
6. Run targeted test to verify RED.
7. Implement minimal handler and any required existing-file adapter/wiring.
8. Run targeted tests to verify GREEN.
9. Run gofmt and requested gate.
10. Release coordination lock and report Chinese Owner summary with files, risks, clean-room self-proof, and blockers.
