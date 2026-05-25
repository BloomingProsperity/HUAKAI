# HUAKAI trust-chain GitHub survey - Codex lane

Metadata:

- Date: 2026-05-13
- Scope: GitHub/WebSearch survey for user-verifiable AI gateway trust-chain patterns
- Baseline from Owner: local 4 refs already compared for log redaction; Portkey = opt-in ML PII, LiteLLM = env-var secret regex, New API = no observed redaction, Sub2API = no observed redaction; HUAKAI direction = strict allowlist plus planned Ed25519 audit ledger.
- Observed source regions: 38
- Inferences: 12
- Open questions: 7
- Clean-room lane: specifier; behavior-only summary; no upstream code blocks copied.

## 1. 调研方法

本轮按 Owner 给定范围做 WebSearch + GitHub source-read。WebSearch 关键词覆盖四组：

- AI gateway / LLM proxy: `LLM gateway proxy log redaction PII audit signature tokens cache verification`, `AI gateway model mismatch token usage cache audit`, `LLM API audit model substitution`
- 通用 gateway / mesh: `Kong AI gateway prompt guard signature`, `Envoy AI Gateway token usage cache`, `service mesh signed request audit log`
- transparency log: `Rekor Trillian transparency log API inclusion proof consistency proof`, `Tessera signed checkpoint Merkle log`
- 厂商/观测平台: `OpenAI cached tokens SDK`, `Anthropic cache read input tokens signature SDK`, `LangSmith anonymizer hide inputs`, `Langfuse ingestion masking`, `W&B Weave redaction Presidio`

随后只把能落到 source file:line 的项目写入强结论。README 只用于定位项目意图；机制判断优先使用源文件、测试文件或 API schema。对“没有”的表述全部限定为“本轮 targeted read 未观察到”，不是全仓库绝对不存在。

本轮新增或复用的关键仓库与提交：

| 项目 | commit | 本轮用途 |
|---|---:|---|
| trylonai/gateway | `bdb1d8b71b01` | default-enabled PII policy / Presidio behavior |
| RelayPlane/proxy | `df3d3edc7c05` | routed/requested model transparency, response model mismatch warning, provider usage capture |
| pydantic/pydantic-ai-gateway | `feab1b532f58` | streaming usage enforcement, response usage extraction, cost estimate injection |
| invariantlabs-ai/invariant-gateway | `9baeade022cc` | gateway metadata propagation for model/usage |
| oktsec/oktsec | `8efb444de840` | Ed25519 signing, audit hash chain, audit export redaction |
| sigstore/rekor | `9bc540f21471` | transparency log API, inclusion/consistency proof verification |
| google/trillian | `3d57cf1a97c8` | generic Merkle log API, inclusion/consistency proofs |
| transparency-dev/tessera | `db8e65f3001b` | signed checkpoint / RFC6962-style leaf hash framework |
| Kong/kong | `58f2daa56b90` | credential stripping, AI prompt guard, webhook signature verification |
| langfuse/langfuse | `692aa600549d` | configurable ingestion masking, fail-open/fail-closed behavior |
| langchain-ai/langsmith-sdk | `8f635fbb0e78` | client-side hide/anonymizer controls |
| wandb/weave | `7fd43a9d2a99` | sensitive-key redaction and optional PII redaction setting |
| Helicone/helicone | `3f4bd44b85f9` | response cache key / cache storage behavior |
| envoyproxy/ai-gateway | `4d3eae8b35c4` | model-derived routing and token/cache usage telemetry |
| openai/openai-python | `38d75d74a562` | official SDK usage/cache token fields |
| anthropics/anthropic-sdk-python | `04b468daf76e` | official SDK cache token fields and thinking-continuity signature field |
| sunblaze-ucb/llm-api-audit | `21ffeef65c4c` | research-grade model substitution audit patterns |
| CASE-Lab-UMD/LLM-Auditing-CoIn | `06c3246ae19d` | hidden reasoning token audit and Merkle proof pattern |

## 2. 发现项目表

| 项目 | Observed 行为 | 对 HUAKAI 的意义 | 证据 |
|---|---|---|---|
| Trylon Gateway | 默认样例策略把 PII 检查置为启用，同时作用于用户输入和 LLM 输出；检测到 PII 后默认走阻断路径而不是返回遮蔽后的内容。Presidio 分析器和匿名化器都初始化，但本轮只观察到 PII 检测/阻断，没有观察到代理日志默认 redaction。 | 证明“默认启用 PII 防护”在 gateway 类项目里存在，但不是 HUAKAI 所需的“日志 strict allowlist 默认遮蔽”。HUAKAI 的默认日志最小化仍更强。 | trylonai/gateway@bdb1d8b71b01:policies.yaml:6, trylonai/gateway@bdb1d8b71b01:policies.yaml:7, trylonai/gateway@bdb1d8b71b01:src/domain/validators/pii_leakage/main.py:76, trylonai/gateway@bdb1d8b71b01:src/domain/validators/pii_leakage/main.py:83, trylonai/gateway@bdb1d8b71b01:src/core/startup.py:66 |
| RelayPlane Proxy | 响应头暴露“请求模型、实际路由模型、provider、routing mode”等透明度信息；非流式响应会读取响应体模型字段并在与目标模型不一致时记录 warning；usage 直接来自 provider 响应，并记录 cache creation/read token。 | 可借鉴“模型替换可观测性”的轻量做法，但它不是签名证明；HUAKAI 应把 requested/resolved/provider-returned model 写入签名事件。 | RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:2874, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:2887, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:2894, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:7579, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:7585 |
| Pydantic AI Gateway | 对 OpenAI 兼容流式请求强制保留 usage 事件；如果调用方试图关闭 usage，它返回错误；响应侧提取模型与 usage 并用于计费估算，也写出 OpenTelemetry usage/cache token 属性。 | 对 HUAKAI 的 token cross-check 是弱信号：它强化 provider usage 必须可见，但仍是 provider usage + price 计算，不是本地 tokenizer 交叉校验。 | pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/providers/openai.ts:64, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/providers/openai.ts:70, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/handler.ts:264, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/handler.ts:274, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/otel/attributes.ts:29 |
| Invariant Gateway | 从 provider 响应里把 model/usage 这类 metadata 带入网关处理链；Anthropic stream 合并路径也更新 usage。 | 对 HUAKAI 是“metadata 留痕”参考，不是替换检测、签名证明或 token 校验。 | invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/open_ai.py:203, invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/open_ai.py:208, invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/anthropic.py:165, invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/anthropic.py:203 |
| Oktsec | 配置样例要求 agent 身份签名；每条 audit entry 计算链式 SHA-256 hash，可选 Ed25519 proxy signature；验证命令检查 hash 链和签名；导出层有不同 redaction 级别。 | 最接近 HUAKAI “Ed25519 audit ledger”的实现形态，但领域是 agent/tool security，不是 LLM proxy。HUAKAI 可借鉴链式事件、verify CLI、按受众 redaction 的产品形态。 | oktsec/oktsec@8efb444de840:oktsec.yaml.example:24, oktsec/oktsec@8efb444de840:oktsec.yaml.example:53, oktsec/oktsec@8efb444de840:internal/audit/chain.go:19, oktsec/oktsec@8efb444de840:internal/audit/chain.go:36, oktsec/oktsec@8efb444de840:internal/audit/chain.go:63, oktsec/oktsec@8efb444de840:cmd/oktsec/commands/audit_verify_chain.go:39, oktsec/oktsec@8efb444de840:internal/audit/redact.go:33 |
| Rekor | API schema 暴露当前 Merkle root/size、consistency proof、entry inclusion proof、signed checkpoint；CLI verify 会检查 entry inclusion proof、checkpoint 和 signed entry timestamp。 | 适合 HUAKAI 做“周期性根锚定”或外部透明日志插件，不适合每请求同步写公共 log。 | sigstore/rekor@9bc540f21471:openapi.yaml:63, sigstore/rekor@9bc540f21471:openapi.yaml:101, sigstore/rekor@9bc540f21471:openapi.yaml:138, sigstore/rekor@9bc540f21471:openapi.yaml:602, sigstore/rekor@9bc540f21471:cmd/rekor-cli/app/verify.go:88, sigstore/rekor@9bc540f21471:cmd/rekor-cli/app/verify.go:194 |
| Trillian | 通用 log API 支持写入叶子、按 index/hash 查询 inclusion proof、查询 consistency proof；叶子 hash 覆盖 leaf value，不覆盖 extra data。 | 如果 HUAKAI 使用 Trillian，必须把完整 canonical audit envelope 或其 digest 放进 leaf value，不能把关键字段放在不受 Merkle 覆盖的附加数据里。 | google/trillian@3d57cf1a97c8:trillian_log_api.proto:32, google/trillian@3d57cf1a97c8:trillian_log_api.proto:61, google/trillian@3d57cf1a97c8:trillian_log_api.proto:77, google/trillian@3d57cf1a97c8:trillian_log_api.proto:300, google/trillian@3d57cf1a97c8:trillian_log_api.proto:312 |
| Tessera | Entry 有 raw data、identity 和 leaf hash；append 侧支持本地签名 checkpoint，且 checkpoint 形态对齐标准透明日志 checkpoint。 | 对 HUAKAI 是更轻量的嵌入式透明日志候选；适合后续 P1/P2 做 hourly/daily root checkpoint。 | transparency-dev/tessera@db8e65f3001b:entry.go:41, transparency-dev/tessera@db8e65f3001b:entry.go:47, transparency-dev/tessera@db8e65f3001b:append_lifecycle.go:802, transparency-dev/tessera@db8e65f3001b:append_lifecycle.go:827 |
| Kong OSS | 普通 auth 插件支持配置后把凭据从上游请求移除；AI prompt guard 支持 allow/deny regex；standard-webhooks 插件验证 body signature 与 timestamp。 | 通用 API gateway 有“凭据不上游”“请求签名验证”“prompt guard”这些横向能力，但本轮未观察到用户可验证的 LLM 响应账本。 | Kong/kong@58f2daa56b90:kong/plugins/key-auth/schema.lua:17, Kong/kong@58f2daa56b90:kong/plugins/key-auth/handler.lua:145, Kong/kong@58f2daa56b90:kong/plugins/ai-prompt-guard/schema.lua:10, Kong/kong@58f2daa56b90:kong/plugins/ai-prompt-guard/filters/guard-prompt.lua:81, Kong/kong@58f2daa56b90:kong/plugins/standard-webhooks/internal.lua:65 |
| Envoy AI Gateway | 路由规则能基于请求内容抽取出的模型名匹配；成本表达式支持 input/output、cache read、cache creation、reasoning token 等变量；tracing 常量记录 prompt cache hit/write token。 | 对 HUAKAI 的 usage/cache 字段设计有参考价值；未观察到默认 redaction、签名 ledger、cache cryptographic verify。 | envoyproxy/ai-gateway@4d3eae8b35c4:api/v1alpha1/ai_gateway_route.go:74, envoyproxy/ai-gateway@4d3eae8b35c4:api/v1alpha1/shared_types.go:116, envoyproxy/ai-gateway@4d3eae8b35c4:api/v1alpha1/shared_types.go:117, envoyproxy/ai-gateway@4d3eae8b35c4:internal/tracing/openinference/openinference.go:158 |
| Helicone | cache key 由请求 URL、body、缓存相关 header、部分 auth 信息和 cache seed 等输入组成；缓存值保存响应 header、latency、body。 | 有 response cache，但本轮未观察到“缓存命中可由用户校验”的签名或 Merkle 证明。HUAKAI 不应只做 cache key hash，还应签名 hit/miss 与 payload digest。 | Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:33, Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:59, Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:100 |
| Langfuse | ingestion masking 是企业/云配置式回调能力；未配置时返回原数据；配置后 callback 成功才返回 masked data；失败时可按配置 fail open 或 fail closed，worker 侧 fail closed 时丢弃 OTEL event。 | 对 HUAKAI 的默认 redaction 是反例：能力存在但不是 default-on。HUAKAI strict allowlist 应作为默认路径，不依赖外部 callback。 | langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:23, langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:43, langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:151, langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:200, langfuse/langfuse@692aa600549d:worker/src/queues/otelIngestionQueue.ts:268 |
| LangSmith SDK | 客户端支持传入 anonymizer、hide inputs、hide outputs；未显式传参时由环境开关决定；隐藏逻辑会在上传前替换 inputs/outputs。 | 对 HUAKAI 是 client-side observability redaction pattern，不是 gateway 默认 redaction 或审计账本。 | langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:859, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:1260, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:2556, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/anonymizer.py:175 |
| W&B Weave | trace client 默认会对敏感 key 做递归 redaction；PII redaction 设置默认 false；另有 Presidio scorer/guardrail 可做实体识别和匿名化。 | 有“默认 secret key redaction”，但 PII redaction 非默认。对 HUAKAI 可以借鉴 key allowlist/denylist，但不能等同于完整 prompt 日志 redaction。 | wandb/weave@7fd43a9d2a99:weave/trace/weave_client.py:778, wandb/weave@7fd43a9d2a99:weave/trace/weave_client.py:2841, wandb/weave@7fd43a9d2a99:weave/utils/sanitize.py:38, wandb/weave@7fd43a9d2a99:weave/trace/settings.py:89, wandb/weave@7fd43a9d2a99:weave/scorers/presidio_guardrail.py:20 |
| OpenAI Python SDK | SDK 类型暴露 prompt/input usage 分解，包括 cached token；Responses usage 也有缓存输入 token 计数字段。 | 只能作为 provider-reported usage 证据；本轮未观察到用户侧响应签名、模型替换证明、cache proof。 | openai/openai-python@38d75d74a562:src/openai/types/completion_usage.py:34, openai/openai-python@38d75d74a562:src/openai/types/completion_usage.py:40, openai/openai-python@38d75d74a562:src/openai/types/responses/response_usage.py:8, openai/openai-python@38d75d74a562:src/openai/types/responses/response_usage.py:11 |
| Anthropic Python SDK | SDK 类型暴露 cache creation/read input token；message 文档说明总 input token 是普通 input、cache creation、cache read 的合计；thinking omitted 场景有 multi-turn continuity signature，但这是 thinking continuity，不是 API response audit ledger。 | 对 HUAKAI token cross-check 有字段参考；不能替代 gateway 侧签名账本。 | anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/usage.py:17, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/usage.py:20, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/message.py:126, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/thinking_config_enabled_param.py:30 |
| LLM API Audit | 研究仓库明确面向 LLM API service integrity / model substitution；包含 classifier、identity prompting、model equality testing、logprob 收集。mixed distribution 实验以 fp32/int8 分布混合模拟替换比例，并用 two-sample test 估计拒绝能力。 | 对 HUAKAI 的 model substitution detection 是“离线/抽样审计”路线，不是每请求证明。可作为 P2 采样巡检插件。 | sunblaze-ucb/llm-api-audit@21ffeef65c4c:README.md:3, sunblaze-ucb/llm-api-audit@21ffeef65c4c:README.md:40, sunblaze-ucb/llm-api-audit@21ffeef65c4c:model_equality_testing/mixed.py:12, sunblaze-ucb/llm-api-audit@21ffeef65c4c:model_equality_testing/mixed.py:117, sunblaze-ucb/llm-api-audit@21ffeef65c4c:logprobs/run_logprobs.py:55 |
| CoIn | 研究仓库面向 opaque LLM API 隐藏 reasoning token 审计；README 描述 token-to-block、block-to-answer、hash-tree verification；源码包含 Merkle tree construction/proof verification 与 rule-based relevance threshold。 | 对 HUAKAI 的 token count cross-check / Merkle audit 是强研究参考，但它验证 hidden reasoning token 诚实性，不是通用 gateway billing ledger。 | CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:README.md:8, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:README.md:16, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:README.md:18, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:2_hash_tree/main.py:12, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:2_hash_tree/verify.py:9, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:5_CoIn_pipline/main_rule_based_efficient_acc.py:145 |

## 3. HUAKAI 6 要求映射

| HUAKAI 要求 | 外部观察 | 缺口 | HUAKAI 建议状态 |
|---|---|---|---|
| 1. default-on log redaction | Trylon 有默认启用 PII 检测/阻断；Weave 默认敏感 key redaction 但 PII redaction 默认 false；Langfuse/LangSmith 是配置式遮蔽。 | 没有观察到主流 LLM gateway 默认 strict allowlist 记录日志。多数能力是 opt-in、callback、client SDK 或 secret-key redaction。 | `Implemented Better` 目标：默认不持久化 raw prompt/body；仅 allowlist 字段入库；红action失败 fail closed；导出再分级 redaction。 |
| 2. user-side signature | Oktsec 有 Ed25519 entry signature + verify-chain；Rekor/Tessera 有 signed checkpoint；Kong standard-webhooks 是 incoming request signature verification。 | LLM gateway 类项目没有观察到“用户可下载 response/audit envelope 并用公钥验签”。 | `Mandatory P0/P1`: 每请求 canonical audit envelope 用 HUAKAI key 签名；提供 CLI/SDK verifier；key rotation 写入 ledger。 |
| 3. model substitution detection | RelayPlane 暴露 requested/routed model 并检查 provider response model mismatch；LLM API Audit 提供离线 statistical audit；Pydantic/Invariant 把 response model 作为 metadata/cost 输入。 | 这些都不是强证明；provider 可能不返回 model 或返回不可验证字段。 | `Safe Equivalent + Better`: 在 signed event 里绑定 requested model、routing decision、provider request model、provider response model、fallback reason；mismatch 可按策略 warn/block。 |
| 4. token count cross-check | OpenAI/Anthropic SDK 暴露 provider-reported usage/cache fields；Pydantic 强制 streaming usage 可见；RelayPlane/Pydantic/Envoy AI Gateway记录 usage/cache token；CoIn 是 hidden reasoning token 审计研究路线。 | 大多只信 provider usage；本轮未观察到生产 gateway 默认本地 tokenizer cross-check + drift alert。 | `Mandatory P0`: 保存 provider usage + local tokenizer estimate + provider tokenizer profile + tolerance；超阈值生成 audit anomaly，不直接影响请求成功，避免误杀。 |
| 5. cache verify | Helicone/RelayPlane 有 cache key、cache hit metadata 或 provider cache token记录；OpenAI/Anthropic 暴露 cache read/creation token。 | 没有观察到用户可验证的 cache hit proof 或 payload digest signature。 | `Mandatory P1`: signed event 绑定 cache namespace、cache key digest、hit/miss、cached payload digest、scope policy、provider cache token fields；verify tool 检查 cache event 与 ledger hash。 |
| 6. Merkle audit | Rekor/Trillian/Tessera 是成熟 transparency log primitives；Oktsec 是本地 hash chain + Ed25519；CoIn 是 token fingerprint Merkle proof研究。 | 透明日志项目不懂 LLM gateway 语义；Oktsec 不处理 LLM routing/cache/token schema；CoIn 不是运营账本。 | `Implemented Better Roadmap`: 先做本地 append-only hash chain，后做 hourly/daily root Merkle tree；可选外部 anchor 到 Rekor/Tessera/Trillian。 |

结论：本轮没有发现一个项目同时覆盖 HUAKAI 六项要求。外部最强组合是：

- Redaction: Trylon / Langfuse / LangSmith / Weave 分别覆盖检测、配置式遮蔽、客户端遮蔽、敏感 key redaction。
- Model visibility: RelayPlane 最接近 runtime mismatch detection。
- Token visibility: Pydantic AI Gateway / Envoy AI Gateway / RelayPlane / OpenAI / Anthropic 覆盖 provider usage/cache fields。
- Cryptographic trust: Oktsec + Rekor/Trillian/Tessera 最接近 ledger/verify。
- Research audit: LLM API Audit 和 CoIn 对 model substitution / hidden reasoning token 提供离线或实验性路线。

HUAKAI 的差异化空间仍成立：把这些碎片能力合并成 user-verifiable AI gateway，而不是单点 observability 或 provider-reported metadata。

## 4. 升级建议

P0: canonical signed audit event

- 定义最小 canonical envelope：request_id、tenant_id hash、API key hash、request timestamp、requested model、routing rule id、resolved provider/model、provider response model、status、latency、usage_provider、usage_local_estimate、cache event、redaction policy version、previous event hash、event hash。
- 用 Ed25519 签名 event hash；响应 header 暴露 audit id 和 signature id；完整 envelope 通过 audit API / export 获取。
- 每个签名事件只包含 allowlist 后字段；raw body 默认不进入 ledger。

P0: strict allowlist log redaction

- 默认持久化字段必须是 allowlist，不是“写入后再遮蔽”。
- 对 prompt/messages/tool payload 只存 digest、长度、token estimate、MIME/shape metadata；用户显式开启 debug 才允许短 TTL raw capture。
- redaction policy version 写入 signed event，便于用户证明某次请求适用哪套日志规则。

P0: model mismatch detection

- 在 response path 同时记录三类模型：用户请求模型、HUAKAI 路由后模型、provider response 声称模型。
- provider response model 缺失时标 `provider_model_absent`，不要默默认为一致。
- fallback、alias、model mapping 必须写入 signed event；高风险租户可配置 mismatch block。

P0: token cross-check

- 对每次请求记录 provider usage 和 HUAKAI 本地估算；本地估算按 provider/model tokenizer profile 选择，无法精确时写明 estimator id。
- 设置阈值策略：例如 abs/relative drift、cache read/write 不一致、output token 异常为 audit anomaly。
- 不要用估算直接改 billing ledger，先进入 dispute/audit 队列；避免 tokenizer 差异导致误扣费。

P1: cache verification

- cache key digest 的输入必须 canonical 化，并写入 signed event：tenant scope、provider、model、request shape digest、redaction/debug mode、cache policy version。
- cache hit 时签名 cached payload digest、original upstream event id、TTL/expiry、cache namespace。
- verify CLI 应能证明：这次响应来自哪条上游事件或哪个 provider cache read token，而不是只显示“命中”。

P1: Merkle / transparency layer

- 第一阶段使用本地 append-only hash chain，保证没有外部依赖也能验。
- 第二阶段按 tenant/hour/day 生成 Merkle root；root 可写入内部 immutable storage。
- 第三阶段提供外部 anchoring 插件：Rekor/Tessera/Trillian 三选一，默认不把 per-request 内容发到公共 log，只锚定 root/checkpoint。

P2: audit UX / verifier

- 提供 `huakai audit verify <bundle.jsonl>`：验证 event signature、hash chain、Merkle inclusion、checkpoint signature、cache digest、model mismatch policy。
- Admin UI 不显示 raw secrets；按 Owner/tenant/operator 角色展示不同 redaction level。
- 给终端用户提供“response receipt”：request id、model route、provider response model、usage pair、cache status、signature status。

Open questions:

- OQ-1: 是否需要把 Anthropic thinking-continuity signature 显示给 HUAKAI 用户，还是只作为 provider-specific metadata 存入 signed event？
- OQ-2: HUAKAI 本地 tokenizer cross-check 的容忍阈值如何按 provider/model 版本配置？
- OQ-3: cache payload digest 是否覆盖 response body 全量，还是只覆盖 provider response canonical JSON？
- OQ-4: 多 provider fallback 时，一个用户请求是否生成一条 aggregate event，还是每次 provider attempt 一条 event + parent event？
- OQ-5: public transparency anchoring 默认开关应按租户、项目还是全局 edition 控制？
- OQ-6: Istio/Envoy core 本轮未做完整 source-read；如要对 service mesh 的 mTLS/access-log/ext-authz 做正式 parity，需要单独计划。
- OQ-7: LLM API Audit / CoIn 属研究性质，若产品化为 HUAKAI 插件，需要确认模型/数据 license 与运行成本。

## 5. 必读 file:line

| 类别 | 必读 file:line | 为什么必读 |
|---|---|---|
| PII default-on / no-redaction distinction | trylonai/gateway@bdb1d8b71b01:policies.yaml:6, trylonai/gateway@bdb1d8b71b01:src/domain/validators/pii_leakage/main.py:83, trylonai/gateway@bdb1d8b71b01:src/core/startup.py:71 | 证明 default PII policy 与 Presidio 初始化存在，同时 validator 在 blocking 分支没有返回遮蔽内容。 |
| Runtime model mismatch | RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:2874, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:2887, RelayPlane/proxy@df3d3edc7c05:src/standalone-proxy.ts:7579 | 证明 runtime route transparency 和 response model mismatch warning 是现成 pattern。 |
| Usage enforcement | pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/providers/openai.ts:64, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/providers/openai.ts:70, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/handler.ts:264, pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/handler.ts:380 | 证明有 gateway 会强制 usage 可见并在缺 usage/model 时失败。 |
| Provider metadata propagation | invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/open_ai.py:203, invariantlabs-ai/invariant-gateway@9baeade022cc:gateway/routes/anthropic.py:203 | 证明 model/usage metadata 留痕是常见但弱证明能力。 |
| Ed25519 audit chain | oktsec/oktsec@8efb444de840:oktsec.yaml.example:24, oktsec/oktsec@8efb444de840:internal/audit/chain.go:19, oktsec/oktsec@8efb444de840:internal/audit/chain.go:36, oktsec/oktsec@8efb444de840:internal/audit/chain.go:63, oktsec/oktsec@8efb444de840:cmd/oktsec/commands/audit_verify_chain.go:39 | HUAKAI audit ledger 最直接的工程参考。 |
| Audit export redaction | oktsec/oktsec@8efb444de840:internal/audit/redact.go:7, oktsec/oktsec@8efb444de840:internal/audit/redact.go:33, oktsec/oktsec@8efb444de840:internal/audit/redact.go:84 | 证明 ledger/export 可以按受众分层 redaction，不必把所有字段交给所有操作者。 |
| Transparency log API | sigstore/rekor@9bc540f21471:openapi.yaml:63, sigstore/rekor@9bc540f21471:openapi.yaml:101, sigstore/rekor@9bc540f21471:openapi.yaml:138, sigstore/rekor@9bc540f21471:openapi.yaml:602 | 证明 Rekor API 层具备 root/size、consistency proof、entry creation、inclusion proof。 |
| Rekor verify path | sigstore/rekor@9bc540f21471:cmd/rekor-cli/app/verify.go:88, sigstore/rekor@9bc540f21471:cmd/rekor-cli/app/verify.go:194, sigstore/rekor@9bc540f21471:pkg/verify/verify.go:67, sigstore/rekor@9bc540f21471:pkg/verify/verify.go:121 | HUAKAI verifier CLI 可借鉴“先搜 entry，再验 inclusion/checkpoint/consistency”的流程。 |
| Trillian proof semantics | google/trillian@3d57cf1a97c8:trillian_log_api.proto:32, google/trillian@3d57cf1a97c8:trillian_log_api.proto:61, google/trillian@3d57cf1a97c8:trillian_log_api.proto:77, google/trillian@3d57cf1a97c8:trillian_log_api.proto:300, google/trillian@3d57cf1a97c8:trillian_log_api.proto:312 | 证明 leaf value 与 extra data 覆盖边界；决定 HUAKAI canonical envelope 应放哪里。 |
| Tessera checkpoint | transparency-dev/tessera@db8e65f3001b:entry.go:47, transparency-dev/tessera@db8e65f3001b:append_lifecycle.go:802, transparency-dev/tessera@db8e65f3001b:append_lifecycle.go:827 | 证明轻量透明日志框架可本地签 checkpoint。 |
| Kong orthogonal controls | Kong/kong@58f2daa56b90:kong/plugins/key-auth/schema.lua:17, Kong/kong@58f2daa56b90:kong/plugins/ai-prompt-guard/schema.lua:10, Kong/kong@58f2daa56b90:kong/plugins/standard-webhooks/internal.lua:65 | 通用 gateway 能做凭据 stripping、prompt guard、incoming signature verification，但不是 response ledger。 |
| Envoy AI Gateway usage/cache | envoyproxy/ai-gateway@4d3eae8b35c4:api/v1alpha1/ai_gateway_route.go:74, envoyproxy/ai-gateway@4d3eae8b35c4:api/v1alpha1/shared_types.go:116, envoyproxy/ai-gateway@4d3eae8b35c4:internal/tracing/openinference/openinference.go:158 | 证明 usage/cache token 字段可进入路由/cost/tracing surface。 |
| Helicone cache | Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:33, Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:59, Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:100 | 证明 response cache key/value 机制存在，但缺用户可验证 proof。 |
| Langfuse masking | langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:43, langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:151, langfuse/langfuse@692aa600549d:packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts:200, langfuse/langfuse@692aa600549d:worker/src/queues/otelIngestionQueue.ts:268 | 证明 ingestion masking 是配置式，不是 default-on。 |
| LangSmith client masking | langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:859, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:1260, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/client.py:2556, langchain-ai/langsmith-sdk@8f635fbb0e78:python/langsmith/anonymizer.py:175 | 证明 client SDK 层可隐藏/匿名化 inputs/outputs。 |
| Weave sensitive-key / PII | wandb/weave@7fd43a9d2a99:weave/trace/weave_client.py:778, wandb/weave@7fd43a9d2a99:weave/trace/weave_client.py:2841, wandb/weave@7fd43a9d2a99:weave/utils/sanitize.py:59, wandb/weave@7fd43a9d2a99:weave/trace/settings.py:89, wandb/weave@7fd43a9d2a99:weave/scorers/presidio_guardrail.py:20 | 证明 secret-key redaction 和 PII redaction 是不同层级。 |
| OpenAI usage/cache | openai/openai-python@38d75d74a562:src/openai/types/completion_usage.py:34, openai/openai-python@38d75d74a562:src/openai/types/completion_usage.py:40, openai/openai-python@38d75d74a562:src/openai/types/responses/response_usage.py:8, openai/openai-python@38d75d74a562:src/openai/types/responses/response_usage.py:11 | 证明 provider usage/cache 字段存在，但不是 proof。 |
| Anthropic usage/cache/signature nuance | anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/usage.py:17, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/usage.py:20, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/message.py:126, anthropics/anthropic-sdk-python@04b468daf76e:src/anthropic/types/thinking_config_enabled_param.py:30 | 证明 cache token 合计规则与 thinking continuity signature 语义。 |
| Model substitution audit research | sunblaze-ucb/llm-api-audit@21ffeef65c4c:README.md:3, sunblaze-ucb/llm-api-audit@21ffeef65c4c:README.md:40, sunblaze-ucb/llm-api-audit@21ffeef65c4c:model_equality_testing/mixed.py:117, sunblaze-ucb/llm-api-audit@21ffeef65c4c:logprobs/run_logprobs.py:55 | 证明存在离线/抽样式 substitution audit 路线。 |
| Hidden reasoning token audit | CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:README.md:8, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:README.md:16, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:2_hash_tree/main.py:12, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:2_hash_tree/verify.py:9, CASE-Lab-UMD/LLM-Auditing-CoIn@06c3246ae19d:5_CoIn_pipline/main_rule_based_efficient_acc.py:194 | 证明 token count audit 可以结合 sampling、embedding relevance 和 Merkle proof，但目前是研究实现。 |

Source coverage proof:

- Trylon Gateway: `policies.yaml`, `src/domain/validators/pii_leakage/main.py`, `src/core/startup.py`, `src/shared/types.py`
- RelayPlane Proxy: `src/standalone-proxy.ts`, `src/middleware.ts`, `src/estimate.ts`
- Pydantic AI Gateway: `gateway/src/providers/openai.ts`, `gateway/src/handler.ts`, `gateway/src/otel/attributes.ts`
- Invariant Gateway: `gateway/routes/open_ai.py`, `gateway/routes/anthropic.py`
- Oktsec: `oktsec.yaml.example`, `internal/audit/chain.go`, `cmd/oktsec/commands/audit_verify_chain.go`, `internal/audit/redact.go`, `internal/audit/store.go`
- Rekor: `openapi.yaml`, `cmd/rekor-cli/app/verify.go`, `pkg/verify/verify.go`
- Trillian: `trillian_log_api.proto`
- Tessera: `entry.go`, `append_lifecycle.go`
- Kong: `kong/plugins/key-auth/schema.lua`, `kong/plugins/key-auth/handler.lua`, `kong/plugins/ai-prompt-guard/schema.lua`, `kong/plugins/ai-prompt-guard/filters/guard-prompt.lua`, `kong/plugins/standard-webhooks/internal.lua`
- Envoy AI Gateway: `api/v1alpha1/ai_gateway_route.go`, `api/v1alpha1/shared_types.go`, `internal/tracing/openinference/openinference.go`
- Helicone: `worker/src/lib/util/cache/cacheFunctions.ts`
- Langfuse: `packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts`, `worker/src/queues/otelIngestionQueue.ts`, `worker/src/__tests__/ingestionMasking.test.ts`
- LangSmith SDK: `python/langsmith/client.py`, `python/langsmith/anonymizer.py`
- Weave: `weave/trace/settings.py`, `weave/trace/weave_client.py`, `weave/utils/sanitize.py`, `weave/scorers/presidio_guardrail.py`
- OpenAI Python SDK: `src/openai/types/completion_usage.py`, `src/openai/types/responses/response_usage.py`
- Anthropic Python SDK: `src/anthropic/types/usage.py`, `src/anthropic/types/message.py`, `src/anthropic/types/thinking_config_enabled_param.py`, `src/anthropic/types/thinking_block.py`, `src/anthropic/types/signature_delta.py`, `src/anthropic/types/message_tokens_count.py`
- LLM API Audit: `README.md`, `model_equality_testing/mixed.py`, `logprobs/run_logprobs.py`, `classifier/classification.py`
- CoIn: `README.md`, `2_hash_tree/main.py`, `2_hash_tree/verify.py`, `5_CoIn_pipline/main_rule_based_efficient_acc.py`

Chinese owner summary:

本轮真实观察到的部分是：少数 gateway/observability 项目有 PII/secret 遮蔽或 usage/cache metadata，RelayPlane 有模型 mismatch warning，Pydantic AI Gateway 强制 streaming usage，Oktsec/Rekor/Trillian/Tessera 提供最接近 HUAKAI audit ledger 的密码学原语，LLM API Audit/CoIn 提供模型替换和 hidden reasoning token 审计的研究路线；合理推断是 HUAKAI 可以把这些碎片能力合并成 strict allowlist + signed audit envelope + local token estimate + cache digest + Merkle root anchoring；open question 共 7 个，主要集中在 tokenizer 阈值、cache digest 范围、外部透明日志锚定策略和研究项目产品化风险。

Source files read: trylon-gateway/policies.yaml; trylon-gateway/src/domain/validators/pii_leakage/main.py; trylon-gateway/src/core/startup.py; trylon-gateway/src/shared/types.py; relayplane-proxy/src/standalone-proxy.ts; relayplane-proxy/src/middleware.ts; relayplane-proxy/src/estimate.ts; pydantic-ai-gateway/gateway/src/providers/openai.ts; pydantic-ai-gateway/gateway/src/handler.ts; pydantic-ai-gateway/gateway/src/otel/attributes.ts; invariant-gateway/gateway/routes/open_ai.py; invariant-gateway/gateway/routes/anthropic.py; oktsec/oktsec.yaml.example; oktsec/internal/audit/chain.go; oktsec/cmd/oktsec/commands/audit_verify_chain.go; oktsec/internal/audit/redact.go; oktsec/internal/audit/store.go; rekor/openapi.yaml; rekor/cmd/rekor-cli/app/verify.go; rekor/pkg/verify/verify.go; trillian/trillian_log_api.proto; tessera/entry.go; tessera/append_lifecycle.go; kong/kong/plugins/key-auth/schema.lua; kong/kong/plugins/key-auth/handler.lua; kong/kong/plugins/ai-prompt-guard/schema.lua; kong/kong/plugins/ai-prompt-guard/filters/guard-prompt.lua; kong/kong/plugins/standard-webhooks/internal.lua; envoy-ai-gateway/api/v1alpha1/ai_gateway_route.go; envoy-ai-gateway/api/v1alpha1/shared_types.go; envoy-ai-gateway/internal/tracing/openinference/openinference.go; helicone/worker/src/lib/util/cache/cacheFunctions.ts; langfuse/packages/shared/src/server/ee/ingestionMasking/applyIngestionMasking.ts; langfuse/worker/src/queues/otelIngestionQueue.ts; langfuse/worker/src/__tests__/ingestionMasking.test.ts; langsmith-sdk/python/langsmith/client.py; langsmith-sdk/python/langsmith/anonymizer.py; weave/weave/trace/settings.py; weave/weave/trace/weave_client.py; weave/weave/utils/sanitize.py; weave/weave/scorers/presidio_guardrail.py; openai-python/src/openai/types/completion_usage.py; openai-python/src/openai/types/responses/response_usage.py; anthropic-sdk-python/src/anthropic/types/usage.py; anthropic-sdk-python/src/anthropic/types/message.py; anthropic-sdk-python/src/anthropic/types/thinking_config_enabled_param.py; anthropic-sdk-python/src/anthropic/types/thinking_block.py; anthropic-sdk-python/src/anthropic/types/signature_delta.py; anthropic-sdk-python/src/anthropic/types/message_tokens_count.py; llm-api-audit/README.md; llm-api-audit/model_equality_testing/mixed.py; llm-api-audit/logprobs/run_logprobs.py; llm-api-audit/classifier/classification.py; llm-auditing-coin/README.md; llm-auditing-coin/2_hash_tree/main.py; llm-auditing-coin/2_hash_tree/verify.py; llm-auditing-coin/5_CoIn_pipline/main_rule_based_efficient_acc.py
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-13T10:00:09Z
