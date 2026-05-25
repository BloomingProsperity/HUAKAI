# LLM 模型降算力 / 静默替换 / 模型验真全网调研

日期：2026-05-21  
Agent：Codex  
范围：web search / public GitHub / PyPI / arXiv / 社区帖 / 商业服务页。未执行 git 操作，未 clone 外部仓库，未读取外部源码。  
可信度规则：GitHub stars 只记录在可读 GitHub 页面中看到的数值；看不到就写“未核验”。社区帖只作为“用户观察/土办法/争议线索”，不当成事实证明。

## 一、做法分类地图

### 1. 硬证明：TEE、可验证计算、签名 attestation、请求-响应绑定

核心思想：不要只看输出像不像，而是让可信执行环境、可验证计算或签名协议证明“某个请求确实由某个模型/某条推理链路处理”。这是目前最接近“会做假的中转商也伪造不了”的方向。

| 名称 | URL | 解决什么 | 检测/证明手段 | 成熟度 | 绕过性 |
|---|---|---|---|---|---|
| Are You Getting What You Pay For? Auditing Model Substitution in LLM APIs | https://arxiv.org/abs/2504.04715 | 明确定义 LLM API 模型偷换/量化/小模型替代问题 | 比较软件方法后主张 TEE 才能给模型完整性提供密码学保证；代码指向 https://github.com/sunblaze-ucb/llm-api-audit | 学术论文 + 小型研究 repo | 若 verifier 直接验证 TEE 证明，中转商不能伪造；但依赖官方/上游采用 TEE |
| AEX: Non-Intrusive Multi-Hop Attestation and Provenance for LLM APIs | https://arxiv.org/abs/2603.14283 | 多跳 API / 中转链路中，请求和输出的出处不可见 | 在 JSON API 边界附加签名 attestation，把请求、输出、流式 lineage 绑定起来 | 2026 论文 + TypeScript prototype 描述 | 中转商不能伪造可信 issuer 签名；但必须有 issuer / provider 配合 |
| IMMACULATE | https://arxiv.org/abs/2602.22700 ，代码重定向到 https://github.com/paulguoyanpei/Immaculate | 模型偷换、量化滥用、token 过度计费 | 抽样审计少量请求，用 verifiable computation 检查推理执行与计费 | 论文 + GitHub 8 stars / 1 commit / 无 release | 如果证明系统上线，中转商难以伪造；当前工程成熟度很早 |
| VeriLLM | https://arxiv.org/abs/2509.24257 | 去中心化 LLM 推理的公开可验证 | 一诚实验证者假设 + 轻量验证算法 + 激励机制 | 学术方案 | 面向去中心化网络；对普通商业 API 需改造 |
| AgentMark | https://agentmark.dev/ | AI 生成代码的模型输出 provenance | output_hash、challenge token、request_id、Ed25519 SDK 签名、跨 provider request_id 验证 | 真服务/SDK 方向，偏代码 provenance | 能证明“这段代码来自某次 API 原始响应”，不能单独证明模型未被降级，除非 provider 侧也签名 |
| sigstore/model-transparency | https://github.com/sigstore/model-transparency | 模型 artifact 供应链签名 | 签 model 文件 hash 和 identity | GitHub 项目，stars 未核验 | 证明文件没被换，不证明远端 API 实际加载了这个文件 |

结论：硬证明是唯一能靠密码学/硬件把“中转商伪造”压到最低的方向。缺点是它不是纯网关功能，需要上游模型方、TEE 基础设施或签名 issuer 配合。

### 2. 可信参考对照：token/logprob/seed 级验证

核心思想：如果有可信参考模型或可信参考 provider，同一 prompt、同一解码配置下，输出 token 或 token 概率分布应该高度一致。这个方向对 open-weight 模型特别实用，对闭源 frontier 模型受限。

| 名称 | URL | 解决什么 | 检测手段 | 成熟度 | 绕过性 |
|---|---|---|---|---|---|
| DiFR: Inference Verification Despite Nondeterminism | https://arxiv.org/abs/2511.20621 | 非确定性推理下如何验证推理是否正确 | Token-DiFR 用同 seed 的可信参考实现比对生成 token；Activation-DiFR 用压缩 activation fingerprint | 学术 + PyPI 工具生态 | 私密随机样本下很难伪造；但 closed model 没参考实现时受限 |
| token-difr | https://pypi.org/project/token-difr/ | 验证 API provider 是否运行宣称的 open-weight 模型 | temperature=0，收集输出 token，与 Fireworks / 本地 vLLM / 可信参考的 token 预测匹配率比较 | PyPI alpha，0.1.2，2025-12-27，MIT；GitHub 精确路径未在本轮页面中核验 | 若中转商识别审计流量并路由到真模型可绕过；若抽样来自真实生产流量且 prompt 私密，难度明显上升 |
| Log Probability Tracking of LLM APIs | https://arxiv.org/abs/2512.03816 | 低成本连续监控 API 是否静默变更 | 每次只请求 1 个 token，用 logprob 均值做统计检测，目标是发现微小 fine-tune 变化 | ICLR 2026 论文 | 依赖 API 暴露可用 logprob；中转可对已知探针做特殊路由 |
| Auditing Black-Box LLM APIs with a Rank-Based Uniformity Test | https://arxiv.org/abs/2506.06975 | 无 logits/weights 时验证黑盒 LLM 是否等同行为 | Rank-based Uniformity Test，对本地真模型和黑盒 API 做行为等价检验，强调低 query 和避免可识别探针模式 | ICLR 2026 方向，论文 | 比固定 benchmark 更抗绕过；仍不是密码学证明 |
| Obtaining logprobs from an LLM API | https://mattf.nl/openlogprobs.html | API 只给有限 logprobs 时如何恢复目标 token 概率 | 利用 logit bias 单 token 查询推回无偏 logprob | 技术博客 | 需要 provider 支持 logit bias/logprobs；可被限流或禁用 |

结论：这是 HUAKAI 最适合先做 MVP 的技术层。对开源/开放权重模型，可以把 token-level verification 做成“私密抽样 + 可信参考 + 长期基线”。对闭源模型，只能退化到官方 API 交叉对照、logprob drift、行为统计。

### 3. 行为指纹：主动 probe、分类器、模型家族识别

核心思想：模型在安全拒答、畸形 prompt、元信息问题、风格、特定推理题、token 偏好上有稳定差异。通过一组 probe 形成指纹，判断底层模型或模型家族。

| 名称 | URL | stars / 维护 | 解决什么 | 检测手段 | 成熟度 | 绕过性 |
|---|---:|---|---|---|---|---|
| LLMmap | https://github.com/pasquini-dario/LLMmap | 311 stars；6 commits；无 release；README 显示 LLMmap0.2 | 像 nmap 一样识别 LLM | 少量主动 query + 行为 trace 分类；默认模板覆盖 52 个 LLM | 真研究工具，MIT，能直接借鉴 | 可被识别探针后特殊路由/过滤；私有 probe 和不断更新可提高成本 |
| LLMmap paper | https://arxiv.org/abs/2407.15847 | 论文 | 黑盒 LLM-integrated app 指纹 | crafted queries，少量交互识别 42 个版本 | USENIX Security 2025 论文 | 论文声称对 system prompt/RAG/CoT 有鲁棒性，但仍非硬证明 |
| LLM Fingerprinter | https://pypi.org/project/llm-fingerprinter/ ，源码路径见 PyPI：https://github.com/litemars/LLM-Fingerprinter | stars 未核验 | 识别 GPT/LLaMA/Mistral/Gemini/Claude 等家族 | 75 个 discriminative prompts + 特征抽取 + ensemble classifier；支持 custom HTTP endpoint | PyPI 工具，偏早期 | 能识别家族，不能证明精确版本；可被对抗绕过 |
| Fingerprinting LLMs via Prompt Injection / LLMPrint | https://arxiv.org/abs/2509.25448 | 论文 | 识别 base model 及其 post-trained/quantized 变体 | 利用 prompt injection 触发稳定 token preference，黑盒/灰盒统一验证 | 学术前沿 | 对后处理鲁棒性较强；但上线需要维护私密指纹 prompt |
| A Fingerprint for Large Language Models | https://arxiv.org/abs/2407.01235 | 论文 | LLM provenance / copyright auditing | 用模型内在特征形成 fingerprint | 学术 | 多用于模型来源/版权，不等价于 API 实时验真 |
| Awesome-LLM-Fingerprinting | https://github.com/shaoshuo-ss/Awesome-LLM-Fingerprinting | stars 未核验；8 commits | 指纹论文清单 | 分类 black-box / targeted / side-channel 等方法 | 资料库 | 不是检测器 |

这类还包括 TRAP、ProFLingo、RoFL、RAP-SM、LLM-FIN、inter-token timing / network traffic side-channel 等论文线索，见 Awesome-LLM-Fingerprinting 清单。它们的共同问题是：能显著提高造假成本，但只要探针固定、公开、可被识别，精明中转商就能“探针流量上真模型，普通流量上假模型”。

### 4. 黑盒统计等价与审计框架

| 名称 | URL | stars / 维护 | 解决什么 | 检测手段 | 成熟度 | 绕过性 |
|---|---:|---|---|---|---|---|
| Model Equality Testing | https://github.com/i-gao/model-equality-testing | 17 stars；3 commits；无 release | 判断黑盒 API 与参考分布是否相同 | 用户任务分布上的 two-sample test；MMD/Hamming/permutation p-value；附 1.6M completions 数据集 | 小型研究包 | 比 benchmark 更贴近用户任务；仍可能被审计路由绕过 |
| LLM API Audit | https://github.com/sunblaze-ucb/llm-api-audit | 10 stars；12 commits；MIT；无 release | 系统比较模型替换检测方法 | classifier、identity prompting、model equality testing、benchmark、logprobs | 研究 repo | 它自己也指出纯软件方法在强对手下不可靠 |
| Real Money, Fake Models | https://arxiv.org/abs/2603.01919 | 论文 | 系统审计 shadow APIs 是否真实 | 对 utility、safety、model verification 做多维 audit | 2026 论文 | 适合当威胁模型和实验设计参考；不是现成工具 |
| Auditing Pay-Per-Token in LLMs | https://openreview.net/pdf/4e561a503ad25bc9da88fd439635399dc382fcda.pdf | 论文 | token 计费误报/滥报审计 | token accounting audit | 邻近问题 | 可纳入 HUAKAI 计费验真，不直接证明模型身份 |

结论：这类是 HUAKAI “证据评分”的骨架。它不能给绝对证明，但可以形成持续风险分、置信区间、异常告警和 provider 排名。

### 5. Benchmark / regression / canary eval

| 名称 | URL | stars / 维护 | 解决什么 | 检测手段 | 成熟度 | 绕过性 |
|---|---:|---|---|---|---|---|
| promptfoo | https://github.com/promptfoo/promptfoo | 21.4k stars；8,651 commits；MIT；README 说明已并入 OpenAI 但保持开源 | LLM app eval、red teaming、CI/CD 回归 | 自定义测试集、provider 对比、assertions、red-team | 成熟工程工具 | 能测质量回归，不能单独证明模型身份 |
| OpenAI Evals | https://github.com/openai/evals | 18.5k stars；691 commits | OpenAI/LLM 系统 eval 框架和 registry | 自定义 eval + benchmark registry | 成熟 | 固定 eval 可被 overfit；适合内部 canary |
| EleutherAI lm-evaluation-harness | https://github.com/EleutherAI/lm-evaluation-harness | 12.6k stars；4,023 commits | few-shot 模型评测 | MMLU、HellaSwag 等大量任务 | 成熟标准工具 | benchmark 型，成本高、易被识别，不适合单独验真 |
| DriftBench | https://driftbench.ai/ | stars 不适用 | 每日检测 LLM provider 漂移 | deterministic scoring、速度指标、日常 benchmark | 真服务/早期站点 | 固定题库可能被识别；适合趋势监控 |
| LLM Output Drift: Cross-Provider Validation & Mitigation for Financial Workflows | https://arxiv.org/abs/2511.07585 | 论文 | 金融任务的跨 provider 输出漂移 | greedy decoding、fixed seeds、RAG/JSON/SQL invariant、dual-provider validation | 应用论文 | 适合行业任务验真，不是通用身份认证 |

结论：benchmark/canary 是最低门槛，适合 HUAKAI 快速上线“质量持续监控”。但要把公开题库、私有题库、真实生产抽样分开；否则中转商很容易针对公开题库做 routing。

### 6. API 协议 / 功能能力探针

核心思想：真模型/真 provider 常有特定协议能力，例如 structured outputs、tool calling、vision、prompt logprobs、reasoning tokens、thinking budget、context length、JSON schema 约束、SSE 格式、错误码和 usage 字段。便宜代理或假模型往往只模拟 chat completions 表面。

| 来源 | URL | 方法 | 成熟度 | 绕过性 |
|---|---|---|---|---|
| LINUX DO：结构化输出保真检测 | https://linux.do/t/topic/172733 | 用 gpt-4o-2024-08-06 / gpt-4o-mini 的 structured outputs 特性测试中转是否支持；作者也提醒可能出现 mini 冒充 4o | 社区方法帖 | 很容易被知道后补实现或特殊路由；但能抓低级假站 |
| LINUX DO：API 中转逆向/混用判断 | https://linux.do/t/topic/57660 | 讨论 function calling、模型混用、逆向通道、价格倍率、模型风格差异 | 社区经验 | function calling 不可靠；中转可只对带工具调用请求走真渠道 |
| LINUX DO：Claude 真伪常规方法 | https://linux.do/t/topic/2040321 | 社区讨论 Claude/Opus 鉴别、skill/安全分词器等线索 | 社区经验 | 细节依赖具体模型版本，且容易过期 |
| V2EX：GPTAPI 中转模型不符 | https://www.v2ex.com/t/1121874 | 用户用官方/字节/SiliconFlow 对照输出，怀疑 4o 被 4o-mini 或别的模型替代 | 投诉/观察 | 单例输出不能证明，但能触发进一步审计 |

结论：协议探针非常适合 HUAKAI 做“廉价第一层过滤”。它抓不住精明对手，但能抓大量偷懒实现、逆向网页壳、one-api 表面兼容站。

### 7. Catalog / marketplace / 元数据漂移监控

| 名称 | URL | 解决什么 | 方法 | 成熟度 | 绕过性 |
|---|---|---|---|---|---|
| Provider Drift / Holy Lab | https://holyai.me/about/provider-drift | OpenRouter 上 provider 价格、上下文、量化、可用性、移除等静默变化 | 每 6 小时抓取 OpenRouter model/provider metadata 并做 diff；计划加入 deterministic prompt fingerprinting | 真站点，方向非常贴 HUAKAI | 只能证明公开 metadata 变了，不能证明实际 runtime |
| OpenRouter State of AI / OpenRouter usage context | https://arxiv.org/abs/2601.10088 | 了解多 provider 市场真实流量和使用模式 | 用 OpenRouter 大规模流量做实证分析 | 论文/市场研究 | 不是验真工具 |
| llm-registry | https://github.com/yamanahlawat/llm-registry | 管理模型价格、能力、限制 | 模型 registry / metadata | GitHub repo，stars 未核验 | 元数据可信度取决于维护者，不能证明 runtime |

结论：对 HUAKAI 来说，这一层不解决“真假”，但解决“对外宣称变化、价格变化、上下文缩水、量化标注变化”。它应该和行为检测分开建模。

### 8. 生产观测 / 质量漂移商业服务

这类大多不是“模型身份验真”，而是“生产 LLM 质量、成本、延迟、hallucination、trace、eval、drift 监控”。它们能提供 HUAKAI 的 Ops/UI/告警参考。

| 名称 | URL | 解决什么 | 方法 | 成熟度 | 绕过性 |
|---|---|---|---|---|---|
| Quelm | https://quelm.ai/ | provider silent updates、cross-provider drift、live traffic monitoring | LLM-as-judge、客户侧流量聚合、自动回归集 | 早期服务/early access | 质量监控，不是身份硬证明 |
| Arize Phoenix | https://github.com/Arize-ai/phoenix | LLM observability、eval、production monitoring | traces、evals、drift dashboards | 成熟开源/商业生态；搜索页第三方显示约 9.7k stars，未由 GitHub 页面核验 | 能发现行为漂移，不证明模型没被换 |
| DeepRails | https://deeprails.com/ | hallucination detection/correction、monitor | quality metrics、audit logs、ReGen/FixIt | 商业服务 | 输出质量层，非身份层 |
| Deadpipe | https://www.deadpipe.com/ | prompt observability、cost/quality drift | wrap SDK、统计学习、auto-optimization | 商业/早期 | 可检测回归，不能证明模型 |
| AgentLens | https://www.agentlens.one/ | enterprise LLM observability | quality/cost/risk dashboard、window drift | open-core/商业页 | 质量层 |
| Watchlog GenAI Monitoring | https://watchlog.io/products/gen-ai-monitoring | hallucination、PII、prompt injection、quality drift | 全 provider 交互监控 | 商业服务 | 质量层 |
| Langfuse | https://langfuse.com/ | LLM tracing/evals/prompt management | trace + eval + prompt/version management | 成熟开源/商业，社区常提 | 质量层 |
| LangSmith | https://www.langchain.com/langsmith | LangChain 生态 tracing/eval/observability | traces、datasets、evals | 成熟商业 | 质量层 |
| Braintrust | https://www.braintrust.dev/ | evals、prompt/version、observability | dataset eval + logs | 成熟商业 | 质量层 |
| Maxim AI | https://www.getmaxim.ai/ | AI eval / observability / simulation | evals、monitoring、agent testing | 商业 | 质量层 |
| WhyLabs LangKit | https://github.com/whylabs/langkit | LLM text signals and monitoring | prompt/response quality、安全、relevance signals | 开源工具，stars 本轮未核验 | 质量层 |
| Evidently | https://github.com/evidentlyai/evidently | ML/LLM eval and monitoring | drift reports、test suites、dashboards | 成熟开源/商业 | 质量层 |
| Giskard | https://github.com/Giskard-AI/giskard | LLM testing / vulnerability scanning | test suites、scan、evaluation | 成熟开源/商业 | 质量层 |

结论：这些平台成熟，但解决的是“输出和业务是否变坏”，不是“供应商是否偷换模型”。HUAKAI 可借鉴它们的 trace、eval suite、drift alert、dashboard，但验真核心仍要另建。

### 9. Side-channel / 性能 / 运行时迹象

| 名称 | URL | 方法 | 成熟度 | 绕过性 |
|---|---|---|---|---|
| detLLM | https://github.com/tommasocerruti/detllm | 本地/可控后端的 deterministic-mode 检查、run/batch variance、first divergence repro pack | GitHub 18 stars；60 commits；Apache-2.0 | 对远端中转只能当可复现/本地 baseline 工具；不能证明远端身份 |
| LLMs Have Rhythm 等 side-channel 论文线索 | https://github.com/shaoshuo-ss/Awesome-LLM-Fingerprinting | inter-token time、network traffic、memory usage pattern | 论文线索 | 延迟/吞吐可被排队、限速、缓存、流式策略污染；单独弱 |
| 社区速度/TTFT/TPS 对比 | https://driftbench.ai/ | TTFT、tokens/sec、total latency | 工程可做 | 很容易被硬件、负载、路由、限速影响；只能当辅助信号 |

结论：side-channel 不能单独判真，但对“降算力/降档/量化/换供应商”有辅助价值。建议只作为多信号模型中的低权重特征。

## 二、GitHub / 工具清单

| Repo / 包 | 真实 URL | stars | 是否维护/状态 | 简述 | 可被绕过性 |
|---|---|---:|---|---|---|
| pasquini-dario/LLMmap | https://github.com/pasquini-dario/LLMmap | 311 | README 显示 0.2；6 commits；无 release | 主动行为指纹识别 LLM | 固定探针可被特殊路由 |
| sunblaze-ucb/llm-api-audit | https://github.com/sunblaze-ucb/llm-api-audit | 10 | 12 commits；无 release；README 仍提示实验 rerun | 模型替换审计方法集合 | 研究框架，非生产系统 |
| i-gao/model-equality-testing | https://github.com/i-gao/model-equality-testing | 17 | 3 commits；无 release；含 PyPI 安装说明和数据集 | two-sample model equality test | 强对手可审计路由 |
| paulguoyanpei/Immaculate | https://github.com/paulguoyanpei/Immaculate | 8 | 1 commit；无 release | verifiable computation 审计代码 | 若协议部署则强，当前很早 |
| tommasocerruti/detllm | https://github.com/tommasocerruti/detllm | 18 | 60 commits；有 PyPI quickstart；Apache-2.0 | deterministic inference 检查和 repro pack | 本地可靠性工具，不是远端验真 |
| promptfoo/promptfoo | https://github.com/promptfoo/promptfoo | 21.4k | 8,651 commits；活跃；MIT | LLM eval / red-team / CI | 质量检测，不是身份硬证明 |
| openai/evals | https://github.com/openai/evals | 18.5k | 691 commits | eval framework / registry | benchmark 可被 overfit |
| EleutherAI/lm-evaluation-harness | https://github.com/EleutherAI/lm-evaluation-harness | 12.6k | 4,023 commits；README 有 2025/12 更新 | 标准 LM benchmark harness | 成本高，非身份验证 |
| litemars/LLM-Fingerprinter | https://github.com/litemars/LLM-Fingerprinter | 未核验 | PyPI 可安装，README 说明 custom backend | 75 prompt 黑盒家族指纹 | 家族级，非硬证明 |
| token-difr | https://pypi.org/project/token-difr/ | GitHub 未核验 | PyPI 0.1.2，alpha，MIT | open-weight provider token-level verification | 对私密随机流量强；对闭源弱 |
| shaoshuo-ss/Awesome-LLM-Fingerprinting | https://github.com/shaoshuo-ss/Awesome-LLM-Fingerprinting | 未核验 | 8 commits | 指纹论文清单 | 资料库 |
| sigstore/model-transparency | https://github.com/sigstore/model-transparency | 未核验 | GitHub repo | 模型 artifact 签名 | 不证明 API runtime |
| yamanahlawat/llm-registry | https://github.com/yamanahlawat/llm-registry | 未核验 | GitHub repo | 模型能力/成本 registry | 元数据，不是 runtime |
| model-sentinel | https://pypi.org/project/model-sentinel/ | 不适用 | PyPI | Hugging Face remote model 文件变化/安全扫描 | 模型文件供应链，不是 API 验真 |
| cmvk | https://pypi.org/project/cmvk/ | 不适用 | PyPI | output drift 数学 kernel | 本轮未能核验项目深度 |

## 三、中文中转社区发现

| 来源 | URL | 他们怎么测 | 可信度 |
|---|---|---|---|
| LINUX DO：怎么判断中转站 GPT5.4 是否降智 | https://linux.do/t/topic/1707314 | 问训练数据截止日期；用户发现多个站回答一致 | 低：自我认知/截止日期最容易被 system prompt 改写 |
| LINUX DO：不同中转站 gpt-5.4 自我认知不一样 | https://linux.do/t/topic/1719619 | 要求回答后输出模型名称，观察 Model: GPT-5 Codex / o3 / assistant 等差异 | 低到中：能暴露混乱 prompt，但不能证明真伪 |
| LINUX DO：中转站逆向、混用判断 | https://linux.do/t/topic/57660 | 价格倍率、gpt-4-all、function calling、模型风格、第三方壳来源 | 中：对商业动机和低级掺水很有用，技术证明弱 |
| LINUX DO：结构化输出验真 | https://linux.do/t/topic/172733 | 用 gpt-4o 系列 structured outputs 能力做 API feature probe | 中：抓协议假兼容有效；可被补实现/特殊路由 |
| LINUX DO：如何辨别中转站真正模型 | https://linux.do/t/topic/2085210 | 用户已试过问模型名/截止日期，认为被 system prompt 限制；讨论鉴别工具 | 中：说明社区已意识到 self-ID 无效 |
| LINUX DO：第三方 Claude 真伪 | https://linux.do/t/topic/2040321 | Claude/Opus 常规鉴别、skill/security tokenizer 等 | 中：适合做模型特定 probe，需持续更新 |
| LINUX DO：Cursor 中转 thinking budget/真假比例讨论 | https://linux.do/t/topic/1898040 | 社区怀疑 playground 与实际环境不同、thinking budget 被 cap、真假混流 | 低到中：是用户观察/怀疑，但 threat model 很重要 |
| V2EX：GPTAPI 中转模型不符 | https://www.v2ex.com/t/1121874 | 对比官方/字节/SiliconFlow 输出，认为 4o 像 4o-mini、DeepSeek-v3 差异大 | 中：不是统计证明，但说明用户投诉形态 |
| V2EX：ComeU 中转广告 | https://v2ex.com/t/1213154 | 商家明确宣传“不混卖、不降智” | 市场信号：说明“保真/不降智”已成卖点 |

中文社区的土办法可以归纳为：问自我身份、问训练截止日期、用知名难题/数学/代码题、比较官方输出、测 structured outputs/tool calling/vision/上下文、看价格倍率和渠道声明、看 thinking 是否被压、看模型自报是否混乱。真正抗绕过的很少。

## 四、英文社区发现

| 来源 | URL | 内容 | 启示 |
|---|---|---|---|
| Reddit LocalLLaMA：How to Check Models Authenticity | https://www.reddit.com/r/LocalLLaMA/comments/1ioe1ps | 用户明确说“问模型名”不可靠；有人用复杂 NYT Connections 题识别 o1 一类模型 | 社区也从 self-ID 转向私有能力 probe |
| Reddit AIEval：How to do LLM identification | https://www.reddit.com/r/AIEval/comments/1qnwcr4 | 讨论不能信 endpoint metadata 或 self-ID；logprobs 不一定可用 | 真实需求存在，但方法不统一 |
| Reddit LocalLLaMA：OpenRouter provider 选择 | https://www.reddit.com/r/LocalLLaMA/comments/1mk4kt0 | 讨论低价 provider、质量验证和 degraded output 监控 | marketplace 需要 provider 级质量评分 |
| Reddit LocalLLaMA：第三方 provider downgrade 讨论 | https://www.reddit.com/r/LocalLLaMA/comments/1nqkx7o | 讨论 provider 量化/precision 标注和质量 | quantization disclosure 是关键 metadata |
| Reddit openrouter：Provider Validator | https://www.reddit.com/r/openrouter/comments/1meh33x | 用户做 provider validator 连接 OpenRouter provider | 工具化需求出现，但本轮未读到完整 repo |
| Reddit：tracking every LLM API call | https://www.reddit.com/r/learnmachinelearning/comments/1ssp9uk | 评论提到 ground-truth labels、held-out task、LLM judge、user signals、embedding input drift | 生产质量监控不能只靠模型身份 |
| Reddit：cheap Chinese proxy sellers | https://www.reddit.com/r/tech_x/comments/1tfm69f/chinese_students_are_buying_gpt5455_and_claude/ | 讨论低价代理无法知道真实模型 | 社区风险认知强，但多为讨论 |

本轮没有核验到可引用的 Hacker News 或 X/Twitter 原帖；不在报告中编造 HN/X 结论。Model Equality Testing repo 有 Twitter announcement 链接，但未能在本轮打开核验内容。

## 五、总结问题回答

### 1. 这个方向全世界一共有几类做法？代表是谁？

1. 硬证明 / attestation：TEE、AEX、IMMACULATE、VeriLLM、AgentMark、sigstore/model-transparency。
2. 可信参考 token/logprob 对照：DiFR、token-difr、Log Probability Tracking、RUT。
3. 行为指纹 / active probing：LLMmap、LLMPrint、LLM-Fingerprinter、A Fingerprint for LLMs、TRAP/ProFLingo/RoFL 系列。
4. 黑盒统计等价：Model Equality Testing、llm-api-audit。
5. Benchmark / canary / regression eval：promptfoo、OpenAI Evals、lm-evaluation-harness、DriftBench。
6. API feature / protocol conformance probe：structured outputs、tool calling、vision、context、usage、logprob、reasoning/thinking 字段。
7. Provider catalog / metadata drift：Provider Drift、llm-registry、OpenRouter metadata 监控。
8. Production observability / quality drift：Phoenix、LangSmith、Langfuse、Braintrust、Maxim、Quelm、DeepRails、Watchlog、WhyLabs LangKit、Evidently、Giskard。
9. Side-channel / performance：inter-token timing、TTFT/TPS、batch determinism、detLLM。
10. 社区启发式：自我认知、截止日期、难题、价格倍率、官方对照、截图曝光。

### 2. 公认“会做假的中转商也伪造不了”的检测手段有哪些？

严格说，只有“验证者直接检查可信根签发的证明”接近不可伪造：

- TEE remote attestation：证明特定代码/模型 artifact 在可信硬件中执行。
- Verifiable computation / proof：证明某批抽样推理步骤按声明执行。
- Provider/issuer 签名的 request-output attestation：签名绑定 request hash、response hash、model id、runtime claim、时间戳、stream chunks。
- Model artifact transparency：证明模型文件 hash 属于某个发布者；必须与 runtime attestation 组合才有意义。

其他方法都只能提高造假成本：

- 私密随机 token/logprob 对照：中转商不知道哪些生产请求会被审计时，很难长期假装；但如果能识别审计流量，可以路由真模型。
- 私有行为指纹和不断轮换的 canary：能抓大量混用/降级，但不是密码学证明。
- API feature probe：能抓低级假兼容，不能防精明伪造。
- latency/TTFT/吞吐/context window：都是辅助信号，容易受噪声和人为限速影响。

### 3. 有没有成熟到能直接用/借鉴的方案？还是整个方向很早期？

分层看：

- 生产 eval/observability 很成熟：promptfoo、OpenAI Evals、lm-evaluation-harness、Phoenix、Langfuse、LangSmith、Braintrust、Evidently、Giskard 都能直接借鉴。
- “模型身份验真”仍早期：LLMmap 可跑，Model Equality Testing 可做统计测试，token-difr 已有 PyPI alpha，但都不是完整商业级反作弊系统。
- “不可伪造证明”更早期：TEE/AEX/IMMACULATE/VeriLLM 是正确终局，但需要上游 provider 配合或重构推理链路。
- 商业服务正在冒头：Provider Drift、DriftBench、Quelm 等方向贴近需求，但多数还偏早期/营销/特定 marketplace，不是通用标准。

### 4. HUAKAI 最值得吃透的 top 5

1. DiFR / token-difr：做 open-weight provider 验真的最直接路线。HUAKAI 可以先做“私密随机抽样 + 可信参考 + token match / probability score”。
2. RUT + Log Probability Tracking：做低成本、长期、黑盒 drift 监控，尤其适合 provider/模型版本每日巡检。
3. LLMmap + LLMPrint：建设 HUAKAI 私有行为指纹库，覆盖 closed models、model family、假4/假 Opus/假 reasoning 检测。
4. Model Equality Testing + llm-api-audit：把统计检验、benchmark、identity prompting、logprob、分类器放进同一风险评分框架，避免单一信号误判。
5. AEX / IMMACULATE / TEE：作为长期“硬证明”路线，设计 HUAKAI 的 attestation 字段、request/response hash、provider proof 接口，等待上游生态成熟。

补充：Provider Drift 值得单独做成 HUAKAI Ops 模块，监控价格、上下文、量化、可用性、模型移除、provider routing 变化；它不是验真核心，但非常适合运营和风控。

## 六、HUAKAI 可落地分层建议

第一层：低成本协议探针。检测 structured outputs、tool calling、vision、context、usage、reasoning token、SSE、错误码、logprobs、seed、response_format。目标是抓低级假站。

第二层：私有 canary benchmark。每个 provider/model 每日跑小样本；题库分公开回归、私有回归、真实生产抽样三类。禁止只靠公开 benchmark。

第三层：行为指纹。引入 LLMmap/LLM-Fingerprinter 思路，维护模型家族和版本指纹。探针必须私密、轮换、随机混入正常流量。

第四层：token/logprob 对照。对 open-weight 模型，使用本地 vLLM 或可信 reference provider 做 token-level score；对闭源模型，做 first-party official API 对照和 logprob drift。

第五层：反审计规避。检测中转商是否“测试流量真、生产流量假”：把审计请求伪装成普通客户请求、随机延迟判定、按账号/地区/时间/负载切片对比。

第六层：硬证明预留。HUAKAI API 响应 schema 预留 `provider_attestation`、`request_hash`、`response_hash`、`model_claim_hash`、`issuer`、`signature`、`tee_quote`、`proof_url` 等字段，未来接 AEX/TEE/IMMACULATE。

## 七、风险和空白

- GitHub stars 是 2026-05-21 抓取时页面显示值，动态变化。
- 部分 PyPI 包未能核验 GitHub star 和活跃度，已标“未核验”。
- 社区帖中出现 GPT5.4、GPT5.5、Claude 4.x 等称呼，按原帖记录，不代表官方模型命名真实。
- 本轮未核验 Hacker News / X/Twitter 的具体原帖，不编造结论。
- 未读取任何外部源码；所有项目只基于 README、PyPI、arXiv、搜索结果、服务页和社区帖做行为层摘要。

## Owner 中文总结

本轮真实观察到：学术界已经形成硬证明、token/logprob 对照、行为指纹、黑盒统计、生产漂移监控五条主线；工程界成熟的是 eval/observability，不是模型身份验真；中文中转社区大量使用自我认知、截止日期、structured outputs、官方对照、价格倍率等土办法，但抗绕过能力弱。合理推断是：HUAKAI 应先做多信号风险评分和私有随机审计，不要承诺“绝对验真”；真正不可伪造要等 TEE/AEX/verifiable computation 或 provider 签名生态。Open questions：闭源 frontier 模型缺乏可信参考；官方是否愿意提供 request-output attestation；中转商识别审计流量后的攻防样本还需要 HUAKAI 自己采集。
