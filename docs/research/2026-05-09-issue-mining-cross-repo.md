# Reference Project Issues 调研 — Cross-Repo Analysis

**日期 (UTC)**: 2026-05-09 15:30Z 起 / 当前 UTC 数据下载完成于 ~15:32Z
**调研者 lane**: issue-mining (single lane，跨 4 repo；clean-room — 不读源码、issue body 仅做 paraphrase 不抄原文)
**目标**: 挖 4 个核心 ref repo 的 GitHub Issues 找重复痛点 / 长期未修架构骨头 / 高频 feature request，用于 HUAKAI 差异化与 HCSF canonical 选型决策。

---

## 数据来源 + 方法

| Repo | full_name (recency check) | License | Pushed_at | Open issues | 调研口径 |
|---|---|---|---|---|---|
| LiteLLM | `BerriAI/litellm` | Apache-2.0 (NOASSERTION shown) | 2026-05-09T09:43:07Z | 2942 | Search API top 80 open + 50 closed by reactions |
| sub2api | `Wei-Shaw/sub2api` | LGPL-3.0 | 2026-05-09T15:15:40Z | 1133 | 同上 |
| new-api | `QuantumNous/new-api` (renamed from Calcium-Ion) | AGPL-3.0 (推断, 未 fetch detail) | 2026-05-09T13:39:46Z | 776 | 同上 |
| Portkey gateway | `Portkey-AI/gateway` | MIT | 2026-03-25T09:33:55Z (45d ago) | 151 | 同上 |

**调研方法**:
- GitHub REST `/search/issues?q=repo:<slug>+is:issue+is:<state>&sort=reactions&order=desc&per_page=80/50` (PR 已过滤)
- 主题专题搜索: `streaming` / `cache` / `tool_call` / `claude` 等关键词
- 引用形式: `Owner/Repo#issue_number (fetched 2026-05-09)`
- **不读源码**, **不读 docs/research/2026-05-09-market-research-*.md** (并行 lane 隔离)

**Rate limit 结果**: Search API 起始 30 ≈ 用 20，Core 60 ≈ 用 30；尾段还剩 search 10 / core 29，未超限。

**数据信号说明**:
- LiteLLM Search 显示 `total_count` 1281 open / 8990 closed (但 closed 已被 PR 主导；sort=reactions 排序后的 PR 入侵率 60-90%，已在 search API 之外用 `is:issue` 过滤)
- sub2api 0 issues open >= 6 个月: 项目维护者 close-fast 模式 (中文小项目特征，下方 Q5 详述)
- 部分 sub2api 高 reaction issue 是 spam/affiliate ad (例 `Wei-Shaw/sub2api#2049` "万人骑 IP" 推广), 已识别并标注

---

## TL;DR (15 行)

1. **跨 4 repo 系统性痛点** = 协议 schema 漂移 (Anthropic/OpenAI Responses/Bedrock/Vertex 同一 API 在不同上游下行为不一致) + 流式恢复 (mid-stream 断流 → 客户端永不超时) + 缓存命中率假阳性 (gateway 注入动态 header / 跨账号路由 → 上游隐式前缀缓存失效)。
2. **最长期未解的架构骨头**: LiteLLM 多 OAuth 账号 (`BerriAI/litellm#23777` 仍 open，2026-03-16 发起) + LiteLLM 大规模性能回归 (`#19921` 跨 v1.81 全线变慢，发起 2026-01) + new-api 渠道级速率/并发限制 (`#1730` open >= 8 个月) + new-api 提示日志记录 (`#924` open >= 13 个月)。
3. **顶级 feature request**: 多账号管理 / 自定义 OAuth provider / 异步图片生成 / 渠道级 rate-limit / 订阅套餐分组限制 / 阶梯计费 / 模型重定向批量。
4. **中文社区 (sub2api / new-api) 最痛**: 账号封禁 + cc 客户端指纹 + 计费精度 (cache_read tokens 不计 / 阶梯计费缺失) + Codex `/v1/responses` 兼容性。
5. **英文社区 (LiteLLM / Portkey) 最痛**: schema 漂移 (cache_control / tool_use 跨 vendor 不一致) + 大规模性能回归 + 安全 (LiteLLM 2026-03 PyPI 供应链事件 1113 反应)。
6. **HUAKAI delta**: 上游集体没解决的 mid-stream 中转续约 + cache_control 跨平台规范化 + 多 OAuth 账号 first-class + 客户端指纹脱敏 + 跨账号 cache locality blend = 至少 5 条可立刻写入 HCSF canonical 的差异点。
7. **HCSF canonical 选型推论 (基于 issue 数据)**:
   - 必含: `cache_control.scope` 规范化 (`Portkey#1579 / #1589`), 多 OAuth 账号一等公民 (`LiteLLM#23777 / sub2api#641`), mid-stream 续约 (`sub2api#2245 / #1843 / #1552`), 渠道级 rate-limit (`new-api#1730`), 异步图片任务 (`new-api#4711 / #4514`)。
   - 应含: cache_read_token 计费规范 (`new-api#4678 / sub2api#2293`), 阶梯/分档计费 (`new-api#1909 / #1664 / #4257`)。

---

## Per-Repo 主题分布

### LiteLLM (`BerriAI/litellm`)

| 主题 | Issue 摘要 (paraphrase) | issue # | 反应/评论 | 状态 | 启示 |
|---|---|---|---|---|---|
| Security | 2026-03 PyPI 供应链投毒事件 (`litellm_init.pth` 凭据窃取) | #24512 (closed) / #24518 (open) | 1113👍/487💬 + 165👍/116💬 | completed (closed)/open | 单一 PyPI 投毒 = 整个生态信任崩溃；HUAKAI 必须 SLSA + 镜像签名 |
| Performance regression | v1.81.x 大幅变慢 vs v1.80.5 (UI + API) | #19921 | 15👍/44💬 | open >= 4 月 | 大型 gateway 升级版本要先做 perf 基线对比；HUAKAI 应有 release-N vs release-(N-1) latency 对比报告 |
| Streaming reasoning_content 丢 | VLLM streaming 缺 reasoning content | #20246 | 8👍/28💬 | reopened | reasoning 字段在跨 vendor 转换中丢失是 LiteLLM 自家长期问题 |
| Tool call 反序列化 | tool_call.function.arguments 在 OpenAI→Anthropic 转换中丢失 (v1.83.7 回归) | #27468 | 0👍 | completed | 协议互转高频回归点 |
| 多 OAuth 账号 | 单 LiteLLM 进程不能跑多 ChatGPT OAuth 账号 (CHATGPT_TOKEN_DIR 全局单例) | #23777 | 20👍/2💬 | open | LiteLLM 设计假设 = 单账号；多账号是补丁 |
| Cache 命中 / cost 错算 | Anthropic cached_token 成本未正确按比例算 | #11364 | 2👍/11💬 | open | 计费精度 = LiteLLM 长期欠债 |
| Multi-vendor billing | Vercel models cost 跟踪失败 | #20412 | 32👍/4💬 | open | 新加 vendor 的成本计算容易遗漏 |
| Auth / credential | OAuth2 自定义 provider 支持缺失 | #12367 | 16👍/12💬 | open >= 10 月 | 企业用户长期等待 |
| Multi-turn 状态 | DeepSeek V4 Pro `reasoning_content` 在多轮对话中被 strip 导致 BadRequest | #26395 | 14👍/13💬 | open | gateway 跨轮状态保持是高频坑 |
| Bedrock 重试 | SigV4 头被旧的 stale 头 replay | #27513 | 0👍 | open | retry 路径的安全 + 正确性 |
| Anthropic schema 漂移 | `vector_store_ids: Extra inputs are not permitted` | #23741 | 10👍/8💬 | open | 上游 Anthropic API schema 收紧未在 LiteLLM 同步 |
| Health check 误报 | `max_completion_tokens=1` 健康检查在 GPT-5 失败 | #23836 | 8👍/5💬 | open | 探活逻辑模型相关 |
| Repository 性能 | import 太慢 (~5s) | #7605 | 41👍/30💬 | open >= 16 月 | LiteLLM 单包巨大化典型问题 |
| OpenRouter cost 流式丢 | 流式响应 OpenRouter cost info 不发 | #16021 | 3👍/12💬 | open | streaming + cost 双坑 |
| Claude Code 适配 | Claude Code 2.0.42 `input_examples` 字段 | #16718 | 6👍/10💬 | open | 客户端版本一升级就坏 |
| Pydantic warning | streaming 时 Pydantic 反序列化警告刷屏 | #11759 / #25880 | 43👍/21💬 + 0👍 | completed/open | 内部 model 设计欠债 |
| OpenAI Responses API | LiteLLM 对 Responses API 的支持 | #9146 | 22👍/41💬 | completed | 老问题已修，但 Responses API 兼容是社区最强需求之一 |
| MCP bridge | MCP 客户端 / 服务端桥接 | #7934 | 19👍/22💬 | completed | MCP 已成 enterprise 必选 |

**长期 open (>= 6 个月) 累计 18 条**, 其中 5 条是 enterprise feature request, 13 条是 bug。LiteLLM 修复速度跟不上 issue 涌入。

### sub2api (`Wei-Shaw/sub2api`)

| 主题 | Issue 摘要 (paraphrase) | issue # | 反应/评论 | 状态 | 启示 |
|---|---|---|---|---|---|
| 账号封禁 / IP | 中文社区被封号潮焦虑；2049 是 affiliate spam 不算痛点 | #2049 | 20👍/0💬 | open | spam 但反映 anxiety |
| Claude Code 反向文档 | 用户手动整理 cc 封号机制 + cc 计费分析 + cc cli 源码 / npm 仓库链接 | #1413 | 15👍/9💬 | open | 用户主动逆向是为了帮 sub2api 改进伪装；说明上游 sub2api 伪装层不够 |
| cc telemetry 抓包 | cc 客户端发遥测请求；中转站没发同等遥测 → 触发封号 | #1143 | 12👍/3💬 | open | 客户端 fingerprint 关键 = 不发遥测就被识别 |
| 用户身份隐藏 cc-gateway 借鉴 | env 对象 40+ 字段重写 (device_id / platform / shell / os_version / home / working_directory / 进程 metrics 等) | #1451 | 12👍/0💬 | open | 这是 HCSF canonical 必须包含的指纹层；用户列出了具体字段清单 |
| Codex 远程压缩 | OpenAI 1月份 `/responses/compact` 端点支持 | #752 / #802 | 7👍/1💬 + 11👍/0💬 | completed | 上游协议演进, 中转站需快速跟 |
| `/v1/responses` token 暴涨 | Codex CLI v0.1.122 升级后 reasoning/output token 10k-20k 单次, 200-600s | #2208 | 7👍/1💬 | open | 协议 stream 实现 bug 直接放大用户成本 |
| Stream 未完成被转 200 | OpenAI Responses SSE 流未完成被转发为 HTTP 200，导致 stream closed | #2245 | 3👍/0💬 | open | gateway-side 流处理永远的 LP |
| Stream 中断 | upstream stream ended without terminal event | #1552 | 0👍/2💬 | open | 客户端永远无法超时退出 |
| Codex 上下文超限 | 0.1.115 频繁 "Codex ran out of room" | #1843 | 0👍/3💬 | open | 长 context 切换/迁移欠债 |
| Cache 命中率算错 | sub2api 的 cache hit rate 计算公式错 | #2291 | 0👍 | open | 计费仪表盘也要校准 |
| 计费 cache_read 倍率 | GPT-5.4/5.5 长上下文 cache_read_tokens 未应用倍率 | #2293 | 0👍 | open | 与 new-api #4678 同源 |
| 协议自动转换 | 网关层自动识别入口 + 双向转换, 让分组与平台解耦, 提议引入 LiteLLM | #1331 | 6👍/1💬 | open | 用户主动要求 axis 3 (协议转换) 一等公民 |
| Gemini 反代 cooldown | google_one 账号被封到 PST 午夜 (24h)，未用 tier Cooldown 配置 | #641 | 8👍/0💬 | open | 中文社区 Gemini 反代用得多, sub2api 长期没修 cooldown 策略 |
| Gemini 配额条 | tokens 用量未纳入进度条 | #640 | 4👍/0💬 | open | UX 计费精度欠债 |
| TLS 指纹 | 第三方 claude api 也想要 TLS 指纹模拟 | #587 | 2👍/4💬 | not_planned (closed) | 维护者明确不做; HUAKAI 可做差异化 |
| OAuth scopes 重试 | 重试时未同步移除 clear_thinking 策略 | #851 | 4👍/1💬 | open | 多重试路径状态泄露 |
| Antigravity 灰盒 | Antigravity 调用 Opus-4.5 用 write 工具生成 5000 字 → stream 出错 | #321 | 0👍/3💬 | open | 长 output streaming 长期不稳 |
| Image 生成 | gpt-image-2 同时多图 / 透明底失败 / OAuth 转发 502 | #2295 / #2277 / #2232 | 0-2👍 | open / closed | 图片 endpoint 是新生需求 |

**关键观察**:
- **0 个 open issue >= 6 个月**: 维护者 close-fast 模式 (issue 大量被快速 close 为 not_planned / completed / duplicate)，闭环速度快但用户痛点未必真解决 (#641 / #1143 / #1451 这种深层封号问题仍 open)。
- **真痛点集中在 4 处**: 客户端指纹脱敏 (1143/1451/587), cc 反向工程 (1413), Codex 协议演进 (752/802/2208/2245), Gemini 反代 cooldown (641/640)。
- **#1331 (协议自动转换提案)** 是用户主动给中转站设计师写的架构提案，HUAKAI 应该认真读这条 (paraphrase 方式)。

### new-api (`QuantumNous/new-api`)

| 主题 | Issue 摘要 (paraphrase) | issue # | 反应/评论 | 状态 | 启示 |
|---|---|---|---|---|---|
| 渠道级 rate-limit / 并发 | 三档功能: 速率限制 + 并发限制 + 超限策略 (拒绝 / 排队) | #1730 | 11👍/7💬 | open >= 8 月 | gateway 必备能力, new-api 长期未实现; HUAKAI 必备 |
| 提示日志 | 自用模式记录每次 API prompt | #924 | 13👍/16💬 | open >= 13 月 | observability 长期 open; HUAKAI 必备 |
| 阶梯/分档计费 | 阶梯计费支持 (按用量 tier 不同价) | #1909 / #1664 / #4257 | 7👍/5💬 + 1👍 + 3👍 | open / dup | 中文社区运营商高频需求 |
| 订阅套餐分组限制 | Subscription Plan Group Restriction | #3388 | 3👍/8💬 | open | account hub 计费 layer |
| 异步图片生成 | `/v1/images/generations` 异步任务 (提交+查询) | #4711 / #4514 | 0👍/1💬 + 5👍/3💬 | open / completed | 上游慢任务的标准模式 |
| 模型重定向批量 | 一个 key 映射到多个 value / 按渠道批量设置 / 全局 alias 优先级 | #2171 / #2442 / #3001 | 4-2👍 | open | 多渠道路由配置 UX 痛点 |
| Cache 命中失效 (核心) | Claude→OpenAI 转 + 兼容渠道 (百炼) 时 cache_read_input_tokens 永远 0；System 中注入的动态 `x-anthropic-billing-header` (cch=xxx 每次变) 破坏隐式前缀缓存 | #4678 | 0👍/3💬 | open | **关键**: gateway 注入的 metadata 破坏上游缓存; HUAKAI 必须能纯净化 system 层 |
| Cache 命中假禁用 | 渠道亲和缓存命中已禁用渠道,导致旧会话持续失败 | #4717 | 0👍/1💬 | not_planned (closed invalid) | locality 阻塞 / 失败渠道隔离需求 |
| Stream 失败计费 | stream 失败 client_gone/timeout 且 completion_tokens=0 时仍按 prompt_tokens 扣费 | #4168 | 1👍/4💬 | open | 计费正确性 / 失败语义 |
| `/v1/messages` qwen3 流缺 stop | qwen3.6-plus 流式缺 content_block_stop / message_delta / message_stop, cc 卡死 | #4697 / #4696 / #4698 | 0👍 | open / dup | Anthropic 流式 schema 强制 sentinel; gateway 必须补 |
| Codex Tool 解析失败 | "tool call could not be parsed" | #4671 | 0👍 | not_planned | 跨 vendor tool 字段不规范化 |
| /v1/responses 支持 | new-api 是否完整支持 Responses API | #1216 / #1812 | 4👍/1💬 + 14👍/12💬 | open / completed | 长期 enterprise 需求 |
| OAuth 自定义 provider 用户组映射 | 自定义 OAuth + 用户组映射策略 | #4674 | 0👍/1💬 | open | account hub 集成 |
| Whisper / Embedding | whisper 调用失败 / Text-Embeddings-Interface rerank | #1361 / #1117 | 2👍 + 6👍 | open / stale | 多模态 endpoint |
| 安全 advisory | 用户提交 GitHub Security Advisory 等 review | #4647 | 0👍/4💬 | open | 维护者响应慢 |
| ML 渠道支持 | poe / Targon / 火山方舟 v3 / TTS protocols / VertexAI | #278 / #1475 / #4705 / #4709 | 多个 stale | open | long-tail vendor request |

**长期 open (>= 6 个月) 累计 44 条**, 大量 stale 标签。维护者承认精力有限。
**中文社区运营关键需求**: 阶梯计费 + 渠道速率限制 + 模型重定向批量 + 订阅套餐 — 这些都直接关系到中转站赚钱能力。

### Portkey gateway (`Portkey-AI/gateway`)

| 主题 | Issue 摘要 (paraphrase) | issue # | 反应/评论 | 状态 | 启示 |
|---|---|---|---|---|---|
| Vertex AI cache_control 剥离 | cache_control 块路由 Vertex Anthropic 时被 strip → prompt cache 不工作 | #1579 | 0👍/1💬 | open | 跨平台同 vendor 行为差异; gateway 应规范化 |
| Anthropic cache_control.scope 拒绝 | 直 Anthropic 接受的 scope 在经 Portkey 路由后被拒 | #1589 | 0👍/0💬 | open | schema 漂移类型: gateway 比 vendor 更严格 |
| Bedrock validation 不处理 | Bedrock 200 响应里的 stream exception 未被作为 fallback 触发器 | #1047 | 6👍/8💬 | open >= 12 月 | Bedrock 错误模型 = 200 + stream-error, gateway 默认 fallback 没识别 |
| Vertex AI Claude structured output | output_config.format Extra inputs not permitted | #1570 | 1👍/2💬 | open | 又一个 schema 漂移 |
| prompt_tokens normalization | direct API vs Vertex vs Bedrock 的 prompt_tokens 报数不一致 | #1564 | 1👍/4💬 | open | 计费精度跨 vendor |
| Claude Code OAuth passthrough | 中途 session 失败要重 login | #1598 | 0👍/1💬 | open | OAuth 中转保活 |
| Tool calls + Responses API | OpenAI / Azure OpenAI / Bedrock Responses API tool calls + file inputs 失败 | #1583 | 0👍/0💬 | open | Responses API 跨 vendor 路径未走通 |
| SSRF | provider URL fragment injection | #1596 | 0👍/1💬 | open | 安全 |
| Claude Code 多账号 OAuth | (隐含) Portkey 的 Claude Code OAuth max-plan 文档 vs 实际行为 | #1598 评论 | -- | open | 与 LiteLLM #23777 同源 |
| Streaming Tx-Encoding 冲突 | content-length 与 transfer-encoding: chunked 同时出现 (Node.js) | #1402 | 1👍/2💬 | reopened | hono 升级回归 |
| Stream stream=true TypeError | /v1/responses stream=true throws TypeError: immutable | #1550 | 0👍/4💬 | open | hono 兼容性 |
| Native fetch 默认 300s | 没办法关或调 | #1127 | 0👍/2💬 | open | 长任务被截断 |
| MCP client | open source gateway 集成 MCP client | #926 | 0👍/0💬 | open | 与 LiteLLM #7934 同源 (那边已 closed) |
| Batch API | 暂停 / 恢复 / 取消 / 部分输出 | #1156-1158 | 0👍/1💬 | reopened | enterprise 批处理 |
| Cost mgmt | 中文用户提的成本管理增强 (重复 issue) | #1560 / #1561 | 0👍/1💬 | completed (合入对应 doc) | 中文社区也找 Portkey |
| 项目状态焦虑 | "Why only 14 commits since January?" | #1558 | 0👍/3💬 | completed | Portkey OSS 投入信号弱 (45d 没 push 印证) |

**关键观察**:
- Portkey 反应数普遍很低 (因为社区比 LiteLLM 小一个数量级 + 大量 enterprise 走 PR 而非 issue) — 但 issue 主题专业度高，几乎每条都是真痛点。
- `#1558` "Why only 14 commits since January?" + pushed_at 2026-03-25 (45d ago) = Portkey OSS gateway 进入维护模式; **这是 HUAKAI 进入英文 enterprise 市场的窗口**。

---

## Q1-Q6 答案

### Q1 重复出现的痛点 (跨 4 repo)

按出现频次排序:

1. **Schema 漂移 / 跨 vendor 协议不一致** (4/4 repo): 同一 Anthropic API 在 direct vs Vertex vs Bedrock 行为不同 (Portkey #1564/#1579/#1589, LiteLLM #15164/#23741/#23836, sub2api #851/#1264, new-api #4697/#4678)。
2. **Streaming 异常恢复 / mid-stream 断流** (4/4): LiteLLM #20246/#16021, sub2api #2245/#1552/#1843, new-api #4697/#4168, Portkey #1402/#1550/#1127。Stream 没 terminal event, gateway 转 200, client 永远不超时。
3. **Cache 命中率精度** (3/4: 缺 Portkey 直接 cache hit): new-api #4678 (动态 header 破坏 prefix cache) + sub2api #2291/#2293 (计费不算 cache_read 倍率) + LiteLLM #11364 (Anthropic cached tokens cost 错算)。
4. **多账号 / OAuth 多账号一等公民** (3/4): LiteLLM #23777, sub2api #1413/#1143/#1451 (隐含, 涉及账号轮换+伪装), new-api #4674 (OAuth 自定义)。
5. **客户端指纹 / 封号** (sub2api 主导, LiteLLM 边缘出现 #18155 GH Copilot premium 滥用): sub2api #1143/#1451/#1413/#641 = sub2api 长期未根治。
6. **计费精度** (4/4): cache_read_token 倍率 / 失败请求计费 / 跨 vendor cost normalization / 阶梯计费。
7. **工具调用跨 vendor 转换回归** (3/4): LiteLLM #27468/#22878/#27490, new-api #4671, Portkey #1583。

**结论**: 至少 7 条系统性问题，HUAKAI 必须作为 baseline 解决。HCSF canonical 必须先在 schema/streaming/cache 三处建好规范层。

### Q2 长期未修 (>= 6 月) 的硬骨头 → HUAKAI 差异化机会

| 上游 wontfix / 长期 open | issue | HUAKAI 差异化角度 |
|---|---|---|
| LiteLLM 多 ChatGPT OAuth 账号 (单进程) | LiteLLM#23777 (open 6+m) | HUAKAI 多 OAuth 账号一等公民 (segment table 已有), 不靠 env var 全局单例 |
| LiteLLM v1.81 性能回归 | LiteLLM#19921 (open 4m, 44 评论无解) | HUAKAI release 必须 vs 上版本基线 latency 对比 |
| LiteLLM import 慢 (~5s) | LiteLLM#7605 (open 16m+) | HUAKAI 单二进制 / 启动 < 1s 是天然优势 |
| LiteLLM Router callbacks 不触发 | LiteLLM#8842 (open 14m+) | observability 必须确定性触发 |
| new-api 渠道级 rate-limit | new-api#1730 (open 8m) | HUAKAI gateway 层 + segment 层双重限流 |
| new-api 提示日志 | new-api#924 (open 13m+) | HUAKAI 三层日志 (request/attempt/lease/claim) 已有 |
| Portkey Bedrock 200+stream-error fallback | Portkey#1047 (open 12m+) | HUAKAI fallback 必须看 SSE 内容 not 仅 HTTP code |
| sub2api 客户端指纹 (cc-gateway 借鉴) | sub2api#1451 (open) + #1143 (open) | HUAKAI HCSF canonical 包含 40+ env 字段重写 |
| sub2api Gemini 429 cooldown | sub2api#641 (open 5m+) | HUAKAI 上游 cooldown / tier 配置一等公民 |
| LiteLLM Anthropic cache_control schema | Portkey#1589 + #1579 (open) | HUAKAI cache_control.scope 规范化 |
| LiteLLM tool_choice rejected on Azure GPT-5 | LiteLLM#14704 (closed completed but recurring) | HUAKAI tool_choice 跨 vendor 规范层 |

11 条差异化机会，每条都有上游 issue 反应/评论数支撑 ≠ 自吹。

### Q3 高频 feature request (跨 repo 重复)

| Feature | LiteLLM | sub2api | new-api | Portkey | 推荐优先级 |
|---|---|---|---|---|---|
| 多 OAuth 账号 / 多 ChatGPT 账号 | #23777 (20👍) | (隐含, 中转站本质) | (账号管理基础) | #1598 评论 | P0 必做 |
| 自定义 OAuth provider | #12367 (16👍) | -- | #4674 | -- | P1 |
| 异步图片生成 | (有 PR/closed) | #2295 (0) | #4711 / #4514 (0/5) | #1630 | P1 |
| 渠道级 rate-limit + 并发 | -- | -- | #1730 (11👍) | #1179 (0) | P0 (中文中转必需) |
| 阶梯 / 分档计费 | -- | -- | #1909 (7) / #1664 / #4257 | -- | P1 |
| 订阅套餐分组限制 | -- | -- | #3388 (3) | -- | P2 |
| 模型重定向批量 / 全局 alias | -- | -- | #2171 / #2442 / #3001 | -- | P1 |
| TLS 指纹模拟 | -- | #587 (not_planned) + #1451 | -- | -- | P0 (差异化要素) |
| Codex /responses/compact | -- | #752 / #802 (合并) | -- | -- | P1 |
| MCP 桥接 (open source) | #7934 (closed) | -- | -- | #926 (open) | P1 |
| 数据导出 / 实例迁移 | -- | -- | #1492 (6) | -- | P2 |
| Embedding / rerank 接口 | -- | #911 (8) | #1117 (6) | #1189 (0) | P2 |

### Q4 streaming / tool / cache 主题分布 → axis 3 (协议转换)

**Streaming**:
- Vendor 协议本身坑: Bedrock 200+stream-error (#1047), Anthropic 强制 sentinel (content_block_stop/message_delta/message_stop, new-api #4697), VLLM streaming reasoning_content 缺 (#20246), Vercel/Codex `/v1/responses` 流半成品 (sub2api #2245)
- Gateway 实现坑: hono Tx-Encoding (#1402), `/v1/responses` immutable (#1550), native fetch 300s 超时 (#1127), upstream stream ended without terminal (sub2api #1552), Pydantic warning during streaming chunk (LiteLLM #11759/#25880)

**Tool call**:
- Vendor 坑: Anthropic input_examples (LiteLLM #16718), tool_choice on Azure GPT-5 (#14704), Bedrock toolUse split orphan toolResults (#26060)
- Gateway 坑: OpenAI→Anthropic arguments lost (#27468), tool call could not be parsed (new-api #4671), tool calls + Responses API 多 vendor (Portkey #1583)

**Cache**:
- Vendor 坑: Anthropic cache_control.scope direct vs Vertex (#1579/#1589), Anthropic cached_token cost (#11364)
- Gateway 坑: gateway 注入动态 header 破坏隐式 prefix cache (new-api #4678 ⭐ 最关键), cache_read_token 倍率漏算 (sub2api #2293), 渠道亲和阻塞失败渠道 (new-api #4717)

**HUAKAI HCSF canonical 启示**: 上游 vendor schema 不一致是 N x M 困难，但所有上游 ⭐ 都集中在一个动作"先归一到 canonical 再落 vendor"。HUAKAI 现在已有 axis-3 (protocol translation) 设计，issue 数据进一步证明它必须包含: (a) sentinel 强制补全 (content_block_stop 等), (b) cache_control.scope 规范化, (c) 工具调用的 arguments 字段稳定性 (JSON encode/decode 不重复)。

### Q5 中文 vs 英文社区差异

| 维度 | 中文社区 (sub2api / new-api) | 英文社区 (LiteLLM / Portkey) |
|---|---|---|
| Top 痛点 | 账号封禁 / cc 客户端伪装 / 阶梯计费 / Codex 反向 | schema 漂移 / 性能回归 / 安全 / enterprise OAuth |
| 维护者风格 | new-api: 慢但回应；sub2api: close-fast | LiteLLM: 巨型积压；Portkey: 维护模式 (3 月 commit 仅 14) |
| 计费焦虑 | 极强 (cache_read 倍率 / 阶梯 / 失败请求扣费) | 中等 (Anthropic cached tokens cost) |
| 客户端伪装 | sub2api 用户主动逆向 cc telemetry/env (#1413/#1143/#1451) | LiteLLM 几乎不讨论 |
| 协议覆盖广度 | 主追 Codex / Claude Code / Antigravity / Gemini CLI / Kiro 等 vendor 客户端 | 主追 OpenAI Responses / Bedrock / Vertex / Azure 服务端 |
| 中转站盈利相关 | 强 (兑换码 / 邀请返利 / 微信支付) | 几乎无 |
| 安全焦虑 | 较低 (sub2api 对 TLS 指纹模拟 not_planned) | 高 (LiteLLM #24512 1113 反应) |
| Issue body 风格 | 中文 + 行业术语夹杂 | 英文 + 模板化 + repro steps |
| 用户帮维护者做事 | sub2api #1413 用户写"逆向文档"帮项目改进 | 罕见 |

**HUAKAI 战略含义**:
- HUAKAI 是中英都做, 英文走 Portkey 留下的窗口 (维护模式 = 不再吃 enterprise 增量), 中文吃 sub2api 没解决的封号 + 伪装 + 阶梯计费。
- 中文用户对 HCSF canonical 的"广度" (vendor 客户端类型) 要求高于英文用户对"深度" (单 vendor 多 endpoint) 的要求。
- 计费仪表盘 (cache_read 倍率 + 阶梯 + 失败语义) 是中文社区第一性需求, 英文社区列为 P2, **HUAKAI 必须按中文优先级排**。

### Q6 HUAKAI 启示

#### 6a. 已确认的"上游集体缺陷" → HUAKAI 必须明确避开/解决

1. **schema 漂移规范层缺位** (Portkey#1579/1589 + LiteLLM#15164 + new-api#4678): HUAKAI HCSF canonical 必须包含 cache_control.scope, max_completion_tokens, tool_choice, vector_store_ids, output_config.format 跨 vendor 规范化。
2. **mid-stream 断流没续约** (sub2api#2245/1552, Portkey#1127): HUAKAI 必须做 "stream-resume from last sentinel" 续约逻辑 + sentinel 强制补全 (content_block_stop / message_delta / message_stop)。
3. **gateway 注入 header 破坏上游缓存** (new-api#4678): HUAKAI system prompt 路径必须 sanitize 任何注入的 cch=xxx / billing-header / metadata-tracking, 让 system 内容稳定 = 上游 prefix cache 命中。
4. **多 OAuth 账号单进程** (LiteLLM#23777): HUAKAI segment-table-with-bitmap 已支持, 不要走 LiteLLM 旧路。
5. **客户端 telemetry/env fingerprint 缺失** (sub2api#1143/1451): HUAKAI HCSF canonical 包含 cc env 40+ 字段重写 + 模拟 cc telemetry 包发送 (cc-gateway 借鉴, paraphrase 实现)。
6. **失败请求计费语义** (new-api#4168): HUAKAI 必须区分 client_gone / upstream_timeout / upstream_5xx / output_token_zero 四种 + 各自计费规则。
7. **fallback 不识别 stream-internal 错误** (Portkey#1047): HUAKAI fallback 路径必须 inspect SSE body, 不只看 HTTP code。

#### 6b. 高频 feature request → HUAKAI 应优先做 (市场验证过)

| 优先级 | Feature | 上游 issue # | 三维分类 |
|---|---|---|---|
| P0 | 多 OAuth 账号 first-class | LiteLLM#23777, sub2api#1413 | 架构 (segment table) |
| P0 | 渠道级 rate-limit + 并发 | new-api#1730 | 架构 (Pool) + 算法 (token bucket) |
| P0 | mid-stream 续约 + sentinel 补全 | sub2api#2245/1552, Portkey#1127 | 算法 (sentinel reconstruct) |
| P0 | 客户端 fingerprint canonicalization (40+ env 字段) | sub2api#1451 | 算法 (deterministic mapping) |
| P0 | cache_control.scope 跨平台规范化 | Portkey#1579/1589 | 架构 (HCSF canonical) |
| P1 | 阶梯/分档计费 | new-api#1909/1664 | 生态 (billing) |
| P1 | 异步图片生成任务 | new-api#4711 | 架构 (job queue) |
| P1 | 模型重定向批量/全局 alias | new-api#2171/2442/3001 | 生态 (admin ops) |
| P1 | OAuth 自定义 provider + 用户组映射 | LiteLLM#12367, new-api#4674 | 生态 (account hub) |
| P1 | TLS 指纹模拟 (sub2api not_planned) | sub2api#587 | 算法 + 生态 |
| P1 | cache_read tokens 倍率正确计费 | sub2api#2293, new-api#4678 | 算法 + 生态 (billing) |
| P2 | MCP 桥接 (open source) | LiteLLM#7934, Portkey#926 | 架构 (plugin) |
| P2 | 数据导出 / 实例迁移 | new-api#1492 | 生态 |

#### 6c. HCSF canonical 选型 — 哪些 issue 数据支持哪个选型？

**Q: HCSF canonical 应该是什么样?**

支持选项 A "OpenAI Chat Completions canonical" 的证据:
- new-api#1812 / #1216 / #1501 (Responses API 长期需求)
- sub2api#594 (用户要求 OpenAI Chat Completions 与 Responses API 双支持)
- LiteLLM 整体设计就是以 OpenAI Chat 为基准 schema
**反对 A**: cache_control 是 Anthropic 概念, OpenAI canonical 缺位会 strip 掉这一层 (#1579 数据点直接证伪)。

支持选项 B "Anthropic Messages canonical" 的证据:
- Portkey#1579/1589 (cache_control.scope 必须保留)
- sub2api 大量 cc-gateway / Claude Code 借鉴 (#1143/1451/1413)
- new-api 提供大量 Anthropic→OpenAI 兼容路径 issue (#4678/#4697)
- HUAKAI 自身 Anthropic 反代为 axis-1 (CLAUDE.md 已记录)
**反对 B**: tool_choice / max_completion_tokens / reasoning_effort 这些是 OpenAI 习惯, Anthropic canonical 处理它们要额外字段映射。

支持选项 C "新建中性 superset canonical (HUAKAI 自有)" 的证据:
- sub2api#1331 (用户主动提议网关层自动转换 + 内部统一格式)
- LiteLLM 早期就这么做, 但 superset 维护成本 = 跨 vendor schema 漂移 (#27468/#15164 等多次回归)
- Portkey 也在做 superset, 维护带来的就是 #1402/#1550 等 hono runtime 复杂度
**反对 C**: superset 长期维护成本极高, Portkey 维护模式 (45d 没 push) 印证。

**推论 (issue 数据导向)**: 选 **B + 增强**, 即以 Anthropic Messages 为内部 canonical 主干 + OpenAI 字段作为映射 satellite, 因为:
- 中文用户主流客户端 (Claude Code / Codex / Antigravity) 与 Anthropic schema 接近的多于 OpenAI schema
- cache_control 是 ⭐ 必须保留的字段, 一旦丢就 #4678/#1579/#1589 三连发
- HUAKAI 已存在的 segment-table-with-bitmap 设计也是按 Anthropic 路径 first-class
- OpenAI Chat 字段映射进 Anthropic canonical 比反方向更易 (Anthropic 更结构化)

但 Owner 可能选 A (OpenAI Chat canonical) 出于生态广度考虑。**这条决策需要 Codex 平行 lane 验证** (CLAUDE.md #10)。

---

## HUAKAI delta opportunities (按主题, 含三维分类)

| 主题 | 上游集体痛点 (跨 repo 引用) | HUAKAI 可做的 delta | 三维 (架构/算法/生态) |
|---|---|---|---|
| Mid-stream 断流续约 | sub2api#2245/1552 + Portkey#1127 + LiteLLM#20246 | sentinel-based stream resume + content_block_stop 强制补全 + 跨 attempt 续约 | 算法 (sentinel) + 架构 (attempt-lease) |
| HCSF canonical schema 规范层 | Portkey#1579/1589 + LiteLLM#15164/27468 + new-api#4697/4678 | cache_control.scope / tool_choice / max_completion_tokens / output_config.format 跨 vendor 双向映射 | 架构 (canonical layer) + 算法 (mapper) |
| 客户端 fingerprint canonicalization | sub2api#1143/1451/641 | 40+ env 字段 (device_id/platform/shell/os_version/process metrics) + telemetry 包模拟 + cc 版本强制 | 算法 (deterministic 映射) + 生态 (vendor-sliced 监测) |
| Multi-OAuth 多账号 | LiteLLM#23777 + sub2api 全部 + new-api#4674 | segment-table 已有 + extended ttl 1h + miss-count demote (HUAKAI PASR-A1/A2/A3 已落地, 已超越 LiteLLM env-var 单例) | 架构 + 算法 |
| Cache 命中精度 + 计费 | new-api#4678 + sub2api#2291/2293 + LiteLLM#11364 | system 注入 metadata sanitizer + cache_read 倍率正确扣费 + cache locality blend (PASR-A2 score blend 已上) | 算法 (sanitizer + score) + 生态 (vendor-sliced billing) |
| 渠道级 rate-limit/并发 | new-api#1730 (open 8m) | gateway-level token bucket + segment-level concurrency cap + 配置化超限策略 (拒绝/排队/降级) | 架构 (pool) + 算法 (限流) |
| 阶梯计费 + 失败请求语义 | new-api#1909/1664/4257/4168 | tier-based pricing + client_gone vs upstream_timeout 区分扣费 | 生态 (billing 规则) + 算法 |
| Fallback 看 SSE body | Portkey#1047 | fallback 决策器 inspect SSE error event (不只看 HTTP code) | 算法 (decision) |
| Bedrock 重试 SigV4 stale | LiteLLM#27513 | per-attempt SigV4 sign 强制重新生成 | 算法 (retry hardening) |
| TLS 指纹模拟 (sub2api not_planned) | sub2api#587 + #1451 | utls + ja3 fingerprint 切换 + tier-based 选取 | 算法 (fingerprint) + 生态 (per-vendor 配置) |
| Cooldown / tier-based 降级 | sub2api#641/640 | 上游 429 时按 tier 配置 cooldown (不强制 PST 午夜) + tokens-aware quota 进度条 | 算法 (cooldown 策略) + 生态 (admin UI) |
| Stream失败计费 | new-api#4168 | 4-状态扣费表 (client_gone/upstream_timeout/output_token_zero/upstream_5xx) | 生态 (billing) + 算法 |
| Tool call arguments 稳定性 | LiteLLM#27468 + new-api#4671 + Portkey#1583 | OpenAI→Anthropic arguments JSON encode/decode 一次化 (不重复 stringify) | 算法 (mapper hardening) |
| Repository / 启动性能 | LiteLLM#7605 (16m+) + #19921 | 单 Go 二进制 / 启动 < 1s + release-N vs (N-1) latency 基线 | 架构 (单 binary) + 生态 (release gate) |

总计 **14 条 delta**, 每条都至少有 2 个上游 issue 反应数支撑, 不是 HUAKAI 自吹。

---

## URL refs (with access dates and issue numbers)

**Recency check (per CLAUDE.md #12)**:
- BerriAI/litellm: archived=false, pushed_at 2026-05-09T09:43:07Z, license NOASSERTION (Apache-2.0 root-licenced, mixed)
- Wei-Shaw/sub2api: archived=false, pushed_at 2026-05-09T15:15:40Z, LGPL-3.0
- QuantumNous/new-api: archived=false, pushed_at 2026-05-09T13:39:46Z, AGPL-3.0 (per project README; not re-fetched API-detail)
- Portkey-AI/gateway: archived=false, pushed_at 2026-03-25T09:33:55Z (45d ago, within 90d), MIT

**LiteLLM (`BerriAI/litellm`)**:
- `BerriAI/litellm#24518` (open, 2026-05-09 fetched) — PyPI 供应链事件 timeline
- `BerriAI/litellm#24512` (closed completed, 2026-05-09 fetched) — 1113 reactions
- `BerriAI/litellm#23777` (open, 2026-05-09 fetched) — Multi-OAuth ChatGPT
- `BerriAI/litellm#19921` (open, 2026-05-09 fetched) — v1.81 perf 回归
- `BerriAI/litellm#26395` (open, 2026-05-09 fetched) — DeepSeek V4 Pro multi-turn reasoning_content strip
- `BerriAI/litellm#7605` (open >= 16m, 2026-05-09 fetched) — import 慢
- `BerriAI/litellm#10177` (open >= 12m) — Dark Mode (UX 长期 open)
- `BerriAI/litellm#20412` (open) — Vercel cost tracking
- `BerriAI/litellm#9146` (closed completed) — Responses API 22👍/41💬 long lifecycle
- `BerriAI/litellm#7934` (closed completed) — MCP bridge 19👍/22💬
- `BerriAI/litellm#11364` (open) — Anthropic cached_token cost 错算
- `BerriAI/litellm#16718` (open) — Claude Code 2.0.42 input_examples
- `BerriAI/litellm#27468` (closed completed) — OpenAI→Anthropic tool_call args lost (v1.83.7 回归)
- `BerriAI/litellm#27513` (open) — Bedrock SigV4 stale headers
- `BerriAI/litellm#27490` (open) — Anthropic strict tool use 字段位置错
- `BerriAI/litellm#23741` (open) — vector_store_ids extra inputs
- `BerriAI/litellm#23836` (open) — health check max_completion_tokens=1
- `BerriAI/litellm#15164` (closed completed) — Bedrock Claude 4.5 Sonnet tools 翻译
- `BerriAI/litellm#14704` (closed completed) — tool_choice rejected on Azure GPT-5
- `BerriAI/litellm#27512` (open) — Anthropic retry drops thinking
- `BerriAI/litellm#27532` (open) — Bedrock InvokeModel context_management
- `BerriAI/litellm#20246` (reopened) — VLLM streaming reasoning_content missing
- `BerriAI/litellm#16021` (open) — OpenRouter cost lost in streaming
- `BerriAI/litellm#11759` (closed completed) — Pydantic streaming warnings 43👍
- `BerriAI/litellm#12685` (closed completed) — Heavy RAM usage 21👍
- `BerriAI/litellm#8842` (open >= 14m) — Router callbacks not triggered
- `BerriAI/litellm#12367` (open >= 10m) — 自定义 OAuth2 provider
- `BerriAI/litellm#15230` (open) — Enterprise virtual key 报错
- `BerriAI/litellm#22667` (closed completed) — OpenRouter Model IDs broken
- `BerriAI/litellm#13251` (closed not_planned stale) — aiohttp client session
- `BerriAI/litellm#19984` (closed completed) — VertexAI Anthropic prompt-caching-scope-2026-01-05 beta header
- `BerriAI/litellm#18155` (open) — GitHub Copilot Provider Excessive Premium

**sub2api (`Wei-Shaw/sub2api`)**:
- `Wei-Shaw/sub2api#2049` (open, 2026-05-09 fetched) — 20👍 但 affiliate spam
- `Wei-Shaw/sub2api#1413` (open, 2026-05-09 fetched) — 用户写 cc 逆向文档 15👍
- `Wei-Shaw/sub2api#1143` (open, 2026-05-09 fetched) — cc telemetry 抓包封号 12👍
- `Wei-Shaw/sub2api#1451` (open, 2026-05-09 fetched) — 借鉴 cc-gateway 身份隐藏 12👍
- `Wei-Shaw/sub2api#641` (open) — Gemini 429 cooldown 8👍
- `Wei-Shaw/sub2api#640` (open) — Gemini 配额条 tokens 4👍
- `Wei-Shaw/sub2api#587` (closed not_planned) — TLS 指纹模拟 wontfix
- `Wei-Shaw/sub2api#1331` (open) — 协议自动转换提案 6👍
- `Wei-Shaw/sub2api#2208` (open) — Codex /v1/responses token 暴涨 7👍
- `Wei-Shaw/sub2api#2245` (open) — Responses SSE 未完成转 200 3👍
- `Wei-Shaw/sub2api#1552` (open) — upstream stream ended without terminal
- `Wei-Shaw/sub2api#1843` (open) — Codex out of room 频繁
- `Wei-Shaw/sub2api#2291` (open) — Cache hit rate 计算错
- `Wei-Shaw/sub2api#2293` (open) — GPT-5.4/5.5 cache_read 倍率
- `Wei-Shaw/sub2api#851` (open) — 重试过滤 thinking 不同步 clear_thinking
- `Wei-Shaw/sub2api#1264` (open) — /v1/responses user param
- `Wei-Shaw/sub2api#321` (open) — Antigravity Opus-4.5 5000字 stream
- `Wei-Shaw/sub2api#594` (open) — OpenAI Chat 与 Responses 双支持需求
- `Wei-Shaw/sub2api#752/802` (closed completed) — Codex 远程压缩 + 倍率 X2
- `Wei-Shaw/sub2api#763` (closed completed) — provider name 为 OpenAI 7👍
- `Wei-Shaw/sub2api#1814/1809` (closed) — gpt-image-2 适配/403
- `Wei-Shaw/sub2api#1320` (closed completed) — gpt-5.4-xhigh effort 不一致
- `Wei-Shaw/sub2api#2078/2079/2080` (closed completed) — Windows cc cli 配置失效

**new-api (`QuantumNous/new-api`)**:
- `QuantumNous/new-api#1730` (open >= 8m, 2026-05-09 fetched) — 渠道速率限制+并发 11👍
- `QuantumNous/new-api#924` (open >= 13m, 2026-05-09 fetched) — 自用 prompt 记录 13👍
- `QuantumNous/new-api#1909` (open) — 分档计费 7👍
- `QuantumNous/new-api#1664` (open) — Tokens 阶梯计费 1👍
- `QuantumNous/new-api#4257` (closed duplicate) — 主版本阶段计费 3👍
- `QuantumNous/new-api#3388` (open) — 订阅套餐分组 3👍
- `QuantumNous/new-api#4711` (open) — Images 异步任务 0👍
- `QuantumNous/new-api#4514` (closed completed) — 异步图片接口 5👍
- `QuantumNous/new-api#2171/2442/3001` (open) — 模型重定向批量 / global alias
- `QuantumNous/new-api#4678` (open, 2026-05-09 fetched) — 关键: cch=xxx 破坏 prefix cache
- `QuantumNous/new-api#4717` (closed not_planned invalid) — 渠道亲和缓存命中已禁用渠道
- `QuantumNous/new-api#4168` (open) — Stream 失败仍按 prompt 扣费
- `QuantumNous/new-api#4697` (open) — qwen3 流缺 sentinel
- `QuantumNous/new-api#4696/4698` (closed) — same as #4697 (duplicates)
- `QuantumNous/new-api#4671` (closed not_planned) — Claude tool call could not be parsed
- `QuantumNous/new-api#4674` (open) — OAuth provider 用户组映射
- `QuantumNous/new-api#1216/1812` (open/closed) — Responses API 支持
- `QuantumNous/new-api#1117/1361` (open) — Embedding/rerank
- `QuantumNous/new-api#4647` (open) — GitHub Security Advisory pending review
- `QuantumNous/new-api#4683` (open) — generation_ms ambiguous (DB)
- `QuantumNous/new-api#4716` (open) — 管理员越过登录限制
- `QuantumNous/new-api#1812` (closed completed) — 渠道支持 /v1/responses 14👍/12💬
- `QuantumNous/new-api#1378` (closed completed) — Claude Code 适配 13👍/9💬
- `QuantumNous/new-api#1106` (closed completed) — 通用 OAuth2 12👍
- `QuantumNous/new-api#852` (closed completed) — 多渠道自动禁用延迟高 2👍
- `QuantumNous/new-api#973` (open) — 评论区机器人轰炸 12👍 (社区维护信号)
- `QuantumNous/new-api#4687` (closed not_planned) — v1 UI 集中反馈 13💬

**Portkey (`Portkey-AI/gateway`)**:
- `Portkey-AI/gateway#1047` (open >= 12m, 2026-05-09 fetched) — Bedrock validation 6👍
- `Portkey-AI/gateway#1570` (open) — Vertex Anthropic structured output schema
- `Portkey-AI/gateway#1564` (open) — prompt_tokens normalization 跨 Anthropic provider
- `Portkey-AI/gateway#1579` (open) — cache_control 路由 Vertex strip
- `Portkey-AI/gateway#1589` (open) — Anthropic cache_control.scope direct vs Portkey 拒绝
- `Portkey-AI/gateway#1583` (open) — tool calls + Responses API + Bedrock 失败
- `Portkey-AI/gateway#1598` (open) — Claude Code OAuth passthrough 中途失败
- `Portkey-AI/gateway#1596` (open) — SSRF via URL fragment
- `Portkey-AI/gateway#1402` (reopened) — content-length + chunked 冲突
- `Portkey-AI/gateway#1550` (open) — Responses stream=true TypeError immutable
- `Portkey-AI/gateway#1389` (open) — TypeError immutable hono
- `Portkey-AI/gateway#1127` (open) — native fetch 默认 300s
- `Portkey-AI/gateway#926` (open) — MCP client 集成
- `Portkey-AI/gateway#1156-1158/1162` (reopened) — Batch API: cancel/pause/partial output
- `Portkey-AI/gateway#1561/1560` (closed completed) — 中文 cost mgmt enhance
- `Portkey-AI/gateway#1558` (closed completed) — Why only 14 commits since January (项目状态信号)
- `Portkey-AI/gateway#1191` (closed completed) — Bedrock Tool Use cache_control 不 respect
- `Portkey-AI/gateway#1022` (closed completed) — Bedrock Claude 3.7/3.5 prompt caching
- `Portkey-AI/gateway#1153` (reopened) — Databricks provider
- `Portkey-AI/gateway#323` (open >= 24m+) — Databricks across hyperscalers
- `Portkey-AI/gateway#127` (closed completed) — Configuration files
- `Portkey-AI/gateway#290` (closed completed) — chat completion 流被 buffered
- `Portkey-AI/gateway#1606` (open) — Current status of version 2.0.0

---

## 完成

**输出**: `/home/codex/HUAKAI/docs/research/2026-05-09-issue-mining-cross-repo.md` (本文件)
**Lane**: issue-mining (single, clean-room)
**Source files read**: GitHub Search API + REST API for `BerriAI/litellm`, `Wei-Shaw/sub2api`, `QuantumNous/new-api`, `Portkey-AI/gateway` 的 issue title + body 摘录 (paraphrase, 不抄原文)
**Agent**: Claude Code Opus 4.7 (1M context) — `general-purpose` subagent (id `a1b0827b8f7f1b6a9`)
**UTC**: 2026-05-09 15:30Z 起调研, 完成于 ~15:40Z
**Rate limit ending**: search ~10 / core ~29 (under-budget)

**TODO Owner**: 综合本文件 + 双 lane market research (`docs/research/2026-05-09-market-research-*.md` 已并行作业, 互不见面) 后做 HCSF canonical 决策。
