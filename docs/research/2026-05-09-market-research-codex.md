# AI 网关市场独立调研 — Codex Lane

**日期**: 2026-05-09  
**Lane**: codex (xhigh + fast_mode)  
**对应 Claude lane**: docs/research/2026-05-09-market-research-claude.md（写作时未见）

**边界**: 本 lane 只做公开市场调研；未打开 Claude lane 草案；未读取 8 个 reference repo 源码。GitHub 数据来自 GitHub REST API 当前字段，作为公开热度/活跃度代理，不等同于真实用户数。

## TL;DR (5 sentences max)

2026 年 AI gateway 市场已经分成两条钱线：海外/enterprise 在买 governance、routing、observability、SLA，国内/个人运营侧在买可用账号池、国内直连、低价、稳定结算。OpenRouter、Vercel AI Gateway、Helicone 的 pricing 说明 OpenAI-compatible 单入口仍是获客默认形态，但 OpenAI Responses、Anthropic Computer Use、Gemini Live、MCP、prompt caching 的差异已经让“纯 OpenAI canonical”开始丢功能。Portkey 被 Palo Alto Networks 宣布收购、并披露 1T+ tokens/day、120M+ requests/day、$180M+ annualized AI spend，是 AI Gateway 作为企业安全/控制平面的最强市场信号；但这条线已经有强资本玩家。HUAKAI 更应先服务 Personal Edition 的中转站运营方和小型 SaaS 运营方，而不是正面打 enterprise generic gateway。HCSF 建议采用“OpenAI-compatible storefront + capability-preserving native envelopes + capability graph”的第四种风格：市场入口像 LiteLLM/OpenRouter，能力保真不能像单 canonical 那样压平。

## Q1 客户分层

| 层级 | 规模信号 | 付费意愿 | 关心什么 | HUAKAI 含义 |
| --- | --- | --- | --- | --- |
| 个人开发者 / AI 编程用户 | Vercel `ai` npm 包最近 30 天下载约 51.49M，`@ai-sdk/provider` 约 72.61M；OpenRouter 公开定价页写 400+ models / 60+ providers [R-GH-NPM][R-OPENROUTER-PRICING]。 | 低到中；典型是免费额度、$5-$20 credit、少量 PAYG。 | 低门槛、兼容 OpenAI SDK、免费模型、Codex/Claude Code/Gemini CLI 可用。 | 不应直接作为 HUAKAI 早期主要付费客户；应作为 Personal Edition 运营方的下游用户。 |
| 中国中转 API 用户 | TokenNav 页面快照显示 totalCount=106、availableCount=92；Token1000 首页展示 59 家收录、10 家推荐、9 家高风险 [R-TOKENNAV][R-TOKEN1000]。 | 中；单个用户可能小额充值，但愿意为“国内直连 / Claude / Codex / Gemini 可用”付费。 | 国内网络可达、微信/支付宝/USDT、Claude Code/Codex 客户端兼容、账号不封、价格透明。 | Personal Edition 最贴合这一层，但合规和账号风控必须前置。 |
| 小团队 / AI-native startup | Helicone Pro $79/mo、Team $799/mo；Portkey Production $49/mo + usage overage [R-HELICONE-PRICING][R-PORTKEY-PRICING]。 | 中高；愿意为排障、fallback、budget、日志、团队席位付费。 | Provider outage、成本归因、prompt 版本、速率限制、日志保留。 | HUAKAI 可做二级目标，但只有“协议转换”不够，必须带运营后台、key/quota/billing。 |
| Agent 框架 / 工具开发者 | OpenAI 2026 changelog 连续新增 Responses tool search、Computer Use、WebSocket、server-side compaction、Skills；Anthropic Computer Use 仍是 beta 且有专门 token overhead；Gemini Live 是 WSS stateful API [R-OPENAI-CHANGELOG][R-ANTHROPIC-CU][R-GEMINI-LIVE]。 | 中；如果 gateway 能保真工具调用、缓存、长会话，愿意付费。 | Tool use 可靠性、长上下文成本、prompt caching、MCP/server tools、流式和实时音频。 | HCSF 不能只做 Chat Completions；agent workload 是协议差异的主要驱动力。 |
| Enterprise 大客户 | Portkey 披露 24,000+ organizations、$180M+ annualized AI spend；Palo Alto Networks 在 2026-04-30 宣布 intent to acquire Portkey [R-PORTKEY-OSS][R-PANW-PORTKEY]。 | 高；custom contract、on-prem/VPC、SAML/SSO、SLA、compliance。 | 安全治理、审计、数据驻留、agent 身份、99.99% uptime、采购流程。 | 这是高价值市场，但早期 HUAKAI 不宜正面竞争；可借鉴控制面能力，先做 operator SaaS。 |
| LLMOps / observability 客户 | Helicone 官方称 14.2T tokens、16,000 organizations、33M end users；Langfuse GitHub 约 26.9k stars 且 2026-05-08 发布 v3.173.0 [R-HELICONE-MINTLIFY][R-GH-OSS]。 | 中高；从开源 self-host 到 hosted team/enterprise。 | Trace、cost attribution、eval、prompt management、incident debugging。 | HUAKAI 不应变成纯 observability 产品，但 billing/usage/audit 是中转站商业化的刚需。 |
| 运营方 / reseller / “卖 API 的人” | GreatRouter、青栀AI、QuickRouter 等中文站点把“统一 API / 国内直连 / 兼容 OpenAI SDK / 多模型”作为核心卖点；TokenNav/Token1000 已出现导航和避坑生态 [R-GREATROUTER][R-QINZHIAI][R-TOKENNAV][R-TOKEN1000]。 | 高于个人用户；他们买的是生产工具而不是 token。 | 账号池、结算、充值、用户分组倍率、风控、封号恢复、模型目录更新。 | HUAKAI 最该服务的付费层：Personal Edition 先给 Owner 自运营，SaaS Edition 后卖给同类运营方。 |

## Q2 竞品现状

| 对手 | 定位 | 付费模式 / 数字 | 最近 6 个月动作 | 差异化点 |
| --- | --- | --- | --- | --- |
| Portkey hosted / OSS Gateway | Production AI control plane + AI Gateway。 | Free 10k recorded logs/mo；Production $49/mo；Enterprise custom；GitHub API: 11,651 stars、latest release v1.15.2 on 2026-01-12 [R-PORTKEY-PRICING][R-GH-OSS]。 | 2026-03-24 宣布 Gateway fully open source；披露 1T+ tokens/day、120M+ requests/day、24,000+ orgs；2026-04-30 Palo Alto Networks 宣布 intent to acquire [R-PORTKEY-OSS][R-PANW-PORTKEY]。 | 企业安全治理、MCP Gateway、agent traffic control、收购退出信号强。 |
| Helicone hosted / OSS | LLM observability + AI Gateway。 | Hobby free 10k requests；Pro $79/mo；Team $799/mo；Enterprise on-prem；GitHub API: 5,630 stars [R-HELICONE-PRICING][R-GH-OSS]。 | 2026-03-03 官方宣布 joining Mintlify；披露 14.2T lifetime tokens、16,000 orgs、33M end users；站点说明服务维护/迁移语境 [R-HELICONE-MINTLIFY]。 | 观测和排障强，gateway 是 observability 延伸；但收购后新产品势能不如 Portkey/OpenRouter。 |
| OpenRouter | Universal LLM marketplace / routing layer。 | Free: 25+ free models、50 req/day；PAYG 5.5% platform fee；400+ models、60+ providers；BYOK 1M req/mo free, then 5% fee [R-OPENROUTER-PRICING][R-OPENROUTER-FAQ]。 | 官方 pricing/docs 显示 prompt caching、data policy routing、regional routing、Zero Completion Insurance；第三方 Sacra 估算 early 2026 annualized revenue $50M，2.5M users、8.4T tokens/month需标低置信 [R-OPENROUTER-PRICING][R-OPENROUTER-SACRA]。 | 最强开发者聚合入口；计费是 credit fee，不加价 token。 |
| Hugging Face Inference Providers | Model hub 上的 routed inference marketplace。 | Free user monthly credits $0.10；Pro/Team/Enterprise $2/seat credits；支持 routed requests 和 custom provider key [R-HF-PRICING]。 | 继续把外部 provider 路由内嵌到 HF SDK/Hub。 | 模型社区/Hub 分发强，不是专门 gateway 控制面。 |
| Together AI | Open model inference / fine-tuning / GPU clusters。 | Serverless per-token；示例价格 Kimi K2.5 $0.50 in / $2.80 out per 1M tokens；batch inference 低价；dedicated endpoint for steady traffic [R-TOGETHER-PRICING][R-TOGETHER-OVERVIEW]。 | 官网 2026 pricing 显示多类新模型、GPU clusters、batch API。 | 开源模型云与算力平台，不解决多 SaaS 账号池。 |
| Fireworks AI | Fast open-model serverless + on-demand deployments。 | Serverless per-token；cached input default 50%；H100/H200 $7/hr、B200 $10/hr、B300 $12/hr [R-FIREWORKS]。 | 2026 pricing 强调 Turbo/Priority、cached input、batch 50%。 | 速度/吞吐和开源模型托管，不是中转站运营工具。 |
| Replicate | Public/private model marketplace，强图像/视频。 | 多数 public models 按运行时间/hardware；部分按 token/image/video-second；例：Wan video $0.09-$0.25/sec [R-REPLICATE]。 | 继续扩张 proprietary + OSS media models。 | multimodal 创作者生态强；文本 gateway 非核心。 |
| Vercel AI Gateway | Frontend/AI SDK 生态里的 model gateway。 | $5/mo free credits；paid no markup；BYOK no markup/fee；AI SDK npm 包近 30 天下载 51.49M [R-VERCEL-PRICING][R-GH-NPM]。 | 2026-02/03 docs 更新；支持动态 provider routing、provider ordering、automatic caching [R-VERCEL-PROVIDERS]。 | 对 Next.js/AI SDK 开发者极强，OpenAI-compatible + providerOptions。 |
| Cloudflare Workers AI / AI Gateway | Edge AI + gateway + agent platform。 | Workers AI 50+ open-source models，serverless pay-for-use；Gateway 提供 caching、rate limit、fallback、observability [R-CF-WORKERSAI][R-CF-AI]。 | 2026 页面把 Agents、Remote MCP、AI Gateway、Workers AI 放在同一 full-stack agent 平台。 | 边缘网络、Workers/Durable Objects、MCP/OAuth 组合。 |
| Anyscale / Modal 类 serverless GPU | AI workload compute platform。 | Anyscale hosted/BYOC、pay-as-you-go compute and committed contracts；Modal pricing 写“pay for what you use”，示例 H100 $0.001097/sec、Starter $30/mo free credit [R-ANYSCALE][R-MODAL]。 | Anyscale 定位从 endpoint 更转向 Ray workload/GPU clusters；Modal 更偏 serverless GPU app/inference/batch。 | 适合训练/批处理/自定义服务，不是 API reseller OSS 工具。 |
| AWS Bedrock | Cloud-native model access + enterprise procurement。 | On-demand、provisioned/batch；batch for selected FMs “50% lower price” than on-demand [R-BEDROCK]。 | model catalog 持续增加 Anthropic/Meta/Mistral/DeepSeek/Google 等。 | 企业合规、AWS IAM/VPC/采购强，开发者入口慢。 |
| Vertex AI / Gemini Agent Platform | Google Cloud managed generative AI。 | Google Cloud pay-as-you-go + prepaid discounts/custom quote [R-VERTEX]。 | Gemini Live、context caching、agent platform 推进。 | Gemini native / data residency / GCP integration。 |
| Azure OpenAI | Microsoft enterprise OpenAI channel。 | Azure pricing 表按 deployment/region/model；企业采购/区域能力强 [R-AZURE]。 | 继续是 regulated enterprise 的 OpenAI 采购入口。 | 企业合同和 Azure 网络/合规强。 |
| LiteLLM OSS | Open-source LLM proxy / OpenAI-compatible multi-provider adapter。 | GitHub API: 46,312 stars、latest release v1.83.14-stable.patch.3 on 2026-05-07 [R-GH-OSS]。 | 2026-05 仍高频 release；社区和下载热度强。 | 开发者默认 OSS adapter，但商业中转运营面的 billing/account lifecycle 不完整。 |
| New API / one-api / sub2api / all-api-hub | 中文生态自部署中转/账号池/管理台。 | GitHub API: one-api 33,302 stars；New API 31,979；sub2api 19,236；all-api-hub 3,470；sub2api / New API / all-api-hub 均 2026-05 发布或 push [R-GH-OSS]。 | sub2api v0.1.125 2026-05-07；New API rc.4 2026-05-06；all-api-hub v3.37.0 2026-05-07。 | 直接验证 HUAKAI 目标市场：账号池、运营后台、中文用户、低价/多账号。 |
| Langfuse / Vellum | LLMOps / prompt/eval platform。 | Langfuse GitHub API: 26,880 stars、v3.173.0 2026-05-08；Vellum 多为商业平台，未取到 comparable OSS core [R-GH-OSS]。 | Langfuse 仍高频 release。 | 与 HUAKAI 在 observability/eval 有交集，但不主打中转站账号池。 |
| Envoy AI Gateway | Cloud-native Kubernetes / Envoy Gateway extension。 | GitHub API: 1,599 stars、v0.6.0 on 2026-05-05 [R-GH-OSS]。 | 作为 Envoy/K8s 生态 AI gateway 继续发布。 | 强 cloud-native per-endpoint/gateway API 风格，但不适合中文中转站用户的低门槛需求。 |

## Q3 中国市场特殊性

1. **订阅级账号到 API 套利仍在增长，但公开数据以站点/导航为主，不是审计数据。** TokenNav 的页面快照有 106 total / 92 available，且 items 显示微信、支付宝、USDT、VISA 等 paymentMethods；Token1000 首页自称收录 59 家、10 家推荐、9 家高风险 [R-TOKENNAV][R-TOKEN1000]。这证明“API 中转站”已经有聚合导航和避坑生态，但不能证明全行业收入规模。

2. **收费标准呈两种形态：官方价 pass-through / “1 元=1 美元”式倍率 / 折扣式中转。** GreatRouter meta 写“50+ 主流 AI 模型”“价格低于官方 10% 以上”“Azure 官方直连”；青栀AI写“1:1 充值”“700+ 模型”“国内直连”；OpenAirRouter 搜索结果展示“az 1元/刀、官转 2.5元/刀”这类折算口径 [R-GREATROUTER][R-QINZHIAI][R-OPENAIROUTER-CN]。观察结论：用户接受“按美元模型价折成人民币/积分”的抽象，运营方需要倍率、分组、充值、余额、发票/国内支付。

3. **跨境网络限制把架构重点从纯 protocol translation 拉到线路和账号 lifecycle。** 中文站点普遍把“国内直连”“无需科学上网”“兼容 Claude Code/Codex/OpenAI SDK”放在首页/SEO；Claude 中转站搜索结果还写“号池代理技术隔离封号风险” [R-QINZHIAI][R-GREATROUTER][R-CLAUDE-ZZ]。HUAKAI 不能只做 adapter，需要 endpoint 地域、健康探测、账号池隔离、失败重试、余额/额度告警。

4. **政策风险不是抽象风险。** 《生成式人工智能服务管理暂行办法》2023-08-15 生效；第十七条要求具有舆论属性或社会动员能力的服务开展安全评估并履行算法备案/变更/注销备案 [R-GOV-GENAI]。对 HUAKAI 的实际含义：Personal Edition 自运营面向国内公众卖 API 时，不能只按“技术工具”处理；至少要准备实名/支付/KYC、内容风险日志、模型/内容服务边界、备案咨询路径。此处为合规风险提示，不是法律意见。

5. **主要讨论社区碎片化：导航站、Telegram、微信群、V2EX/Reddit。** 青栀AI页面直接列 Telegram 频道/群组和微信客服；TokenNav/Token1000是站点导航；Reddit 2026-05 有“中国便宜 Claude tokens”讨论，描述中转站为中国开发者生态里的 API proxy [R-QINZHIAI][R-TOKENNAV][R-REDDIT-CHINA-CLAUDE]。V2EX 搜索未在本轮取得高质量可引用页面，不能强行声称规模。

## Q4 2026 年趋势

1. **OpenAI Responses 在增长，但 Chat Completions 仍未消失。** OpenAI changelog 显示 2026-03 GPT-5.4 同时进入 Chat Completions 和 Responses；同页在 2026-01 宣布 Open Responses 开源 spec；Responses 侧新增 tool search、computer use、WebSocket、server-side compaction、Skills、Hosted Shell 等 [R-OPENAI-CHANGELOG]。结论：对市场入口，OpenAI-compatible Chat Completions 仍是低摩擦入口；对高端 agent，Responses-native 能力必须保留。

2. **Anthropic Computer Use / tool use 是高意愿但高风险能力。** Anthropic 文档写 Computer Use 需 beta header，支持截图、鼠标、键盘、桌面自动化，并要求 sandbox/allowlist/human confirmation；pricing 还列 system prompt overhead 和 tool definition tokens [R-ANTHROPIC-CU]。这不是“便宜文本代理”需求，而是 agent runtime 控制面需求。

3. **Gemini Live / realtime audio 是单独协议，不适合压成 Chat Completions。** Gemini Live API 技术规格列 input audio/images/text，output audio，protocol 是 stateful WebSocket，且 client-to-server 建议用 ephemeral tokens [R-GEMINI-LIVE]。HUAKAI 如果要做多模态/实时，HCSF 必须支持 per-endpoint/native stream。

4. **Prompt caching 已成为成本工程，不是营销功能。** Google Gemini 2.5+ 默认 implicit caching，Mistral cached tokens 10% input price，Fireworks cached input default 50%，OpenAI GPT-5.5/future models require extended prompt caching，Vercel AI Gateway提供 automatic caching 适配 provider [R-GEMINI-CACHE][R-MISTRAL-CACHE][R-FIREWORKS][R-OPENAI-DATA][R-VERCEL-PROVIDERS]。市场含义：gateway 能卖“省钱”和“别破坏缓存”的能力，尤其对 agent 长上下文。

5. **Vendor 增长/萎缩：xAI、Chinese open models、cloud gateways 在增长；Mistral 仍活跃但不是 gateway 方向主角。** xAI docs 2026-05-07 更新 models/pricing，并列 tools pricing、batch discounts、Remote MCP token billing、violation fee；Together/Fireworks 定价页大量列 Kimi/Qwen/DeepSeek/GPT-OSS；Mistral 仍发布 prompt caching official docs [R-XAI][R-TOGETHER-PRICING][R-FIREWORKS][R-MISTRAL-CACHE]。HUAKAI 的 provider catalog 不能只盯 OpenAI/Anthropic/Gemini。

6. **Structured output / tool use 比“单纯多模态”更接近付费刚需。** OpenRouter provider routing 文档会按 tools/tool_choice 和 max_tokens 过滤 provider；Vercel providerOptions 支持 reasoning/caching；Helicone/Portkey都把 cost/control/trace 放在生产卖点 [R-OPENROUTER-ROUTING][R-VERCEL-PROVIDERS][R-HELICONE-OVERVIEW][R-PORTKEY-PRICING]。Vision/audio/video 有需求，但 pricing 非 token 化且平台分散，早期应作为能力保真而非核心商业主轴。

## Q5 商业模式 benchmark

| 模式 | 观察到的价格 / 机制 | 活下来的原因 | 对 HUAKAI 的启发 |
| --- | --- | --- | --- |
| Credit purchase fee，不加 token markup | OpenRouter: PAYG 5.5% credit fee；BYOK 1M req/mo free 后 5%；不 markup provider token price [R-OPENROUTER-PRICING][R-OPENROUTER-FAQ]。 | 用户觉得透明；平台赚支付/聚合/路由费。 | SaaS Edition 可对运营方收平台费/seat，而不是强行 markup token。 |
| Zero markup gateway + subscription | Vercel: $5 free credits、paid no markup、BYOK no fee；Helicone: credits 0% markup + Pro/Team subscriptions [R-VERCEL-PRICING][R-HELICONE-OVERVIEW]。 | 降低迁移阻力，用生态/观测/平台留存。 | HUAKAI 对国内运营方可卖软件费 + 自有运营 token 毛利分开核算。 |
| Recorded logs / requests tier | Portkey: Free 10k recorded logs、Production 100k logs + $9/100k overage、Enterprise 10M+ logs [R-PORTKEY-PRICING]。 | 企业愿意为观测和治理付费；日志量是可理解 meter。 | Admin Ops 和 audit/log retention 可做 SaaS 计费维度。 |
| Observability subscription | Helicone: Pro $79、Team $799，按 requests/storage/retention/ingestion limits 分层 [R-HELICONE-PRICING]。 | 生产排障和成本归因有预算。 | HUAKAI 的 operator dashboard 不能只是 CRUD；要可解释收入、成本、封号、退款。 |
| Open model inference cloud | Together/Fireworks/Replicate 按 token、GPU-hour、image/video-second 等收费 [R-TOGETHER-PRICING][R-FIREWORKS][R-REPLICATE]。 | 算力和模型运行是直接成本。 | HUAKAI 不宜自建算力；应聚合/转接，保留自建 provider 插件可能性。 |
| 中国中转站 payg / hybrid | TokenNav items 显示 minimumSpend 1 CNY/USD、paymentMethods 包含微信/支付宝/USDT/VISA、billingMode 有 payg/hybrid/subscription [R-TOKENNAV]。 | 小额充值 + 国内支付 + 快速可用。 | Personal Edition 必须支持充值、余额、倍率、分组价、支付插件。 |
| Enterprise custom/on-prem | Portkey/Helicone enterprise 都是 custom pricing/on-prem/VPC/SSO/SAML；Palo Alto 收购说明安全控制面有战略价值 [R-PORTKEY-PRICING][R-HELICONE-PRICING][R-PANW-PORTKEY]。 | 大客户买风险降低，不买便宜 token。 | HUAKAI 后期可做，但早期不能把 enterprise 功能拖慢运营方 PMF。 |

## Q6 给 HUAKAI 的市场结论

### 1. 应专注哪 1-2 个客户分层

**第一层：Personal Edition 自运营中转站运营方。** 这和 HUAKAI 已定定位完全一致：Owner 先自部署卖 API。公开证据显示中文中转站已经有 50-100+ 可见站点、导航/避坑/价格对比、微信/支付宝/USDT 小额充值、Claude/Codex/Gemini 客户端兼容诉求 [R-TOKENNAV][R-TOKEN1000][R-QINZHIAI]。这层最愿意为“能卖 API、能管账号、能结算、能控风险”付费。

**第二层：SaaS Edition 的小型/中型中转站运营方。** 不是 enterprise generic AI gateway，而是“想做模式 1 的运营方”。Portkey 的企业安全路线已经被 Palo Alto 收购信号验证，Vercel/Cloudflare/OpenRouter 已经占据通用 gateway 入口；HUAKAI 需要避开正面竞争，卖“中转站运营系统”而不是“又一个 AI gateway” [R-PANW-PORTKEY][R-VERCEL-PRICING][R-OPENROUTER-PRICING]。

### 2. HCSF canonical 应选哪种风格

建议选 **第四种：OpenAI-compatible storefront + capability-preserving native envelopes + provider capability graph**。

- 市场入口必须 OpenAI-compatible。OpenRouter FAQ 写迁移方式是更新 base URL 和 model names；Helicone gateway 文档写通过 OpenAI SDK 访问 100+ providers；Vercel AI Gateway 也以统一入口和 provider options 获客 [R-OPENROUTER-PRICING][R-HELICONE-OVERVIEW][R-VERCEL-PROVIDERS]。
- 但内部不能是 LiteLLM 式单 OpenAI canonical。Responses 的 tool search/compaction/Skills、Anthropic Computer Use、Gemini Live WSS、xAI server-side tools/Remote MCP 都有 native 差异；压平会让高付费 agent workload 丢能力 [R-OPENAI-CHANGELOG][R-ANTHROPIC-CU][R-GEMINI-LIVE][R-XAI]。
- 也不应完全采用 envoy per-endpoint 风格作为对外主入口。中国中转站用户要一个 base_url / 一个 API Key / 客户端即插即用；per-endpoint 适合 K8s/platform teams，不适合 Personal Edition 的商业启动。
- Portkey bi-canonical 可借鉴，但 2026 的协议面已超过 OpenAI/Anthropic 两家。HUAKAI 应把 HCSF 设计成 capability graph：text/chat/tool/response/live/audio/video/cache/batch/reasoning/data-retention 均为 capability，adapter 负责最佳保真映射；对外默认 OpenAI-compatible，必要时暴露 native passthrough endpoints。

### 3. Axis-3 优先级是不是最高

**Axis-3 是获客必需，但不是单独最值钱的 axis。** 市场数据显示 provider breadth/one endpoint 是入口；但真正付费点在中国中转站场景里是账号 lifecycle、充值计费、quota、分组倍率、并发控制、可用性、封禁/余额/退款处理 [R-QINZHIAI][R-TOKENNAV][R-PORTKEY-PRICING][R-HELICONE-PRICING]。因此优先级应改成：

1. **Commercial spine first**: API key、用户余额、usage ledger、quota/rate/concurrency、充值/手工入账、operator audit。
2. **Axis-3 breadth second but parallel**: OpenAI-compatible storefront + Claude/Gemini/OpenAI/xAI/OpenRouter/Bedrock/Vertex basic coverage。
3. **Account lifecycle / health / routing third**: 账号池、sticky、cooldown、失败原因归类、成本/毛利归因。
4. **高级协议保真 fourth**: Responses advanced tools、Computer Use、Gemini Live、prompt caching controls。

“检测对抗”只能做安全等价：健康探测、异常隔离、账号风险评分、滥用限制、人工恢复 SOP。不要把规避平台规则当公开卖点；否则 clean-room 和合规风险都会放大。

### 4. 资金有限，1 年内应证明什么 metric

建议 Owner 只选 4 个北极星指标，避免被 stars/provider count 误导：

1. **付费运营方数**: 10-20 个真实运营方或 1 个 Owner 自营站跑出稳定收入；至少 3 个月留存。
2. **Token GMV / processed spend**: 月处理 token 成本或充值流水达到可融资叙事的量级，例如 $50k-$100k/month GMV；毛利、坏账、退款要单独列。
3. **可靠性**: 成功路由率、账单准确率、账号池可用率；目标可设 99.9% gateway uptime、<0.1% usage ledger correction rate。
4. **商业效率**: MRR/净收入、gross margin、operator CAC payback、top-up repeat rate。对 SaaS Edition，$20k-$50k MRR 比“支持 200 providers”更能 raise/卖出。

这些数字不是 Owner 决策，而是基于竞品 benchmark 的建议：Portkey 的退出叙事来自 spend/tokens/orgs，OpenRouter 的估值叙事来自 GMV/annualized revenue，Helicone 的收购叙事来自 tokens/orgs/end users [R-PORTKEY-OSS][R-PANW-PORTKEY][R-OPENROUTER-SACRA][R-HELICONE-MINTLIFY]。

## 数据 confidence 矩阵

| 断言类型 | 数据强度 | 缺口 |
| --- | --- | --- |
| 竞品官方价格 / 功能 | 高 | 官方 pricing 随时变，需每月复核。 |
| GitHub stars / release 活跃 | 高（作为公开热度） | 不代表真实用户或收入；GitHub API rate/redirect 可能影响单次读数。 |
| Portkey enterprise 市场信号 | 高 | 收购金额未公开；Palo Alto deal 仍有 closing 条件。 |
| OpenRouter 收入 / 用户数 | 中低 | Sacra 是第三方估算；官方未披露用户数/收入。 |
| 中国中转站数量 | 中 | TokenNav/Token1000 是导航站，不是监管或财务审计；有重复、失效、推广链接。 |
| 中国中转价格 | 中低 | 站点价格实时变；“1 元/刀”“1:1 充值”口径不统一，可能有倍率和模型分组。 |
| 2026 protocol 趋势 | 高 | 官方 docs 能证明功能存在和更新，但不能直接证明付费采用率。 |
| 付费意愿排序 | 中 | 由 pricing、收购、站点行为推断，需要访谈/真实交易数据验证。 |

## 风险与盲点（自评）

- 中国市场公开数据高度 SEO 化，很多站点带推广/返利链接；本报告只把它们作为“生态存在和卖点”证据，不当作收入事实。
- 未使用 SimilarWeb/付费数据库；流量和收入只有 Sacra/官方披露/站点自称，置信度不均。
- 未打开 Claude lane 草案，因此不会交叉污染，但也没有对照另一 lane 的遗漏。
- 没有做终端用户访谈；Q1 的付费意愿是基于公开 pricing/产品定位推断。
- 本报告未提供法律意见；中国合规部分只说明官方规则和产品风险。

## URL refs (with access dates)

| Ref | URL | Access date | 简短引文 / 证据 |
| --- | --- | --- | --- |
| R-OPENAI-CHANGELOG | https://developers.openai.com/api/docs/changelog | 2026-05-09 | “Chat Completions and Responses API”；“Tool search”；“Open Responses”。 |
| R-OPENAI-DATA | https://developers.openai.com/api/docs/guides/your-data | 2026-05-09 | “Extended prompt caching requires...” and `/v1/responses` endpoint limits. |
| R-ANTHROPIC-CU | https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool | 2026-05-09 | “Computer use is in beta”；“Screenshot capture”；“standard tool use pricing”。 |
| R-ANTHROPIC-THINKING | https://platform.claude.com/docs/en/build-with-claude/extended-thinking | 2026-05-09 | “Extended thinking with prompt caching”；“1-hour cache duration”。 |
| R-GEMINI-CACHE | https://ai.google.dev/gemini-api/docs/caching | 2026-05-09 | “Implicit caching is enabled by default”；“TTL defaults to 1 hour”。 |
| R-GEMINI-LIVE | https://ai.google.dev/gemini-api/docs/live-api | 2026-05-09 | “Stateful WebSocket connection”；input audio/images/text. |
| R-XAI | https://docs.x.ai/developers/models | 2026-05-09 | “Tools Pricing”；“Batch API Pricing”；last updated May 7, 2026. |
| R-MISTRAL-CACHE | https://docs.mistral.ai/studio-api/conversations/advanced/prompt-caching | 2026-05-09 | “billed at 10%”；`prompt_cache_key`. |
| R-PORTKEY-PRICING | https://portkey.ai/pricing | 2026-05-09 | Free 10k logs; Production $49/mo; Enterprise custom. |
| R-PORTKEY-OSS | https://www.globenewswire.com/news-release/2026/03/24/3261574/0/en/Portkey-s-Gateway-is-Now-Fully-Open-Source-Processing-over-1-Trillion-Tokens-Every-Day.html | 2026-05-09 | “1T+ tokens”; “120M+ AI requests”; “24,000+ organizations”. |
| R-PANW-PORTKEY | https://investors.paloaltonetworks.com/node/20456/pdf | 2026-05-09 | “intent to acquire Portkey”; “AI Gateway for Prisma AIRS”. |
| R-HELICONE-PRICING | https://www.helicone.ai/pricing | 2026-05-09 | Hobby 10,000 free requests; Pro $79; Team $799; Enterprise on-prem. |
| R-HELICONE-OVERVIEW | https://docs.helicone.ai/getting-started/platform-overview | 2026-05-09 | “AI Gateway with pass-through billing”; “0% markup”. |
| R-HELICONE-MINTLIFY | https://www.helicone.ai/blog/joining-mintlify | 2026-05-09 | “14.2 trillion tokens”; “16,000 organizations”; “33 million end users”. |
| R-OPENROUTER-PRICING | https://openrouter.ai/pricing | 2026-05-09 | “400+ models”; “60+ providers”; “5.5%”; “50 reqs/day”. |
| R-OPENROUTER-FAQ | https://openrouter.ai/docs/faq | 2026-05-09 | “5.5% ... fee”; “1M BYOK requests per-month are free”. |
| R-OPENROUTER-ROUTING | https://openrouter.ai/docs/guides/routing/provider-selection | 2026-05-09 | provider preferences include `order`, `allow_fallbacks`, `data_collection`, `zdr`, `sort`. |
| R-OPENROUTER-SACRA | https://sacra.com/research/openrouter/ | 2026-05-09 | Third-party estimate: $50M annualized revenue; $1.3B raise talk. |
| R-HF-PRICING | https://huggingface.co/docs/inference-providers/en/pricing | 2026-05-09 | Free $0.10 credits; Pro/Team $2 credits; routed/custom provider key. |
| R-TOGETHER-PRICING | https://www.together.ai/pricing | 2026-05-09 | Serverless per 1M tokens; Kimi/Qwen/DeepSeek model examples. |
| R-TOGETHER-OVERVIEW | https://docs.together.ai/docs/inference/overview | 2026-05-09 | “Serverless models”; “Dedicated endpoints”. |
| R-FIREWORKS | https://fireworks.ai/pricing | 2026-05-09 | cached input 50%; H100/H200 $7/hr; B200 $10/hr. |
| R-REPLICATE | https://replicate.com/pricing | 2026-05-09 | “pay for the time”; image/video examples. |
| R-VERCEL-PRICING | https://vercel.com/docs/ai-gateway/pricing | 2026-05-09 | “pay-as-you-go”; “no markups”; $5/month included. |
| R-VERCEL-PROVIDERS | https://vercel.com/docs/ai-gateway/models-and-providers/provider-options | 2026-05-09 | “dynamically chooses”; `order` / `only`; “caching: auto”. |
| R-CF-AI | https://workers.cloudflare.com/solutions/ai | 2026-05-09 | “AI Gateway”; “caching, rate-limiting, model fallback”. |
| R-CF-WORKERSAI | https://developers.cloudflare.com/workers-ai/ | 2026-05-09 | “50+ open-source models”; “serverless, pay-for-what-you-use”. |
| R-ANYSCALE | https://www.anyscale.com/pricing | 2026-05-09 | Hosted/BYOC; pay-as-you-go and committed contracts. |
| R-MODAL | https://modal.com/pricing | 2026-05-09 | “pay for what you use”; H100 $0.001097/sec; $30/mo free credit. |
| R-BEDROCK | https://aws.amazon.com/bedrock/pricing/ | 2026-05-09 | batch inference “50% lower price” than on-demand. |
| R-VERTEX | https://cloud.google.com/gemini-enterprise-agent-platform/generative-ai/pricing | 2026-05-09 | Google Cloud pay-as-you-go and custom quote. |
| R-AZURE | https://azure.microsoft.com/en-us/pricing/details/azure-openai/ | 2026-05-09 | Azure OpenAI pricing table by deployment/model/region. |
| R-GH-OSS | GitHub REST API repo/release endpoints for Wei-Shaw/sub2api, songquanpeng/one-api, QuantumNous/new-api, BerriAI/litellm, Helicone/helicone, Portkey-AI/gateway, langfuse/langfuse, qixing-jk/all-api-hub, envoyproxy/ai-gateway | 2026-05-09 | Fields read: stargazers_count, pushed_at, releases/latest. |
| R-GH-NPM | https://api.npmjs.org/downloads/point/last-month/ai and https://api.npmjs.org/downloads/point/last-month/@ai-sdk/provider | 2026-05-09 | `ai`: 51,491,438 downloads; `@ai-sdk/provider`: 72,611,209 downloads. |
| R-GREATROUTER | https://www.greatrouter.com/pricing | 2026-05-09 | “50+ 主流 AI 模型”; “价格低于官方 10% 以上”; Azure 官方直连. |
| R-QINZHIAI | https://docs.qinzhiai.com/ and https://docs.qinzhiai.com/guide/pricing.html | 2026-05-09 | “1:1 充值”; “700+ 模型”; “国内直连”. |
| R-TOKENNAV | https://tokennav.cc/ | 2026-05-09 | Snapshot: totalCount 106, availableCount 92; paymentMethods include 微信/支付宝/USDT/VISA. |
| R-TOKEN1000 | https://www.token1000.com/ | 2026-05-09 | “59 收录中转站”; “10 已验证推荐”; “9 高风险勿入”. |
| R-OPENAIROUTER-CN | https://openairouter.net/ | 2026-05-09 | Search result captured “az 1元/刀，官转 2.5元/刀”. |
| R-CLAUDE-ZZ | https://claude-zhongzhuan.cloud/ | 2026-05-09 | Search result captured “国内直连中转”; “号池代理”. |
| R-GOV-GENAI | https://www.gov.cn/zhengce/zhengceku/202307/content_6891752.htm | 2026-05-09 | 《生成式人工智能服务管理暂行办法》；第十七条安全评估/算法备案。 |
| R-REDDIT-CHINA-CLAUDE | https://www.reddit.com/r/China/comments/1t5x0p0/how_to_buy_cheap_claude_tokens_in_china/ | 2026-05-09 | Reddit discussion, low-confidence community signal about “中转站” as API proxy. |
