# HUAKAI Upgrade #1 U1-A PRE-REVIEW

Lane=codex  
Time=2026-05-08  
Mode=REVIEWER only; no production code or repo mutation

## Executive Decision

**推荐选项：B for U1-A only.** U1-A 作为第一原子应只落 `BindingCache`/`BindingIndex` 的接口、类型、noop/stub 行为和 schema-free 单测，不应在这个原子里创建表，也不应改变生产 routing 结果。

**但生产 routing/health 真正启用 binding-aware 之前，A 是前置条件。** 当前 HUAKAI 没有 user/account 或 key/account 持久绑定表；已有 `sticky_bindings` 是 `(tenant_id, session_hash, model) -> provider_account_id` 的会话亲和，不是用户或 API key 绑定。将 U1-B/U1-C 接入生产选择逻辑前，必须先有 Owner 批准的显式 binding schema。

**选项 C 必须拒绝。** 用 `sticky_bindings.session_hash` 曲解成 user 维度会把会话亲和、用户授权、account contract 混成同一个字段，破坏审计、TTL、模型维度和未来 binding_id 语义。

## Evidence Read

- `sticky_bindings` 当前 schema 是 session affinity：`tenant_id`, `session_hash`, `model`, `provider_account_id`, `expires_at`，唯一索引为 `(tenant_id, session_hash, model)`；注释也写明 `session_hash` derived from cache_control / metadata.user_id / SessionContext。见 `backend/sql/migrations/0001_pool_routing.up.sql:199-215` 和 `backend/sql/queries/pool_sticky_bindings.sql:1-27`。
- Auth 已解析 `TenantID/APIKeyID/UserID`，且 `api_keys` 已有 `(tenant_id,user_id)` FK。见 `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51-70`、`backend/internal/auth/api_key_resolver.go:38-46`、`backend/internal/auth/api_key_resolver.go:138-142`。
- HTTP hot path 已把 `TenantID/UserID/APIKeyID` 传给 Router 和 Pool selector。见 `backend/internal/gatewayhttp/chat_completions_handler.go:131-149`、`backend/internal/gatewayhttp/chat_completions_handler.go:198-208`。
- Pool selector 现有 seam 明确：先 `ListAccounts`，再 `filter`，然后 `trySticky`，最后 `tryLayer`/slot/claim。`trySticky` 在 `selector.go:166-179` 只查 StickyStore 并在候选集中命中时选中。见 `backend/internal/pool/selector.go:100-140`、`backend/internal/pool/selector.go:150-179`。
- GateChain 有可插入过滤点，但当前 default gates 没有 binding gate。见 `backend/internal/pool/gates.go:34-52`、`backend/internal/pool/gates.go:54-103`。
- `health_fsm` 注释和结构是 per-account snapshot；FSM 是纯函数，调用方持久化 side effects。见 `backend/internal/gateway/health_fsm.go:83-100`、`backend/internal/gateway/health_fsm.go:153-184`、`backend/internal/gateway/health_fsm.go:309-323`。
- 历史 Account-to-API spine 计划已指出 `APIKeyBinding` critical missing，并建议 `api_key_bindings`，不是 `user_account_bindings`。见 `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:37-45`、`docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:101-124`、`docs/plans/2026-05-02-accapi-spine.md:16-29`。
- 当前迁移已到 `0012_provider_accounts_proxy_url.*`；旧的 “0011 accapi spine” 计划没有落地，编号 0011 已被 protocol family/session extension 占用。

## Option Comparison

| Option | Decision | Blast radius | Reason |
| --- | --- | --- | --- |
| A. 先建 binding 表再动 routing | **生产 routing 前必须；U1-A 不必须** | High | 触碰 DB schema、sqlc、admin/write path、route contract、usage/audit 语义。正确性最好，但属于高风险文件范围，需 Owner 明确确认。 |
| B. U1-A 只做 in-memory cache interface + noop/stub | **推荐给 U1-A** | Low-Medium | 可先固定接口和调用边界，默认不改变行为；避免第一原子把 schema、routing、health、quota 全绑在一起。必须设置硬门禁：noop 不得让生产 routing 误以为有 binding。 |
| C. 复用 sticky_bindings.session_hash 当 user | **拒绝** | High and corrupting | sticky 是 session/model TTL 亲和，不是授权 contract。会污染 sticky 语义、审计语义和未来 binding_id；也无法表达 API key fallback priority、tenant_default、direct account/pool target。 |

## Migration Sequence If Owner Selects A

如果 Owner 要求 U1-A 直接包含 schema，推荐用**下一号迁移 0013**，不要放到 U5 之后。

理由：

1. 迁移目录当前最高是 `0012`，因此新 schema 应该是 `0013_*`，不能复用已经存在的 `0011`。
2. binding-aware routing/health 的生产正确性依赖持久 binding identity。若迁移放到 U5 之后，U1-B/U1-C 只能使用 fake/sticky/cache-only 身份，会形成不可审计的半功能。
3. schema 名称建议沿用已评审过的 spine：`api_key_bindings`。Auth hot path 已有 `APIKeyID` 和 `UserID`；API key 是当前请求可审计的 customer contract owner。若 Owner 坚持 user-scoped product semantics，应先明确 `user_account_bindings` 与 `api_key_bindings` 的优先级和冲突规则，不应临时新增孤立表。

最小 0013 前置范围应只包含：

- `api_key_bindings` 或 Owner 批准的等价 binding table；
- composite tenant FK 防 cross-tenant binding；
- per-kind target columns/checks/indexes；
- sqlc read queries for active bindings;
- optional nullable audit columns only if U1 immediately要记录 binding_id。不要在 U1-A 同时改 billing ledger/quota enforcement。

## BindingCache Interface Pattern

如果选 B，`BindingCache` 应参考 **`registry.Cache` 的接口 + noop stub 模式**，而不是直接暴露 `sync.Map`。

理由：

- `registry.Cache` 已有同类 precedent：接口保留、`noopCache` 默认 miss、L0 不改变行为。见 `backend/internal/registry/cache.go:1-34`、`backend/internal/registry/postgres_registry.go:42-56`。
- `sync.Map` 是实现细节，只适合局部反射缓存这类无业务一致性的微缓存；`backend/internal/proto/passthrough.go:147-178`、`203` 的 `typeCache sync.Map` 不承担 tenant/key/schema version 语义，不适合 binding。
- BindingCache 需要从一开始表达 key space：至少包含 `tenant_id`, `api_key_id` 或明确的 `user_id`, requested model/family, snapshot/version。noop miss 必须 fail-closed 或 preserve-current-behavior，不能 fake allow。

建议接口语义：

- `Lookup(ctx, scope) (BindingSnapshot, bool, error)`：noop 返回 `(zero,false,nil)`。
- `Put/Replace(ctx, snapshot)` 或后续 reload 才加入；U1-A 可先不提供写接口，只提供 read + test stub。
- `BindingSnapshot` 包含 `BindingID`, `Kind`, `PoolGroupID`, `ProviderAccountID`, `Priority`, `Version`，但 production noop 不生成 synthetic binding。

## Routing And Health Blast Radius

### `selector.go:166` / sticky 接入点

Blast radius: **Medium if binding gate only; High if sticky semantics are changed.**

Safe path:

- 在 `filter` 或 GateChain 中加入 `BindingGate`，让候选集先被 binding scope 收窄。
- `trySticky()` 只能在已通过 binding gate 的 candidates 上命中；现在它已经只在传入 candidates 中匹配账号，所以如果 binding gate 先跑，sticky 不会越界。
- 需要新增 routing reason 字段/原因，例如 `binding_scope`、`binding_id`、`sticky_broken_binding_scope`。当前 `routingReason` 已有 `StickyBreakReason` 字段但 selector 未设置 break reason，接入时要补审计，不要静默 fallback。

Risk:

- 若直接在 `trySticky()` 里查 BindingCache，容易把 sticky 和 binding 两个状态源耦合。
- 若 BindingCache miss 被当成 allow-all，bound 用户可能逃逸到 tenant default。
- 若 BindingGate 放在 health 后面，health exclusion count 可能掩盖 binding mismatch，操作员难以定位。

### `health_fsm.go`

Blast radius: **Medium for pure type extension; High for persistence/wiring.**

Safe path:

- 不改 FSM 核心转移规则；先在调用侧引入 health scope key：account-global vs binding-local。
- transient/rate/latency/ambiguous errors 写 binding-local overlay；credential revoked/account disabled/expired 等 iron-clad 写 account-global。
- U1-A 不应改 `provider_accounts.health_state` schema；U1-C 若要持久 overlay，需要单独 Owner schema 决策。

Risk:

- 当前 `HealthSnapshot` 是 per-account 注释和输入。如果在没有 binding store 的情况下复用同一个 snapshot，会继续污染 unbound 全局健康。
- 如果把所有 429/5xx 写回 `provider_accounts.health_state`，则 bound 失败仍会污染所有用户。
- 如果把 credential hard failure 只写 binding-local，坏凭据会继续被其它 binding 使用。

## Schema-Free E2E/Test Strategy

不引入 schema migration 时，可以跑通的是 **schema-free vertical/e2e-lite**，不是持久化 DB e2e。

推荐测试结构：

1. 用 in-memory `BindingCache` test double seed：`tenant=1/api_key=bound -> binding B1 -> account A`；另一个 identity 为 unbound/noop。
2. 用 stub `AccountSource` 返回同 pool 两个账号 A/B，并保持生产 SQL stub discipline：按 `TenantID` 过滤，避免测试隐藏 cross-tenant bug。
3. 用 `BindingGate` 或等价 injected gate 验证 bound request 只看到 A，unbound/default request 仍看到全局候选。
4. 用 in-memory binding-health overlay store 记录 B1 的 transient failure；全局 account health snapshot 不变。
5. 对 unbound/default request 再跑 selection，断言不会因为 B1 overlay 被排除；同时如果注入 iron-clad credential failure，则断言 account-global gate 会排除所有 binding。
6. 如果需要 HTTP handler 级别，使用 stub Auth/Registry/Router/Selector/Dispatcher/FSM writer 注入，不连 Postgres。此测试只证明 request-path contract，不证明 migration/FK/sqlc。

测试命名上建议标为 `binding cache/schema-free` 或 `e2e-lite`，不要标成 Released-spec DB e2e。真正 Released gate 仍需 0013 之后补：

- binding rows FK/cross-tenant tests；
- binding cache reload/read tests；
- stale binding version tests；
- routing reason/attempt audit contains `binding_id` tests。

## Owner Decision Points

1. 确认 U1-A 是否按 **B: interface + noop/stub only** 执行，且不得改变生产 routing。
2. 确认 U1-B/U1-C 生产启用前是否必须落 `0013` binding schema。
3. 确认 schema owner scope：推荐 `api_key_bindings`；若要 `user_account_bindings`，需先定义与 `api_keys.user_id`、API key fallback、tenant_default 的关系。
4. 确认绑定缺失行为：SaaS 默认建议 fail-closed；Personal Edition 是否允许 last-known-good/tenant-default fallback 是单独 policy。
5. 确认 health scope policy：transient binding-local，iron-clad account-global。
6. 确认 U1-A schema-free tests 只能作为 pre-migration confidence，不作为 Released gate。

## Final Recommendation

Proceed with **B for U1-A**: define `BindingCache`/`BindingSnapshot`/noop/stub following `registry.Cache` style; add only schema-free tests. Add an explicit blocker note that production binding-aware routing and binding-health persistence require Owner-approved `0013` binding schema before U1-B/U1-C can be treated as production-correct.

Do not use `sticky_bindings` for user/account binding under any circumstance.
