# Juice 功能市场调研报告 — 模型算力/质量检测专项

**日期**: 2026-05-21  
**Lane**: specifier（市场调研 → 行为摘要，不复制代码/标识符）  
**Agent ID**: claude-sonnet-4-6 / general-purpose acdee61eb990abfcb  
**UTC 时间戳**: 2026-05-21T09:30:00Z  
**前置依赖**: 已读 `2026-05-21-juice-model-degradation-detection.md`（参考项目五个均无降算力检测，空白点已确认）

---

## 1. 模型质量 / 算力基准测试与监控服务

### 1.1 Artificial Analysis

**网址**: https://artificialanalysis.ai

**定位**: 跨 provider 同模型性能测评平台，目前是业界最系统的独立 LLM benchmark 服务。

**方法论**:
- **Intelligence Index v4.0**：含 10 项真实任务测评（GDPval-AA / τ²-Bench Telecom / Terminal-Bench Hard / SciCode / AA-LCR / AA-Omniscience / IFBench / HLE / GPQA Diamond / CritPt），覆盖 Agents(25%)、Coding(25%)、Scientific Reasoning(25%)、General(25%)，95% 置信区间 ±1%。
- **性能指标**：Time to First Token(TTFT)、Output Speed(TPS)、Total Response Time for 100 output tokens、End-to-End Response Time，均按用户侧实际体验采集。
- **Mystery Shopper 政策**：Artificial Analysis 用非本域名账号匿名注册，运行 intelligence eval 和 performance benchmark，防止 lab 识别并针对其 endpoint 优化表现。这是业界首个针对"provider 可能给已知评测者更好服务"风险的主动对策。
- **跨 provider 比对**：同一模型在不同 hosting provider 上均被测试；报告方式是"first-party API 结果 OR 多 provider 中位数"，未公开逐 provider 质量打分（仅内部路由决策使用）。

**是否检测过 provider 降级**:  
Artificial Analysis 未公开披露"某 provider 被抓到降级"的具体案例。但其 mystery shopper 方法论的设计前提正是"provider 有动机在已知测评时给更好服务"，暗示已意识到这种风险。其 performance 数据是按 endpoint 切分的，同一模型跨 provider 的延迟/吞吐差异是可查公开数据。

**质量检测覆盖**: 覆盖能力质量（解题正确率）+ 性能（延迟/吞吐），但**不覆盖**"reasoning effort 算力是否被降低"这一维度——测评结果的答案正确率间接反映质量，但不是针对中转商降算力设计的。

**参考链接**:  
- https://artificialanalysis.ai/methodology/intelligence-benchmarking  
- https://www.latent.space/p/artificialanalysis（mystery shopper 描述）

---

### 1.2 OpenRouter

**网址**: https://openrouter.ai

**定位**: 多 provider 路由网关，同时是目前最成熟的"基于质量信号动态路由"实践案例。

**Per-provider 质量路由机制**:

- **滚动窗口性能统计**：对每个 (model, provider) 组合，按 5 分钟滚动窗口计算 p50/p75/p90/p99 延迟和吞吐，用于实时路由决策。
- **Auto Exacto**（tool-calling 质量感知路由）：对发起 tool-call 的请求，OpenRouter 追踪每个 provider 的 tool-calling 成功率作为"质量信号"，用 **中位数 + MAD（中位绝对偏差）** 方法对各 provider 排名；落后超过 1 标准差的 provider 降权；数据不足的 provider 保守处理。公开数据显示 Auto Exacto 激活后 tau-bench 得分和 tool-calling 成功率有可测量提升。
- **Uptime 计算公式**：成功请求数 ÷ 总请求数（排除用户侧 4xx 错误），全局可用性约 99.97%。

**是否检测 reasoning 算力降低**:  
OpenRouter 的质量信号目前仅覆盖 tool-calling 成功率 + 延迟/吞吐，**未覆盖**推理模型的 reasoning token 数量、thinking block 存在性、或答题正确率。当前 reasoning 算力降低对 OpenRouter 来说是不可见的。

**用户投诉记录**:  
2026 年独立评测博客（ofox.ai）记录了"通过 OpenRouter 访问开源模型有时输出质量低于直接访问"的现象，归因于 provider 侧负载均衡、model version drift、限流时静默降级而非报错。该记录属于定性观察，无量化检测数据支撑。

**参考链接**:
- https://openrouter.ai/docs/guides/routing/auto-exacto  
- https://ofox.ai/blog/is-openrouter-reliable-honest-review-2026/

---

### 1.3 LMSYS / Chatbot Arena

**网址**: https://lmarena.ai

**定位**: 众包人类偏好对战评测平台，Elo 评分体系。

**与降算力检测的相关性**:
- LMSYS 通过 Bradley-Terry 模型把数百万场人类偏好投票转化为 Elo 分，**已实践对同名模型不同版本的质量区分**（如 GPT-4-0314 vs GPT-4-0613 打分有明显差异）。
- 这是间接证明"同模型不同配置质量可测量差异"的最大规模实证。
- 但 LMSYS 评测针对模型 lab 的官方 endpoint，**不覆盖**第三方中转商或 provider 对同一权重的降级部署。
- 2025 年论文《The Leaderboard Illusion》（arxiv 2504.20879）指出 Arena 存在测评偏差，但这与降算力检测无直接关联。

**参考链接**:
- https://arxiv.org/html/2403.04132v1  
- https://arxiv.org/pdf/2504.20879

---

### 1.4 Catchpoint AI 监控

**网址**: https://www.catchpoint.com

**定位**: 企业级 Internet Performance Monitoring 平台，2024-2025 年扩展了 LLM 监控能力。

**检测方法**:
- 从多地理位置发送相同 prompt 到多个 LLM provider（GPT / Claude / Gemini），比对响应 tone、连贯性、freshness、延迟、成本。
- 可自动化 prompt-level 测试并触发告警（drift、hallucination 风险、latency spike）。
- 实例：测出 Claude 平均响应 3.3s，OpenAI 6.8s，期间三者可用性均 100%。

**是否检测 reasoning 算力降低**: 不直接检测。其"质量"指标是主观 tone/连贯性评估 + 延迟，不是推理 token 数量或解题正确率。

**商业定价**: Expert Plan 起价 $11,988/年，使用 token-based points 计费模型。

**参考链接**:
- https://www.catchpoint.com/blog/llms-dont-stand-still-how-to-monitor-and-trust-the-models-powering-your-ai

---

## 2. 中转 API 模型校验工具（中文中转圈专项）

### 2.1 行业背景

2026-05-12，每日经济新闻、虎嗅等媒体集中报道中文 AI 中转站乱象，标题为"1元钱285万Token的陷阱！起底'AI中转站'"，揭露三大降智技术手段：

1. **动态路由/模型降级**：高峰期把请求路由到更便宜的模型，显示相同产品名；
2. **多租户限流**：限制单请求 GPU 时间、显存窗口、工具调用次数；
3. **安全后处理加重**：高峰期审核/重写链路变重，回答更保守、更模板化。

这些报道均**未提出任何检测方法**，只有定性描述。

**参考链接**:  
- https://www.nbd.com.cn/articles/2026-05-12/4388468.html  
- https://www.huxiu.com/article/4857684.html

---

### 2.2 RelayRadar（relay-radar）

**GitHub**: https://github.com/AetherCore-Dev/relay-radar  
**Stars**: 27（截至 2026-05-21 调研时）  
**维护状态**: 活跃，39 commits，有 open issues，npm 包可用（`npx relay-radar scan`）  
**许可证**: 未明确，作者声明开源

**检测方法（据 README 及描述）**:
1. **行为指纹分析**（正常使用时分析 response style，参考 ICLR 2025 sequential testing 研究）
2. **主动探测**（8 条标准化问询，参考 USENIX Security 2025 的 LLMmap 方法论）
3. **Token 成本审计**（检测异常高 input token 数，判断是否有 prompt injection / token 虚报）

**声称精度**: 盲测 98% 准确率（100 组 Opus vs Sonnet 替换测试）  
**支持模型**: Claude Opus 4.6/4.5、Sonnet 4.6/4.5、Haiku 4.5/3.5、GPT-4o  
**费用**: 每次检测约 ¥0.2（完整），¥0.15（快速）  
**独立性声明**: 不收集数据，不需注册，代码开源

**评估**:
- 这是中文圈目前**最接近 HUAKAI juice 功能定位**的工具，但实现方式和可信度存疑：
  - 27 stars 表明极早期项目，社区验证不足；
  - 98% 准确率是自我声明，缺乏第三方验证；
  - 方法论依赖 LLMmap 行为指纹（见第 3 节），适合检测模型替换，对"同模型降 reasoning effort"的检测能力未说明；
  - **无法抵抗蓄意做假的中转商**：若中转商知道探测模式（8 条标准问询），可对已知 fingerprint query 给出"正确"答案。

---

### 2.3 Hvoy AI

**网址**: https://www.hvoy.ai/en/  
**定位**: 免费在线检测服务，面向用户"购买前低成本技术健康检查"

**检测信号（6 维度）**:
1. Protocol consistency（协议一致性）
2. Response structure patterns（响应结构模式）
3. Knowledge behavior answers（知识问答行为）
4. Identity consistency（身份一致性）
5. Thinking traces（thinking 链路，适用支持 thinking 的模型）
6. Signature fingerprints（签名指纹）

**支持模型**: Claude Opus 4.7/4.6、Sonnet 4.6、GPT 5.5/5.4、Gemini 3.1 Pro  
**定价**: 免费（无注册）  
**局限性**: 明确声明"不是正式审计，不保证完美准确"；无 star 数据（非 GitHub 项目）

**评估**:
- Hvoy 是目前功能最全面的免费中转检测在线服务，综合检测 6 个维度。
- 其 "thinking traces" 检测信号直接对应 HUAKAI juice 的核心场景（检测 thinking 是否被静默关闭）。
- 但作为**无历史基线、无统计检验**的单次扫描工具，对"同模型降低 reasoning effort"（而非整体替换）的检测力未知。
- 蓄意做假的中转商可对 Hvoy 的探测 pattern 进行针对性优化，降低被抓概率。

---

### 2.4 stampr_ai

**网址**: https://www.stampr-ai.com/  
**定位**: OpenAI system_fingerprint 追踪与模型身份核验工具，Python 包 + Web tracker

**检测方法**:
- 维护 OpenAI 各模型的 fingerprint 历史版本库；
- Python 包对比 live 模型行为与已发布指纹；
- Web tracker 可视化 fingerprint 和 model/system card 的历史变化。

**当前状态**: Alpha（v0.9.0a1），fingerprinting 算法和核验覆盖面仍在开发中  
**定价**: 免费（pip 安装，无订阅）  
**局限性**:
- 仅覆盖 OpenAI 模型（有 `system_fingerprint` 字段的 API）；
- Anthropic / Gemini 无等效字段，无法直接移植；
- 依赖 OpenAI 官方 fingerprint，中转商可伪造此字段（见第 6 节）。

---

### 2.5 其他中文中转测活工具

以下工具仅提供"是否在线 / 能否返回 200"级别的测活，**不做模型质量或算力检测**：

| 工具/项目 | 功能层次 | Stars | 维护 |
|-----------|---------|-------|------|
| all-api-hub (qixing-jk) | 余额看板 + 可用性测试（200检测）| ~200+ | 活跃 |
| awesome-ai-proxy (mn-api) | 中转站目录列表 | ~50 | 不定期 |
| wll8/ai-proxy | 可访问性不定时检测 | ~30 | 低频 |

这些项目均停留在"connectivity check"层面，不涉及模型质量验真。

---

## 3. 模型指纹 / Fingerprinting 技术与学术研究

### 3.1 LLMmap — 行为指纹识别（USENIX Security 2025）

**论文**: https://arxiv.org/html/2407.15847v4  
**代码**: https://github.com/pasquini-dario/LLMmap（311 stars，MIT，活跃）

**核心方法**:  
- 仅需 **3-8 次交互**即可在 42 个 LLM 版本中以 **>95% 准确率**识别目标模型。
- 8 条优化 query 覆盖四类模式：
  1. Meta-information queries（模型创建/训练的版本特异性回答）
  2. Banner grabbing（"Who created you?"等身份问题）
  3. Malformed/alignment queries（各模型拒绝有害请求的方式不同）
  4. Inconsistent inputs（多语言/语义破损 prompt 暴露错误处理签名）
- 机器学习分类器（闭集 neural net + 开集对比学习）
- 对未知系统 prompt、随机采样超参数、RAG/CoT 框架均鲁棒

**对中转商的抵抗性**: LLMmap 利用模型"内在行为差异"，这些差异无法通过系统 prompt 或参数调整消除，中转商**无法用简单配置欺骗**。但若中转商专门识别 LLMmap 的 8 条特征问询并分流处理，可能规避。

---

### 3.2 Rank-Based Uniformity Test（RUT）— 最新黑盒 API 审计方法（2026-04）

**论文**: https://arxiv.org/abs/2506.06975

**核心方法**:  
- 通过**行为等价性验证**，将 black-box LLM API 的输出分布与本地部署的真实模型分布对比；
- 方法采用 **秩统计量 + 均一性检验（Rank-Based Uniformity Test）**，无需访问 logits 或模型权重；
- **刻意避免可检测的 query 模式**，具备抗探测路由规避能力；
- 在量化（quantization）、有害微调、jailbreak prompt 注入、完全模型替换四种 threat scenario 下均表现优于 MMD 等先前方法；
- 在受限 query 预算下统计功效（statistical power）显著优于前代方法。

**实用性**:  
- 需要本地部署一份真实参考模型（对 Closed-source 模型 claude-opus-4 等不可行）；
- 主要适用于开源模型的 hosting provider 验证场景；
- 学术成果，暂无生产级 SDK 或 SaaS 封装。

---

### 3.3 McNemar 统计检验法（当前最实用的统计降级检测框架）

**论文**: https://arxiv.org/pdf/2602.10144（"When LLMs get significantly worse: A statistical approach"）

**核心方法**:  
- 基于 **McNemar 检验**，对样本级别的正确率差异进行假设检验（而非聚合任务级别）；
- 可置信地检测 **0.3% 的准确率下滑**，同时避免对理论无损优化的误报；
- 已集成进 LM Evaluation Harness（广泛使用的开源 LLM 评测框架）。

**对 HUAKAI juice 的适用性**:  
McNemar 检验逻辑可以直接用于 canary probe 题库的答题正确率监控——当统计假设检验拒绝"本次正确率与基线相同"的零假设时，可判为降算力事件。

---

### 3.4 HuRef / Chain&Hash — 模型水印与所有权验证研究

**HuRef 论文**: NeurIPS 2024（https://proceedings.neurips.cc/paper_files/paper/2024/file/e46fc33e80e9fa2febcdb058fba4beca-Paper-Conference.pdf）  
**Chain&Hash 论文**: https://arxiv.org/html/2407.10887v1  

这类研究侧重"模型版权归属"（所有权证明），而非"服务端是否降低算力"。需要在训练阶段植入指纹（proactive），Black-box API 用户无法直接使用。

---

### 3.5 NANOZK — 零知识证明可验证推理

**论文**: https://arxiv.org/abs/2603.18046

- 使用 **分层 ZK 证明**（layerwise zero-knowledge proofs）让 LLM 推理过程可密码学验证；
- GPT-2 规模：43 秒生成证明，6.9KB 证明体积，23ms 验证时间；
- 比 EZKL 快 52×。

**实用性评估**:  
- **理论上最强的不可伪造方案**——provider 必须运行特定模型权重才能生成有效 ZK proof；
- 但当前仅在 GPT-2（117M 参数）规模上验证，Claude/GPT-4 级别（>100B 参数）的计算成本还不可行；
- 2026 年内不可能用于生产级 API 中转检测。

---

### 3.6 TEE 可信执行环境 — Phala / OLLM

**Phala**: https://phala.com  
**OLLM**: https://ollm.com

- Intel TDX + NVIDIA H100/H200 GPU TEE，推理过程密码学证明"在指定硬件和软件配置中运行"；
- 远程证明（remote attestation）可向用户证明未被中间人篡改；
- 性能损耗约 0.5%-5%。

**评估**: TEE 方案是"基础设施层"的可信度证明，适合 Phala 这类另起炉灶的新网关。对于已运行的中转站，要求其切换到 TEE 不现实。对 HUAKAI 是中期生态路标，不是近期 juice 方案。

---

### 3.7 OpenAI system_fingerprint 实际可靠性

**社区发现**（OpenAI Developer Community 线程）:
- `system_fingerprint` 对相同模型、相同 seed、相同参数的多次调用**可能返回不同值**；
- OpenAI 官方承认 fingerprint 会随后端例行更新变化；
- 因此 fingerprint 单独用于降算力检测**误报率中等**，只能作为辅助信号，而非主要判据。

**中转商伪造难度**: 极低——中转商完全控制响应体，可以随意写 `system_fingerprint` 值。

---

## 4. 检测技术清单与可靠性评估

### 4.1 按技术类别分类

#### 类型 ①：响应元数据监控

| 信号 | 谁在用 | 可靠性 | 被蓄意做假的中转商识破概率 |
|------|-------|--------|--------------------------|
| `reasoning_tokens` 字段值 | HUAKAI（规划中），无人在生产中用于检测 | 高（数字硬指标）| 中高 — 中转商可伪造 usage 字段 |
| `system_fingerprint` 突变 | stampr_ai，OpenAI 社区 | 中（误报率高）| 极高 — 直接覆写字段 |
| `model` 字段回显 | new-api 透传，sub2api 回写 | 低（普遍被篡改）| 极高 — 最容易伪造 |
| Anthropic thinking block 是否出现 | CLIProxyAPI（识别），HUAKAI（规划中）| 高 | 中 — 伪造 thinking block 需模拟格式 |
| Gemini `thoughtsTokenCount` | 无生产使用案例 | 高 | 中高 — Gemini 中转商少，但可伪造 |

#### 类型 ②：主动 Benchmark Probe

| 方案 | 谁在用 | 可靠性 | 被识破概率 |
|------|-------|--------|----------|
| Canary 难题正确率 | Artificial Analysis（质量评测），sub2api（简单算术）| 高（需足量样本）| 中 — 中转商若对已知题库特殊处理可规避 |
| LLMmap 行为指纹 8 query | RelayRadar（声称参考），LLMmap 研究 | 高（95%+ 准确率）| 中 — 专门针对 8 条 query 分流可规避 |
| McNemar 统计检验框架 | 学术（LM Eval Harness 集成）| 高（0.3% 降级可检测）| 低-中 — 需知道题库才能针对性做假 |

#### 类型 ③：延迟/吞吐基线

| 信号 | 谁在用 | 可靠性 | 被识破概率 |
|------|-------|--------|----------|
| TTFT 缩短（reasoning 跳过代理信号）| Artificial Analysis、Catchpoint | 中高（噪声大）| 低 — 中转商难伪造延迟（成本高）|
| TPS 飙升（推理模型 reasoning 被跳过）| 无成熟生产工具 | 中 | 低 |

#### 类型 ④：指纹 Prompt（temperature=0 + seed 固定）

| 信号 | 谁在用 | 可靠性 | 被识破概率 |
|------|-------|--------|----------|
| 确定性输出哈希比对 | 无成熟生产工具 | 中（非严格确定性）| 中 — temperature=0 不保证同一输出 |
| stampr_ai fingerprint track | stampr_ai Alpha | 中 | 高 — 中转商可伪造 fingerprint |

#### 类型 ⑤：统计漂移检测

| 方案 | 谁在用 | 可靠性 | 被识破概率 |
|------|-------|--------|----------|
| CUSUM / KS 检验（reasoning_tokens 时序）| 无现有产品实现 | 高（样本充足后）| 中 — 中转商若平滑降级可延缓触发 |
| RUT 黑盒 API 审计 | 学术（arxiv 2506.06975）| 高，抗探测规避 | 低（专门设计抗识别）|

#### 类型 ⑥：密码学/硬件可信

| 方案 | 谁在用 | 可靠性 | 被识破概率 |
|------|-------|--------|----------|
| ZK Proof（NANOZK）| 学术，GPT-2 规模 | 极高（不可伪造）| 极低（密码学保证）|
| TEE 远程证明（Phala）| Phala 商用 | 高（硬件级）| 极低 | 
| LLMmap 行为指纹闭集分类 | LLMmap 研究，RelayRadar 声称参考 | 高 | 中（可针对 query 分流）|

---

### 4.2 核心结论：哪些检测手段对蓄意做假的中转商有效？

**公认"不可伪造"（或极难伪造）的手段**：

1. **延迟/吞吐指标**（TTFT、TPS）—— 中转商无法伪造低延迟并实际提供高质量推理；即使撒谎响应时间，用户的网络层 timestamp 可验证。属于**物理层约束**，最难造假。

2. **ZK Proof / TEE 远程证明** —— 密码学/硬件级保证，原理上不可伪造，但**当前对旗舰商业闭源模型不可行**（需要接触模型权重）。

3. **题库多样化 + 轮换的 canary probe 正确率** —— 若题库足够大（数千题）、轮换足够快（单次探测随机抽取），中转商无法对所有题预缓存正确答案。这是**最实用的抗伪造方案**，代价是题库维护成本和探测 API 费用。

4. **RUT 黑盒 API 审计**（arxiv 2506.06975）—— 专门设计了"避免可检测 query 模式"，使中转商无法识别探测请求并分流处理，是目前学术上最先进的抗识破检测框架。

**容易被蓄意做假中转商识破的手段**：

- `model` 字段回写（几乎所有中转站都做）
- `system_fingerprint` 字段伪造（直接覆写）
- `reasoning_tokens` 数字伪造（修改 usage body）
- 固定 fingerprint prompt（中转站可缓存已知指纹题）
- banner grabbing（"Who made you?"）—— 中转站早已系统覆写

---

## 5. 商业 / 付费服务

### 5.1 Catchpoint

- **定位**: 企业级 Internet + LLM 性能监控平台
- **定价**: Expert Plan $11,988/年起，token-based points 模型
- **LLM 功能**: 跨 provider 同 prompt 对比、drift 告警、latency/quality 监控
- **对降算力检测覆盖**: 不直接覆盖 reasoning token 降低，侧重 tone/latency 等外在质量
- **目标客户**: 企业级（有预算专门做 LLM QoS 监控的团队）

### 5.2 Arize AI / Braintrust / Confident AI / Helicone（已覆盖）

这类 LLM Observability 平台（市场规模 2024 年 $1.44B，2029 年预计 $6.8B）主要做应用层 trace 记录、eval 评分、cost 监控，**均不做"同 channel 推理模型 reasoning token 降低检测"**。详见已有报告 §4.5 helicone 分析。

**定价参考**:
- Braintrust: 免费 tier（1M trace spans/月）→ $249/月
- Confident AI: $0 / $29.99 / $199 / $2,499（年付 enterprise）
- Arize Phoenix: 开源免费（~8k stars）

### 5.3 Hvoy AI

- **定位**: 免费在线中转检测服务（无订阅）
- **目标客户**: 个人开发者买中转 API 前做的"技术健康检查"
- **局限**: 无历史基线，单次扫描，非正式审计

### 5.4 Phala / OLLM（TEE 可信推理）

- **定位**: 密码学可证明推理服务，面向 privacy + model authenticity 双需求
- **商业模式**: Infrastructure-as-a-Service，按 GPU compute 计费
- **对 juice 场景覆盖**: 若 HUAKAI 将来接入 Phala 作为"可信推理通道"，用户可获得密码学证明；但这是改变 routing 架构，不是被动检测现有中转商。

### 5.5 目前专门卖"模型降算力检测"的 SaaS —— 几乎空白

市场调研结论：**目前市面上不存在专门针对"推理模型 reasoning effort 被静默降低"这一场景的成熟商业 SaaS 产品**。现有产品要么只做 connectivity check（中文中转圈），要么做通用 LLM observability（企业级），要么是学术工具（LLMmap / RUT），要么是基础设施服务（Phala）。这一细分市场是真实的空白。

---

## 6. 核心问题：可信度分析

### 6.1 蓄意做假的中转商能伪造什么

一个完全控制响应链路的中转商可以做到：

| 伪造目标 | 难度 | 方法 |
|---------|------|------|
| `model` 字段 | 极易 | 直接覆写 response body |
| `system_fingerprint` | 极易 | 直接覆写 response body |
| `reasoning_tokens` 数字 | 易 | 修改 usage.completion_tokens_details |
| Anthropic thinking block | 中 | 注入空 thinking block 或伪造格式 |
| 延迟（TTFT）伪造 | 难 | 需要精确 sleep 控制，成本高，用户侧 timestamp 可验证 |
| 题库 canary 答案 | 中-难 | 需预缓存题库答案，题库大/轮换快则难 |
| LLMmap 行为指纹 | 中 | 需识别 8 条特征 query 并路由到真实高端模型 |
| RUT 黑盒统计检验 | 难 | 需对所有非识别探测 query 也维持高质量输出（代价高）|
| ZK Proof | 不可能（当前）| 需要目标模型权重 |
| TEE 远程证明 | 不可能（当前）| 需要 Intel TDX + NVIDIA 认证 |

### 6.2 业界公认"不可伪造"或极难伪造的有效方案

结合所有调研，按实用性排序：

**Tier 1 — 理论不可伪造（近期不可用）**
- ZK Proof（NANOZK：密码学，但仅限开源/自部署小模型）
- TEE 远程证明（Phala：硬件级，但需重构 routing 基础设施）

**Tier 2 — 实践最强防伪（当前可落地）**
- **RUT 黑盒审计**：通过随机化、避免可检测 query 模式，对不知道题库的中转商有效；学术成果，需工程化
- **大题库轮换 canary probe**：千题以上题库 + 每次随机抽取 + 结合 McNemar 统计检验，使预缓存答案不可行
- **TTFT 物理约束**：响应延迟是网络层可观测的物理量，中转商撒谎延迟的同时无法真正降低推理成本

**Tier 3 — 有效但可绕过（需配合使用）**
- LLMmap 行为指纹：95%+ 准确率，但已知 query pattern 可被识别绕过
- reasoning_tokens + TTFT 联合统计检验：单独都弱，组合后误报率下降
- Anthropic thinking block 缺失检测：有效但中转商可注入假 block

### 6.3 最终结论

**"只是抓诚实失误"的方案**：`model` 字段比对、`system_fingerprint` 监控、`reasoning_tokens` 单次读取——这些只能抓不用心做假的中转站，完全无法对抗蓄意欺骗。

**"对蓄意做假中转商部分有效"的方案**：LLMmap 行为指纹（95%+ 但可绕过）、TTFT 基线（物理层难伪造但噪声大）、canary probe 正确率（题库小则易预缓存）。

**"对蓄意做假中转商实质有效"的方案**：RUT 黑盒统计审计（不可检测的随机探测）+ 大题库轮换 canary（预缓存不可行）+ TTFT 物理约束（联合使用）。

**不可伪造但当前不可用**：ZK Proof / TEE（闭源旗舰模型上不可行）。

---

## 7. 借鉴到 HUAKAI juice 功能的具体可执行建议

### 7.1 立即可借鉴：Canary Probe 题库设计（参考 McNemar + Artificial Analysis）

**借鉴自**: McNemar 统计检验框架（论文 2602.10144）+ Artificial Analysis 的 benchmark 设计

**具体建议**:
- 题库规模：至少 **500-1000 题**，覆盖数学竞赛（AIME 难度）、多步推理、科学推理，分三个难度档（easy/medium/hard）分别对应 reasoning_effort=low/medium/high；
- 每次 canary probe 随机抽取 **5-8 题**（不固定），防止预缓存；
- 判定方法：McNemar 检验而非简单阈值——同一 channel 的历史正确率 baseline 已知时，McNemar 可以在 0.3% 降幅就触发置信报警；
- 题库需 **定期轮换**（建议每月更新 20%），进一步防止缓存泄露；
- **HUAKAI 差异化**：公开 canary 题库的设计原则（不公开题目）+ 统计方法论，让用户理解"HUAKAI 是怎么证明的"，建立方法论透明度。

### 7.2 立即可借鉴：RUT 防探测检测策略（参考 arxiv 2506.06975）

**借鉴自**: Rank-Based Uniformity Test

**具体建议**:
- 探测请求与真实用户请求混合（mixing strategy）：**不单独发探测批次**，而是在正常用户流量中混入标定请求，使中转商无法通过 traffic pattern 识别探测流量；
- 探测 query 不使用固定模板，而是从题库随机生成，每次 query 的措辞也略作变化（paraphrase）；
- 这一策略在 RelayRadar / Hvoy 等工具中均未实现，是 HUAKAI 的工程差异化点。

### 7.3 立即可借鉴：Hvoy AI 的 6 维度多信号联合

**借鉴自**: Hvoy AI 检测框架

**具体建议**:
- 不依赖单一信号，而是对每次请求综合评分：reasoning_tokens + thinking block 存在性 + TTFT 偏差 + canary 正确率 → 加权 juice score；
- 用 OpenRouter Auto Exacto 的 **中位数 + MAD 排名法**（而非简单平均）计算 channel 质量基线，对异常 channel 降权；
- **HUAKAI 差异化**：每个信号打分附带"该信号是否可能被伪造"的置信度说明，诚实告知用户哪个分数是硬证据、哪个只是辅助参考。

### 7.4 立即可借鉴：延迟物理约束作为"难伪造"基准锚点

**借鉴自**: TTFT 作为推理算力的物理代理

**具体建议**:
- 在 HUAKAI 的 stream 响应路径上，在用户侧记录 **client-side TTFT**（从发出请求到 headers 到达，到第一个 data: 字节）；
- 对 reasoning 模型（o1/o3/claude-opus thinking），high effort 下 TTFT 通常 5-30 秒；若实测 TTFT < 1 秒，几乎可以确认 thinking 被跳过（这是物理层证据，中转商不能伪造）；
- TTFT 基线使用 **P10-P90 分位** 而非均值（参考 OpenRouter 的 p50/p75/p90/p99 统计方式），对异常低值触发 canary 升级探测；
- 向用户展示 TTFT 历史趋势，这是用户最直观可理解的"算力信号"。

### 7.5 立即可借鉴：Mystery Shopper 匿名测评策略（参考 Artificial Analysis）

**借鉴自**: Artificial Analysis mystery shopper 政策

**具体建议**:
- HUAKAI 的 canary probe 请求**不使用区别于用户请求的特殊 API key**，避免中转商识别"这是 HUAKAI 的监控账号"；
- 探测使用与用户请求相同的 API key pool，请求头与正常用户请求完全一致；
- 这一策略被中文中转检测圈所有已知工具（RelayRadar / Hvoy）完全忽略，是 HUAKAI 的实质性优势。

### 7.6 立即可借鉴：公开方法论透明度（参考 HUAKAI F-TRUST 使命）

**借鉴自**: LMSYS 对 CI/评测方法的公开 + McNemar 论文的可重复性

**具体建议**:
- 发布 HUAKAI juice 检测方法论白皮书（不需要公开题库，但公开：统计检验框架、信号权重、触发阈值）；
- 向用户提供 **per-request juice score**（类似 Hvoy 的 6 维度打分），并附链接到方法论文档；
- 每个月发布 per-channel juice 健康报告（类似 Artificial Analysis 的 provider 比对数据），成为业界内容资产；
- **差异化**：Hvoy 声明"不是正式审计"，HUAKAI 的目标是"可提供正式可引用的核验报告 + ed25519 签名"，这是 Hvoy 不具备的可信度层。

### 7.7 中期路标：LLMmap 集成（模型替换检测）

**借鉴自**: LLMmap（USENIX Security 2025）

**具体建议**:
- LLMmap 的 8-query 模型识别适合"检测完整模型替换"（如以 Sonnet 冒充 Opus）；
- 将 LLMmap 的查询模板集成进 canary probe 库，作为"模型身份核验"子模块；
- 注意：LLMmap 对"同模型降 reasoning effort"不直接有效（行为差异不如整体替换明显），与 canary 正确率检测互补使用；
- 代码 MIT 许可，可直接参考工程实现（注意不得复制代码，需改写）。

### 7.8 避坑点

**坑 1 — 固定题库被泄露**  
RelayRadar 的 8 条标准问询是公开的，任何有心的中转商都可以针对这 8 条给出正确答案。HUAKAI 的题库必须大而多样、轮换且带随机 paraphrase。

**坑 2 — reasoning_tokens 伪造**  
`usage.completion_tokens_details.reasoning_tokens` 字段完全在 response body 里，中转商可以伪造。单独依赖此字段=只能抓懒得做假的中转站。必须与 TTFT / canary 正确率联合使用。

**坑 3 — 基线建立期无信号**  
新 channel 前 48 小时没有历史基线，无法触发降算力告警。需要做"冷启动 canary"：新 channel 接入时强制跑 20 题基线测试，才允许正式流量接入。

**坑 4 — 探测成本**  
每个 reasoning 模型的 high-effort canary probe 一次约 2000-5000 reasoning tokens，费用约 $0.05-0.20/次。每 channel 每天 48 次则 $2.4-9.6/channel/day。需要按 channel 价值分级：高价值 channel 高频探测，低价值 channel 低频。

**坑 5 — 假绿**  
中转商"平时降智、看到探测请求时给高质量回答"是最难防的攻击。RUT 的混入策略（探测请求与真实用户请求不可区分）是目前学术上最好的应对，RelayRadar/Hvoy 均未实现。

---

## 参考资料

**在线服务 / 产品**:
- Artificial Analysis: https://artificialanalysis.ai  
- OpenRouter Auto Exacto: https://openrouter.ai/docs/guides/routing/auto-exacto  
- Hvoy AI: https://www.hvoy.ai/en/  
- stampr-ai: https://www.stampr-ai.com/  
- Catchpoint LLM monitor: https://www.catchpoint.com/blog/llms-dont-stand-still-how-to-monitor-and-trust-the-models-powering-your-ai  
- Phala Confidential AI: https://phala.com/solutions/private-ai-inference  

**GitHub 项目**:
- LLMmap: https://github.com/pasquini-dario/LLMmap（311 stars，MIT）
- RelayRadar: https://github.com/AetherCore-Dev/relay-radar（27 stars，活跃）
- model-audit: https://github.com/liuxiaotong/model-audit（2 stars，MIT，活跃）

**学术论文**:
- LLMmap USENIX Security 2025: https://arxiv.org/html/2407.15847v4  
- RUT 黑盒审计 2026: https://arxiv.org/abs/2506.06975  
- McNemar 统计降级检测: https://arxiv.org/pdf/2602.10144  
- NANOZK ZK Proof 推理: https://arxiv.org/abs/2603.18046  
- HuRef NeurIPS 2024: https://proceedings.neurips.cc/paper_files/paper/2024/file/e46fc33e80e9fa2febcdb058fba4beca-Paper-Conference.pdf  
- When Same Model ≠ Same Service: https://arxiv.org/html/2605.02821  

**新闻报道**:
- 中转站降智报道（每经网）: https://www.nbd.com.cn/articles/2026-05-12/4388468.html  
- OpenRouter 可靠性评测: https://ofox.ai/blog/is-openrouter-reliable-honest-review-2026/

---

**Agent ID**: claude-sonnet-4-6 / general-purpose acdee61eb990abfcb  
**Lane**: specifier（外部资料读取 → 摘要转述，未复制代码/标识符，所有描述用不同句式转述）  
**UTC 时间戳**: 2026-05-21T10:15:00Z
