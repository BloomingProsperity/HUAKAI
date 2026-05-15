# 2026-05-15 F-CACHE-001 L2 response cache Codex 独立计划

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none for this Codex artifact. Claude sibling plan exists but was not opened or used.

REFERENCE PROJECTS IN SCOPE: LiteLLM / Helicone / Portkey by Owner-provided reputation cues only; no reference source read.

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| 字段 | 内容 |
| --- | --- |
| Owner directive | "Write CODEX plan for HUAKAI F-CACHE-001 (simple L2 response cache) - Go backend (Owner 2026-05-15 临时解冻 Go for this feature)." |
| Authorization | Owner 2026-05-15 全 session 授权；本任务只写计划，不实现、不提交。 |
| Independent plan rule | 遵守 CLAUDE.md #10；未打开 `docs/plans/2026-05-15-f-cache-001-l2-cache-claude.md`。 |
| Lane | SPECIFIER；只读 HUAKAI 内部源码和文档；不读 sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway 源码。 |
| Feature | F-CACHE-001: simple L2 response cache, exact-key only, semantic cache 留到 L3+。 |

## scope

本计划范围锁定 Go backend 的 simple L2 response cache，不做 semantic cache、不做跨请求相似度、不做持久化对象存储、不做参考项目源码机制复刻。

纳入范围：

- 对 non-streaming `/v1/chat/completions`, `/v1/responses`, `/v1/messages` 的 successful buffered response 做 exact response cache。
- Key 逻辑按 Owner 指令的 `(vendor, model, canonical(request_body))`，但物理 key 必须额外前缀 `tenant_id`，否则会违反 HUAKAI 多租户隔离。这个前缀不改变 Owner 的产品维度，只是安全边界。
- TTL 可配置，默认启用时 60s；支持 per-vendor override。
- Cache miss 走现有 upstream path。
- Cache hit 不调 upstream，返回 cached body，并记录 hit metric。
- Go backend 提供 operator stats endpoint，供 Admin Ops UI 展示 per `(vendor, model)` hit rate；实际前端组件可由 Gemini 后续接 UI。
- 提供 operator purge endpoint，至少支持全量 purge 和 `(vendor, model)` purge；这是 F-CACHE-001 在矩阵中的 TTL + operator invalidation 义务的最小实现。

不纳入范围：

- Redis / Badger / S3 backend 落地。
- Streaming response replay。
- Semantic cache。
- 跨 tenant 共享命中。
- 新数据库 schema 或 migration。
- 改 `LICENSE`、auth core、billing ledger schema、quota enforcement。

现有 Go 后端观察：

- HTTP handler 已读取完整 request body，并在 non-streaming 和 streaming 分支前解析 `stream` 与 `model` (`backend/internal/gatewayhttp/chat_completions_handler.go:116-159`)。
- 当前 non-streaming 路径在 pool 选择后调用 upstream，再转换为 HCSF buffered response、写 ledger、Settle、返回 client body (`backend/internal/gatewayhttp/chat_completions_handler.go:330-475`)。
- 当前 streaming 路径在 dispatch 后直接设置 SSE headers 并交给 `StreamForwarder` (`backend/internal/gatewayhttp/chat_completions_handler.go:478-555`)。
- 现有 `cachemetrics` 只统计 vendor prompt cache tokens，不是实际 L2 response cache (`backend/internal/cachemetrics/cachemetrics.go:1-23`, `backend/internal/cachemetrics/cachemetrics.go:161-241`)。
- 现有 config 是 env-first；PASR selector 已有独立 typed config loader 模式，可复用同风格 (`backend/internal/config/config.go:1-47`, `backend/internal/config/pool_selector.go:81-131`)。

## backend choice

我建议 F-CACHE-001 第一版选择 **in-memory LRU + TTL**。

对比：

| Backend | 优点 | 风险 | Codex verdict |
| --- | --- | --- | --- |
| In-memory LRU | 零新 runtime dependency；无外部服务；无 disk retention；可用 stdlib `container/list` + `map` 实现；适合临时解冻 Go 的小步实现。 | 多副本不共享；重启丢缓存；命中率低于 Redis；单进程内存要限额。 | 推荐 L2 第一版。 |
| Redis | 多副本共享；更像生产 L2；可集中 purge。 | Go 端当前没有 Redis client；新增 runtime dependency 属高风险，需要 Owner 确认；Redis 配置虽在 example 中出现，但未在 Go hot path 落地。 | Phase 2 backend，先保留接口。 |
| Badger | 本地持久化，重启可保留。 | 新依赖 + disk privacy/retention 风险；cache response 可能含用户敏感输出；Windows/容器文件权限也会增加运维复杂度。 | 不建议用于第一版。 |

Phase 1 设计要留下 `Store` interface，让 Redis 可以作为 Phase 2 插入，不把 handler 绑死到内存实现。

## file-by-file impact

基于 `rg` 对 `backend/` 的实际结构扫描，建议影响面如下。

| 文件 | 动作 | 原因 |
| --- | --- | --- |
| `backend/internal/responsecache/cache.go` | 新增 | 定义 `Store`, `Entry`, `LookupResult`, `MemoryLRU`。TTL 到期 lazy delete；capacity eviction；`PurgeAll` / `PurgeVendorModel`。 |
| `backend/internal/responsecache/key.go` | 新增 | 计算 `tenant_id + vendor + model + sha256(canonical_json_body)`；只存 hash，不存 request body。 |
| `backend/internal/responsecache/canonical_json.go` | 新增 | 用 `json.Decoder.UseNumber` 解析，递归按 object key 排序，array 顺序保留；非法 JSON 返回 typed error，handler 走 bypass 或 400 的既有路径。 |
| `backend/internal/responsecache/metrics.go` | 新增 | expvar 统计 per `(vendor, model)` 的 `hit`, `miss`, `store`, `skip`, `evict`, `purge`；提供 `Snapshot()` 给 admin handler 测试。 |
| `backend/internal/responsecache/*_test.go` | 新增 | LRU eviction、TTL expiry、canonical hash 稳定性、tenant isolation、vendor/model stats。 |
| `backend/internal/config/response_cache.go` | 新增 | 仿 `LoadPoolSelector()` 风格解析 `HUAKAI_RESPONSE_CACHE_MODE`, `TTL`, `MAX_ENTRIES`, `MAX_BODY_BYTES`, `TTL_BY_VENDOR`；启动期 fail-fast。 |
| `backend/internal/config/response_cache_test.go` | 新增 | 默认值、非法 TTL、非法 capacity、per-vendor override parser。 |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | 小改 | `ChatHandlerDeps` 增加 `ResponseCache responsecache.Store` 和 config；non-streaming 分支在 pool acquire 后、credential/upstream 前查 cache；miss 后在 Settle 成功后写 cache；streaming 分支记录 skip。 |
| `backend/internal/gatewayhttp/chat_completions_handler_headers.go` | 小改 | 增加 `X-HUAKAI-Response-Cache: hit/miss/skip`，避免复用旧 ledger headers。 |
| `backend/internal/gatewayhttp/response_cache_admin_handler.go` | 新增 | Admin auth 保护的 stats + purge handlers，返回 per `(vendor, model)` hit rate。 |
| `backend/internal/gatewayhttp/response_cache_admin_handler_test.go` | 新增 | admin unauthorized、tenant operator scope、stats shape、purge action。 |
| `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go` | 新增 | miss calls upstream once; second exact request hit does not call upstream; hit still writes fresh ledger/settle; stream requests skip。 |
| `backend/cmd/gateway/main.go` | 小改 | boot 时 load response cache config，构造 in-memory store，注入 `ChatHandlerDeps`，mount `/admin/v1/cache/response`。 |
| `backend/config.example.yaml` | 小改 docs-only | 补 response cache 示例配置；真实加载仍 env-first，避免 YAML loader 假承诺。 |
| `backend/Makefile` 或 existing test scripts | 不改优先 | 只用 existing `go test`，不引入新工具。 |

避免改动：

- 不改 `backend/go.mod`，除非 Owner OCAW 明确选择 Redis/Badger。
- 不改 migrations / SQL schema。
- 不改 billing ledger status / usage_source CHECK。
- 不改 auth core。

## request path placement

第一版推荐插入点：**after `Reserve` + pool `Select`, before `CredentialVault.Resolve` + upstream `Dispatch`**。

理由：

- Cache hit 仍走现有 Tx1/Tx2 billing lifecycle，不绕过 usage/billing/audit。
- `Settle` 当前要求 `provider_account_id` 和 `acquisition_token`，并会 release slot (`backend/internal/billing/settler.go:61-67`, `backend/internal/billing/settler.go:150-160`)；如果要在 pool select 前命中 cache，就需要改 billing path 或 schema，风险更高。
- Pool acquire 只在 cache hit 时短暂占用 slot，不会调 credential vault / upstream，成本收益仍成立。
- 如果 Owner 后续要求 "cache hit 即使 pool empty 也可返回"，那是 OCAW 决策，需要新增 cache-hit-specific settlement path。

Hit path 建议：

1. Auth / body parse / registry / router / Reserve 保持现状。
2. Pool `Select` 保持现状，拿 `provider_account_id` 和 acquisition token。
3. 计算 cache key。
4. `ResponseCache.Get(key)` hit：
   - clone cached buffered response into fresh HCSF envelope。
   - 用当前 request metadata 重建 hop chain，标记 response cache hit warning/metadata。
   - `submitAuditLedgerEntry` 生成 fresh ledger，不复用旧 ledger id。
   - `Settle` 写 usage record，`ActualCost=0` 或 Owner-approved cache price；第一版推荐 0 upstream cost，customer charge policy 见 OCAW-003。
   - 返回 client body，header `X-HUAKAI-Response-Cache: hit`。
5. miss：走现有 upstream；Settle 成功后 `Set(key, entry, ttl)`；header `miss`。

## cache backend API

建议接口形态：

- `Get(ctx, key) (Entry, bool)`：过期即返回 false 并计 expiry eviction。
- `Set(ctx, key, entry, ttl) error`：只在 upstream success + Settle success 后调用。
- `Purge(ctx, filter)`：admin purge。
- `Snapshot()`：admin stats，不暴露 raw request/response。

`Entry` 内容建议：

- `TenantID`
- `Vendor`
- `RequestedModel`
- `UpstreamModel`
- `BodyHash`
- `StatusCode`
- `CanonicalResponse` 或最小 buffered response payload
- `ClientBody` 可选；若存 client body，仍要保存 usage/model chain 以便 ledger/settle。
- `Usage`
- `StoredAt`
- `ExpiresAt`
- `SizeBytes`

不存：

- raw request body
- Authorization / API key
- upstream credential
- full request headers
- old ledger id / old signature

## key/hash strategy

Logical key per Owner: `(vendor, model, canonical(request_body))`。

Physical key per HUAKAI safety:

```text
response-cache:v1:tenant=<tenant_id>:vendor=<vendor>:model=<upstream_model_id>:body=<sha256(canonical_json)>
```

Key decisions:

- `tenant_id` 必须入 key，防止跨 tenant 输出泄露。
- `vendor` 使用 HUAKAI 本地 routing 派生值，例如 handler 当前从 `ProtocolFamily` 派生 vendor 给 selector metrics (`backend/internal/gatewayhttp/chat_completions_handler.go:259-273`)；如可从 selected provider account 取 platform，优先使用 account platform。
- `model` 使用 `upstreamModelID`，另在 metrics 同时记录 `requested_model`，避免 alias 造成 operator 看不懂。
- `canonical(request_body)` 是 JSON structural canonicalization，不做 semantic normalization。
- 顶层字段顺序和 whitespace 不影响 hash。
- array 顺序、string/number 类型、explicit `stream:false` vs omitted 均保持不同，避免过度合并。
- 对 body > `max_body_bytes` 的请求直接 skip cache。
- 对包含明显非确定性字段的未来参数（如 seed absent + high temperature）是否缓存，第一版只按 exact body；不尝试推断模型随机性。

## stream cache semantics

第一版建议：**streaming request skip cache**。

原因：

- final-only cache 会把 stream 变成 buffered response，破坏客户端对 SSE 的交互预期。
- full SSE replay 必须保存 event 序列、timing/header、terminal marker、ledger timing 和 client disconnect 语义，blast radius 高。
- 当前 stream path 的 `StreamForwarder` 同时负责 scanner、canonical usage、drain、ledger callback (`backend/internal/gateway/forwarder.go:85-218`)；把 replay 塞进去会混淆 "actual upstream stream" 和 "local replay stream"。

第一版行为：

- `req.Stream == true`：不查、不写 response cache；metric `skip{reason="streaming"}`；header 可设 `X-HUAKAI-Response-Cache: skip; reason=streaming`。
- Non-streaming only：cache full buffered response。

Phase 2 可选：

- Full SSE replay cache only，不做 final-only。
- 必须单独设计 ledger/hop chain、billing、client disconnect、replay event timing、max memory。

## TTL strategy

推荐配置：

- `HUAKAI_RESPONSE_CACHE_MODE=off|memory`，默认 `off`，避免未确认计费/隐私策略时自动缓存。
- `HUAKAI_RESPONSE_CACHE_DEFAULT_TTL=60s`，开启时默认 60s。
- `HUAKAI_RESPONSE_CACHE_MAX_TTL=1h`，per-vendor 不得超过 hard max。
- `HUAKAI_RESPONSE_CACHE_TTL_BY_VENDOR=anthropic=60s,openai=30s,gemini=30s`，可选。
- `HUAKAI_RESPONSE_CACHE_MAX_ENTRIES=10000`。
- `HUAKAI_RESPONSE_CACHE_MAX_BODY_BYTES=1048576`，沿用 handler 1 MiB body 保护的数量级 (`backend/internal/gatewayhttp/chat_completions_handler.go:116-118`)。

默认 off 是安全默认，不是功能缩水：功能完整存在，enablement path 明确。若 Owner 要立即降本，可以 OCAW 确认 Personal Edition 默认 `memory` + 60s。

## operator UI / admin surface

Go backend 第一版交付 UI 数据面：

- `GET /admin/v1/cache/response`
  - filters: `tenant_id`, `vendor`, `model`
  - response per row: `vendor`, `model`, `hits`, `misses`, `stores`, `skips`, `evictions`, `hit_rate`, `entry_count`
- `DELETE /admin/v1/cache/response`
  - filters: none = purge all; `vendor+model` = purge slice
  - audit/log: at least structured log; full DB audit event can be later if Owner wants schema.

Mount 位置建议靠近现有 admin observability routes (`backend/cmd/gateway/main.go:475-479`)。

实际前端 Admin Ops UI 可后续由 Gemini 调这个 endpoint 展示 hit rate per `(vendor, model)`；本 Go 计划不改 frontend。

## billing and audit semantics

第一版建议：

- Cache miss: billing/audit 与当前 upstream success 完全一致。
- Cache hit: 仍创建 fresh claim + fresh usage record + fresh billing event + fresh trust-chain ledger entry。
- Actual upstream cost: `0`。
- User charge: OCAW-003 决策。Codex 默认建议 L2 第一版对用户也 `0`，只在 routing_reason / header 标记 `response_cache_hit`；如果 Owner 要按折扣或本地 cache price 计费，必须先锁 pricing policy。
- `usage_source` 保持 `reported`，因为 usage 来自首次 upstream response 的 canonical usage；不新增 enum，避免 schema migration。
- `end_class` 使用现有 non-streaming normal path。
- routing_reason JSON 增加 cache hit marker，不改 schema。

重要保护：cache entry 只在 `Settle` 成功后写入。如果 upstream 成功但 Tx2 失败，不缓存，避免出现 "能返回/命中但账务无记录" 的不一致。

## test plan

Targeted unit tests:

- `internal/responsecache`: canonical hash ignores object order and whitespace; tenant/vendor/model changes produce different keys; TTL expiry; capacity LRU; purge vendor/model; metrics counters。
- `internal/config`: response cache default off; valid memory config; bad duration fails fast; bad max entries/body bytes fails fast; per-vendor TTL parser。
- `gatewayhttp` cache integration:
  - first exact non-stream request misses and calls upstream once。
  - second exact non-stream request hits and upstream call count remains one。
  - cache hit still calls `Settler.Settle` and writes a fresh ledger entry。
  - different tenant same body misses。
  - same tenant same body but different vendor/model misses。
  - upstream non-2xx not cached。
  - Settle failure prevents cache store。
  - `stream=true` skip does not call `Get` / `Set`。
- `gatewayhttp` admin handler:
  - unauthorized rejected。
  - tenant operator cannot read other tenant scope。
  - stats response computes hit rate。
  - purge calls store and increments purge metric。

Integration / smoke:

- Run `cd backend && go test ./internal/responsecache ./internal/config ./internal/gatewayhttp ./internal/cachemetrics ./internal/pool ./internal/gateway`。
- If time permits, run `cd backend && go test ./...`。

Manual verification:

- Start gateway with `HUAKAI_RESPONSE_CACHE_MODE=memory` and small TTL。
- Send same non-stream request twice。
- Confirm upstream mock count one, second response header `X-HUAKAI-Response-Cache=hit`, `/admin/v1/cache/response` shows hit rate > 0。

## time estimate

| Work item | Estimate |
| --- | --- |
| Plan synthesis + Owner OCAW | 0.5 day |
| `responsecache` package + config + unit tests | 0.5-1 day |
| handler integration + hit/miss/store semantics | 1 day |
| admin stats/purge endpoint + tests | 0.5 day |
| billing/ledger hit-path tests | 0.5-1 day |
| full targeted test pass + review fixes | 0.5 day |
| Total | 3-4 engineering days for in-memory LRU version; Redis backend adds 1-2 days plus dependency approval. |

## blast radius

Main risks:

- Returning stale or cross-tenant response.
- Billing inconsistency if cache hit bypasses Tx1/Tx2.
- Trust-chain confusion if old ledger/signature is replayed.
- Memory growth from large cached responses.
- Lower-than-expected hit rate in multi-instance deployment.
- Cache hit requiring pool capacity may surprise operators during upstream outage.
- Exact-body cache can cache non-deterministic model outputs for repeated prompts; TTL and default-off mitigate.

Mitigations:

- Tenant key prefix mandatory。
- Store after Settle success only。
- Fresh ledger per hit。
- Max entries + max body bytes + TTL。
- Admin purge。
- Default off until Owner confirms enablement and charge policy。
- Stream skip。

## decision points (5 Owner OCAW)

OCAW-001: Approve backend choice: in-memory LRU for F-CACHE-001 now, Redis as Phase 2 only. If Owner chooses Redis now, Go dependency + ops config approval is required before implementation.

OCAW-002: Approve physical key includes `tenant_id` despite product key being `(vendor, model, canonical(request_body))`. Codex recommends yes; cross-tenant shared cache is a security-sensitive separate product decision.

OCAW-003: Choose cache-hit charging policy: `0` charge, discounted local-cache charge, or normal charge. Codex recommends `0` for first L2 because upstream cost is zero and pricing tables are not yet dynamic.

OCAW-004: Choose enablement default: `off` by default with explicit env enable, or `memory` default-on for Personal Edition. Codex recommends default off until OCAW-003 is settled.

OCAW-005: Choose cache hit placement: after pool acquire (low-risk, reuses current Tx2) or before pool acquire (better outage behavior but requires billing settlement path changes). Codex recommends after pool acquire for first version.

## clean-room

Reference projects were not read. The names LiteLLM / Helicone / Portkey are treated only as Owner-provided reputation cues for "response cache exists in the ecosystem." This plan does not assert their source-level mechanisms, schemas, file names, algorithms, or UI designs. All implementation choices above are derived from HUAKAI local code paths and project rules.

No feature shrink: F-CACHE-001 simple exact L2 response cache is preserved. Semantic cache remains L3+ and is not silently dropped.

## sources read

HUAKAI internal sources:

- `CLAUDE.md:50-111` — plan-before-execute, independent plan, clean-room, source-must-read rules.
- `docs/RULES.md:22-60` — Owner Start Gate, clean-room, feature preservation.
- `docs/12_AGENT_WORKFLOW.md:52-63` — lane definitions and role split.
- `docs/03_FEATURE_PARITY_MATRIX.md:55-86` — F-CACHE-001 / F-CACHE-002 roadmap rows.
- `docs/10_RISK_REGISTER.md:19-28` — license risk rows.
- `backend/config.example.yaml:20-22` — existing illustrative cache config mention.
- `backend/go.mod:1-24` — current Go dependencies.
- `backend/internal/config/config.go:1-47` — env-first config loader.
- `backend/internal/config/pool_selector.go:81-131` — typed config parser pattern.
- `backend/cmd/gateway/main.go:188-216` — deps construction.
- `backend/cmd/gateway/main.go:243-263` — middleware and `/debug/vars`.
- `backend/cmd/gateway/main.go:391-482` — gateway/admin route mount points.
- `backend/internal/gatewayhttp/chat_completions_handler.go:88-159` — body parse and request validation.
- `backend/internal/gatewayhttp/chat_completions_handler.go:217-328` — Reserve, pool select, forward request metadata.
- `backend/internal/gatewayhttp/chat_completions_handler.go:330-475` — non-streaming upstream, canonical, ledger, Settle, response.
- `backend/internal/gatewayhttp/chat_completions_handler.go:478-555` — streaming branch.
- `backend/internal/gatewayhttp/chat_completions_handler.go:645-710` — HUAKAI headers and audit ledger entry.
- `backend/internal/gatewayhttp/chat_completions_handler.go:750-771` — non-streaming usage draft.
- `backend/internal/gatewayhttp/admin_observability_handler.go:58-211` — admin handler/query style.
- `backend/sql/queries/observability.sql:4-43` — usage list/count filters with provider/model.
- `backend/sql/queries/billing_settle.sql:28-57` — usage record insert fields.
- `backend/internal/billing/settler.go:37-170` — Tx2 settle requirements and slot release.
- `backend/internal/billing/billing.go:19-90` — ClaimGate/Settler interface contracts.
- `backend/internal/cachemetrics/cachemetrics.go:1-23` — existing vendor prompt cache metric scope.
- `backend/internal/cachemetrics/cachemetrics.go:161-241` — cache observer event contract.
- `backend/internal/cache_routing/prompt_hash.go:1-68` — existing prompt-prefix hash is not full response cache key.
- `backend/internal/gateway/singleflight.go:1-102` — existing same-key dedupe primitive, optional for future cache fill coalescing.
- `backend/internal/gateway/forwarder.go:85-218` — streaming forwarder responsibilities.
- `backend/internal/gateway/forwarder.go:377-427` — usage draft/state integration.
- `backend/internal/gateway/upstream_dispatcher.go:74-153` — raw upstream dispatch path.
- `backend/internal/gateway/upstream_dispatcher_hcsf.go:43-140` — non-streaming HCSF dispatch path.
- `backend/internal/proto/hcsf.go:81-107` — buffered response and usage fields.
- `backend/internal/proto/anthropic_sse.go:156-177` and `backend/internal/proto/openai_sse.go:411-431` — current prompt cache token observation paths.
- `backend/internal/pool/pasr_metrics.go:1-161` and `backend/internal/pool/pasr_feedback.go:1-116` — existing PASR/cache telemetry distinction.

Reference project source files read: none.

Reference README files read: none.

Lane: specifier
Agent: Codex GPT-5 session
UTC timestamp: 2026-05-15T15:44:11Z

中文 Owner 摘要：本计划只基于 HUAKAI 内部 Go backend 和 docs 观察，提出 F-CACHE-001 第一版用 in-memory LRU + TTL 做 non-streaming exact response cache，streaming 先 skip，命中仍走 fresh ledger 和 Tx2 settlement，不复用旧审计头，不新增依赖、不改 schema、不读参考项目源码。合理推断是该路径能最快降低重复请求 upstream 成本，同时把 Redis、stream replay、semantic cache 放到后续阶段；需要 Owner 确认 5 个 OCAW，尤其是 tenant 是否必须入物理 key、cache hit 如何收费、默认是否开启。
