# HUAKAI 内部代号 ↔ 公开项目名 映射表

Date: 2026-05-02
Status: source of truth for all internal planning docs that use codenames.

## 0. 为什么有这份文件

Owner directive 2026-05-02: 内部 plan / 设计文档（如 `docs/plans/2026-05-02-huakai-reverse-proxy-core-codex.md` 等）使用**代号**而不是直接点名参考项目，原因：

1. 让文档聚焦"做什么"，而不是"抄哪个"
2. 避免文档外流时被误读为竞品对照
3. 给 architect 留 framing 自由度（同一代号可同时引用多个仓的不同部分）

但 Owner 同时明确：**README 必须公开**所有代号 ↔ 真名映射。这份文件就是那张映射表，README 的 "Reference Projects" 节直接引用。

不允许任何内部 plan 文件用代号但**不在这里登记**。

---

## 1. 项目代号 ↔ 真名（开源参考）

| 代号 | 真名 / 仓库 | License | HEAD commit | 我们读了什么 | 我们没拿什么 |
|---|---|---|---|---|---|
| **Commercial-Pool-Ref** | [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) | LGPL-3.0 | `48912014a16e` | 多账号池/支付/退款/监控 / 调度算法 / OAuth 续期 / channel monitor / 渠道账号状态机 | 源码 / ent schema / 字段名 / Vue 组件 |
| **Clean-Arch-Ref** | [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | MIT | `56df36895a0e` | 反代/operator config 14 knobs / per-provider executor 布局 / TUI / WS relay / 6 路 session-affinity / 4 种 credential storage backend | 源码 / 函数名 / 文件路径 |
| **Billing-Engine-Ref** | [QuantumNous/new-api](https://github.com/QuantumNous/new-api) | AGPL-3.0-or-later | `dac55f0fdeb1` | 计费 session / 价格 DSL + 冻结快照 / body 内存→磁盘分层 / 客户端 affinity 缓存 / 多支付通道状态机 | 源码 / 表达式语法 / Stripe 集成代码 |
| **Obs-Ref** | [Helicone/ai-gateway](https://github.com/Helicone/ai-gateway) | GPL-3.0-or-later | `3f4bd44b85f9` | request explorer / S3 body 保留 + TTL / wallet escrow + dispute / cost+request 双 unit rate-limit / 投放-list pattern | 源码（不能链接 GPL 进 binary） |
| **Retry-Policy-Ref** | [Portkey-AI/gateway](https://github.com/Portkey-AI/gateway) | MIT | `351692fd9236` | 重试 budget / Retry-After 解析 / fallback stop conditions / hooks pipeline / SSRF-safe custom host 校验 / 响应 debug headers | 源码 / plugin 执行结构 / header 命名 |
| **Multi-Provider-Ref** | [BerriAI/litellm](https://github.com/BerriAI/litellm) | MIT (excluding `enterprise/`) | `62920a0` | 4-tier budget 层级 / retry+fallback 优先级 / 健康/cooldown 路由 / cache admin / guardrail registry / 删除 key audit | 源码 / Prisma schema / 企业版代码 |
| **Declarative-Ref** | [envoyproxy/ai-gateway](https://github.com/envoyproxy/ai-gateway) | Apache-2.0 | `bc7297172` | AIGatewayRoute CRD / 配额 CEL / body mutation 接口 / GenAI OTel metrics 命名 | 源码 / CRD 定义 |
| **Operator-Tool-Ref** | [qixing-jk/all-api-hub](https://github.com/qixing-jk/all-api-hub) | AGPL-3.0 | `b9b1acc21` | 外部账号 telemetry profile / 重复账号检测 / 选行执行+重试失败 UX / 写前预览 / WebDAV 加密同步 | 源码 / 浏览器扩展架构 / 浏览器本地凭据存储（明确反例） |
| **Legacy-Ref** | [songquanpeng/one-api](https://github.com/songquanpeng/one-api) | MIT | `8df4a26` | channel CRUD / group multipliers / per-key quota lifecycle / dashboard 结构 / gzip middleware（反例）/ panic recover（反例）| 源码 / 默认凭据反例 / 硬编码 multiplier |

---

## 2. 供应商代号 ↔ 真名（上游 LLM 服务商）

| 代号 | 真名 | 主要 surface 我们对照的 |
|---|---|---|
| **Vendor-X1** | OpenAI Platform | API keys / Projects / Admin API / Service Accounts / rate-limit headers / Usage+Costs API / prompt caching / Responses API / reasoning_effort / Realtime WebSocket |
| **Vendor-X2** | Anthropic Console / API | Workspaces / API keys / spend limits / rate-limits ITPM/OTPM / Usage+Cost API / prompt caching 5min+1h / Extended Thinking / Computer Use beta |
| **Vendor-X3** | Google Vertex AI | GCP project quotas / Standard PayGo + Dynamic Shared Quota / Provisioned Throughput / context caching / Service Account JWT |
| **Vendor-X4** | AWS Bedrock | IAM / cross-region inference profiles (CRIs) / token burndown / prompt caching / Knowledge Bases / Bedrock Agents |
| **Vendor-Meta** | OpenRouter | Provider routing / sticky routing / data policy / ZDR / BYOK / credit limits / provider order/allow_fallbacks |

---

## 3. 使用约定

**内部 plan 文件**（如 `docs/plans/*.md`、`docs/reference_delta/*.md`）默认使用代号。允许在 §0 头部 footnote 中一次性写"代号映射见 codename-mapping.md"，正文不再点名。

**对外公开文件**（README、博客、商业合作文档）必须用真名 + license + 链接。所有真名公示的源头是这份 mapping 文件。

**新增参考项目**：先 commit 一个新代号（追加到表中）+ E-LIC-NNN 行进 `docs/07_REFERENCE_EVIDENCE_LEDGER.md`，之后 plan 文件才能引用。

**代号撤回**：如果某代号背后的项目被 Owner 决定从参考集中移除，先把该代号从 plan 文件移除，再删表行 + ledger 行。

---

## 4. README 公开节引用方式

`README.md` 的 "Reference Projects & Usage Acknowledgement" 节直接引用本文件 §1 + §2 全表：

```markdown
HUAKAI's clean-room study read 9 open-source reference projects and aligned with
5 upstream vendor product surfaces. Full disclosure with license, commit pin,
"what we read for", and "what we did NOT take" lives at:
[docs/reference_delta/2026-05-02/codename-mapping.md](docs/reference_delta/2026-05-02/codename-mapping.md).
```

不要在 README 里复制全表（会与本文件 drift）— 直接链接。

---

## 5. 单行总结

代号是内部 framing 工具，让 plan 文档聚焦"做什么"；真名公开在这个 mapping 文件 + README 链接里。两个层面都对应 Owner 2026-05-02 directive："内部聚焦 + 对外透明"。
