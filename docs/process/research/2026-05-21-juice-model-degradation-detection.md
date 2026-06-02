# Juice / 模型降算力检测 调研报告

**日期**: 2026-05-21  
**Lane**: specifier（读参考源码 → 行为摘要，不复制代码/标识符）  
**Agent ID**: claude-sonnet-4-6 / general-purpose a6251eb0  
**UTC 时间戳**: 2026-05-21T00:00:00Z（本地报告写入时）

---

## 1. 概念厘清：juice / reasoning effort / compute tier 在主流 vendor API 中的对应

### 1.1 "juice" 是什么

"juice" 是 AI 编程工具圈（尤其 Cursor、Windsurf 等社区）的俗语，指模型在一次请求中投入的推理算力预算（reasoning compute budget）。当用户抱怨"模型 juice 变少"时，意指：原本需要深度推理才能解答的问题，现在模型给出的回答变浅，推理步骤减少，思维链缩短，甚至输出质量大幅下滑——但 API 账单上依然显示使用了旗舰模型。

"降算力"（silent compute downgrade）的形式包括：
- 量化降级：fp16 → int8/int4（输出质量下滑，延迟略降）
- 偷换模型：宣称调用 claude-opus-4 或 gpt-4o，实际走小参数模型
- 悄悄调低 reasoning effort / thinking budget（本文核心关注点）
- 截断上下文窗口：实际可用长度远低于宣称值
- 限流降速：强制减少批处理并行度，外显为 TPS 骤降

### 1.2 各 vendor API 中的 reasoning 相关字段

#### OpenAI

- **`reasoning_effort`**（`o1`、`o3`、`o4-mini` 等推理模型）：取值 `low` / `medium` / `high`，控制模型在回答前投入的内部推理链长度。  
  参考：OpenAI Reasoning models guide（公开契约）。
- **`max_completion_tokens`**（推理模型）：包含 reasoning tokens + output tokens 的总预算上限。
- **`usage.completion_tokens_details.reasoning_tokens`**：响应 usage 字段中给出本次请求实际消耗的 reasoning token 数量。这是可观测降算力的**最直接字段**。
- **`system_fingerprint`**：Chat Completions 响应元数据字段，标识服务端模型/配置版本快照。官方说明同一 `system_fingerprint` 值意味着"相同模型版本+相同采样参数"。当 fingerprint 改变时，官方建议重新基线评估输出。这是检测静默模型替换的**次直接信号**。
- **`model`** 字段回显：响应体中的 `model` 字段（如 `gpt-4o-2024-11-20`）。中转商常在此字段做回写，使其显示用户请求的模型名而非实际服务端模型名。

#### Anthropic

- **`thinking`** 块（`claude-3-7-sonnet-20250219` 等扩展思维模型）：请求时在 body 中传 `{"type": "enabled", "budget_tokens": N}`，N 是 thinking token 预算上限（1024–32768 甚至更高）。
- **`usage.output_tokens`** 分解：响应中 thinking 内容块的 tokens 计入 output_tokens，但 Anthropic 推荐通过检查响应 content 数组中是否出现 `type: "thinking"` 块来确认思维链是否被激活。
- **`output_config.effort` + `thinking.type: "adaptive"`**（Anthropic 原生控制）：Anthropic 官方文档把 `effort` 作为 Claude API 的原生参数，取值包括 `low` / `medium` / `high` / `max`，部分模型还支持 `xhigh`；它是行为级指导而非严格 token budget。原报告所称 `reasoning_effort` 不是 Anthropic Messages API 原生字段；Anthropic 的 OpenAI SDK compatibility 页面把 OpenAI-style `reasoning_effort` 放在兼容层语境中，原生扩展思维仍应使用 `thinking` / `output_config.effort`。sub2api 证据只能说明其 OpenAI Responses → Anthropic Messages 适配层会把 OpenAI-style reasoning effort 转成 Anthropic `output_config.effort` / `thinking`，其 budget 换算不是 Anthropic 官方契约。  
  公开契约：Anthropic Effort docs（`https://platform.claude.com/docs/en/build-with-claude/effort`）；Anthropic Adaptive thinking docs（`https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`）；Anthropic OpenAI SDK compatibility docs（`https://platform.claude.com/docs/en/api/openai-sdk`）。  
  源码证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/responses_to_anthropic_request.go:54-63`  
  源码证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/responses_to_anthropic_request.go:70-98`

#### Google Gemini

- **`thinkingConfig.thinkingBudget`**（Gemini 2.0 Flash Thinking、2.5 Pro/Flash）：整数，单位 tokens，控制内部推理预算。设为 0 则禁用思维。
- **`usageMetadata.thoughtsTokenCount`**：响应字段，给出本次实际消耗的思维 tokens 数量。若宣称启用了 thinking 但该字段为 0 或极小值，可判断 thinking 被静默关闭。
- **`modelVersion`**：响应元数据中的具体模型版本字符串，可与请求时的 `model` 字段比对。

#### 通用规律

| 信号 | OpenAI | Anthropic | Gemini |
|------|--------|-----------|--------|
| reasoning token 数量 | `usage.completion_tokens_details.reasoning_tokens` | thinking content block token count | `usageMetadata.thoughtsTokenCount` |
| 模型版本回显 | `model` 字段 | `model` 字段 | `modelVersion` 字段 |
| 配置版本快照 | `system_fingerprint` | （无等效字段）| （无等效字段）|
| effort/budget 参数 | `reasoning_effort` | `thinking.budget_tokens` / `output_config.effort` | `thinkingConfig.thinkingBudget` |

---

## 2. 降算力的可观测信号维度

以下按"可靠性高→低"排列，每个信号说明采集方式、误报率、是否需要额外请求。

### 2.1 响应元数据字段（零误报、无需额外请求）

**a. `reasoning_tokens` 实际值 vs. 请求时的 `reasoning_effort` / `budget_tokens`**

- **采集方式**：从每次正常请求的响应 `usage` 字段中提取 `reasoning_tokens`（OpenAI）或统计 thinking content block 的 token 数（Anthropic）或读取 `thoughtsTokenCount`（Gemini）。
- **降算力判定**：若请求时 `reasoning_effort=high`（理论 reasoning tokens 数千到数万），但实际 `reasoning_tokens` 返回值显著偏低（如 < 200），或多次请求的该值分布突然向低位漂移，则可判为降算力。
- **误报率**：极低。简单问题 reasoning tokens 本来就少；需要建立问题复杂度控制的基线（见第 3 节 canary probe）。
- **需额外请求**：否。

**b. `system_fingerprint` 变化（OpenAI 特有）**

- **采集方式**：记录每个 `(model_name, api_key_hash, channel_id)` 的历史 fingerprint 值集合；检测新值是否出现。
- **降算力判定**：fingerprint 突变通常意味着后端模型版本/配置切换，但也可能是正常升级。需结合其他信号综合判断。
- **误报率**：中等。OpenAI 官方说明 fingerprint 可能随时变化（例如后端例行更新）。单独使用误报率较高；作为辅助信号价值高。
- **需额外请求**：否。

**c. 响应 `model` 字段回显**

- **采集方式**：比对请求时的目标模型名与响应体中 `model` 字段（或 Gemini 的 `modelVersion`）。
- **降算力判定**：若请求 `claude-opus-4` 返回 `claude-3-haiku-xxx`，即模型替换；若中转商做了回写则此信号被篡改，需配合 fingerprint 或 reasoning token 数量联合检测。
- **误报率**：低（当中转商不做字段篡改时）；中等（中转商已做字段改写时）。
- **需额外请求**：否。

**d. Anthropic thinking content block 是否出现**

- **采集方式**：解析响应 content 数组，检查是否存在 `type: "thinking"` 的 block。
- **降算力判定**：请求时显式设置 `budget_tokens > 0` 但响应中 thinking block 缺失，说明 thinking 被静默关闭。
- **误报率**：低。
- **需额外请求**：否。

### 2.2 延迟与吞吐基线偏移（中等误报）

**e. 首 token 延迟（Time to First Token, TTFT）**

- **采集方式**：流式请求时记录从发出请求到收到第一个 token 的时间戳差值。推理模型在 thinking 阶段完成之前通常有更长的 TTFT（数秒到数十秒）。
- **降算力判定**：同一 `reasoning_effort` 参数下 TTFT 突然大幅缩短（如从 8s 降到 0.3s），可能意味着思维链被截断或跳过。
- **误报率**：中高。TTFT 受网络波动、服务端负载影响大，需要充足的基线样本量（建议 ≥ 50 次）才能可信。
- **需额外请求**：否（在正常请求上附加时间戳记录）。

**f. 每秒 output token 数（TPS）**

- **采集方式**：流式响应中统计 output tokens / 实际耗时。
- **降算力判定**：TPS 异常飙升（如从 30t/s 升到 200t/s）在推理模型上可能意味着 reasoning 被跳过；对普通模型则信号较弱。
- **误报率**：中等。需基线控制（同模型同 effort 同问题类型）。
- **需额外请求**：否。

### 2.3 确定性探测题答题质量（低误报、需额外探测请求）

**g. Canary probe — 已知难题定期探测**

- **采集方式**：维护一批"标定题库"，每隔固定时间向目标 channel 发送这些题目，与历史基线答案比对。
- **降算力判定**：旗舰模型+高 reasoning effort 下对同一道难题的"正确率基线"已知（如 > 95%），若探测时正确率跌至 60% 以下，可判为模型降级。
- **误报率**：低（但取决于题目设计难度和基线样本量）。
- **需额外请求**：是，需周期性发送探测请求（有额外成本）。

**h. 确定性 fingerprint prompt — 固定输出比对**

- **采集方式**：向模型发送 temperature=0、seed 固定的 prompt，期望输出在同一 `system_fingerprint` 下是确定性的。记录历史输出，比对散列值。
- **降算力判定**：同一 fingerprint 下输出散列突变 → 配置改变；不同 fingerprint + 输出散列大幅漂移 → 模型显著变化。
- **误报率**：中等（Gemini/Anthropic 无 fingerprint 等效字段；temperature=0 并非严格确定性）。
- **需额外请求**：是（可批量化，单次探测成本极低）。

**i. 上下文截断迹象**

- **采集方式**：发送已知长度的 prompt（如填充到 90% 宣称上下文窗口），在 prompt 末尾埋入"传送门词"，要求模型复述它；检查响应是否正确引用。
- **降算力判定**：若模型无法引用在宣称窗口内的词，说明有效上下文窗口被静默压缩。
- **误报率**：中等（需要精确控制 tokenization）。
- **需额外请求**：是。

### 2.4 综合信号矩阵

| 信号 | 采集成本 | 误报率 | 侵入性 | 覆盖的降算力类型 |
|------|---------|-------|--------|-----------------|
| reasoning_tokens 字段值 | 零 | 低 | 零 | thinking/reasoning 截断 |
| system_fingerprint 突变 | 零 | 中 | 零 | 模型版本替换 |
| model 字段回显比对 | 零 | 低/中 | 零 | 模型替换（未被篡改时）|
| thinking block 缺失 | 零 | 低 | 零 | thinking 被关闭 |
| TTFT 基线漂移 | 低 | 中高 | 零 | reasoning 跳过 |
| TPS 异常飙升 | 低 | 中 | 零 | reasoning 跳过 |
| Canary probe 正确率 | 中（额外请求） | 低 | 低 | 全类型降级 |
| Deterministic fingerprint | 低（额外请求）| 中 | 低 | 模型版本/配置替换 |
| 上下文截断探测 | 中（额外请求）| 中 | 低 | 窗口压缩 |

---

## 3. 检测方法论

### 3.1 被动元数据监控（零成本，持续）

每次请求完成后，从响应中提取以下字段写入本地监控表：

- `reasoning_tokens`（或等效字段）
- `model`（响应回显值）
- `system_fingerprint`
- `first_token_ms`（TTFT）
- `request_reasoning_effort`（请求时的参数值，来自请求侧记录）

对每个 `(channel_id, model_name, reasoning_effort)` 维度建立滑动窗口基线：
- reasoning_tokens 的 P10/P50/P90 分位分布
- TTFT 的 P10/P50/P90 分位分布
- system_fingerprint 的当前已知值集合

当某维度的最新 N 次请求样本与历史基线发生统计显著偏移（如 reasoning_tokens P50 跌破历史 P10 的 50%），触发告警。

**代价**：零额外 API 请求，仅需在正常请求流中附加字段采集和写入监控表的逻辑。

**精度局限**：只能检测已有真实流量的 channel；新 channel 无基线时无法判断。

### 3.2 主动 Canary Probe 调度（有成本，高精度）

维护一批"标定问题库"：

- **数学推理题**：分难度等级，旗舰模型 + high reasoning effort 基线已标注正确率（难题如竞赛题、多步骤推理）。注意题库需定期轮换以防缓存。
- **长链思维题**：要求步骤 > 20 步的问题，检查 reasoning_tokens 量级是否与宣称的 effort 匹配。
- **上下文记忆题**：在 prompt 中埋入传送门词测试有效窗口长度。

调度策略：
- 低频探测（每 channel 每 30 分钟 1 次）以节省成本；
- 任何被动监控触发告警后立即升级为高频探测（每 5 分钟）；
- 探测请求与真实用户请求混合（防止上游识别探测模式并针对性优化）。

**代价**：每 channel 每天约 48 次额外 API 请求（低频下），成本约 $0.01–$0.05/channel/天（取决于 token 用量）。

### 3.3 统计基线漂移检测

对 reasoning_tokens 序列使用 CUSUM（累积和控制图）或 Kolmogorov-Smirnov 检验进行异常检测，而非简单阈值比对。这可以区分：
- 正常日内波动（问题难度变化导致）
- 持续性系统偏移（渠道降算力）

建议：新 channel 上线初 48 小时为基线建立期，不触发告警；基线样本 ≥ 100 次请求后才开始检测。

### 3.4 方法论对比

| 方法 | 工作量 | 精度 | 侵入性 | 适合场景 |
|------|--------|------|--------|---------|
| 被动元数据监控 | 低 | 中（无基线时弱）| 零 | 日常持续监控，已有流量的 channel |
| 主动 canary probe | 中 | 高 | 低 | 高价值 channel 深度核验；告警后升级排查 |
| 确定性 prompt 指纹 | 低 | 中 | 低 | 快速验证模型版本是否切换 |
| 统计基线漂移 | 中（算法开发）| 高 | 零 | 大规模 channel 的自动异常发现 |
| 用户可触发主动 probe | 中 | 中高 | 低 | 用户自我验证，信任链透明化 |

---

## 4. 参考项目现状分析

本节基于本地 `~/refs/` 下各项目一手源码阅读，不依赖记忆或文档。

### 4.1 sub2api

**健康监控机制**：sub2api 有较完善的 channel monitor 体系（`backend/internal/service/channel_monitor_checker.go`），核心是：
- 向目标 channel 发送一道随机算术题（如 "7 + 3 = ?"），验证模型响应中是否包含正确答案。
- 支持 OpenAI chat_completions / responses API、Anthropic Messages API、Gemini generateContent API 三种 provider。
- 检测结果分为：operational（通过且延迟达标）、degraded（通过但延迟高）、failed（答案错误）、error（HTTP/网络错误）。
- 支持自定义请求模板（headers、body 覆盖模式：off/merge/replace）。
- 有限速、防 SSRF、API key 脱敏等安全措施。  
  证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_checker.go:54-107`  
  证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_challenge.go:11-80`

sub2api 还有 proxy quality 检测（`admin_service.go` + `admin_service_proxy_quality_test.go`），通过向上游原始端点发送 HEAD/GET 请求判断是否遇到 Cloudflare Challenge（cf-ray 头 + 403 状态）。  
  证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service_proxy_quality_test.go:1-90`

**usage log 记录**：`usage_log_repo.go` 的 select 列表中包含 `reasoning_effort`、`service_tier`，意味着 sub2api 会记录请求时的 reasoning effort 级别，但**没有**记录响应中实际的 `reasoning_tokens` 数量（从 select 列表和 DTO 字段未见 `thinking_tokens` / `reasoning_tokens` 字段）。  
  证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/usage_log_repo.go:31`

**结论**：sub2api 有能力探测 channel 是否能正常响应（运维健康度），但**没有检测降算力的逻辑**——它不比对 reasoning tokens 数量、不建立质量基线、不做统计漂移检测、不向用户透明展示任何算力核验信息。

### 4.2 new-api

new-api 有 channel test 机制（`controller/channel-test.go`），向 channel 发送 `"hi"` 并验证响应状态码为 200，记录响应时间。有自动定时测试（`AutomaticallyTestChannels`，可配置间隔分钟数）和按响应时间阈值自动禁用 channel 的逻辑。  
证据：`Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/channel-test.go:60-110,874-984`  
证据：`Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:setting/operation_setting/monitor_setting.go:10-35`

new-api 的响应 DTO 包含 `system_fingerprint` 字段（透传），但**没有**任何基于该字段的异常检测逻辑。  
证据：`Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go:147`

new-api 记录 `reasoning_effort` 字段（请求侧），但同样**没有**记录响应侧的 `reasoning_tokens` 实际值，**没有**降算力检测逻辑。

### 4.3 litellm

litellm 有完整的 health check 体系（`litellm/proxy/health_check.py`）：
- 支持按 mode（chat/embedding/image/responses 等）分类发送健康探测请求。
- 支持有界并发（bounded concurrency）的批量健康检测。
- 支持 `reasoning_effort` 参数在健康检测中透传（`_HEALTH_CHECK_MODES_SUPPORTING_REASONING_EFFORT`）。
- 区分 healthy / unhealthy endpoints，输出供 admin 查看（脱敏后）。  
  证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/health_check.py:165-307`

litellm 有 `quality_router`（`litellm/router_strategy/quality_router/`），但这是**路由层**的 quality_tier 配置（运营者事先为模型打 quality tier 分，路由时优先选高质量 tier）——并非运行时检测上游是否实际降低了质量，是静态配置而非动态检测。  
证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_strategy/quality_router/quality_router.py`（文件存在但未见动态检测逻辑）

litellm 的 usage 类型定义中包含 `reasoning_tokens`，会在统计 completion tokens 时扣除（`text_tokens = completion_tokens - reasoning_tokens`），说明它能正确处理该字段，但**没有**基于该字段做异常检测。  
证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/utils.py:1554-1593`

**结论**：litellm 有最完善的健康检测基础设施，但同样**没有降算力检测**——健康检测只判断可用性，不判断"给的算力是否足量"。

### 4.4 CLIProxyAPI

CLIProxyAPI 的已读源码显示：其 thinking 相关路径主要做请求侧配置识别、校验、转换和透传；usage 队列会把请求侧 effort、延迟、失败状态和 token 统计打包输出，其中包含 reasoning token 计数字段，但没有在该路径中观察到 expected-vs-actual reasoning token 比对、历史基线、canary 题库、质量分或 degraded verdict 逻辑。管理路由主要暴露配置、日志、usage queue、模型映射、配额切换和账号文件等运维能力，未见 juice/probe/quality-downgrade 类入口。  
证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/thinking/apply.go:88-200`  
证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:51-103`  
证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:129-133`  
证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:580-604`  
证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:635-640`

### 4.5 helicone

helicone 的已读源码显示：它能从 OpenAI-style usage 中抽取 reasoning token 计数并写入日志消息，后端查询和前端请求列表也能展示/过滤 reasoning tokens、TTFT、token 和延迟等观测字段；另有 alerts 体系，但内置 alert metric 清单覆盖错误率、成本、延迟和常规 token 指标，未把 reasoning token 作为内置告警指标。已读 scoring 路径是把外部/自定义分数合并进请求记录，未观察到基于模型解题质量、reasoning token 低位漂移、system fingerprint 变化或 canary probe 的降算力判定逻辑。  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/dbLogger/DBLoggable.ts:471-524`  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/dbLogger/DBLoggable.ts:993-1020`  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/stores/request/request.ts:184-205`  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/initialColumns.tsx:175-185`  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:packages/filters/alerts.ts:3-13`  
证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/stores/ScoreStore.ts:116-156`

### 4.6 空白点总结

**所有五个参考项目（sub2api、new-api、litellm、CLIProxyAPI、helicone）均没有实现以下功能**：
1. 对比请求时的 reasoning effort 参数与响应中实际 reasoning token 数量的一致性检测
2. 基于 reasoning_tokens 历史分布的统计漂移告警
3. 面向用户透明的"算力核验报告"
4. 主动 canary probe 的质量基线管理
5. 系统性的 thinking content block 出现率监控

这是**可验证的差异化空白**，与 HUAKAI 的 F-TRUST 使命（商家不能做假、用户消费透明）直接对应。

---

## 5. 映射到 HUAKAI 功能架构

### 5.1 应归属的 Feature Family

juice 检测功能最自然的归属是 **F-TRUST** 家族，具体可命名 **F-TRUST-002**（F-TRUST-001 已是 per-request trust ledger），作为 F-TRUST-001 的上层"模型算力可信度核验"组件。

与现有功能的衔接关系：

- **F-TRUST-001**（trust chain + audit ledger）：juice 检测结论（算力核验结果）需要写入 `audit_ledger_entries`，形成可验证的核验记录。用户可通过 F-TRUST-001 的验证 endpoint 查询某次请求的算力核验结论。
- **F-AUDIT-001**（cost receipt + 透明账单）：若检测到降算力，应在 cost receipt 上标记 `quality_flag: degraded`，自动触发 mismatch 退款流程（D-AUDIT-2 OCAW）。
- **F-CH-002**（channel health）：juice 检测触发的 degraded 状态写入 channel 健康状态，联动 F-CH-002 的 cooldown / 自动禁用逻辑。
- **F-OBS-005**（async outbox / DLQ）：canary probe 的调度和告警通知应走异步 outbox，不阻塞主请求链路。

### 5.2 被动监控 vs. 主动 Probe vs. 用户可触发

**维度一：后台被动监控**
- 在 HUAKAI 的请求处理完成后（`chat_completions_handler.go` 的后处理路径），提取 reasoning_tokens（或 thinking block token 数）和 system_fingerprint，写入专用的 `juice_observations` 时序表。
- 后台 worker 定期对每个 `(channel_id, model, effort)` 维度运行统计检验，触发告警并更新 channel 健康状态。
- 对用户：每次请求的 juice 核验结论（正常/偏低/异常）作为一个字段附加到 F-AUDIT-001 的 cost receipt 上，用户可见。

**维度二：主动 canary probe**
- 复用现有 billing_claims / channel pool 机制，在 idle 时间窗向各 channel 发送标准化的推理难题探测包（temperature=0，seed 固定，题目来自 HUAKAI 维护的 canary_probe_library 表）。
- 探测结果（正确率、reasoning_tokens 实测值）写入 `juice_probe_results` 表，形成滚动基线。
- 对运营者：admin 面板可查看每个 channel 的 juice score 趋势图。

**维度三：用户可触发的 Proof-of-Juice**
- 用户调用 `POST /v1/admin/channels/{id}/juice-verify`（或用户侧 `GET /v1/my/juice-report`），HUAKAI 立即发送一组标准探测请求，几秒内返回核验报告，格式类似：
  ```
  {
    "channel_id": "xxx",
    "model": "claude-opus-4",
    "effort_requested": "high",
    "reasoning_tokens_measured": 8421,
    "reasoning_tokens_expected_p50": 8200,
    "verdict": "nominal",   // nominal / low / critical
    "probe_difficulty": "hard",
    "canary_accuracy": "3/3 correct",
    "signed_by": "ed25519:...",   // F-TRUST-001 keypair 签名
    "ledger_ref": "..."
  }
  ```
- 签名保证报告不可被中转商篡改，用户可独立验证。
- 这一功能是**HUAKAI 与所有现有 gateway 的根本差异**之一：不仅测连通性，而且测算力实真性，并向用户公开证明。

### 5.3 三维升级定位

按项目记忆规定的三维分类：

| 维度 | 内容 |
|------|------|
| **架构升级** | 专用 `juice_observations` 时序表 + `juice_probe_results` 基线表，与主请求链路解耦；canary probe scheduler 作为独立 worker；结果经 F-TRUST-001 audit ledger 锚定（append-only + ed25519 签名）|
| **算法升级** | CUSUM / KS 统计检验替代简单阈值；多维度信号融合评分（reasoning_tokens + fingerprint + TTFT + canary accuracy → juice score）；自适应基线（初期宽松，样本充足后收紧）|
| **生态升级** | 用户可触发 Proof-of-Juice endpoint；cost receipt 上附带 `quality_flag`；admin 面板 juice score 趋势图；降算力时自动触发 F-AUDIT-001 mismatch 退款；公开核验报告 + ed25519 签名可用户自验 |

---

## 6. 落地方向选项

### 选项 A：纯被动元数据监控（最小可行）

**范围**：
1. 在 `chat_completions_handler.go` 后处理路径中提取 `reasoning_tokens`、`system_fingerprint`、`model` 回显，写入新表 `juice_observations(channel_id, model, effort, reasoning_tokens, fingerprint, ttft_ms, created_at)`。
2. 一个简单的后台 worker，按 `(channel_id, model, effort)` 分组，计算滑动 P50，若最新 10 次样本的中位数低于历史 P50 的 30%，则写告警到 `channel_health_admin_alerts`（复用 F-ADV-001 的告警通道）。
3. 在 F-AUDIT-001 的 cost receipt 上增加 `quality_signal` 字段，显示采样期间的 reasoning_tokens 范围。

**工作量**：约 5-8 天（1 个 Codex slice）  
**依赖**：F-AUDIT-001 receipt 字段扩展；F-OBS-005 async worker  
**风险**：
- 无基线期的新 channel 无信号，用户初期看不到结果。
- reasoning_tokens 字段 OpenAI 完整，Anthropic 需解析 thinking block，Gemini 需读 `thoughtsTokenCount`，三个 provider 采集逻辑略有差异。
- 被简单请求（用户 prompt 简单）稀释，需控制"只对 `effort >= medium` 的请求采集"。

### 选项 B：被动监控 + 主动 Canary Probe（完整版）

**范围**（在 A 基础上叠加）：
1. 新表 `canary_probe_library`（题目 + 标准答案 + 难度 + 适配 effort 级别）；`juice_probe_results`（probe 结果 + 分时统计基线）。
2. Canary probe scheduler worker（每 channel 低频定时执行，告警后升级高频）。
3. Admin probe trigger endpoint：`POST /v1/admin/channels/{id}/juice-probe`，立即执行 3-5 道标准题，返回实时结果。
4. 用户 Proof-of-Juice 端点（需 F-SESSION-001 鉴权）：`POST /v1/me/juice-verify`，向用户当前关联的 channel 发送探测，返回签名报告（需 F-TRUST-001 keypair）。

**工作量**：约 12-18 天（2-3 个 Codex slice）  
**依赖**：F-TRUST-001（ed25519 签名）；F-SESSION-001（用户端鉴权）；F-AUDIT-001；F-OBS-005  
**风险**：
- Canary probe 题库维护成本（需定期更新避免缓存/泄露）。
- 探测请求本身有 API 成本，需要纳入运营预算。
- Anthropic/Gemini 的 temperature=0 并非严格确定性，canary accuracy 会有噪声，需统计足够样本数。
- F-TRUST-001 keypair 依赖尚未完全完成，签名功能需先确认 F-TRUST-001 Phase B/C 完成。

### 选项 C：两者结合 + 用户可见报告（战略完整版）

即选项 B 的完整实现，加上：
1. 前端 juice 状态展示（在账单页、模型选择页显示每个 channel 的 juice 健康等级：绿/黄/红）。
2. 降算力自动触发 F-AUDIT-001 mismatch 退款（当 juice verdict = critical 时触发 D-AUDIT-2 refund worker）。
3. 公开的 juice 核验方法论文档，供用户独立复现验证。

**工作量**：约 20-30 天（3-5 个 Codex slice，含前端）  
**依赖**：同选项 B，加前端 dashboard（当前搁置）+ F-AUDIT-001 完整实现  
**风险**：
> 【2026-06-02 已更新】本段“前端 dashboard 当前搁置 / 前端处于搁置状态”是 2026-05-21
> 历史。landing commit `bcc4f5d` 已解除前端冻结并完成 Next 14→15 升级；选项 C
> 的剩余风险应理解为“需要真实前端 wire-up 与退款授权”，不是“仍被 Rust 四波冻结”。以下为历史风险表述。
- 前端当前处于搁置状态（见项目记忆 2026-05-21 体检），选项 C 前端部分需等 Rust 四波后。
- 自动退款逻辑需 Owner 对退款触发条件做明确授权（高风险决策）。
- juice 检测误报导致误退款是核心业务风险，需保守的判定阈值。

### 选项比较

| | 选项 A | 选项 B | 选项 C |
|--|--------|--------|--------|
| 工作量 | 5-8天 | 12-18天 | 20-30天 |
| 差异化力度 | 低（仅运营可见）| 中（用户可触发 probe）| 高（用户可见报告 + 自动退款）|
| 依赖复杂度 | 低 | 中 | 高 |
| 核心风险 | 基线不足误判 | canary 成本 + F-TRUST-001 依赖 | 误退款 + 前端搁置 |
| 推荐时机 | 当前可做（Phase N 内）| F-TRUST-001 B/C 完成后 | 前端解冻 + F-AUDIT-001 完成后 |

**推荐落地路径**：先做选项 A（被动监控 + admin 可见告警），作为 F-TRUST-002 的 Phase A；等 F-TRUST-001 键对基础设施就绪后，升级到选项 B 的用户可触发 Proof-of-Juice；选项 C 作为 Q3 里程碑目标。

---

## 附：源文件清单

本报告读取的一手源码文件：

- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_checker.go`（全文）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_challenge.go`（全文）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_template_types.go`（全文）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/channel_monitor_validate.go`（节选）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service_proxy_quality_test.go`（节选）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/dto/types.go`（370-430 行）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/responses_to_anthropic_request.go`（1-120 行）
- `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/usage_log_repo.go`（第 31 行 select 列表）
- `Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/channel-test.go`（全文）
- `Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:setting/operation_setting/monitor_setting.go`（全文）
- `Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go`（140-210 行）
- `Calcium-Ion/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_request.go`（第 39 行）
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/health_check.py`（1-320 行）
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/utils.py`（1554-1593 行）
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/llms/openai.py`（945 行，2188 行）
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/llms/anthropic.py`（396、651 行）
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/thinking/apply.go`（1-220 行；本地目录不是 git checkout，SHA 来自 `.huakai-head-sha`）
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/thinking/convert.go`（1-183 行；本地目录不是 git checkout，SHA 来自 `.huakai-head-sha`）
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go`（1-173 行；本地目录不是 git checkout，SHA 来自 `.huakai-head-sha`）
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go`（540-710 行；本地目录不是 git checkout，SHA 来自 `.huakai-head-sha`）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/dbLogger/DBLoggable.ts`（450-540 行，990-1040 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts`（1-230 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/stores/request/request.ts`（80-220 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/stores/ScoreStore.ts`（90-190 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/initialColumns.tsx`（150-210 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/alerts/alertForm.tsx`（350-420 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:packages/filters/alerts.ts`（1-34 行）
- `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/filterAST/filterUIDefinitions/staticDefinitions.ts`（280-330 行）
- `/home/codex/HUAKAI/docs/03_FEATURE_PARITY_MATRIX.md`（F-TRUST-001/F-AUDIT-001/F-CH-002 相关行）

公开契约文档核验：

- Anthropic Effort docs: `https://platform.claude.com/docs/en/build-with-claude/effort`
- Anthropic Adaptive thinking docs: `https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`
- Anthropic OpenAI SDK compatibility docs: `https://platform.claude.com/docs/en/api/openai-sdk`

**Lane**: specifier（读参考源码 → 行为摘要；未复制函数名/结构体字段/代码块；所有发现均用不同句子结构转述）  
**UTC 时间戳**: 2026-05-21T13:20:20Z（Codex 修订核验）
