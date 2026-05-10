# AI 网关市场独立调研 — Claude (sonnet) Lane

**日期**: 2026-05-09
**Lane**: claude (sonnet via general-purpose Agent, 平行 lane)
**对应 Codex lane**: docs/research/2026-05-09-market-research-codex.md（写作时未读取，按 CLAUDE.md #10）
**调研方法**: WebSearch + WebFetch；不读 8 ref repo 源码；每条断言带 URL + access date (2026-05-09)
**时间盒**: ~30 min

---

## TL;DR (5 sentences)

1. **市场已并购转折**: Portkey 被 Palo Alto Networks 收购 (2026-04-30)、Helicone 被 Mintlify 收购 (2026-03-03)，独立 AI 网关赛道头部正在被网络安全 / docs 平台并购吃掉，留下的独立玩家是 Vercel、Cloudflare、TrueFoundry、Kong、LiteLLM、OpenRouter。
2. **OpenRouter 是赛道最大单点赢家**: $50M ARR (early 2026)、5% markup、150K+ MAU、200K+ apps、$100M+ 年化 inference spend；其本质是中性聚合 + 5% 价差，证明"做账+route"在 SaaS 端是可行 unit economics。
3. **HCSF 选型市场答案明确**: OpenAI Chat Completions / Compatible 是事实标准 (vLLM / SGLang / BentoML / 几乎所有第三方均默认提供)，Anthropic Messages 是次大公民 (claude-code / cline / cursor 默认协议、Claude API 80% 来自企业)，Gemini / Responses API 是第三梯队；HUAKAI 应以 **OpenAI Chat Completions 为 canonical (LiteLLM 路线)**，Anthropic Messages 为强力第二 native (Portkey 路线为辅)。
4. **HUAKAI 真正的市场机会不在 enterprise**：enterprise 端 Kong/Portkey/TrueFoundry 已饱和（per-service licensing $50K+/yr）；机会在 **(a) 中国订阅级账号合规出海中转 (AICodeMirror 10K+ users / 200+ institutional clients 已证明 PMF, Anthropic 实名风控让黑灰市重新洗牌)** 和 **(b) AI agent / coding tool 自带账号路由 (Cline 5M VS Code 装机 / Cursor $2B ARR / Claude Code 18% 工作 adoption)** 两层。
5. **axis-3 (协议转换) 价值 confirmed 但不是孤立卖点**: 是 enterprise 网关的入场票，不是差异化护城河；HUAKAI 的护城河必须叠加 PASR 账号池 + 反风控伪装 + 中国侧合规三件套，单独 "再写个 multi-vendor adapter" 已经被 LiteLLM (46.2K stars) / OpenRouter ($50M ARR) 商品化。

---

## Q1 客户分层（2026 年 5 月）

候选层验证 + 修正后的 6 层：

### L1 个人开发者 / Vibe Coder
- **规模**: JetBrains 2026 调查 — 74% 全球开发者已采用专门 AI dev 工具；Cursor 1M+ paying / 1M DAU；Cline 5M VS Code installs；Claude Code 18% 工作端 adoption。
- **付费意愿**: $20-200/mo 订阅 (Cursor $20、Claude Pro $20、Max $100-200)；个人订阅替代 API token 的诉求强烈 (因为 token 价高且不可预测)。
- **关心**: 模型质量、价格可预测、"原生工具"体验 (claude-code / codex CLI 直连)、不被封号、绕地区限制。
- **HUAKAI 切入**: 个人版自部署 + Personal Edition 卖账号池服务 — 与 sub2api 同生态位，但 PMF 已经被 sub2api / AICodeMirror / AnyRouter 证明。

### L2 中小团队多 vendor 整合（5-50 人）
- **规模**: Cursor "Half of Fortune 500 using"、enterprise 占 45-60% 收入；JetBrains 调查 — GitHub Copilot 工作 adoption 29%、Cursor 18%、Claude Code 18%。
- **付费意愿**: $100-1000/mo 工具订阅 + token 报销，预算敏感但更看 reliability。
- **关心**: 统一计费、用量配额、cost attribution per team、SSO、guardrails、prompt 模板复用。
- **HUAKAI 切入**: 自部署 SaaS edition；Portkey ($5K+/mo enterprise) 和 TrueFoundry ($499 Pro / Enterprise VPC) 已占据，但价格点偏 enterprise — HUAKAI 可在 $50-500/mo 价位以"7-vendor 自动 fallback + PASR 账号池"做差异化。

### L3 AI Agent / Coding Tool 厂商（B2B but small）
- **规模**: Cline、Cursor、Claude Code、Continue、Aider、Windsurf、若干新兴 coding agent；外加 LangChain (97K stars / 50K+ production apps) / LangGraph (34.5M monthly downloads) / CrewAI (45.9K stars / 5.2M downloads) / LlamaIndex (40K+ stars) 等 agent framework 自身需要 backend gateway。
- **付费意愿**: 自带 backend；如果有更便宜的 multi-vendor route + 用户自带订阅入口 (BYOK) 会切换 — Cline 已经默认让用户带自己 API key 或 Claude Code subscription credentials。
- **关心**: 协议保真度（不能丢工具调用元数据）、stream chunk 形态稳定、最低 latency、$ per request 透明。
- **HUAKAI 切入**: 这层是 **HCSF 协议保真**最关键的客户 — 他们直接 hit 你的 endpoint，写错协议立刻报 bug；如果 HUAKAI 能成为 "Cline 推荐 backend" 之一，比卖给 enterprise 容易得多。

### L4 中国中转 API 用户（B2C + B2B 混合）
- **规模**: AICodeMirror 公开 "10,000+ registered users / 200+ institutional clients" (SCMP 2026-04 报道)；中转站行业玩家含 sub2api / one-api / new-api / OneHub / DoneHub / Veloera / AnyRouter / 4.0AI / Sub2API-CRS2 等 10+ 个互打项目；中文社区在 linux.do / 知乎 / B站 / SegmentFault 持续讨论中转评测。
- **付费意愿**: 充值制为主，常见汇率 ¥2.4-7/$ (官方 ¥7/$)，对应 "0.3-0.93 折"区间；Claude Sonnet 4.6 中转价 ¥2.4-7.5/M input；Opus 反代 ¥1.5/7.5 per M token。
- **关心**: 不跑路、不封号、汇率折扣、原生 CLI (claude-code / codex / antigravity) 能直连、绕境内网络限制。
- **政策风险高**: Anthropic 2026-04-17 实名验证 + Bloomberg 2026-04 OpenAI/Anthropic/Google 联合反 China model copying；中国侧《网络安全法》2026-01-01 修订加 AI 合规条款；CAC 算法备案 + 标签法规 2025-09-01/11-01 生效。
- **HUAKAI 切入**: 这是 **HUAKAI 真正的 PMF 重叠区**。8 ref repo 中的 sub2api / one-api / new-api / all-api-hub 全部在打这个市场。HUAKAI 如果不在这层做强差异化（PASR 跨账号智能 + 反风控伪装 + 合规出海设计），与 sub2api 几乎没有定位差。

### L5 Enterprise / Fortune 500
- **规模**: Anthropic 企业占收入 80% / $30B ARR (2026-04)；OpenAI 企业占 40% 上升中 / $25B ARR；Cursor 半数 Fortune 500；Portkey 200+ enterprises / 400B+ tokens daily；Gartner 预测 2028 年 70% 多模型工程团队会用 AI gateway (vs 2025 的 25%)。
- **付费意愿**: $50K-500K+/yr，Kong AI Gateway per-service licensing $50K+；TrueFoundry self-hosted $600-1000/mo + control plane；Portkey enterprise $5K+/mo。
- **关心**: SOC2 / VPC / air-gapped 部署、审计、合规、数据驻留、guardrails、SSO、Zero-trust。
- **HUAKAI 切入**: **不建议主打**。Portkey 被 Palo Alto Networks 吃掉 = 网络安全大厂将免费打包送给 AI 安全客户；Kong / TrueFoundry / Helicone (现 Mintlify 旗下) / Cloudflare AI Gateway / Vercel AI Gateway 已 4-5 家正面血战。HUAKAI 没 sales 团队、没 SOC2、没合规牌就硬冲 enterprise 是死路。

### L6 LLMOps 平台客户（observability / eval / experiment）
- **规模**: Helicone (现 Mintlify, YC W23, 10K req/mo 免费起步)、Langfuse、Arize、Galileo、Weights & Biases；Helicone 已被并购说明该层独立赛道窗口在收窄。
- **付费意愿**: $99-2000/mo，按 log 量计费。
- **关心**: trace 完整性、跨 provider session 拼接、replay、prompt 实验。
- **HUAKAI 切入**: 不是主战场；HUAKAI 应做"够用的 observability"作为捆绑送，而非独立 obs 产品。

**修正候选 vs 实际**:
- 你给的 6 候选基本对，但 "AI agent 框架开发者" 应拆为 **L3 (coding agent / IDE 工具厂商)** 与 **agent framework 库（LangChain etc.）**；后者自身不付钱给你，但他们的下游用户会，所以应通过 L1/L2 间接触达。
- 漏了一层：**SaaS 应用厂商（CRM/CS/工单 plugin AI）**，他们既不是 dev tool 也不是 enterprise IT，介于 L2 和 L5 之间，使用模式更接近 L2 但 token 量更大；2026 年这层在快速涌现 (per Vercel AI Gateway "$500-5K+/mo" 用例)。

---

## Q2 竞品现状（2026 年 5 月活跃）

### A. 商业 SaaS（聚合 + 路由）

| 产品 | 定位 | 付费模式 | 规模指标 | 最近 6 个月动作 | 差异化 |
|------|------|---------|----------|----------------|--------|
| **OpenRouter** | 中性多 vendor 路由 + 计费 | 5% markup on inference spend (5.5% 标准支付 / 5.0% crypto) | $50M ARR (early 2026)；2.5M users；8.4T tokens/mo；$100M+ inference spend | 在 raise $120M @ $1.3B valuation；新增 "Chinese model dominance" (MiniMax M2.5 / Kimi K2.5 蹿升) | 中性 + crypto pay + 海量 model coverage；最大 win 条件 |
| **Portkey** | Production AI control plane | $5K+/mo enterprise；按 logs 计费 | 200+ enterprises / 400B+ tokens daily / 45 employees / $5M ARR (June 2024 数据) / $18M raised | **2026-04-30 被 Palo Alto Networks 收购**，将成 Prisma AIRS 的 AI Gateway | 已退出独立赛道；网络安全捆绑 |
| **Helicone** | LLM observability + AI Gateway | Free 10K req/mo 起步、startup discount | YC W23、pre-seed | **2026-03-03 被 Mintlify 收购** | 已退出独立赛道；docs 平台捆绑 |
| **TrueFoundry** | Enterprise AI control plane | Pro $499、Enterprise VPC（自托管 $600-1000/mo + control plane） | n/a 公开数字 | 持续发竞品 pricing 分析博文 | 主打 self-hosted + air-gapped |
| **Kong AI Gateway** | API gateway 加 AI 插件 | per-service licensing，$50K+/yr 起 | n/a | AI Rate Limiting Advanced 等高级插件加价 | 老牌 API gateway 厂商把 AI 当扩展 |
| **Cloudflare AI Gateway** | Edge AI 路由 + obs | $0.30/M extra requests + $0.02/M CPU-ms；$8/mo 典型；2026 新增 Unified Billing | 不公开 | 2026 新增 Unified Billing — 第三方模型用量直接走 Cloudflare 账单；Workers Paid 含 1M logs/mo | 价格极低 / Edge 部署 / CF 生态绑定 |
| **Vercel AI Gateway** | 部署平台内嵌 AI 路由 | $5/mo 免费额度 + 按 provider list price 走，无 markup；2026-04-02 GA | 不公开 | 2026-04-02 GA、2026-02-26 价目更新；BYOK 无 markup | 部署平台绑定 / 给 Next.js 生态送 |
| **Hugging Face Inference Providers** | 开源 + 200+ 模型聚合 | $0.03-80/hr Inference Endpoints；provider list price + Hub 生态 | n/a 财务 | Replicate 是首批合作 provider | 开源生态原生 / model catalog 大 |

### B. 开源自部署

| 项目 | stars | 定位 | License | 状态 |
|------|-------|------|---------|------|
| **LiteLLM (BerriAI)** | 46.2K | OpenAI 协议为 canonical 的 multi-vendor proxy | MIT (含 enterprise 7-day 试用) | 持续高频更新 |
| **One API (songquanpeng)** | n/a 但中文圈头部 | LLM key 管理 + 二次分发，单 binary + Docker | MIT | 中文中转站事实底座之一 |
| **New API (QuantumNous)** | 高 | 跨协议（OpenAI / Claude / Gemini compatible）统一网关 | MIT | One API 的活跃 fork，演化更快 |
| **Sub2API (Wei-Shaw)** | 高 | 把 Claude/OpenAI/Gemini/Antigravity 订阅级账号转 API（拼车共享） | n/a (待查) | 中文订阅级中转头部 |
| **Helicone OSS** | 高 | OSS LLM observability | n/a (待查) | 母公司被 Mintlify 收 |
| **Langfuse** | 高 | OSS LLM tracing / eval / prompt mgmt | MIT | 独立赛道留存玩家 |
| **Envoy AI Gateway** | n/a stars | CNCF + Tetrate + Bloomberg 合作；EAIGW v0.3；Endpoint Picker Provider；Alibaba Cloud Container Service 已采用 | Apache-2.0 | CNCF 官方背书；211 contributors / 54 companies |

### C. Edge / Cloud-native（vendor-tied）

- **Cloudflare Workers AI**: $0.011 / 1K Neurons; 10K Neurons/day 免费。
- **AWS Bedrock / Vertex AI / Azure OpenAI Service**: 各自走 cloud 账单，HUAKAI 不直接竞争而是作为下游 provider。

### D. Inference 提供商（compute 侧，HUAKAI 主要是消费方）

- **Fireworks AI**: $315M ARR (Feb 2026, +416% YoY)；10K+ customers；Series D $552M @ $2B（注意 webpronews 与 Sacra 的 $4B post-money 数字不一致，需复核）。
- **Together AI**: $537M total raised；2026 具体数字未公开。
- **DeepSeek V4**: $0.30/M input / $0.50/M output；V4 Flash 缓存命中 $0.0028/M（Mar 2026 发布）。
- **Qwen 2.5/3**: ~1/10 GPT-4o 价格、benchmark 接近。
- **Kimi K2.6**: OpenRouter 上 Chinese models 蹿升。

### E. 关键并购信号（最重要的市场动态）

- **2026-04-30 Palo Alto Networks 收购 Portkey** → AI 网关 + AI 安全融合是大趋势
- **2026-03-03 Mintlify 收购 Helicone** → Docs 平台向 LLMOps 延展
- **2026-04-06 OpenAI / Anthropic / Google 联合反 China model copying** (Bloomberg)
- **2026-04-17 Anthropic 实名 ID + selfie 验证**

含义：**独立 "纯 gateway" SaaS 公司在 2026 年面临收购洗牌**，底层赛道开始进入"被巨头吃 / 被生态绑死 / 被开源吃"三元收敛；HUAKAI 走"开源 + 中国合规中转"是少数还有窗口的位置。

---

## Q3 中国市场特殊性

### 订阅级账号 → API 套利现状（2026 年 5 月）

- **黑灰市规模信号**: AICodeMirror 公开 "10,000+ registered users / 200+ institutional clients"（SCMP 2026-04 报道）。这只是头部之一，中文圈 sub2api / one-api / new-api / 4.0AI / OneHub / DoneHub / Veloera / AnyRouter / Sub2API-CRS2 / metapi 等十多个开源/半开源项目活跃。
- **中转价目（2026-05）**: Claude Sonnet 4.6 中转价 ¥2.4-7.5/M input / ¥12-37.5/M output；Opus 反代 ¥1.5/7.5 per M token；常见汇率 ¥2.4-7/$ vs 官方 ¥7/$；折扣区间 0.3-0.93 折。
- **运营方收费**: 充值制 + 消费抵扣；Help AIO / aicoding.csdn 等评测站列出比价。

### 跨境网络限制对架构的影响

- **CN2 / 自建节点 / 全球智能路由**: AICodeMirror 等头部站宣称 "99.95% availability + cross-continent smart routing"；说明出海延迟和稳定性已是 must-have 而非 nice-to-have。
- **VPS 跳板 + 虚拟卡 + 代理 IP**: SCMP 2026-04 — Chinese users' 常见做法 (虚拟卡 / 代理 IP) 触发 Anthropic 风控算法，账号被禁但已付费用不退。

### 政策风险

- **中国侧**:
  - 《网络安全法》2026-01-01 修订加入专门 AI 合规条款（IAPP / White & Case 解读）。
  - CAC 算法备案 (含文本/图像/语音/视频生成 AI 服务)；公开舆论或社会动员能力的 AI 服务必须备案。
  - 2025-09-01 标签法规生效（AI 生成内容隐式 / 显式标识）。
  - 2025-11-01 三项国家标准生效。
  - 跨境提供 generative AI 服务给中国境内公众的 offshore 公司也受管辖。
- **美国侧 / Anthropic 侧**:
  - 2026-04-17 Anthropic 启动政府 ID + selfie 实名验证（小规模 use case 起步）。
  - 2026-04-06 OpenAI / Anthropic / Google 联合公开反 China model copying（Bloomberg）。
  - 2026 年初 Anthropic 全球封禁中国公司海外子公司访问（SCMP 早期报道）。

### 主要社区讨论

- **linux.do**: any-auto-register + CPA + new-api 自用方案讨论
- **知乎**: "2026 年高性价比 Claude API 中转平台测评" / "最近用的几个 Claude API 中转站价格和体验对比"
- **B 站**: 教学视频 + 选型对比
- **SegmentFault**: "对比了 8 个 Claude API 中转站，踩了不少坑"
- **CSDN aicoding 社区**: AnyRouter.top 等推广

含义：**HUAKAI 的中国市场切入需要**:
1. 出海合规设计：服务部署在境外、不主动向中国境内提供（避免 CAC 备案/AI 合规法触发）；
2. 客户侧文档：清楚告知账号绑定 / 实名 / 政策风险；不替客户违规；
3. 反风控伪装（fingerprint / IP / 行为）但与 "可被检测为 abuse" 划清界限 — 这是和 sub2api 路线的边界 trade-off；
4. 不做"代为购买 / 代为充值"的链路（资金合规雷区）。

---

## Q4 2026 趋势

### 协议增长 / 萎缩

- **OpenAI Chat Completions**: 仍是事实标准，第三方推理 (vLLM / SGLang / BentoML / Hugging Face / DeepSeek / Qwen / Kimi / 几乎所有 inference 提供商) 默认提供 Chat Completions 兼容 endpoint。
- **OpenAI Responses API**: 推荐用于新项目、Assistants API 2026-08-26 sunset；3% SWE-bench 提升 + cache 利用 +40-80%；但**未来 1-2 年仍是次大公民**，OpenAI 自己说"无短期废弃 Chat Completions 计划"。
- **Anthropic Messages API**: 单一 endpoint，2024 年代码到 2026 仍可用；架构上比 OpenAI 多 API 更统一；Claude 强势的 enterprise + coding agent 场景必须 native 支持。
- **Gemini API**: multi-modal-by-design (text/image/audio/video)；video 输入 258 tokens/秒；3.1 Pro $2/$12 per M (200K 内)；Batch API 50% off；Veo 3.1 视频生成 $0.15-0.60/秒。

### Vendor 增长 / 萎缩

- **DeepSeek V4 (2026-03)**: $0.30/$0.50；V4 Flash 缓存命中 $0.0028/M；号称"最具性价比的前沿 API"。
- **Qwen 2.5/3**: ~1/10 GPT-4o 价格 + benchmark 接近，coding/数学之王。
- **Kimi K2.6 / MiniMax M2.5**: OpenRouter 月度 token 排行榜上中国模型蹿升。
- **Mistral / Grok**: 在 Vercel AI Gateway / OpenRouter 路由列表中持续在线，但相对市场份额未显著增长。
- **Anthropic Claude**: $30B ARR / 80% enterprise；从 $87M (2024-01) → $30B (2026-04) 是 ~340x 两年。
- **OpenAI**: $25B ARR / 65% ChatGPT 订阅 + 25% API + 10% 合作；API ~$3.2B；enterprise 40% → 50% 目标年内。

### Prompt Caching 采用率

- Anthropic 端：cached input $0.30/M（标准 input 90% off）；月账单 cut 40-60% 是 enterprise 标准操作；70-90% token cost reduction 在 "static prefix + dynamic" 结构里常见。
- 含义：**HUAKAI PASR cache-aware 路由的市场假设成立**——cache hit 比例已是 enterprise 单位经济模型的核心变量，做账户级 cache locality + miss demote 是真需求。

### Agent / Tool Use / Structured Output 付费意愿

- 2026 年，"chatbot vs agent"区分已消失；用户期望 agent 自己执行多步任务。
- 结构化输出已成 mid-2025 起标配（OpenAI + Anthropic constrained decoding），不是 paid-only feature。
- Anthropic tool use 系统提示开销 313-346 tokens/req — 大规模时是显著成本，HUAKAI 网关侧做缓存 + 复用是真价值。

### Multi-Modal 真有多少人付

- Gemini video 处理 / 生成有明确价目；视频生成 $0.15-0.60/秒（非 trivial 单价）；但**没找到具体的多模态付费用户数 / 占比公开数据 (evidence not found)**。
- 直觉信号：Vercel AI Gateway 把 multi-modal 列为常规能力但不重点 push；OpenRouter 月度排行榜以文本模型为主。
- HUAKAI 决策：**multi-modal 协议保真做最小可用层（图像 / 文件 attachment 不丢），不在 1.0 阶段押重注**。

---

## Q5 商业模式 benchmark

### Token markup 比例

| 玩家 | markup | 备注 |
|------|--------|------|
| OpenRouter | **5% on inference spend** (5.5% 标准支付 / 5.0% crypto) | 中性聚合赛道公允水平 |
| Vercel AI Gateway | **0% markup**（按 provider list price） | 平台绑定补贴 |
| Cloudflare AI Gateway | **0% markup on tokens**（收 $0.30/M extra requests + CPU 时间） | 资源费而非 token 费 |
| 中国中转站（典型） | **官方 0.3-0.93 折**（折扣大但合规风险大） | 套利模型 |
| AWS Bedrock / Azure OpenAI | 等同 list price | 走 cloud 账单 |

含义：**HUAKAI Personal/SaaS 端 markup 上限是 5%**（OpenRouter 标杆）；中国中转模式靠"订阅 → API 套利"的折扣空间，不是 markup。

### Subscription tier 划分

| 玩家 | 免费 | Pro | Enterprise |
|------|-----|-----|-----------|
| Anthropic Claude | Free | Pro $20 | Max $100/$200, Team $100/seat (2026-01 降价), Enterprise |
| Cursor | n/a | $20 | enterprise neg |
| Helicone | 10K req/mo 免费 | $2.12 起 | custom |
| Vercel AI Gateway | $5/mo 免费 credits | Hobby/Pro/Enterprise | Enterprise |
| TrueFoundry | n/a | Pro $499 | Enterprise VPC |
| Portkey | n/a | n/a | $5K+/mo enterprise |
| Kong AI Gateway | n/a | per-service licensing | $50K+/yr |

含义：**HUAKAI SaaS 价位空白带是 $50-500/mo**——Portkey/TrueFoundry 太贵、Vercel/Cloudflare 免费但绑生态、OpenRouter 走 5% markup 不收订阅；HUAKAI 可走"$99/$299/$999 三档 + 5% token markup"混合。

### Self-hosted licensing 价位

- TrueFoundry self-hosted: $600-1000/mo（gateway plane）+ control plane
- Kong Enterprise: 按 per-service licensing $50K+/yr
- LiteLLM Enterprise: 7-day 免费试用，具体 enterprise 合同价未公开
- HUAKAI 推荐：**MIT 永久免费基础版 + $99-499/mo enterprise 支持订阅**（避免一次性 license 卡死）

### 真活下来的分润模式

1. **Inference markup**（OpenRouter 5% — 已证活）
2. **平台绑定免费送**（Vercel/Cloudflare — 巨头补贴）
3. **Enterprise 订阅 + 服务**（Portkey $5K+/mo / TrueFoundry $499+）
4. **网络安全/Docs 厂商收购出场**（Portkey → Palo Alto, Helicone → Mintlify — 不是"活下来"是"上岸"）
5. **中国订阅级中转充值套利**（sub2api / AICodeMirror / one-api 生态 — 高 PMF + 高合规风险）

---

## Q6 给 HUAKAI 的市场结论

> 给推荐但不替 Owner 决定。HCSF 选型属于"材料决策"——必须等 Codex lane 草案出来双 lane 对照后再定。

### Q6.1 应专注哪 1-2 客户分层？

**主推 (1)：L4 中国订阅级中转用户 + L1 个人开发者**（Personal Edition + 海外部署 SaaS Edition）
- 理由：sub2api / AICodeMirror / one-api 已证 PMF；Anthropic 实名风控 + 中国侧合规法规 2026 双侧紧缩，正好打开"做合规化 / 做更稳的中转替代"窗口；HUAKAI 8 灵魂融合 + PASR 跨账号智能 + 反风控伪装在这层有差异化。
- 风险：合规边界要早画清楚（不做代购、不做实名规避、出海部署、客户自带订阅）。

**次推 (2)：L3 AI Coding Agent / IDE 工具厂商**（B2B 但不是 enterprise）
- 理由：Cline 5M / Cursor 1M paying / Claude Code 18% 工作 adoption；他们要 multi-vendor 路由 + BYOK + 协议保真；HCSF 协议保真在这层有真客户验证；价格点 $99-999/mo 比挑战 enterprise 务实。

**不推**：
- L5 enterprise（Kong/TrueFoundry/Portkey/Cloudflare/Vercel 已 5+ 家正面打）
- L6 LLMOps 独立 obs（Helicone 被收购 = 窗口在收）

### Q6.2 HCSF canonical 选什么？

基于市场数据的初步推荐（最终需 Codex lane 平行验证）：

**Tier 1 (canonical, 必做 native 强支持)**:
1. **OpenAI Chat Completions** — 事实标准；vLLM / SGLang / BentoML / DeepSeek / Qwen / Kimi / 几乎所有第三方都默认提供；LiteLLM 路线选这个是市场公约。
2. **Anthropic Messages API** — Claude $30B ARR / 80% enterprise；claude-code / cline / cursor 在编码场景 native 用 Anthropic format；HUAKAI 中转主战场 L4 + 主推产品力都依赖这个。

**Tier 2 (重要，多协议网关必接)**:
3. **OpenAI Responses API** — Assistants API 2026-08-26 sunset 后逐步替代；reasoning model + agent 流向；Cache 利用 +40-80% 是真价值。
4. **Gemini API** — multi-modal native；Google Cloud / Vertex AI 客户必接。

**Tier 3 (最小可用)**:
5. Bedrock InvokeModel (per-vendor passthrough)
6. Azure OpenAI（地理合规客户必备）

**HCSF 路线选择 — 候选评分（基于市场而非工程）**:
| 路线 | 市场对齐度 | 工程成本 | 推荐度 |
|------|-----------|---------|-------|
| LiteLLM 单 OpenAI canonical | 高（OpenAI 是事实标准） | 低 | **推荐 starting point** |
| Portkey 双 canonical (OpenAI + Anthropic) | 高（HUAKAI 主战场含 L4 中转 = Anthropic 必须 native） | 中 | **推荐 1.0 终态** |
| envoy per-endpoint passthrough | 中（CNCF 背书强但非 SaaS 习惯） | 中-高 | 不推荐为 canonical，可作为底层 transport |
| 第四种（HUAKAI 自定 IR） | 低（无市场惯性） | 高 | 不推荐 |

**初步推荐路径**：1.0 阶段 **Portkey 双 canonical (OpenAI Chat Completions + Anthropic Messages 都做 native passthrough)** + 其余 vendor 走"最小翻译 + 元数据保真"。理由：HUAKAI 主战场 L4 + L3 都需要 Anthropic native，纯 LiteLLM 路线 (OpenAI 唯一 canonical) 会损失 Anthropic 工具调用 / streaming 元数据保真；纯 envoy 路线则无法做 PASR/反风控/UI 等上层逻辑。

### Q6.3 axis-3 真是最高优先级吗？

**部分是，但不够**。

axis-3 (协议转换) 在 2026 年市场看：
- 是入场票（任何 multi-vendor 网关必做）
- 不是差异化护城河（LiteLLM 46.2K stars / OpenRouter $50M ARR 已商品化）
- 协议保真度（工具调用元数据 / streaming chunk 形态 / cache key / 错误分类）是真正差距点；HUAKAI 如果能在保真度上跑分超 LiteLLM/OpenRouter，就是工程口碑差异化

**真正的护城河应叠加**:
1. **PASR 跨账号 cache-aware 智能调度**（Anthropic prompt caching 已证是 enterprise 单位经济模型核心 → HUAKAI 算法升级在这条路上）
2. **反风控伪装层**（fingerprint / IP / 行为，对 L4 中国中转主战场关键）
3. **多 vendor 账号池生命周期**（auto-checkin / 预轮换 / 健康度监控 — 8 ref repo 中 sub2api / all-api-hub / metapi 都在做但没人做完整）
4. **合规出海架构**（部署在境外 / 不向境内主动提供 / 标识合规）

**优先级排序建议** (按 1 年内 ROI):
1. axis-3 协议保真度 (Tier 1 OpenAI + Anthropic 双 canonical 完整度，含工具调用 / streaming / cache key) — **必做**
2. PASR cache-aware + 跨账号调度 — **必做，市场已证此为单位经济核心**
3. 反风控伪装 + 账号池生命周期 — **L4 主战场必做**
4. 多模态 / Responses API / 高级 enterprise 功能（VPC / SOC2 / SSO） — **1.0 后**

### Q6.4 1 年内应证明哪个 metric 才能 raise / 卖出去？

基于 OpenRouter / Portkey / Helicone benchmark：

| 阶段 | 核心 metric | 目标值（参考 OpenRouter 早期） |
|------|------------|-----------------------------|
| 6 个月 | **MAU + 总年化 inference spend** | 1K MAU + $100K-1M inference spend (= OpenRouter 2024-10 量级) |
| 12 个月 | **ARR**（5% markup + SaaS 订阅混合） | $500K-1M ARR (Portkey 早期量级) |
| 12 个月 | **企业/机构客户数** | 10-50 institutional clients (AICodeMirror 200 的 1/4) |
| 12 个月 | **GitHub stars + 活跃 contributors** | 5K+ stars (LiteLLM 46K 的 1/10 / Helicone YC W23 早期量级) |

**单一最重要 metric**：**月度活跃 inference spend**——这是 OpenRouter 估值故事的核心 ($100M annualized inference spend → $1.3B valuation 谈判)，比 ARR 更早期 + 更早期可见，且对接下游融资 / 收购方 (Palo Alto Networks 收 Portkey 看的也是 token 量) 都是硬通货。

---

## 数据 confidence 矩阵

| 断言类型 | 数据强度 | 缺口 |
|---------|---------|-----|
| OpenRouter 财务 + 规模 | **强**（Sacra + WebFetch 多源） | crypto 真实占比未公开 |
| Portkey 收购 + 财务 | **强**（多源公开报道） | 收购金额 / 整合时间表未公开 |
| Helicone 收购 | **中**（Crunchbase + 行业博文） | 收购金额未公开 |
| Anthropic / OpenAI ARR | **强**（CNBC / Bloomberg / Sacra / Epoch AI） | 季度细分未对账 |
| Anthropic 实名 ID 时间线 | **强**（SCMP / The New Stack / Bloomberg） | 实施细节 / 中国用户被禁占比未公开 |
| AICodeMirror 用户数 | **中**（SCMP 引用 + 自家宣传） | 独立审计无 |
| Cursor / Cline / Claude Code adoption | **强**（JetBrains 2026 调查 / TechCrunch / Bloomberg） | 单 vendor revenue split 未细分 |
| 中国中转价目 ¥2.4-7.5/M | **中**（多个评测站 / SegmentFault 帖子） | 单店波动大、跑路风险数据无 |
| Cloudflare / Vercel AI Gateway pricing | **强**（官方 docs + truefoundry/aistackpicks 二次解读） | 实际企业用量分布未公开 |
| Gartner AI Gateway 2028=70% | **强**（CNCF / TrueFoundry 引述） | 原始 Gartner 报告需付费访问，二手引用 |
| HCSF 路线市场份额 | **弱**（无直接数据） | LiteLLM vs Portkey vs envoy 实际生产部署比例无公开数据 |
| 多模态付费用户占比 | **弱** | evidence not found，按 Gemini video 价目和 OpenRouter 排行榜推断 |
| Anthropic prompt caching 采用率 | **中**（trade press + 厂商博文） | Anthropic 官方未公开 caching 占总流量比例 |
| China 中转市场总规模 $ | **弱**（无单一可信源） | 黑灰市估值不可信、AICodeMirror 200 institutional clients 是已知最高公开数字 |

---

## 风险与盲点（自评）

1. **训练时记忆与 Web 数据混杂的风险**: 我的部分日期感（Anthropic 实名时间、Helicone 收购时间）可能受训练数据污染；所有具体 metric 都尽量带 URL，但二次描述可能复述训练记忆；**Codex lane 应做对照交叉**。
2. **Web 来源质量参差**: 如 truefoundry.com / nxcode.io / metacto.com 等是商业内容站，可能为 SEO 而美化竞品数据；尽量取多源，但仍有偏差。
3. **未读 8 ref repo 源码**: 按任务纪律执行；这导致 "HUAKAI 实际差异化" 的判断只能基于上一轮 source-read 的输出 + 本次市场推断；**真正的 HCSF 选型必须叠加上一轮 axis-3 source-read 的工程结论**。
4. **没看 Codex lane 草案**: 双 lane 平行；本 lane 结论可能与 Codex lane 在 (a) HCSF 路线、(b) 主推客户层、(c) Q6.4 最重要 metric 三处出现冲突；预期由 Owner 综合裁定。
5. **多模态付费用户数据缺失**: Q4 多模态部分主要是间接推断，如果 HUAKAI 1.0 真要押多模态需要专项调研。
6. **金融 / 监管 / 反洗钱合规未深入**: 中国中转涉及充值 / 跨境 / 虚拟卡，本 lane 只触及表层，正式落地前需法务专项。
7. **OpenRouter 估值和 Portkey 收购金额的二级数据可能滞后**: 截稿前最权威源（公开融资公告 / 官方收购新闻稿）未全数手工 fetch，只通过 SearchSnippet。

---

## URL refs (with access dates — 全部 2026-05-09)

### OpenRouter / 中性路由
- [OpenRouter revenue, valuation & funding | Sacra](https://sacra.com/c/openrouter/) — 直接 fetch；$50M ARR / $1.3B valuation 谈判 / 5% markup / 8.4T tokens/mo
- [OpenRouter Monthly Token Usage Ranking 2026](https://aicost.org/blog/openrouter-monthly-token-usage-ranking-2026-chinese-models-dominate)
- [Openrouter: 2026 Verified Stats & Trends | Gitnux](https://gitnux.org/openrouter-statistics/)

### Portkey 收购 + 财务
- [Portkey was just acquired by Palo Alto Networks | Respan](https://www.respan.ai/blog/llm-observability-consolidation) — 2026-04-30 收购
- [Portkey - 2026 Company Profile | Tracxn](https://tracxn.com/d/companies/portkey/...) — $18M total raised / 45 employees / Series A 2026-02-19
- [How Portkey hit $5M revenue with 13 person team | Latka](https://getlatka.com/companies/portkey.ai)
- [Understanding Portkey AI Gateway Pricing For 2026 | TrueFoundry](https://www.truefoundry.com/blog/portkey-pricing-guide)

### Helicone 收购
- [Helicone - Crunchbase Company Profile](https://www.crunchbase.com/organization/helicone) — Mintlify 2026-03-03 收购
- [Helicone Pricing](https://www.helicone.ai/pricing)

### LiteLLM
- [BerriAI/litellm | GitHub](https://github.com/BerriAI/litellm) — 46.2K stars
- [LiteLLM Enterprise docs](https://docs.litellm.ai/docs/enterprise)

### Anthropic / OpenAI 财务
- [Anthropic CEO 80x Q1 growth | CNBC 2026-05-06](https://www.cnbc.com/2026/05/06/anthropic-ceo-dario-amodei-says-company-crew-80-fold-in-first-quarter.html)
- [Anthropic $30B ARR | TrendingTopics](https://www.trendingtopics.eu/anthropic-overtakes-openai-in-revenue-hitting-30-billion-run-rate/)
- [Anthropic vs OpenAI Revenue | Epoch AI](https://epoch.ai/data-insights/anthropic-openai-revenue)
- [Claude Pricing](https://claude.com/pricing)
- [OpenAI revenue, valuation & funding | Sacra](https://sacra.com/c/openai/)
- [OpenAI Statistics 2026 | getpanto](https://www.getpanto.ai/blog/openai-statistics)

### Anthropic 实名 + 中国 crackdown
- [The end of 'anonymous AI'? Anthropic enforces real-name verification | Deepline 2026-04-17](https://english.dotdotnews.com/a/202604/17/AP69e1d8abe4b09ea23312c46d.html)
- [Black market workarounds for Claude scale up | SCMP](https://www.scmp.com/tech/article/3350321/black-market-workarounds-claude-scale-anthropic-tightens-id-checks)
- [OpenAI Anthropic Google Unite Against Chinese AI Theft | Bloomberg 2026-04-06](https://www.bloomberg.com/news/articles/2026-04-06/openai-anthropic-google-unite-to-combat-model-copying-in-china)
- [Anthropic clarifies ban on third-party tool access | The Register 2026-02-20](https://www.theregister.com/2026/02/20/anthropic_clarifies_ban_third_party_claude_access/)

### 中国中转
- [songquanpeng/one-api | GitHub](https://github.com/songquanpeng/one-api)
- [QuantumNous/new-api | GitHub](https://github.com/QuantumNous/new-api)
- [qixing-jk/all-api-hub | GitHub](https://github.com/qixing-jk/all-api-hub)
- [Wei-Shaw/sub2api | GitHub](https://github.com/Wei-Shaw/sub2api)
- [cita-777/metapi | GitHub](https://github.com/cita-777/metapi)
- [我对比了 8 个 Claude API 中转站 | SegmentFault](https://segmentfault.com/a/1190000047718715)
- [2026 高性价比 Claude API 中转平台测评 | 知乎](https://zhuanlan.zhihu.com/p/2016136177900595171)
- [AnyRouter 国内使用教程 | bilibili](https://www.bilibili.com/video/BV1fRhEzjEKD/)
- [Help AIO 中转站比价](https://www.helpaio.com/transit/compare)

### Edge / Cloud-native gateway
- [Cloudflare AI Gateway pricing](https://developers.cloudflare.com/ai-gateway/reference/pricing/)
- [Cloudflare AI Gateway Pricing Explained For 2026 | TrueFoundry](https://www.truefoundry.com/blog/cloudflare-ai-gateway-pricing)
- [Vercel AI Gateway pricing](https://vercel.com/docs/ai-gateway/pricing)
- [Introducing the AI Gateway | Vercel blog](https://vercel.com/blog/ai-gateway)

### Enterprise gateway
- [Kong Gateway Pricing for 2026 | TrueFoundry](https://www.truefoundry.com/blog/kong-gateway-pricing-architecture-an-analysis-for-ai-teams-2026-edition)
- [Kong Pricing](https://konghq.com/pricing)
- [TrueFoundry Pricing](https://www.truefoundry.com/pricing)
- [5 Best AI Gateways For Enterprises in 2026 | TrueFoundry](https://www.truefoundry.com/blog/best-ai-gateway)

### Envoy AI Gateway
- [Envoy AI Gateway | aigateway.envoyproxy.io](https://aigateway.envoyproxy.io/blog/)
- [Open collaboration to bring AI Gateway features to the Envoy community | CNCF 2024-10-18](https://www.cncf.io/blog/2024/10/18/open-collaboration-to-bring-ai-gateway-features-to-the-envoy-community/)
- [A Year of Envoy Gateway GA | CNCF 2025-06-11](https://www.cncf.io/blog/2025/06/11/a-year-of-envoy-gateway-ga-building-growing-and-innovating-together/)
- [AI Gateway Deep Dive (2026) | Jimmy Song](https://jimmysong.io/blog/ai-gateway-in-depth/)

### Coding agents
- [Cursor $2B ARR | TechCrunch 2026-03-02](https://techcrunch.com/2026/03/02/cursor-has-reportedly-surpassed-2b-in-annualized-revenue/)
- [Cursor AI Statistics 2026 | getpanto](https://www.getpanto.ai/blog/cursor-ai-statistics)
- [JetBrains: Which AI Coding Tools Do Developers Actually Use at Work? 2026-04](https://blog.jetbrains.com/research/2026/04/which-ai-coding-tools-do-developers-actually-use-at-work/)
- [JetBrains Developer Ecosystem Survey 2026 | Antigravity Lab](https://antigravitylab.net/en/articles/ai-tools/jetbrains-developer-survey-2026-ai-coding-tools-guide)
- [Cline | AI Wiki](https://aiwiki.ai/wiki/cline) — 5M VS Code installs / 61K+ GitHub stars

### 协议
- [OpenAI Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [OpenAI Responses vs Chat Completions](https://platform.openai.com/docs/guides/responses-vs-chat-completions)
- [OpenAI Responses vs Chat Completions vs Anthropic Messages | Portkey blog](https://portkey.ai/blog/open-ai-responses-api-vs-chat-completions-vs-anthropic-anthropic-messages-api/)
- [Anthropic Messages API translated reference | Olla](https://thushan.github.io/olla/api-reference/anthropic/)
- [Function Calling & Tool Use 2026 Guide | ofox](https://ofox.ai/blog/function-calling-tool-use-complete-guide-2026/)

### Inference 提供商
- [Fireworks AI revenue, valuation & funding | Sacra](https://sacra.com/c/fireworks-ai/)
- [Fireworks AI $2B Series D | WebProNews](https://www.webpronews.com/the-2-billion-bet-how-fireworks-ai-quietly-became-the-most-important-startup-most-people-have-never-heard-of/)
- [Together AI revenue | Sacra](https://sacra.com/c/together-ai/)
- [DeepSeek API Pricing | DeepSeek docs](https://api-docs.deepseek.com/quick_start/pricing)
- [Qwen API Pricing 2026 | DeepInfra blog](https://deepinfra.com/blog/qwen-api-pricing-2026-guide)

### Prompt caching
- [Anthropic API Pricing 2026 | Finout](https://www.finout.io/blog/anthropic-api-pricing)
- [Anthropic Prompt Caching 2026 | AI Checker Hub](https://aicheckerhub.com/anthropic-prompt-caching-2026-cost-latency-guide)
- [Prompt caching | Claude API Docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)

### 中国监管
- [AI Watch: Global regulatory tracker - China | White & Case](https://www.whitecase.com/insight-our-thinking/ai-watch-global-regulatory-tracker-china)
- [China's Key Developments in AI Governance 2025 | ICLG](https://iclg.com/practice-areas/telecoms-media-and-internet-laws-and-regulations/03-china-s-key-developments-in-artificial-intelligence-governance-in-2025)
- [Global AI Governance: China | IAPP](https://iapp.org/resources/article/global-ai-governance-china)

### Gartner / 市场
- [Gartner Market Guide for AI Gateway | Lasso security](https://www.lasso.security/analyst-reports/gartner-ai-gateway)
- [Gartner Market Guide for AI Gateways 2025 | TrueFoundry](https://www.truefoundry.com/blog/building-the-enterprise-ai-control-plane-gartner-r-insights-and-truefoundrys-approach)
- [Gartner Worldwide AI Spending $2.5T 2026](https://www.gartner.com/en/newsroom/press-releases/2026-1-15-gartner-says-worldwide-ai-spending-will-total-2-point-5-trillion-dollars-in-2026)

### 白标 / Reseller
- [SuiteDash Best White-Label SaaS Reseller Programs 2026](https://suitedash.com/best-white-label-saas-reseller-programs/)
- [Best AI White Label Software 2026 | Parallel AI](https://parallellabs.app/best-ai-white-label-software-the-complete-guide-for-agencies-and-service-providers-who-want-to-resell-ai-without-building-from-scratch/)

---

**END Claude lane.** 等 Codex lane 出来后做交叉对比，由 Owner 综合裁定 HCSF 路线 + 主推客户层 + 1 年内核心 metric。
