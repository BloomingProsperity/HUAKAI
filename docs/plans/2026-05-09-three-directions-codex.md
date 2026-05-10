# 三方向差异化评估 — Codex 独立草案

**日期**: 2026-05-09  
**作者**: Codex (xhigh + fast_mode)  
**对应 Claude 草案**: `docs/plans/2026-05-09-three-directions-claude.md`（写作时未打开、未读取）  
**触发**: Owner 提出三方向  
**评估边界**: 只读 HUAKAI 本仓库、官方 provider 文档、官方 agent-framework 文档；未读 sub2api / one-api / LiteLLM / Portkey 等非 MIT 源码。  
**Truth-first 元数据**: Observed regions: 34；Inferences: 18；Open questions: 9。

## TL;DR（每方向一句 verdict）

1. **方向 1 Account Cache Fabric: 暂停验证。** 物理上只在 provider 官方 cache scope 内成立（Anthropic workspace/org、OpenAI org、Gemini project/resource），跨 org 不成立；经济上“静默预热”不是天然省钱，因为预热本身要付一次 prefix 写入/输入成本，必须先用真实账号做同 scope/cross scope smoke test 和 ROI 门槛。
2. **方向 2 Multi-Account Request Decomposition: 否决放入 gateway hot path。** 这是 agent runtime / 用户应用层问题，不是透明网关路由问题；塞进 `/v1/chat/completions` 会破坏当前 1 request -> 1 claim -> 1 upstream stream -> 1 settlement 的账务、幂等、SSE、重试契约。
3. **方向 3 Predictive Session Migration: 暂停验证。** “会话迁移/缓存桥接”是对的产品问题，但 5 分钟默认 TTL 下 EWMA 很难可靠预测离散的 429/403/auth/ban 事件；应先并入 F-SESSION-001 的显式 sticky migration/cache-bridge 方案，等健康遥测足够后再做主动预热。

## 整体框架评估

Owner 的一句话“PASR 不是差异化，而是差异化地基”方向正确，但三方向里只有 **cache-aware substrate** 是稳固基础。主动编排要分三层：

- **Cache-aware routing substrate**: PASR segment table、prefix hash、per-account cache telemetry、TTL/scope 模型。这个必须推进。
- **Cache orchestration**: 预热、迁移、keepalive。这个只在 provider scope 和 ROI 通过后推进。
- **Semantic orchestration**: 拆任务、合并结果、多 agent。这个不应伪装成透明 gateway 行为，应作为显式 agent runtime 或应用层 SDK。

当前 HUAKAI 代码已经有 PASR/cache 基座，但还没有达到主动编排需要的可靠性：OpenAI 只有 read signal、Gemini 没喂 PASR、0/0 miss demote 分支目前被 `ObserveByAccountWithPrefix` 提前返回挡住，billing/handler 也还是单 claim 单 attempt 路径。

## 官方 cache scope 事实

### Anthropic / Claude API

官方 Prompt caching 文档的当前关键事实：

- **TTL 与写/读成本**: 默认 5 分钟；1 小时 TTL 额外成本；5m write = base input 1.25x，1h write = base input 2x，cache read = base input 0.1x。证据：[Anthropic Prompt caching - Pricing / How prompt caching works](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) lines 211-213, 240-245；1h TTL 见 lines 578-622。
- **scope**: 2026-02-05 起 Claude API 与 Azure AI Foundry preview 使用 **workspace-level isolation**；Amazon Bedrock 与 Google Vertex AI 仍维持 **organization-level cache isolation**。不同 organization 永不共享 cache。证据：[Anthropic Prompt caching - Cache storage and sharing](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) lines 531-535。
- **exact match**: 命中要求 cache-control block 之前的 prompt segment 100% 相同。证据同上 lines 536-538。
- **observable fields**: `cache_creation_input_tokens` 与 `cache_read_input_tokens` 在 usage/message_start 中暴露。证据：[Anthropic Prompt caching - Tracking cache performance](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) lines 465-483。

结论：Anthropic 不是 per-API-key cache。Claude API/Azure 现在应按 workspace 边界看；Bedrock/Vertex 按 organization 边界看。**跨 org 不可行；同 org 但不同 workspace 不可行；同 workspace 下不同 API key 理论可行但必须真实 smoke test。** 如果 Owner 说的“两个 Pro 账号”指消费级 Claude/ChatGPT Pro 账号，官方 API 文档不能证明其 cache 可复制。

### OpenAI API

官方 Prompt caching 文档的关键事实：

- **自动启用**: 1024 tokens 以上自动启用，无需代码变更；降低 latency 与 input token cost；无额外 caching fee。证据：[OpenAI Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) lines 667-668, 677-685。
- **routing / key**: request 会按 prefix hash 路由到近期处理过同 prefix 的机器；`prompt_cache_key` 会参与 routing；同 prefix+key 约 15 rpm 后可能 overflow 到其它机器，降低 cache 效果。证据：同页 lines 679-685。
- **retention**: in-memory 通常 5-10 分钟 inactivity，最多 1 小时；extended retention 可到 24h，按请求参数控制。证据：同页 lines 686-718。
- **scope**: FAQ 明确 prompt caches 不在 organization 之间共享，只有同 organization 成员能访问 identical prompt caches。证据：同页 lines 771-775。
- **observable fields**: `usage.prompt_tokens_details.cached_tokens` 暴露 hit tokens。证据：同页 lines 730-760。

结论：OpenAI 官方边界是 **organization**，不是 per-key。**跨 org 不可行；同 org 不同 key 理论可行；同 org 不同 project 是否存在更细隔离，文档未明确，需要 smoke test。**

### Gemini / Google

官方 Gemini API / Vertex AI cache 文档的关键事实：

- **Gemini API 有 implicit 与 explicit 两种 cache**；implicit 在 Gemini 2.5+ 默认启用，explicit 可手动创建 cache 并在后续请求通过 `cached_content` 引用。证据：[Gemini Context caching](https://ai.google.dev/gemini-api/docs/caching) lines 196-220。
- **explicit TTL**: explicit cache 默认 TTL 1 小时，可设 `ttl`，示例里 300s；可 update `ttl` 或 `expire_time`。证据：同页 lines 217-220, 256-267；API reference update TTL 见 [Gemini Caching API](https://ai.google.dev/api/caching) lines 820-826, 845-864。
- **cost model**: explicit caching 的成本由 cached token count 与 TTL storage duration 等决定；无 minimum/maximum TTL bounds（Gemini API docs 说法）。证据：[Gemini Context caching - explicit costs](https://ai.google.dev/gemini-api/docs/caching/) lines 720-733。
- **resource scope**: Google AI Gemini API 使用 `cachedContents/{id}` 资源名；Vertex AI 形态明确是 `projects/{PROJECT_NUMBER}/locations/{LOCATION}/cachedContents/{CACHE_ID}`，并可列出某 Google Cloud project 关联的 context caches。证据：[Gemini API Caching reference](https://ai.google.dev/api/caching) lines 974-982；[Vertex AI context cache use](https://cloud.google.com/vertex-ai/generative-ai/docs/context-cache/context-cache-use) lines 681-690；[Vertex AI get cache info](https://cloud.google.com/vertex-ai/generative-ai/docs/context-cache/context-cache-getinfo) lines 681-686, 953-955。

结论：Gemini explicit cache 不是“隐式跨账号复制”，而是 **命名 CachedContent resource**。Vertex 下 scope 至少是 project+location；Google AI API key 形态的跨 key 共享未在我读到的官方 docs 中明确。HUAKAI 不能假设跨 org/project/key 共享，必须用同 project/不同 key、不同 project 的真实调用验证。

## 方向 1: Account Cache Fabric

### 物理可行性

**Verdict: 暂停验证。**

方向 1 的核心物理前提“账号 A 有 P cache，可以让账号 B 也持有”只在以下条件成立：

- B 与 A 位于同一个 provider cache scope 内：Anthropic 同 workspace 或 Bedrock/Vertex 同 org；OpenAI 同 org；Gemini 同 project/location/resource access。
- P 的 prefix 完全一致，且 cacheable token 数达到模型阈值。Anthropic 要求 exact match 和最低 tokens；OpenAI 也要求 exact prefix match。证据：Anthropic docs lines 409-420、531-536；OpenAI docs lines 672-685, 730-734。
- 预热请求确实把 cache 写到 B 将来会命中的物理/逻辑位置。OpenAI docs 说明 request 是按 prefix hash 路由到机器，`prompt_cache_key` 只能影响 routing，不等于“指定账号 B 的 cache cell”；高频 overflow 还会降低效果（OpenAI docs lines 679-682）。这使“账号 B 持有 cache”的表述在 OpenAI 上不严谨：可观察的是 B 后续请求的 `cached_tokens`，不是可枚举的 cache ownership。

**跨 org 不可行。** Anthropic 与 OpenAI 官方都明确不同 organization 不共享 cache。Gemini/Vertex resource name 带 project/location，也不支持跨 project 自然共享。

**同 org 不同 key可行性**:

- Anthropic: 同 workspace 可能可行；同 org 不同 workspace 不可行；Bedrock/Vertex org-level 另算。
- OpenAI: 同 organization 理论可行；project 细粒度边界未知。
- Gemini: Vertex 同 project/location 且有 IAM 权限可引用；Google AI API key 间共享未知。

### HUAKAI 代码现状对接

当前 PASR/cache substrate 有基础，但不是 Fabric-ready：

- `HasCacheBitmap` 的语义是 “Members[i] 见过 `cache_creation_input_tokens > 0`”，不是 read-hit。证据：`backend/internal/pool/prefix_segment.go:52-53`, `backend/internal/pool/prefix_segment.go:67-69`。
- `MarkCacheSeen` 只设置 bitmap；调用条件在 PASR feedback 中是 `obs.CacheCreation > 0`。证据：`backend/internal/pool/prefix_segment.go:176-190`, `backend/internal/pool/pasr_feedback.go:80-96`。
- `CacheRead > 0` 只刷新 `LastReadAt` 与 reset miss，不会设置 HasCache bit。证据：`backend/internal/pool/pasr_feedback.go:92-96`。这对 OpenAI 是明显 gap，因为 OpenAI 只暴露 read hit，没有 creation signal。
- 设计上有 miss demote：`cache_creation==0 && cache_read==0` 累计 miss，达到阈值 demote。证据：`backend/internal/pool/pasr_feedback.go:97-105`。但 `ObserveByAccountWithPrefix` 在 0/0 时直接 return，不通知 observers。证据：`backend/internal/cachemetrics/cachemetrics.go:226-240`。所以当前 miss demote 可能是死路径。
- OpenAI SSE 已解析 `usage.prompt_tokens_details.cached_tokens` 并映射到 `CacheReadInputTokens`，终态调用 `ObserveByAccountWithPrefix(0, read, tenant, account, prefix)`。证据：`backend/internal/proto/openai_sse.go:95-108`, `backend/internal/proto/openai_sse.go:411-414`, `backend/internal/proto/openai_sse.go:432-440`。
- Gemini SSE 已解析 `cachedContentTokenCount` 并保存 `CachedContentTokens`，但 `canonical()` 未映射到 `CacheReadInputTokens`，也没有 `cachemetrics.ObserveByAccountWithPrefix`；注释明确是 future。证据：`backend/internal/proto/gemini_sse.go:46-50`, `backend/internal/proto/gemini_sse.go:90-95`, `backend/internal/proto/gemini_sse.go:319-325`, `backend/internal/proto/gemini_sse.go:328-334`。
- Anthropic SSE 已把 creation/read 喂给 cachemetrics/PASR。证据：`backend/internal/proto/anthropic_sse.go:156-177`, `backend/internal/proto/anthropic_sse.go:184-201`。

### 经济模型

关键结论：**预热不是省钱动作，默认是买 latency/capacity/reliability 的动作。**

以 prefix token 数 `P`、base input 单价 `B`、未来同 TTL 内在 B 账号上的真实请求数 `N` 计：

- Anthropic 5m 无预热：第一条真实请求写 cache `1.25 * B * P`，后续 `N-1` 条 read `0.1 * B * P`。
- Anthropic 5m 有预热：预热写 cache `1.25 * B * P`，后续 `N` 条真实请求 read `0.1 * B * P`，另加预热输出/请求开销。
- 二者相差约 `0.1 * B * P + prewarm_output`，即有预热更贵；1h TTL 下 write 是 `2 * B * P`，同样不因 `max_tokens=1` 消失。
- OpenAI 类似：写 cache 无额外 fee，但预热仍要付一次 full input processing；它把 first miss 从用户请求移到后台，经济上仍多出一次后续 cached-read 等价成本与 output。
- Gemini explicit cache 还要考虑 storage TTL 成本，false warm 成本更高。

方向 1 只有在这些目标足够值钱时成立：降低用户首 token latency、避免热点账号单点、提高 rate-limit 利用、让 B 在 A 降级前可接力。它不是“低成本复制 cache”。

假阳性率边际影响：

`extra_cost ≈ false_warm_count * C_warm + true_warm_count * C_extra_read - latency_value - avoided_incident_value`

其中 `C_warm` 对 Anthropic 是 1.25x/2x prefix input，对 OpenAI 是 full prefix input，对 Gemini 是 create+storage。若没有 per-prefix 复用概率预测，false positive 会直接烧钱。

### Verdict

**暂停验证。** 建议只推进“cache-aware layer”和“被动 PASR 修补”，不直接做后台静默预热产品化。

推进前必须完成：

1. **真实 smoke test**（需 Owner 提供测试账号并确认可用预算）：Anthropic 同 workspace 两 key、不同 workspace；OpenAI 同 org 两 key、不同 org；Gemini same project/different project。验证指标是 B 的 `cache_read_input_tokens` / `cached_tokens` / `cachedContentTokenCount`。
2. **代码修补**：OpenAI read hit 应能标记 account has-cache 或新增 vendor-specific bitmap 语义；Gemini read signal 接入 PASR；修复或删除 0/0 miss dead path；segment 增加 provider/cache-scope/TTL 维度。
3. **预算闸门**：每 tenant/pool/prefix 的 warm budget、expected reuse threshold、false-positive kill switch、warm job 审计记录。没有这个，Fabric 会成为隐形成本放大器。

## 方向 2: Multi-Account Request Decomposition

### 物理可行性

LLM 请求当然可以被拆成多个子任务再合并，但这不是 provider prompt cache 的物理问题，而是应用语义与 agent runtime 问题。CSV 分析、长文翻译、图表生成这类任务需要理解用户意图、文件语义、输出结构、错误恢复策略。透明 gateway 缺少足够上下文判断“拆了以后是否等价”。

### HUAKAI 代码现状对接

当前 gateway handler 是单请求线性路径：

- 只支持 streaming；非 streaming 直接返回 Phase E scope。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:122-125`。
- 解析 body 后 resolve model、router plan、reserve 一个 billing claim。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:117-168`, `backend/internal/gatewayhttp/chat_completions_handler.go:180-200`。
- 一个 prompt hash 进入 selector，`AttemptSeq: 1`，选一个 upstream account。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:216-236`。
- 一个 upstream stream 进入 forwarder，最后一个 settle request。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:312-350`。
- `rg` 在 gateway/router/billing 相关目录没有发现 decomposition / split / subrequest / fanout 语义逻辑；只有测试注释命中。证据：`rg -n "decompos|decomposition|split|subrequest|sub-request|workflow|agent|semantic|template|fanout|fan-out" backend/internal/gatewayhttp backend/internal/gateway backend/internal/router backend/internal/billing` 仅返回 `backend/internal/gateway/cache_control_apply_test.go:30`。

账务/幂等目前也是单 claim 主导：

- `ClaimGate.Reserve` 是 Tx1：相同 fingerprint committed 走 replay，reserving 走 race，否则插入一个 reserving claim。证据：`backend/internal/billing/claim_gate.go:63-100`, `backend/internal/billing/claim_gate.go:124-160`。
- schema 对 `billing_ledger_claims` 有 `(tenant_id, api_key_id, idempotency_key)` unique。证据：`backend/sql/migrations/0002_observability_billing.up.sql:19-56`。
- `Settler.Settle` 当前插入一个 `usage_records`，再插入一个 `billing_events`，再把 claim committed。证据：`backend/internal/billing/settler.go:78-128`, `backend/internal/billing/settler.go:161-178`。
- `usage_records` 有 `claim_id`，但 schema 只是 index，不表达一个 logical request 下多个 child attempts 的 DAG。证据：`backend/sql/migrations/0002_observability_billing.up.sql:121-188`。
- docs 已经意识到未来应有 `request_id -> claim_id(s) -> attempt_id(s) -> lease_id(s) -> usage_record(s)`，但 current schema/handler 还没落地 request_attempts。证据：`docs/specs/_invariants/cross-module-boundaries.md:85-93`, `docs/specs/_invariants/cross-module-boundaries.md:141-147`。

### Scope 与层级

**方向 2 不应该放 gateway 层。** 原因：

- **idempotency**: 当前 normalized payload hash 对应一个逻辑请求/claim。拆成多个 child requests 后，重放同一个 `Idempotency-Key` 时要 replay 哪些 child？部分 child 成功、部分失败如何去重？当前 `replay_without_cache` 还未实现 replay cache。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:210-213`。
- **billing**: child requests 可能命中不同账号、不同模型、不同价格；当前 settlement 是单 claim 单 actual_cost。方向 2 需要 per-child usage attribution、merge policy、partial failure billing policy。
- **SSE 流语义**: 多 upstream SSE 合并成一个 OpenAI-compatible stream，需要定义 event ordering、tool call id、usage delta、finish reason、error propagation。当前 streaming-forwarder 规范要求 stream end taxonomy 和 Tx2 finalization，透明 fanout 会引入新 end_class/partial semantics。证据：`docs/specs/streaming-forwarder.md:171`、`backend/internal/gatewayhttp/chat_completions_handler.go:312-350`。
- **错误重试**: child 2 失败时，是否取消 child 1/3？是否返回 partial？是否重新合并？这些是 workflow semantics，不是 gateway retry/failover。

官方 agent frameworks 已在做这个层级：

- LangGraph 把 agent workflows 建模成 graph，包含 State、Nodes、Edges，并支持并行 super-steps。证据：[LangGraph Graph API overview](https://docs.langchain.com/oss/python/langgraph/graph-api) lines 133-141。
- AutoGen 官方有 multi-agent conversation framework，并有 Task Decomposition 页面，示例用 planner agent 把复杂任务拆成 3-5 个 subtasks，也有 group chat 分工。证据：[AutoGen Multi-agent Conversation Framework](https://autogenhub.github.io/autogen/docs/Use-Cases/agent_chat/) lines 81-99；[AutoGen Task Decomposition](https://autogenhub.github.io/autogen/docs/topics/task_decomposition/) lines 79-84, 122-135, 707-710。
- Anthropic Agent SDK subagents 可隔离上下文、并行跑 focused subtasks。证据：[Claude Agent SDK Subagents](https://platform.claude.com/docs/en/agent-sdk/subagents) lines 181-186, 211-215。
- CrewAI 明确用 Flows 管 state/control，用 Crews 做复杂任务协作与 delegation。证据：[CrewAI Introduction](https://docs.crewai.com/en/introduction) lines 168-220, 242-248。

### 经济模型

方向 2 的经济模型不稳定：拆分可能减少单个子任务上下文，可能并行降低 latency，但也可能重复发送 shared context、增加 merge call、增加失败重试次数。只有在模板高度结构化、上下文切片天然独立、merge 可确定时才有正 ROI。CSV/长文翻译可以作为用户显式选择的 template workflow；不能作为透明 gateway 默认。

### Verdict

**否决放入 gateway hot path。** 不删除这个产品想法，但应改成：

- `HUAKAI Orchestration Runtime` 或 `workflow templates`，有显式 endpoint / SDK / UI，不伪装成 OpenAI-compatible chat completion。
- 每个 template 必须声明 input schema、child DAG、merge semantics、billing attribution、partial failure behavior、SSE/non-SSE 输出契约。
- Gateway core 只提供 account pool、quota、billing、observability、streaming primitives 给 runtime 调用。

## 方向 3: Predictive Session Migration

### 物理可行性

方向 3 的物理基础和方向 1 相同：只有在同 provider cache scope 内、同 prefix exact match、TTL 未过期时，提前把 session/context 预热到 B 才有意义。Anthropic 默认 5 分钟，1 小时需显式 TTL 且更贵；OpenAI in-memory 5-10 分钟 inactivity，extended 可到 24h；Gemini explicit TTL 默认 1h 可调。

### HUAKAI 代码现状对接

HUAKAI 已有与 session migration 相邻的规格，但实现还没到主动迁移：

- parity matrix 里 F-SESSION-001 已定义 sticky session 与 A04 sticky migration loss function，包含 cache_loss/load/cred/cooldown 等决策信号和迁移响应头。证据：`docs/03_FEATURE_PARITY_MATRIX.md:74`。
- rate-limiting spec 已有 A21 risk-weighted probe scheduler 和 A22 health hysteresis FSM，包含 health_score、cooldown、needs_refresh、manual recovery 等。证据：`docs/specs/rate-limiting.md:301-343`, `docs/specs/rate-limiting.md:353-410`。
- 当前 runtime segment table 有 LastReadAt/ExtendedCacheTTL/MissCount，但不是 per-session migration manifest。证据：`backend/internal/pool/prefix_segment.go:71-95`。
- current handler 只有 `SessionHash` 进入 selector/forwarder，没有 session DAG 或 cache bridge job。证据：`backend/internal/gatewayhttp/chat_completions_handler.go:216-236`, `backend/internal/gatewayhttp/chat_completions_handler.go:321-324`。

### 经济模型与 EWMA 预测

默认 TTL 5 分钟意味着预测窗口必须短。EWMA 对平滑 latency/TTFT 退化有用，但对离散事件（429 reset、403、OAuth 401、账号封禁、provider incident）预测力弱。HUAKAI 本地 spec 的 A22 health_score 本质也是反应式 + hysteresis：错误后降级，干净成功后恢复；这不是“未来 5 分钟内会慢”的强预测器。

无真实 telemetry 时，我不会给“EWMA 精度量级”编数字。可验证目标应是：

- 数据集：每 account 每 10s/30s 的 TTFT、inter-event gap、status class、rate-limit headers、health_state、cache hit/miss、in_flight、quota headroom。
- 标签：未来 5 分钟内是否进入 `cooling_down / needs_refresh / degraded with p95 TTFT > threshold`。
- 门槛：只有 top-risk bucket precision 足以覆盖 warm 成本才启用。例如假阳性成本是一次 full prefix warm，precision 必须让 avoided latency/incidents 的业务价值覆盖 false warms。

假阳性成本同方向 1。方向 3 更危险，因为它可能对长 session 的大 prefix 做 warm，P 更大；false positive 会更贵。

### Verdict

**暂停验证。** 建议把方向 3 降级成 F-SESSION-001 的显式 migration/cache-bridge 子能力：

- 先做 operator-visible migration manifest：为什么迁移、cache 是否丢失、预估成本差、是否使用 cache bridge。
- 只对已触发风险的 session 做 manual-first / feature-flag bridge，不做全局预测性预热。
- 复用 A21/A22 health FSM 的真实遥测后，再训练/评估 EWMA 或更简单的 threshold predictor。

## 真正的差异化空白（独立列举）

1. **Account Lifecycle Operations（优先级最高）**  
   HUAKAI 的产品身份是 relay-station/account hub，而不是 generic gateway。Project brief 明确产品是管理 providers/accounts/keys/quota/billing/routing/security/ops workflow，且核心是 multi-account quota pooling。证据：`docs/01_PROJECT_BRIEF.md:7`, `docs/01_PROJECT_BRIEF.md:46-64`。  
   更值得做的是账号注册/续费/封禁恢复/健康仪表盘/credential refresh storm 控制/风险探测。已有 F-AUTH-005、F-RATE-001、F-OPS-003 方向，但还应产品化成 operator day-2 workflow。

2. **多账号 quota 聚合 -> 用户视角“无限 Pro”**  
   Owner 商业模式需要 Personal Edition 能卖 API，且用户看不到背后账号池。证据：`docs/01_PROJECT_BRIEF.md:40`, `docs/01_PROJECT_BRIEF.md:77`; F-POOL-001 明确“operators pool multiple upstream subscription accounts into one logical capacity”。证据：`docs/03_FEATURE_PARITY_MATRIX.md:73`。  
   这比 cache fabric 更接近付费价值：用户买的是稳定容量，不是知道哪个 cache 命中。

3. **反 detection/身份一致性，但必须 opt-in + audit**  
   R-POOL-001 已承认 pooling 可能被 upstream detection/ban，缓解包括 fingerprint hygiene、sticky pinning、per-account concurrency、health probes。证据：`docs/10_RISK_REGISTER.md:24`。  
   已有 F-AUTH-005 的 Claude Code mimicry opt-in、legal_review_id 和 audit event。证据：`docs/specs/upstream-credential-management.md:26-39`, `docs/specs/upstream-credential-management.md:101-103`。  
   已有 F-CLIENT-IDENTITY-001 通过 identity_signal_config/sticky_cache 影响 sticky routing 和 prompt cache。证据：`docs/specs/client-identity.md:23-45`, `docs/specs/client-identity.md:102-116`。  
   建议把“强伪装层”定义为 **operator opt-in plugin + legal review + audit + kill switch**，不要默认启用。

4. **Provider breadth + protocol/cost correctness**  
   Project brief 和 DR-007 都把 provider/model breadth 作为 HUAKAI 超越 Sub2API 的主差异化。证据：`docs/01_PROJECT_BRIEF.md:18`, `docs/01_PROJECT_BRIEF.md:40-44`; `docs/decisions/DR-007-product-positioning-and-breadth.md:34-35`。  
   这比三方向更确定：更多 upstream、协议转换不丢语义、billing/usage 可解释，能直接支撑商业化。

5. **Cache-aware operations, not cache magic**  
   建议把 cache 做成 operator 能看懂的 ops capability：per account hit ratio、prefix hotness、TTL expiry、cache miss reason、warm ROI simulator、manual warm。当前 HUAKAI 已有 per-account cachemetrics 与 PASR segment table，证据：`backend/internal/cachemetrics/cachemetrics.go:110-159`, `backend/internal/cachemetrics/cachemetrics.go:162-240`。这比后台自动织网更可控。

## 风险与盲点（自评）

- 我没有使用真实 Anthropic/OpenAI/Gemini 账号做跨 key/cross workspace/cross org smoke test；“同 scope 可行”是从官方 scope 文档推断，不是实测。
- OpenAI 官方文档说 organization 级 cache access，但没有明确 project 是否再隔离；需要测试。
- Gemini Google AI API key 形态的 cache sharing 边界不如 Vertex project/location 文档清楚；需要测试。
- 我没有证明 sub2api/one-api/LiteLLM/Portkey “都没有碰这三方向”；按任务纪律我未读其源码，只能说本评估不依赖该竞品断言。
- 经济模型没有引入具体美元价格，因为 provider 价格会变；本评估用官方 multiplier 建模。
- EWMA 预测精度没有数字，因为没有 HUAKAI 历史 telemetry。给数字会违反“保证真实 不造假”。

## 决策建议

给 Owner + Claude 综合时建议按以下动作：

1. **把 PASR/cache-aware substrate 定为 P0**：修 OpenAI read signal 标 cache、Gemini cache signal 入 PASR、0/0 miss dead path、vendor/cache-scope/TTL 维度、cache metrics UI。
2. **方向 1 只批准实验，不批准产品化**：写 smoke-test plan，Owner 提供测试账号和预算后验证 Anthropic/OpenAI/Gemini 的同 scope 与 cross scope hit 行为。
3. **方向 1 上线门槛**：必须有 warm budget、expected reuse threshold、per-prefix false-positive accounting、operator kill switch、warm audit log。
4. **方向 2 从 gateway scope 移出**：另立 `orchestration runtime / workflow templates`，显式 endpoint，不污染 OpenAI-compatible chat completion contract。
5. **方向 3 并入 F-SESSION-001**：先做 migration manifest + manual/feature-flag cache bridge；等 A21/A22 telemetry 足够再做预测。
6. **真正商业优先级**：Account Lifecycle Ops + 多账号 quota 聚合 + provider breadth + opt-in detection hygiene，高于主动 cache weaving。

## Owner 中文摘要

本次独立评估的真实观察是：官方文档支持“同 provider cache scope 内可能复用 cache”，但明确否定跨 org 共享；HUAKAI 代码有 PASR/cache 基座但 OpenAI/Gemini/miss-demote 仍有缺口；当前 billing/handler 是单请求单 claim 单 upstream stream，不支持透明拆分。合理推断是：方向 1/3 可以作为 cache-aware ops 的实验路线，但不是立刻可产品化的省钱引擎；方向 2 不应塞进 gateway，而应放到显式 agent runtime。Open questions 共 9 个，最高优先级是用真实账号验证同 workspace/org/project 的跨 key cache hit。
