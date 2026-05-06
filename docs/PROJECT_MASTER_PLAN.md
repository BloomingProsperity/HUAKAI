# HUAKAI 项目总规划 / Project Master Plan

> 这是 **Owner 总览文档**——把散在 25+ 份治理文件里的关键信息汇总到一处。**正式规则仍以分散的英文权威文件为准**（如 docs/01..24, docs/decisions/DR-*）。本文档每次重大决策后由 Claude PM 刷新。最近实现同步：**2026-05-01**。
>
> **Current implementation sync:** 项目已不是“Phase 3+ 未启动”。当前代码处于 **Phase C / N+5b**：`/v1/chat/completions` 已串起 inbound API key auth、Model Registry、Router.Plan、ClaimGate、Resource Pool selector、stream forwarder、Tx2 Settler 与 Obs Reader。仍未完成的是 admin UI、支付/充值、真实 pricing、真实 upstream provider、multi-attempt executor。

## 一、项目本质（一句话）

**HUAKAI = 商业化 relay-station 网关 + 配额拼车平台。**

- **驱动**：赚钱（个人版/SaaS 版双轨道商业化），成功后开源
- **路径**：在 Sub2API 基础上做更全面更好的产品
- **差异化**：上游接入广度（一api/Sub2API 的弱项就是接入太少）
- **质量底线**："必须真实"——所有核心算法要源码级深拆 + 互审 + 严格 spec 才能进实现

来源：[01 Owner-Stated Goal](01_PROJECT_BRIEF.md) + [DR-007 商业定位](decisions/DR-007-product-positioning-and-breadth.md)

## 二、商业模式（两条平行赚钱路）

| 模式 | 对应 Edition | Owner 角色 | 客户 | 客户付什么 | 时序 |
|---|---|---|---|---|---|
| **模式 1：自部署 + 卖 API** | Personal Edition | 中转站运营方（您自己） | 终端开发者 / 用户 | Token 用量 / 订阅 | Phase 6+ 商业化（您自己开张） |
| **模式 2：卖 SaaS** | SaaS Edition | SaaS 提供方（您） | 想做模式 1 的运营方 | SaaS 订阅 / 抽成 | Phase 10+ 启动（模式 1 跑顺再做） |

**同一份代码库**通过 Edition 配置开关切换两种形态。来源：[DR-002](decisions/DR-002-product-editions.md)。

## 三、项目流程（您问的"调研→研究→规划→落实→检测"）

### 阶段映射

| 您的术语 | HUAKAI Phase | 状态 | 主要产出 |
|---|---|---|---|
| **调研** | Phase 0-0.5 治理基线 + Phase 1 第一趟 mining | ✅ 完成 | 8 个参考项目 license 已查；READMEs 全挖；12 份 inventory（Claude 4 + Codex 7 + Sub2API 1） |
| **研究** | Phase 1 第二趟 deep decomposition + 互审 | 🚧 5-10% 完成 | 3 份 Sub2API prose decomposition + 1 份跨职能 prose（Quota+Billing claim gate）+ 2 份互审报告（v1 + v2）+ 1 份综合方案 |
| **项目+功能规划** | Phase 2 contract lock + slice specs | 🚧 进行中 | 已有 released specs + N+4/N+5 implementation plans；后续 admin/payment/executor contracts 继续补 |
| **落实** | Phase 3-9 实现 | 🚧 已启动（Phase C / N+5b） | 后端核心 slice 已跑通：auth → registry → router → claim/pool → forward → settle；下一步是真 upstream、executor、pricing/payment、admin |
| **检测** | 持续（Phase 1 起到 Phase 9）+ Phase 8 集中 | 🚧 已启动 | Go unit/integration/smoke 已覆盖当前 slice；生产硬化与完整验收矩阵仍待 Phase E+ |

### 当前**真实进度**（不灌水）

```
治理基线 (Phase 0.5):      ████████████████████ 100%
8 项目 license 查证:       ████████████████████ 100%
README 挖矿 (Phase 1.1):   ████████████████████ 100% (118 条证据)
核心 specs / slice plans:  ████████████░░░░░░░░  约 60%（当前核心链路够实现；后续 admin/payment/executor 待补）
Phase C / N+5b 后端核心:  ██████████░░░░░░░░░░  约 50%（chat streaming money path 已通；真 upstream/定价/executor 未通）
L0 商业化闭环:             ████░░░░░░░░░░░░░░░░  约 20%（API key resolver 已落；充值、签发 UI、余额扣减待做）
Admin / Frontend:          ░░░░░░░░░░░░░░░░░░░░  0%
```

**执行口径更新**：DR-008 后项目已采用“严格 clean-room + slice 互审 + 小步实现”的路径推进，不再停留在 Phase 1/2 门禁前。后续新增 L1/L2 能力仍必须保留源码级逆推、互审、spec 与测试证据。

## 四、功能规划全景（53 行功能矩阵）

源：[03 Feature Parity Matrix](03_FEATURE_PARITY_MATRIX.md)（含 Disposition + Status 两列）。按 L 级分组：

### L1 MVP（13 个，第一次出货必须）

**网关核心**（4）
- F-GW-001 路由：Route → Channel → Pool → Account
- F-GW-002 流式 + 用量记录一致性
- F-GW-004 重试 + 指数退避 + 操作员可见 fallback 原因
- F-CH-001 Channel CRUD + 模型白名单 + 批量创建

**身份 + Key**（3）
- F-AUTH-001 邮箱 + ≥1 OAuth 登录
- F-AUTH-002 Session 跨重启跨实例（强制 operator-supplied secret）
- F-KEY-001 API Key 全生命周期 + 配额对账

**池化身份 + Edition**（2）
- ⭐ F-POOL-001 多账号配额池化（**relay-station 产品身份**）
- F-MODE-001 Edition 开关（个人 vs SaaS 切换）

**安全**（4）
- F-SEC-001 IP 限速
- F-SEC-002 首次启动强制改密码
- F-SEC-005 上游响应头白/黑名单（默认安全）
- F-TIMEOUT-001 每请求超时

### L2 生产可用（约 22 个，L1 出货后立刻规划）

**网关增强**：F-GW-003 性能 SLO + 资源预算 / F-CH-002 健康探测 + 告警 / F-SESSION-001 粘连 session / F-CONC-001 双层并发限制 / F-CB-001 计费熔断 / F-ROUTE-001 性能感知路由 / F-ROUTE-002 端点选取策略 / F-CACHE-001 简单缓存 / F-CACHE-002 缓存后端可插拔

**安全增强**：F-RBAC-001 RBAC + 撤销 diff / F-SEC-004 User × Model 限速 / F-SEC-006 多 scope 限速 + 限额 / F-GUARD-001 输出守卫插件

**协议 + 模型**：F-PROTO-002 跨格式翻译 + 损失矩阵 / F-MODEL-001 reasoning 透传（drift 2026-05-06: OpenAI 当前 nested `reasoning: {effort: ..., summary: ...}` 为 canonical；top-level `reasoning_effort` 是 legacy/deprecated；F-MODEL-001 须以 nested form 为正态）

**计费 + 多租户**：F-BILL-001 价格上下文版本化 / F-GROUP-001 用户组 × 渠道组定价 / F-TENANT-001 租户配置随 API Key 走

**观测**：F-OBS-001 操作员仪表板 / F-OBS-002 OpenTelemetry 标准导出 / F-OPS-001 第三方工具引入 API / F-CONFIG-001 路由 config-as-code / **⭐ F-PAY-001 支付插件层（模式 1 商业化必需，已升级到 L2）**

### L3 参考对齐（约 14 个，中长期）

各种增强 + Phase 6+ 计费 + Phase 7+ UI / 国际化 + Phase 8 部署硬化 + Phase 9+ 多模态/Realtime/MCP

### L4 超越参考（约 5 个，SaaS 版差异化）

- F-PAY-001 多租户支付编排（您从每租户抽成）
- F-SYNC-001 跨设备 WebDAV 同步
- F-ARCH-001 双层网关拓扑

## 五、技术栈（已锁，DR-003..006）

| 决策 | 选择 | 来源 |
|---|---|---|
| 后端语言 | **Go**（stdlib net/http + chi） | [DR-003](decisions/DR-003-technology-stack.md) + [DR-005](decisions/DR-005-go-http-framework.md) |
| 前端 | **TypeScript + React + Vite + TanStack** + Tailwind | [DR-003](decisions/DR-003-technology-stack.md) + [DR-004](decisions/DR-004-frontend-framework.md) |
| 数据库 | **PostgreSQL + sqlc + Docker Compose** | [DR-006](decisions/DR-006-database.md) |
| 多租户 | **tenant-aware schema 从第一天起** | [DR-001](decisions/DR-001-multi-tenancy.md) |
| Clean-room | **Option B 双车道 + Option C carve-out**（账号池路由 / 计费对账 / Provider 健康） | [DR-000](decisions/DR-000-clean-room-methodology.md) |

## 六、参考项目矩阵 + 拆解状态

| 项目 | License | 风险 | 角色 | 当前状态 |
|---|---|---|---|---|
| **Sub2API** | LGPL-3.0 | 🟠 | 身份范本 + 计费 claim gate 范式 | inventory + 3 prose decomposition |
| **one-api** | MIT | 🟢 | 安全锚点 + 基础架构 | inventory（双方）+ 跨职能 quota prose |
| New API | AGPL-3.0 | 🔴 | cache-aware 计费 + 协议翻译矩阵 | inventory（双方）;  ❌ 完整源码深拆 |
| LiteLLM | MIT | 🟢 | retry/router/concurrency 范式 | inventory（双方）+ Codex deep |
| Portkey | MIT | 🟢 | guardrail + 语义缓存 | inventory（双方）;  ❌ 完整源码深拆 |
| Helicone | GPL-3.0 | 🟠 | 性能感知路由 + OpenTelemetry | inventory（Codex）;  ❌ 完整源码深拆 |
| Envoy AI Gateway | Apache-2.0 | 🟢 | 双层拓扑 + K8s | inventory（Codex）;  ❌ 完整源码深拆（Phase 10+ 候选） |
| All API Hub | AGPL-3.0 | 🔴 | 浏览器扩展，SaaS UI 范本 | inventory（Codex）; 多数 SaaS Phase 10+ |

## 七、互审制度（Owner 直接指令）

> "同样的事情你们都要做，然后互审对方的结果。然后给出最终的优化排版行动方案。"

每个核心算法跑一轮：
1. **Claude 独立读源 → 写 prose**（不偷看 Codex）
2. **Codex 独立读源 → 写 prose**（不偷看 Claude）
3. **互审**：Claude 审 Codex / Codex 审 Claude（双方各 ~12 条 finding）
4. **综合**：Claude PM 写最终行动方案
5. **第三方 reviewer**（不同 session，CL-001..010）签字才能 `Released`
6. **Released** 后才能进 `docs/specs/` 作为实施依据

已跑完 1 轮：[Quota+Billing claim gate](decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md)。

## 八、持续维护（Owner 直接指令）

> "我们后续的维护也主要看借鉴平台的更新，他们更新后我们吸取问题，然后自查，更新我们的产品。"

[docs/24 持续追踪政策](24_REFERENCE_TRACKING_POLICY.md) 三档节奏：
- **每发布审查**（7 天内）—— 任何参考项目发新版触发 HUAKAI 自审
- **每月扫描**——上游 commit log 走读
- **每季战略复审**——Owner 参与的方向校准

每条上游 bug fix 必须给 HUAKAI 一个判定：VULNERABLE / SAFE-BY-DESIGN / SAFE-BY-CODE / UNKNOWN-NEED-INVESTIGATION。

## 九、风险登记（9 条已识别）

源：[10 Risk Register](10_RISK_REGISTER.md)。最新增加的（互审产出）：

| ID | 风险 | 状态 |
|---|---|---|
| R-LIC-001 | License clean-room | Mitigated |
| R-LIC-002 | Specifier-lane 跨 session 累积污染 | Open |
| R-POOL-001 | 上游封号（Sub2API 已发生） | Open |
| R-BILL-002 | 池化计费对账漂移 | Open |
| R-OPS-002 | Sticky session 热点 / 上下文丢失 | Open |
| R-SEC-001 | Admin 操作泄密 | Open |
| R-BILL-001 | 用量计费漂移 | Open |
| R-OPS-001 | UI 隐藏关键状态 | Open |
| R-REL-001 | failover 掩盖问题 | Open |

## 十、当前**最优先**的 Mandated Next Dives

按 [DR-007 + DR-002 §Owner Refinement](decisions/DR-007-product-positioning-and-breadth.md) 排序：

### 立即（Phase 1.2 → Phase 1.3 → Phase 2 转换前）

1. **Quota+Billing claim gate** prose synthesis 已完成 → 应用 v2 review 的 9 条新发现 → reviewer-lane 签字 → 移到 `docs/specs/`
2. **Provider Account 选择算法**（F-POOL-001）严格 spec — Option C carve-out
3. **流式 + Usage Record settlement** spec — F-GW-002
4. **Retry+Fallback+Cooldown taxonomy** spec — F-GW-004（要修 v2 发现的 5 条 missing categories）
5. **session 污染 ledger**（v1 review CRITICAL）— `docs/sessions/<id>.md` 制度
6. **Owner-go-commercial 检查表** — 模式 1 自己开张前必须的 13 个 L1 功能完整版

### 中期（Phase 2 期间）

7. New API / Portkey / Helicone / Envoy / All API Hub 的 prose decomposition（每项目 5-15 个核心算法）
8. 53 行 F-* 矩阵每行的 acceptance test direction（Codex 已写 13 行，剩 40 行）
9. 持续追踪 baseline 文件（`docs/tracking/<reference>/2026-04-28-baseline.md` × 8）

### 长期（Phase 3+ 实施期）

10. Go module 骨架 + sqlc 接入 + OpenAPI codegen
11. 提供商适配器矩阵（DR-007 success criterion #2 所需，量化超过 Sub2API）

## 十一、当前数据快照（截至 2026-04-28）

| 指标 | 数值 |
|---|---|
| 本地 commit | 32 |
| 远程 `claude/phase-1` 同步 | ✅ |
| 决策记录 (DR) | 8（含 0/1/2/3/4/5/6/7） |
| 治理文件 | 24 + 1 总览（本文件）+ 多份决议 + 多份 spec 模板 |
| 行为证据 | 118+（持续增加） |
| Inventory 文件 | 12 |
| Prose decomposition | 4（Sub2API 3 + 跨职能 1） |
| 互审报告 | 4（v1 双向 + Codex synth + Claude v2） |
| Codex 派活次数 | 9（含全部 deep dive） |
| Codex 累计 token 消耗 | ~1.4M tokens（gpt-5.5 + xhigh） |
| 已锁的 Owner 约束 | 7（商业目的 / 两商业模式 / Sub2API+广度 / 必须真实 / 持续学习 / 命名规范 / 无冗余） |

## 十二、给 Owner 的真实判断

**好消息**：
- 治理基线扎实，决策已锁
- 互审制度真在跑（v1+v2 各 ~12 条发现，双方都找到对方漏洞）
- 模式 1 商业化路径明确（F-PAY-001 已升 L2）
- 第一个核心算法（Quota+Billing）跑完整套互审 + 综合

**坏消息**：
- Phase 1 真实进度只 ~5-10%（按"必须真实"标准）
- 30+ 份 prose decomposition 还要写
- Phase 2 契约锁定 0% 启动
- 任何代码 0% 启动

**真实工作量估算**：
- 剩余 Phase 1 deep decomposition：~40-100 小时纯专注工作
- Phase 2 契约锁定：~20-40 小时
- Phase 3-6（骨架 + 网关核心 + 账号 + 计费）：~150-300 小时
- 至模式 1 可商业化（最小集合）：**~250-500 小时纯工程时间**
- Codex 并行能砍 30-50%

**Owner 判断**：是否接受这个时间预期？还是需要重新考虑 scope（缩小 L1 / 加速节奏 / 改方法论）？

## 十三、Owner 方法论决策（2026-04-28）

> **Owner Decision: A — 严格走，慢但真。**

正式锁定为 [DR-008 Methodology Choice — Strict Authenticity Over Speed](decisions/DR-008-methodology-choice-strict-authenticity.md)。

后果：
- **每一个 L1/L2 功能**必须走完整 `prose decomposition → 互审 → 综合 → reviewer-lane CL-001..010 → Released`
- **Phase 2 契约锁定**不能在 Phase 1.2/1.3 完成前开始
- **任何代码（含骨架）** 不在 Phase 2 完成前写
- **时间预期：250–500 小时纯专注工程**（Owner 接受）；schedule slip 优先于 scope 缩水
- 加速手段：Claude + Codex 并行；不允许通过降低真实度加速
