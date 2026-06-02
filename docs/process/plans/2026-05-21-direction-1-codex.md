# 2026-05-21 方向 1 执行计划（Codex 独立稿）

| 字段 | 内容 |
|---|---|
| Owner directive | “HUAKAI 战略方向已由 Owner 锁定为「方向 1」: Go gatewayhttp 管线作账号转 API 大脑，Rust core_gateway 重定位为「高性能 + 强伪装的出站传输层」。” |
| 独立性声明 | 本稿按 CLAUDE.md #10 平行计划纪律独立起草。未读取 `docs/process/plans/2026-05-21-direction-1-claude.md`。 |
| 已读上下文 | `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md`、`docs/RULES.md`、`docs/01_PROJECT_BRIEF.md`、`docs/03_FEATURE_PARITY_MATRIX.md`、`docs/10_RISK_REGISTER.md`、`docs/12_AGENT_WORKFLOW.md`、`docs/process/plans/2026-05-21-rust-core-path.md`，以及 Go/Rust 内部实现路径。 |
| Scope | 方向 1 的执行计划：Go 补 6 个洞、Rust 重定位、Go-Rust 传输契约、已有 Rust C 档工作收敛、阶段计划、风险和 Owner 决策点。 |
| Out of scope | 本稿不改代码、不改数据库 schema、不改部署脚本、不做 git 操作、不读取非 MIT 参考项目源码、不做实际实现。 |
| Success criteria | Owner 能据此拍板：先做什么、哪些包要改、Go 与 Rust 如何交互、失败模式如何处理、Docker 怎么部署、哪些高风险点需要确认。 |
| Time estimate | 写计划：本轮完成。后续执行估计见“总体阶段计划”。 |
| Blast radius | 执行该方向会触及 Go 热路径、账号选择、凭据刷新、重试/计费边界、Rust 进程角色和 Docker 拓扑；其中 billing/quota/auth/DB/deployment 为高风险区，需要 Owner 中途确认。 |
| Failure modes | 计划层失败主要是边界不清、把 Rust 又推回完整数据面、Go-Rust 契约只覆盖 happy path、忽略计费原子性、忽略 Docker 运行形态。本文逐项约束。 |
| Decision points | gRPC over UDS 是否批准、新运行依赖是否批准、Rust 不可用时默认 fallback 还是 fail-fast、凭据是否允许穿过本机 UDS、caller limit 是否先做内存版还是直接做持久/分布式版。 |
| Pre-execution checklist | 1. Owner 批准方向 1 合成计划；2. 明确 Go/Rust 契约传输；3. 明确 sidecar fallback 策略；4. 明确 billing source of truth；5. 明确 Docker 拓扑；6. 对高风险 schema/auth/billing/quota 变更单独开确认。 |

> 【2026-06-02 已更新】本 Codex 独立稿是 2026-05-21 的历史输入。当前 C 方向已锁定并推进：
> ②⑥ retry/failover/跨池、③ Anthropic buffered、transport mimicry/sidecar
> 接线已落地；旧 `RouteService` / `route_client` / `mock_control_plane` 路线按 C
> 退役为 legacy。本文关于 `ApplyMimicryPlan` 未接生产 dispatch 的判断，本轮全仓搜索仍未发现
> 非测试 caller，因此不更新为已闭；以下为历史草案。

## 0. 战略结论

方向 1 的核心判断是：**Go `gatewayhttp` 是账号转 API 大脑，Rust `core_gateway` 不再承担账号规划、凭据选择、控制面路由或 client-facing API 入口。**

Go 继续承担：

- 调用方认证、租户/API key 限流、quota/billing 决策。
- 模型解析、跨池候选、账号选择、凭据解析和 OAuth 热刷新。
- OpenAI/Anthropic/Responses 等协议翻译。
- 应用层 cloaking body 改写。
- 单请求内 retry/failover 编排。
- usage 抽取、账务结算、审计。

Rust 收敛为：

- Go 内部调用的 **outbound transport sidecar**。
- 负责 L1/L2 传输伪装、TLS/H2 profile、连接池、流式 relay、超时、过载保护、底层 IO 指标。
- 不再负责账号规划、不再调用不存在的 Go `RouteService` 控制面、不再把 `listener` 当生产 client 入口。

这个方向的第一原则：**Go-Rust 之间的 contract 必须比任一单点优化更优先。** 如果契约不能可靠处理崩溃、版本不一致、流中断、背压、取消、计费原子性，Rust sidecar 就不能进入生产主链路。

## 1. 依据与现状归纳

本稿只基于 HUAKAI 内部文档和本地代码阅读。关键观察：

- gap 分析已判定：Go `gatewayhttp` 是当前唯一生产账号转 API 路径，约 13/15 步已在主链路；Rust `core_gateway` 依赖不存在的 Go 控制面 RouteService，现状更接近探索数据面/透传外壳。
- Go 现有 wiring 使用 `router.NewDefaultRouter()`，而该 L0 router 只取第一个 pool candidate 并产生单次 attempt。
- Go handler 当前账号选择硬编码 `AttemptSeq: 1`，billing/header 相关路径也有固定 attempt 序号痕迹。
- `ApplyMimicryPlan` 及应用层 cloaking helper 已存在，但只有测试调用，没有接入生产 dispatch。
- 非流式 Anthropic Messages 响应翻译当前是 fail-fast 501 stub。
- OAuth 凭据刷新已有后台 scheduler/refresher/store 能力，但请求热路径 `CredentialVault.Resolve` 遇到过期凭据不会同步刷新。
- caller-side tenant/API key QPS/并发限制没有形成热路径 gate；现有 `internal/rate` 更偏上游账号限流/cooldown。
- Rust `core_gateway` 当前 `listener` 暴露 `/v1/messages`、`/v1/chat/completions` 等 client-facing route，并通过 `account_planner`/`route_client` 依赖 `RouteService`。
- Rust 里 `proxy_engine`、`relay`、`resource_limits`、`server_runtime`、`body_timeout`、mimicry 相关模块是可保留资产，适合转成 transport sidecar 内核。

## 2. Go 管线补 6 个洞

### 2.1 总体优先级

建议把 6 个洞分成两个执行波次：

| 优先级 | 洞 | 原因 |
|---|---|---|
| P0-A | #2 单请求 retry + account failover；#6 跨池多候选编排 | 这是 Go 作为“大脑”的核心。如果仍然只有单 attempt，Rust 再强也只是把一次错误更快送出去。 |
| P0-B | #3 非流式 Anthropic Messages 响应翻译 | 当前是明确 501，属于可见功能缺口。 |
| P0-C | #4 OAuth token 请求热路径即时刷新 | 否则 retry/failover 会被过期凭据噪音污染，产生无意义上游失败。 |
| P1-A | #1 应用层 cloaking 接线 | 产品差异化强，但必须在协议翻译和 retry 语义稳定后接入；先 feature flag 默认保守。 |
| P1-B | #5 调用方 tenant/API key 限流 | 生产保护必需。若不先改 schema，可先做单进程内存版，再升级分布式/持久策略。 |

#6 与 #2 不应完全拆开实现。跨池候选是 router/planner 的输入扩展，retry/failover 是 handler/executor 的消费方式；两者一起设计，分 PR 落地。

### 2.2 洞 #1：应用层 cloaking 接线

**目标**

把已存在的 `ApplyMimicryPlan` 及 system rewrite、cache_control、tool naming、metadata rewrite 等能力接入 Go 出站请求构造路径。Rust 负责传输层伪装，Go 负责应用层 JSON/body cloaking。

**涉及包**

- `backend/internal/gatewayhttp`
- `backend/internal/gateway`
- `backend/internal/proto/*`
- `backend/internal/provider`
- `backend/internal/registry` 或 policy/config 读取路径
- 后续可能需要 `backend/internal/audit` 记录摘要事件

**实施思路**

1. 增加 Go 侧 `MimicryPlanner`/`MimicryPolicyResolver` 接口。
   - 输入：tenant、model、vendor、provider account、request protocol、stream/buffered、risk mode。
   - 输出：`gateway.MimicryPlan`，包括是否启用、启用哪些 step、profile id、审计摘要。
2. 接线点放在 **协议翻译之后、真正 dispatch 之前**。
   - 客户端 OpenAI/Anthropic body 先转为 provider-bound body。
   - Go 对 provider-bound body 调用 `ApplyMimicryPlan`。
   - 改写后的 body 只用于上游请求；原始 client body 仍用于 idempotency、request hash、审计安全摘要。
3. streaming 和 non-streaming 都必须走同一 planner。
   - streaming 的 request body 也是一次性请求体，不影响响应流。
   - response stream 不由 `ApplyMimicryPlan` 修改。
4. 默认 feature flag 保守。
   - 初始只允许 per-tenant/per-provider 开启。
   - 默认 no-op，测试覆盖 no-op 不改变 body。
5. 审计只记录 “启用了哪些 step / profile id / body hash before-after”，禁止记录完整 prompt、tool schema、credential。

**风险**

- 应用层改写可能改变模型行为、token 数、cache 命中和工具调用语义。
- 若在错误位置接线，可能改写 client 原始 body，破坏 idempotency 或 replay。
- 不同 provider 的 system/tool/cache_control 语义不同，不能一套规则全局硬套。

**回归面**

- OpenAI Chat Completions buffered/streaming。
- Anthropic Messages buffered/streaming。
- Responses API 若复用同一 dispatcher，也要验证。
- L2 cache key、claim id、billing usage 抽取不应因 cloaking 改写而错绑。
- golden tests：no-op、单 step、多 step、非法 JSON、unknown vendor、disabled policy。

**估时**

- 接口和接线：1.0-1.5 天。
- policy/config 和审计摘要：0.5-1 天。
- 回归测试：1 天。
- 合计：2.5-3.5 天。

### 2.3 洞 #2：单请求内 retry + account failover

**目标**

替换 L0 单 attempt 行为。一个 client request 内，Go 根据 route plan、错误分类和未交付状态，尝试多个账号/候选；但一旦响应已向客户端开始交付，就不再跨号重试，避免“半截 200”和重复计费。

**涉及包**

- `backend/internal/router`
- `backend/internal/gatewayhttp`
- `backend/internal/pool`
- `backend/internal/gateway`
- `backend/internal/rate`
- `backend/internal/channelhealth` 或上游健康记录路径
- `backend/internal/billing` / settlement 相关路径
- `backend/internal/transport`，后续接 Rust sidecar client

**实施思路**

1. 先抽出 handler 内的 “一次 attempt 执行函数”。
   - 输入：`RoutePlan`、`AttemptPlan`、attempt seq、已缓冲请求体、claim context。
   - 输出：成功响应、可分类错误、是否已经向客户端交付、usage/settlement 信息。
2. route plan 支持多个 attempts。
   - `RoutePlan.Attempts` 不再只有第一个 pool candidate。
   - `AttemptBudget` 明确最大尝试次数。
   - `RetryableEndClasses` 明确哪些错误可换号/重试。
3. handler 执行 loop。
   - 每个 attempt 调用 selector、credential vault、dispatcher。
   - `AttemptSeq` 使用 attempt index，而不是固定 1。
   - pre-delivery 失败才允许进入下一 attempt。
   - post-delivery 失败只向客户端传递明确错误，并进入 ambiguous/partial settlement，不 retry。
4. 错误 taxonomy 前置。
   - 上游 5xx、连接超时、TLS handshake、连接 reset、429/cooldown 可按 policy retry。
   - 401/403 默认不跨号盲重试；若是凭据过期，触发 hot refresh 后同号重试一次。
   - 4xx client/protocol error 不 retry。
5. billing/claim 原子性。
   - 一个 client request 只能有一个最终 chargeable settlement。
   - 每个 failed attempt 要释放或结束对应 in-flight/claim attempt 记录。
   - 所有 attempt 的审计链路共享 request id，但 attempt seq 不同。

**风险**

- 高风险：会触及 billing/quota/claim settlement 语义。实现前需要 Owner 对具体改动确认。
- retry 可能放大上游流量和成本。
- 如果错误分类不准，会把不该 retry 的 prompt/protocol 错误打到多个账号。
- streaming 中途失败不能跨号补救，只能明确终止和标记 ambiguous。

**回归面**

- 第一个账号连接失败，第二个账号成功：只结算一次。
- 第一个账号返回 500，第二个成功：route/health 记录正确。
- 上游 401：触发 refresh 或 fail，不盲目跨池。
- streaming 已发 header/首 token 后失败：不 retry，不成功结算。
- selector in-flight 计数释放。
- idempotency/replay 不因多 attempt 写多份最终结果。

**估时**

- attempt executor 重构：1.5-2 天。
- router plan 多 attempt 消费：1-1.5 天。
- error taxonomy + retry policy：1 天。
- billing/claim/settlement 回归：1.5-2 天。
- 合计：5-6.5 天。

### 2.4 洞 #3：非流式 Anthropic Messages 响应翻译

**目标**

移除 `chat_completions_handler.go` 中 buffered Anthropic 501 stub，实现 provider response 到 canonical，再到 client protocol response 的翻译闭环。

**涉及包**

- `backend/internal/proto/anthropic`
- `backend/internal/proto`
- `backend/internal/gatewayhttp`
- `backend/internal/gateway`
- 测试 fixture 所在路径

**实施思路**

1. 实现 Anthropic provider buffered response parser。
   - 输入：上游 status、headers、body。
   - 输出：HCSF/canonical response，包含 content blocks、stop reason、usage、model。
2. 复用现有 client response translator。
   - provider response 先进入 canonical。
   - 再由 Anthropic Messages client translator 输出给调用方。
3. 错误响应不能误当成功 response 翻译。
   - 非 2xx 走错误 taxonomy。
   - 上游返回 JSON error 时保留分类和可审计摘要。
4. body limit 与异常 JSON 行为明确。
   - 非流式可以 buffer，但需要大小限制和 502/413 映射策略。

**风险**

- Anthropic content blocks 形态多，tool_use/tool_result/empty content 容易漏。
- usage 字段缺失时 billing 要进入缺失/估算/ambiguous 策略，不能静默成功。
- 错误 JSON 与成功 JSON 混淆会影响 retry 和计费。

**回归面**

- 普通 text response。
- 多 content block。
- tool_use response。
- stop_reason / stop_sequence。
- usage 完整、usage 缺失。
- 上游 4xx/5xx error body。
- malformed JSON。

**估时**

- translator：1-1.5 天。
- tests/fixtures：1-1.5 天。
- handler stub 移除与回归：0.5 天。
- 合计：2.5-3.5 天。

### 2.5 洞 #4：OAuth token 请求热路径即时刷新

**目标**

请求热路径遇到 expired/near-expiry OAuth credential 时，可以在单请求内即时刷新，避免仅依赖后台 scheduler。

**涉及包**

- `backend/internal/provider`
- `backend/internal/credentialworker`
- `backend/internal/credentialstore`
- `backend/internal/auth`
- `backend/internal/gatewayhttp`
- 审计与指标路径

**实施思路**

1. 增加 `RefreshingCredentialVault` 包装层，先不改底层 store contract。
   - 调用现有 vault resolve。
   - 遇到 `ErrCredentialExpired` 或 `refresh_before_at <= now + skew`，调用 `AccountCredentialRefresher`。
   - refresh 成功后重新 resolve。
2. 复用现有 refresh lock/storm controller。
   - 同一 account 同一时间只允许一个热刷新。
   - 其他请求要么等待短 deadline，要么使用 grace token，要么快速失败，策略需配置。
3. 与 retry/failover 联动。
   - 上游 401 若分类为 token expired，可触发同号 refresh + 同号 retry 一次。
   - refresh 失败后才按 route policy 决定是否换号。
4. 安全审计。
   - 不记录 access token/refresh token。
   - 记录 account id、credential version、refresh outcome、latency、storm gate decision。

**风险**

- 高风险：涉及 upstream credential、auth material、refresh storm 控制。
- 热路径刷新会增加请求 latency。
- refresh provider 失败时如果错误分类不准，可能导致多账号级联失败。
- 多实例下 refresh lock 必须以 store/DB 级语义为准，不能只靠进程内 mutex。

**回归面**

- active token 正常 resolve。
- expired token refresh 成功后 dispatch。
- refresh 失败返回明确错误，不泄露 secret。
- 并发 20 个请求只触发一次实际 refresh。
- 401 后同号 refresh retry once。
- no-grace policy 与 grace policy 分别测试。

**估时**

- wrapper 和接口接线：1 天。
- 401 联动 retry：0.5-1 天。
- 并发/安全/审计测试：1.5 天。
- 合计：3-3.5 天。

### 2.6 洞 #5：对调用方的 tenant/API key QPS/并发限流

**目标**

在 Go ingress 热路径对调用方做 QPS 和 concurrency gate，key 至少包含 tenant 和 api key。该能力不同于上游账号 cooldown/rate table。

**涉及包**

- `backend/internal/auth`
- `backend/internal/gatewayhttp`
- 新包建议：`backend/internal/inboundlimit` 或 `backend/internal/callerlimit`
- `backend/cmd/gateway`
- 后续若持久化策略，需要 SQL migration，高风险需 Owner 确认

**实施思路**

分两阶段。

**阶段 A：无 schema 的内存版，默认 disabled**

1. `APIKeyResolver` 返回 tenant id、api key id 后，进入 `CallerLimiter`.
2. `CallerLimiter` 对 `(tenant_id, api_key_id)` 做：
   - token bucket QPS。
   - weighted semaphore concurrency。
   - streaming 请求占用 concurrency 到 response 完成或 client cancel。
3. policy 来源先用 config/static map，默认未配置即不限制。
4. 超限返回 429，包含 `Retry-After` 和 HUAKAI 内部 error code。

**阶段 B：持久/分布式策略**

1. 若 Owner 批准 schema，给 tenant/API key 增加 limit policy。
2. 多实例强一致限制需 Redis/Postgres advisory lock/lease 或专门 rate-limit service；不能假装进程内限流覆盖集群。

**风险**

- 内存版只能保护单实例，不是分布式强限制。
- streaming 长连接会长期占用 concurrency，配置过低会误伤正常客户。
- 如果与 quota/billing 顺序不清，可能先占 quota 后被限流。

**回归面**

- 同 tenant 不同 key 隔离。
- 同 key QPS 超限。
- 并发超限。
- streaming cancel 后释放。
- handler panic/error 后释放。
- 多实例限制能力在文档中明确标注，不夸大。

**估时**

- 阶段 A：2-3 天。
- 阶段 B：取决于 Owner 是否批准 schema/分布式依赖，估 3-6 天。

### 2.7 洞 #6：跨池多候选编排

**目标**

Router 不再只取第一个 pool candidate，而是根据 registry/model binding、pool group、健康、容量、策略和 retry budget 生成跨池 attempt 序列。

**涉及包**

- `backend/internal/router`
- `backend/internal/registry`
- `backend/internal/pool`
- `backend/internal/channelhealth`
- `backend/cmd/gateway/wiring.go`

**实施思路**

1. 保留 `RoutePlan` 结构，但替换 `DefaultRouter` 的策略实现。
2. 从 `ResolvedModel.PoolCandidates` 生成多个 `AttemptPlan`。
3. ranking 维度：
   - tenant/model route policy。
   - pool health/cooldown。
   - capability match。
   - account/pool concurrency hint。
   - cost/priority/fault domain。
   - sticky/session hash，如已有 policy 需要保留。
4. 生成 `AttemptBudget`。
   - 默认 1 或 2 需 Owner/ops 配置。
   - streaming 默认更保守，避免长连接成本放大。
5. 与 selector 边界清楚。
   - router 选 pool/group/order。
   - selector 在 pool 内选 account。
   - credential vault 只取已选 account 的凭据。

**风险**

- ranking 过度复杂会难以解释。
- route plan 与 selector 都做权重会重复决策。
- 跨池 fallback 可能突破租户隔离或地区/合规限制，必须保留 policy hard constraint。

**回归面**

- 多 pool candidate 时生成多个 attempts。
- capability 不匹配的 pool 不进入 plan。
- cooldown/health bad pool 降级或跳过。
- tenant hard constraint 不被 retry 绕开。
- `AttemptSeq` 和 `PoolGroupID` 与实际选择一致。

**估时**

- planner：1.5-2 天。
- ranking/health/capacity 接入：1-2 天。
- tests：1.5 天。
- 合计：4-5.5 天；与 #2 可部分重叠。

## 3. Rust `core_gateway` 重定位为传输层

### 3.1 应保留的能力

Rust 应保留并强化这些能力：

| 能力 | 保留原因 | 方向 1 下的位置 |
|---|---|---|
| L1/L2 mimicry | Rust 的核心价值之一是强传输伪装。 | sidecar 根据 Go 传入的 profile/ref 执行 TLS/H2/connection 行为。 |
| 连接池 | 高性能出站 transport 需要复用连接和隔离 profile。 | Rust 内部按 upstream/profile/proxy/account-ish key 维护池，但不做账号选择。 |
| 流式 relay | SSE/streaming 的低开销转发仍有价值。 | Rust 把上游响应流回传给 Go；Go 再向 client 转发并抽 usage。 |
| 超时 | connect/TLS/header/body idle/downstream write idle 属于 transport。 | Rust 执行 per-attempt 子超时；Go 拥有整体 deadline。 |
| 过载保护 | sidecar 需要保护自身 FD、连接、in-flight。 | Rust 返回 typed overload，Go 可 fallback 或快速失败。 |
| `/healthz`、`/metrics` | Go 和 Docker 需要探测与告警。 | sidecar internal endpoints；不可成为 public gateway API。 |
| custom serve loop / body timeout | 已完成的 P6 work 对 sidecar ingress 有价值。 | 保留，配置默认保守。 |

### 3.2 应删减、退役或 legacy 化的能力

不建议直接删除历史代码，先退到 legacy feature/bin，避免一次性大拆风险。但生产默认形态应退役：

| 当前能力 | 处理 | 原因 |
|---|---|---|
| `account_planner` | 退役出默认 binary；保留 legacy/mock 测试可选。 | Go 才是账号选择和 route brain。 |
| `route.proto` 里的 `RouteService` | 不作为方向 1 生产契约；若保留仅作历史/legacy。 | Go backend 没有该 CP，实现它会把 Rust 又推回完整数据面路线。 |
| `route_client` 调 Go CP | 默认不启用。 | 方向 1 不需要 Rust 向 Go 请求账号计划。 |
| `attempt_reporter` 上报 CP | 退役为非权威 local metrics。 | billing/retry/attempt truth 必须在 Go。 |
| public `listener` client API | 不作为生产入口。 | client-facing API 由 Go `gatewayhttp` 提供。 |
| Rust 解析 tenant/model headers 并决定 route | 删除生产依赖。 | 这些是 Go 大脑职责。 |

### 3.3 最终形态

最终建议形态：

```text
Client
  -> Go gatewayhttp
       auth / caller limit / protocol translation / account routing
       credential resolve+refresh / app cloaking / retry+failover / billing
       -> Rust transport sidecar over UDS gRPC
            TLS/H2 mimicry / proxy / connection pool / transport timeout
            upstream streaming relay
       <- response stream to Go
     usage extraction / response translation / settlement
  <- Client
```

Rust binary 目标名可以是 `huakai-transport-sidecar` 或保留现名但启动 mode 明确为 `transport-sidecar`。默认 Docker 不暴露 public 端口，只暴露 UDS 或 loopback internal port。

## 4. Go ↔ Rust 传输层契约

这是方向 1 的命脉章节。结论先行：**推荐 gRPC over Unix Domain Socket 作为同机 Docker 部署的生产契约；localhost HTTP 只作 debug/fallback；跨主机才使用 TCP+mTLS。**

### 4.1 传输选型

| 选项 | 优点 | 缺点 | 结论 |
|---|---|---|---|
| gRPC over UDS | typed contract、双向/服务端 streaming、deadline/cancel/backpressure 语义成熟、proto 版本演进清楚、本机不暴露端口。 | 需要 Go/Rust gRPC 依赖；需要处理 UDS volume/权限。 | 推荐生产默认。 |
| HTTP over UDS | 依赖较少，和现有 HTTP 思维接近。 | typed error、stream terminal、版本协商、取消语义要自建，容易漏中途失败。 | 可作简化 fallback，不推荐作为主契约。 |
| localhost TCP HTTP/gRPC | Docker 调试简单。 | 暴露端口面更大；凭据经过 TCP stack；跨容器网络策略要更细。 | 仅 debug/不能共享 UDS 时使用。 |
| 跨主机 TCP+mTLS | 支持独立扩缩容。 | 最大安全和运维复杂度；凭据传输风险显著增加。 | 后续 Enterprise 选项，不作为第一版 Docker 推荐。 |

**推荐**

- Personal/单机 Docker：`gateway-go` 与 `transport-rust` 两个容器，共享 named volume `/var/run/huakai`，Rust 绑定 `unix:///var/run/huakai/transport.sock`，socket mode `0660`，同组访问。
- Go hot path dial UDS，connect timeout 50-100ms，使用健康缓存和 circuit breaker。
- 若 Owner 不批准 gRPC 依赖，则退而求其次：HTTP/2 over UDS，并强制定义 terminal trailer/error frame；但这是次优方案。

### 4.2 正常路径契约

Go 在调用 Rust 前必须已经完成：

1. client auth。
2. caller tenant/API key 限流。
3. model registry resolve。
4. route plan + attempt selection。
5. pool account selection。
6. credential resolve/hot refresh。
7. protocol translation。
8. application-layer cloaking。
9. retry budget/deadline 计算。

Go 发送给 Rust 的请求应是“已完全指定的上游请求”，建议 contract 形态：

```text
TransportRequest v1
  contract_version
  request_id
  attempt_seq
  tenant_id               // 仅用于指标/审计标签，可脱敏
  provider
  provider_account_id     // Rust 不据此取凭据，只用于池隔离/指标
  upstream_method
  upstream_scheme
  upstream_authority
  upstream_path_query
  upstream_headers        // 包含 Authorization，但必须日志脱敏
  body_stream
  response_mode           // buffered / sse / raw stream
  deadline_unix_ms
  connect_timeout_ms
  header_timeout_ms
  body_idle_timeout_ms
  mimicry_profile_ref
  mimicry_profile_params  // 只允许 profile 安全参数，不传 prompt
  proxy_ref
  required_features       // 如 h2_profile、boring_tls、sni_strategy
```

Rust 返回：

```text
TransportResponse v1
  response_headers
  body_stream
  terminal_state          // complete / upstream_error / transport_error / cancelled
  transport_error_class
  upstream_status
  bytes_from_upstream
  bytes_to_go
  timing_breakdown
  profile_applied
```

关键边界：

- Rust 不做账号选择。
- Rust 不读取数据库。
- Rust 不刷新凭据。
- Rust 不做最终 usage/billing 判断。
- Rust 不直接向 client 写响应。
- Go 必须能看到完整 response byte stream 或明确 terminal error。

### 4.3 伪装 profile 的逐请求传递

Go 传 `mimicry_profile_ref` 和必要的 profile 参数，不传完整策略源码或不受控 JSON。

建议 profile 分层：

- `app_mimicry_profile_id`：Go 已应用，仅用于审计关联。
- `transport_profile_id`：Rust 执行 TLS/H2/connection 行为。
- `required_features`：Go 表明本 attempt 是否必须使用特定传输能力。

如果 Rust 不支持某个 required feature：

- 若 Go policy 允许 fallback：回退 Go native transport 或低级 Rust profile，并记录 degradation。
- 若 profile mandatory：该 attempt fail-fast，进入 retry/failover；不能静默降级。

### 4.4 失败模式 ①：Rust 未启动 / 崩溃 / 不可达

**问题**

Go hot path 如果同步等待一个不可达 sidecar，会造成请求挂起和线程/连接耗尽。

**处理**

- Go sidecar client 维护 health cache + circuit breaker。
- dial/connect timeout 必须短，建议 50-100ms 起步，可配置。
- circuit open 时不要每个请求都尝试慢 dial，按 backoff 探测。
- policy 分两类：
  - `transport_sidecar_optional=true`：回退 Go native `transport.Factory` standard 路径。
  - `transport_sidecar_required=true`：快速返回 503/typed error，或尝试下一个不要求该 profile 的 account/pool。
- fallback/fail-fast 都必须写 audit/metrics，防止隐形降级。

**禁止**

- 禁止无限等待 Rust readiness。
- 禁止在 streaming 已开始后 fallback 到 Go transport。

### 4.5 失败模式 ②：Go / Rust 版本不一致

**问题**

sidecar 与 Go gateway 可能独立构建/部署，字段不匹配若直接 panic 会导致全链路故障。

**处理**

- contract 带 `contract_version`、`min_supported_version`、`capabilities`。
- Go 启动和周期性 health check 调 Rust `GetCapabilities`。
- optional field 未识别时忽略。
- required feature 未识别时 Rust 返回 `unsupported_contract`，Go 按 policy fallback 或 fail-fast。
- schema evolution 规则：
  - 新字段默认 optional。
  - required feature 必须显式列在 `required_features`。
  - 删除字段至少跨一个 minor release。

**测试**

- Go 新字段 + Rust 老版本。
- Go required feature + Rust 不支持。
- Rust 新 error class + Go 老版本，应映射为 `transport_unknown_retryable=false` 或保守不可重试。

### 4.6 失败模式 ③：流式中途失败

**问题**

Rust 流到一半崩溃或上游中断，如果 Go 已给 client 200 但最终静默 EOF，会造成“截断但 200”，直接影响 usage 和计费正确性。

**处理**

- Rust stream 必须有 terminal state。
  - 正常结束：`terminal_state=complete`。
  - 上游/transport 错误：返回 gRPC status 或 terminal frame/trailer。
- Go 不能把 “body EOF 但无 complete terminal” 当成功。
- Go 只有在 Rust 返回上游 headers 后才向 client 写 headers。
- 若失败发生在 client headers 写出前：Go 可以按 retry policy 换 attempt。
- 若失败发生在 headers/body 已写出后：
  - 不 retry。
  - 尽可能向 SSE client 发送 error event 或关闭并记录 typed stream error。
  - settlement 进入 ambiguous/partial，不做完整成功结算。
  - audit 记录 `stream_terminal_missing` 或具体 class。

**测试**

- Rust 在返回 headers 前断开。
- Rust 在 body 中间断开。
- Rust 正常 complete。
- Go client 慢读时 Rust 被 kill。

### 4.7 失败模式 ④：背压

**问题**

客户端慢读时，如果 Go/Rust 之间或 Rust/上游之间无限 buffer，会导致内存膨胀和 usage/latency 失真。

**处理**

- gRPC streaming 使用 HTTP/2 flow control 和 bounded message size。
- Rust relay 内部 channel 深度保持小值，建议 1-16，不允许无界队列。
- Go 从 Rust 读取的节奏必须绑定到向 client 写出的节奏。
- Rust 从 upstream 读取的节奏必须绑定到向 Go 发送成功的节奏。
- 所有 buffer 大小成为 config + metrics。

**测试**

- client 每秒只读少量字节，观察 Rust/Go 内存稳定。
- 上游高速 SSE，client 慢读，验证 backpressure 和 idle timeout 不误杀。

### 4.8 失败模式 ⑤：超时归属

**问题**

Go 和 Rust 都设 timeout，若归属不清会出现双重超时、错误分类混乱，或完全无超时。

**处理**

- Go 拥有 overall request deadline 和 retry budget。
- Go 为每个 attempt 计算 `attempt_deadline` 并传给 Rust。
- Rust 拥有 transport sub-timeout：
  - connect timeout。
  - TLS handshake timeout。
  - upstream header timeout。
  - body idle timeout。
  - downstream-to-Go write timeout。
- Rust 所有 timeout 必须被 Go deadline 截断，不能超过 attempt deadline。
- 错误优先级：
  - Go context canceled/deadline exceeded：`go_deadline_exceeded`。
  - Rust 子超时：`rust_timeout_connect/header/body_idle/write_idle`。
  - 上游 HTTP timeout/5xx：独立分类。

**测试**

- Go deadline 先到。
- Rust connect timeout 先到。
- Rust body idle timeout 先到。
- retry budget 不因 Rust 内部 retry 被偷偷消耗；第一版 Rust 不做上游重试。

### 4.9 失败模式 ⑥：错误分类

**问题**

Go 的 retry/failover 必须知道 Rust 错误是否可重试、是否应换号、是否应刷新 token。

**处理**

定义 Rust 到 Go 的错误 taxonomy：

| Rust error class | Go mapping | retry/failover 建议 |
|---|---|---|
| `dns_error` | transport connect failure | 可换 attempt，需健康降权。 |
| `connect_timeout` | transport timeout | 可换 attempt。 |
| `tls_handshake_failed` | transport TLS failure | profile/account/proxy 相关；可换 attempt，但要降权 profile/proxy。 |
| `upstream_header_timeout` | upstream timeout | 可重试，受幂等/未交付限制。 |
| `upstream_body_idle_timeout` | stream incomplete | 若未交付可重试；已交付则 ambiguous，不重试。 |
| `upstream_429` | rate/cooldown | 记录 cooldown，换 account/pool。 |
| `upstream_5xx` | upstream retryable | 可按 policy retry。 |
| `upstream_401_403` | auth/credential error | 先触发 hot refresh 或 account disable policy；不盲目多池打满。 |
| `profile_unsupported` | contract/profile error | fallback 或 fail-fast，通常不应重试同 sidecar。 |
| `sidecar_overloaded` | local overload | 可 fallback Go native 或快速 503；不要扩大到更多 Rust attempts。 |
| `contract_mismatch` | version error | fallback/fail-fast，不重试同 version。 |

Go 对未知 error class 必须保守：默认不可重试，除非 Rust 明确标记 `retry_hint=retryable_pre_delivery`。

### 4.10 失败模式 ⑦：取消传播

**问题**

client 断连后，如果 Go 不取消 Rust，Rust 会继续占用上游连接、凭据并产生潜在费用。

**处理**

- client request context 是根 cancel source。
- Go 检测 client disconnect 后取消 attempt context。
- gRPC cancel 传播到 Rust。
- Rust abort upstream request，释放 in-flight、connection lease 和 body reader。
- Rust metrics 记录 `cancelled_by_go`，但不作为 billable success。

**测试**

- client 在上游首 token 前断开。
- client 在 stream 中间断开。
- Go timeout 取消 Rust。
- Rust 确认 upstream body reader 被 drop。

### 4.11 失败模式 ⑧：计费-传输原子性

**决策建议**

**Go 仍是 usage 抽取和 billing source of truth。Rust 只传字节和 typed transport metadata，不做权威 usage 上报。**

原因：

- Go 已拥有 account/API brain、claim、settlement、protocol translation。
- Rust 若独立抽 usage，会产生双 truth，需要解决签名、重放、丢报、进程崩溃等更多问题。
- 方向 1 的目标是让 Rust 退到 transport，不应把 billing truth 再搬过去。

**契约要求**

- Rust response 必须流回 Go，而不是直接到 client。
- Go 必须看到完整上游 response bytes 或明确 terminal error。
- non-streaming：Go 从 Rust 收完整 body 后走 provider response translator 和 usage extraction。
- streaming：Go 从 Rust stream 中边转发边抽 usage；若 terminal 不完整，settlement 不能标完整成功。
- Rust 可以提供 advisory metrics，如 bytes/timing/profile，但不能作为收费依据。

**禁止**

- 禁止 Rust attempt_reporter 成为账务依据。
- 禁止 Rust 直接向 client relay 导致 Go 看不到完整 response。
- 禁止无 terminal 的 EOF 被 Go 视为 success。

### 4.12 失败模式 ⑨：进程生命周期与 Docker 部署

**推荐 Docker 拓扑**

第一版推荐：**两个容器，同一 Docker Compose project，同机 UDS。**

```text
huakai-gateway-go
  - mounts: huakai-run:/var/run/huakai
  - env: HUAKAI_TRANSPORT_SIDECAR=unix:///var/run/huakai/transport.sock
  - depends_on transport health, but runtime still probes

huakai-transport-rust
  - mounts: huakai-run:/var/run/huakai
  - binds: /var/run/huakai/transport.sock
  - healthcheck: sidecar health command or localhost internal health endpoint
  - restart: unless-stopped
```

**为什么不是同容器**

- 同容器会模糊 supervisor、health、restart、日志和 resource limit。
- 两容器更符合 sidecar 角色，便于单独限 CPU/mem/FD。

**为什么不是跨主机**

- 跨主机需要 mTLS、凭据传输策略、网络 ACL、服务发现和更复杂的故障域。
- 方向 1 第一版先把 contract 做稳，不先扩大部署面。

**监督与重启**

- Docker 负责 Rust 进程重启。
- Go 不直接 fork/exec Rust。
- Go 运行时健康探测 sidecar。
- Rust 重启期间：
  - optional mode：Go fallback native transport。
  - required mode：Go fail-fast，不挂起。

## 5. 已做 Rust C 档工作的收敛

### 5.1 Wave A 性能 SSE

**复用**

- Rust relay 的低分配 stream 处理、frame cap、SSE 探测可继续用于 sidecar 内部流稳定性和 metrics。

**重新定位**

- Rust 不再作为 usage source of truth。
- SSE usage 抽取最终仍在 Go；Rust 可做 terminal/event health 辅助指标。

**不再需要/降级**

- 如果某些 SSE parser 只服务 Rust 自己计费或 attempt reporting，应降级为 diagnostics。

### 5.2 P4 过载卸载

**直接复用**

- `max_in_flight_requests`、connection limit、overload guard、503 typed overload 等能力非常适合 sidecar。

**重新定位**

- Rust 过载只说明 transport sidecar 忙，不代表 tenant/API key 超限。
- Go caller limiter 和 Rust overload 是两层保护：
  - Go 保护平台和租户公平。
  - Rust 保护 transport 进程资源。

**默认**

- 保持之前 Owner 决策：默认限制为 0/disabled 时只记 metrics，不突然改变生产行为；sidecar required mode 上线前再按容量压测开启。

### 5.3 P6 超时 + 自研 serve loop

**直接复用**

- header read timeout、body idle timeout、自研 serve loop、HTTP2 keepalive 等保留为 sidecar ingress 和 upstream IO 保护。

**重新定位**

- P6 的 timeout 不再拥有整体请求生命周期；Go 拥有 overall deadline。
- Rust timeout 只负责 transport 子阶段，并返回 typed error。

### 5.4 L1/L2 mimicry

**升为 Rust 主价值**

- 方向 1 下，Rust 的存在理由主要是强传输伪装和高性能 relay。
- L1/L2 profile、BoringTLS/H2 行为、proxy 绑定、连接池隔离应成为 sidecar P0/P1 后的主线。

**上线条件**

- profile unsupported 必须 fail-closed 或显式 fallback。
- logs/metrics 不得泄露 Authorization。
- real upstream smoke 与 fingerprint gate 必须作为 release gate。

### 5.5 Docker Wave D

**提前**

- Owner 明确会用 Docker，因此 Docker 工作不应等最后。
- 至少在 Go-Rust contract 最小闭环时同步做 Compose 拓扑、UDS volume、healthcheck、restart policy。

## 6. 总体阶段计划

### Phase 0：合成计划与契约决策（2-3 天）

**目标**

把本稿和 Claude 独立稿交叉对比，形成无后缀 authoritative plan。

**工作**

- 对比 agreements/conflicts/gaps。
- Owner 拍板：
  - gRPC over UDS 是否采用。
  - Rust unavailable 默认 fallback 还是 required fail-fast。
  - Go 是否保持 billing source of truth。
  - Docker 是否采用双容器 + shared UDS volume。
  - caller limit 阶段 A 是否先走无 schema 内存版。

**产物**

- `docs/process/plans/2026-05-21-direction-1.md`
- `docs/specs/...` 下的 Go-Rust transport contract 草案，具体路径由 Owner/PM 定。

### Phase 1：Go 大脑 P0-A，retry/failover + cross-pool（5-8 天）

**依赖**

- Owner 确认 billing/claim 改动范围。
- 明确 retry taxonomy。

**工作**

- router 生成多 attempt。
- handler 抽 attempt executor。
- `AttemptSeq` 去硬编码。
- pre-delivery retry/failover。
- post-delivery no retry + ambiguous settlement。
- tests 覆盖多账号、多池、流式中断、billing once。

**完成标准**

- 一个 request 可在未交付前从账号 A failover 到账号 B。
- 不会双结算。
- streaming 中途失败不伪装成成功 200。

### Phase 2：Go 大脑 P0-B/P0-C，Anthropic buffered + OAuth hot refresh（4-6 天）

**工作**

- 实现 Anthropic provider buffered response -> canonical。
- 移除 501 stub。
- 增加 hot-path credential refresh wrapper。
- 401/expired token 与 retry executor 联动。

**完成标准**

- Anthropic non-streaming 正常返回。
- expired OAuth token 可单请求刷新后继续。
- refresh storm 被限制。

### Phase 3：Go P1，应用层 cloaking + caller limiter（4-7 天）

**工作**

- 接入 `ApplyMimicryPlan`。
- per-tenant/provider feature flag。
- caller-side in-memory QPS/concurrency limiter。
- 审计摘要和 metrics。

**完成标准**

- cloaking 默认 disabled 不改变行为。
- 开启后只改 provider-bound body。
- caller limiter 对 tenant/api key 生效，streaming cancel 释放 concurrency。

### Phase 4：Rust sidecar 最小 transport API（5-7 天）

**依赖**

- Phase 0 选定 contract。

**工作**

- 新 sidecar API：`Forward` / `GetCapabilities` / `Health`。
- UDS bind、权限、contract version。
- `proxy_engine.forward_endpoint` 或等价路径接 fully specified request。
- public listener route 默认禁用/legacy。
- account_planner/route_client/attempt_reporter 不参与默认运行。

**完成标准**

- Go 或测试 client 可通过 UDS 调 Rust 访问 mock upstream。
- Rust 返回 status/header/body/terminal/error class。
- Rust 无需 Go RouteService。

### Phase 5：Go-Rust 集成与失败模式 gates（6-9 天）

**工作**

- Go `TransportClient` 接 Rust sidecar。
- sidecar health cache/circuit breaker。
- fallback/fail-fast policy。
- streaming backpressure/cancel/deadline/error taxonomy tests。
- billing-through-Go 验证。

**完成标准**

- 9 类失败模式都有自动化测试或手工 gate 记录。
- Rust 崩溃不会挂住 Go。
- mid-stream Rust failure 不被结算为完整成功。

### Phase 6：Docker 化与运维 runbook（2-4 天）

**工作**

- Docker Compose 双容器拓扑。
- UDS named volume。
- healthcheck/restart policy。
- metrics/log redaction。
- rollout flag 文档。

**完成标准**

- Owner 可用 Docker 启动 Go + Rust sidecar。
- Rust stop/restart 行为符合 optional/required policy。

### Phase 7：强伪装与性能 hardening（6-10 天，持续）

**工作**

- L1/L2 profile hardening。
- BoringTLS/H2 profile gates。
- real upstream smoke。
- latency/memory/FD/connection pool 压测。

**完成标准**

- mimicry profile 有明确 supported/unsupported 行为。
- 性能不回退 Go native baseline 的核心路径。
- fallback/degradation 可观测。

## 7. 依赖关系

```text
Phase 0 contract decisions
  -> Phase 1 Go retry/cross-pool
      -> Phase 2 hot refresh uses retry taxonomy
      -> Phase 5 sidecar errors feed retry taxonomy
  -> Phase 4 Rust sidecar API
      -> Phase 5 Go-Rust integration
          -> Phase 6 Docker runbook
              -> Phase 7 hardening/canary

Phase 3 cloaking/caller limiter can run after Phase 1 executor shape is stable.
```

优先先做 Go 大脑 P0，而不是先扩 Rust：

- 因为 Rust sidecar 只能执行 Go 已经选好的账号和凭据。
- 如果 Go 仍然单 attempt、无 hot refresh、无 response translation，Rust 接入只会把缺口外包到另一个进程。
- Go retry/error taxonomy 也是 Rust contract 的消费方，必须先成形。

## 8. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| retry/failover 导致双计费或错计费 | 高 | Go 保持 billing source of truth；pre-delivery 才 retry；post-delivery ambiguous；测试 billing once。 |
| Rust mid-stream failure 变成截断 200 | 高 | terminal state 必须存在；Go 无 complete terminal 不可成功结算。 |
| 凭据穿过 Go-Rust 边界泄露 | 高 | 第一版只允许 UDS 同机；socket 权限；日志强脱敏；跨主机必须 mTLS，后置。 |
| 版本不一致 crash | 中高 | contract version + capabilities + required_features；unknown required fail-fast/fallback。 |
| fallback 静默降低伪装能力 | 中高 | degradation audit/metrics；mandatory profile 不允许静默 fallback。 |
| caller limiter 内存版被误认为分布式强限制 | 中 | 文档和 UI 明确“single-process”；分布式版另开 Owner 决策。 |
| Rust legacy CP 代码继续被当主线 | 中 | 默认 binary/mode 禁用 account planner/route client；README/READINESS 更新角色。 |
| Docker UDS 权限导致启动失败 | 中 | shared volume、固定 UID/GID 或 group、启动前探测、clear error。 |
| gRPC 新依赖增加 license/运维面 | 中 | 走 dependency-license-auditor；Owner 批准后落地；否则 HTTP/2 over UDS 备选。 |

## 9. 需要 Owner 拍板的决策点

1. **Go-Rust 契约传输**：是否批准 `gRPC over UDS` 作为生产默认？
2. **新 runtime dependency**：是否允许 Go/Rust 引入或强化 gRPC/protobuf 依赖？
3. **Rust 不可用策略**：默认 optional fallback 到 Go native standard transport，还是 required fail-fast？
4. **profile mandatory 语义**：哪些 tenant/provider/profile 不允许 fallback？
5. **凭据边界**：是否接受 Go 将已解析 Authorization header 通过本机 UDS 传给 Rust？若不接受，需要改成 Rust 持有短期 credential handle，但这会扩大 Rust 职责。
6. **billing truth**：是否确认 Go 是唯一 usage/billing source of truth，Rust 只提供 advisory metrics？
7. **caller limiter 路线**：是否先做无 schema 内存版，还是直接批准 DB/Redis 级分布式策略？
8. **Docker 拓扑**：是否确认双容器 + shared UDS volume，而不是 Go fork Rust 子进程或跨主机 sidecar？
9. **高风险实现确认**：retry/failover 涉及 billing/quota/claim，hot refresh 涉及 credential/auth，任何 schema/deployment 脚本改动都需单独 Owner 确认。

## 10. 建议的第一批执行任务

Owner 批准合成计划后，建议第一批任务这样切：

1. 写 Go-Rust transport contract spec。
   - 先写 proto/HTTP contract 文档，不实现。
   - 明确 terminal state、error taxonomy、versioning、deadline/cancel。
2. Go retry/failover executor skeleton。
   - 抽 attempt executor。
   - 去掉 `AttemptSeq: 1` 热路径硬编码。
   - tests 先覆盖 pre-delivery retry 和 post-delivery no retry。
3. Router 多候选 planner。
   - 从 registry pool candidates 生成 attempts。
   - ranking 初版保守，先不引入复杂成本模型。
4. Anthropic buffered response translator。
   - 独立 feature gap，风险低于 billing/auth。
5. Rust sidecar API spike。
   - 只接 mock upstream/explicit upstream，不接账号规划。
   - 证明 UDS streaming terminal semantics。

这样排的原因：先让 Go 大脑具备 retry/cross-pool 基础，再让 Rust 成为可插拔 transport；否则 Rust contract 没有稳定的错误消费方。

## 11. 功能 preservation 与 clean-room 状态

- 没有删除功能。Rust 被退役的 CP/entry 职责不是删除账号转 API 能力，而是把能力回收到 Go 大脑并把 Rust 标记为 transport sidecar。
- 参考项目只作为历史内部文档中的证据来源；本轮没有读取外部参考项目源码，也没有引入非 MIT 实现细节。
- 若后续需要再次对参考项目做机制比较，必须按 clean-room lane guard 和 source-must-read 规则另开 specifier/reviewer lane。

## 12. Owner 中文摘要

1. 做了什么：起草了方向 1 的 Codex 独立执行计划，明确 Go 做账号转 API 大脑，Rust 收敛为高性能/强伪装出站传输 sidecar。
2. 改了哪些文件：仅新增本计划文件 `docs/process/plans/2026-05-21-direction-1-codex.md`。
3. 为什么这样做：gap 分析显示 Go 是当前生产主链路，Rust 依赖不存在的控制面；把 Rust 继续推成完整数据面会扩大风险，方向 1 更符合现状和 Owner 战略。
4. 有没有功能缩水：没有。Rust 退役的账号规划/入口角色由 Go 接管；高风险能力改为 Safe Equivalent/sidecar/feature flag，不静默删除。
5. 有没有 clean-room 风险：本轮只读 HUAKAI 内部文档和本地代码，未读外部参考项目源码，未复制参考实现。
6. 有没有安全风险：有，需要在执行阶段重点处理凭据穿过 UDS、fallback 静默降级、mid-stream 截断、billing 原子性、caller limit 分布式一致性。
7. 哪些地方需要 Owner 确认：gRPC over UDS、新依赖、Rust 不可用 fallback 策略、profile mandatory 语义、凭据是否可通过本机 UDS、Go 是否唯一 billing truth、caller limiter 是否先内存版、Docker 双容器拓扑。
8. 下一步建议：与 Claude 独立稿做 agreements/conflicts/gaps 对比，形成无后缀合成计划；先落 Go-Rust contract spec 和 Go retry/cross-pool P0。

Agent: Codex (GPT-5), independent plan lane
UTC timestamp: 2026-05-21T06:26:27Z
