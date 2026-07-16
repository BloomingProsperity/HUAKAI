# HUAKAI 规则清单（Rules Manifest）

> **每次 PM session 开头必读这一份。** 70+ 份规则散文件不可能每次都加载，本文件是浓缩版"宪法"——每条 binding 规则一行 + 来源指针。规则原文以来源文件为权威；本文件是导航。最近刷新：**2026-07-16**。

## 0. 关于本文件

- **目的**：单一入口防漏检。Codex / Claude / Gemini 任一 session 进来先读这一份，再读任务相关的子集。
- **更新规则**：任何 binding 规则新增 / 修改时，PM 必须同步刷新本文件 + 在 commit message 末尾写 `Rules updated: ...`。
- **审计**：reviewer-lane 复审时第一步就是对照本清单逐条 self-check。

## 1. Owner-Stated Goal（北极星，绝对约束）

| ID | 规则 | 来源 |
| --- | --- | --- |
| G-001 | 商业目的：赚钱（成功后开源），不接受降低真实度加速 | [01 §Owner-Stated Goal](01_PROJECT_BRIEF.md) |
| G-002 | 在 Sub2API 基础上做更全面更好；接入广度是差异化 | [DR-007](process/decisions/DR-007-product-positioning-and-breadth.md) |
| G-003 | "必须真实"——inventory 不等于理解；spec 不等于实现 | [01 §Owner directives](01_PROJECT_BRIEF.md) |
| G-004 | 慢无所谓；250-500 工程小时预期；不缩 scope，加并行 | [DR-008](process/decisions/DR-008-methodology-choice-strict-authenticity.md) |
| G-005 | 持续维护看上游更新→自审→修 | [24](24_REFERENCE_TRACKING_POLICY.md) |
| G-006 | 两商业模式平行：Personal Edition 卖 API（模式 1），SaaS Edition 卖给运营方（模式 2） | [DR-002 §Owner Refinement](process/decisions/DR-002-product-editions.md) |

## 2. Owner Start Gate

| ID | 规则 | 来源 |
| --- | --- | --- |
| S-001 | Agent 不在 Owner 明确"开始/Proceed/确认开始/开干"等信号前推进实现 | [00](00_PM_OPERATING_SYSTEM.md) |
| S-002 | Owner 给出 start signal 后，agent 主动跑——低风险直接做，中风险记录原因，高风险停下问 | [00 §Risk-Based Confirmation](00_PM_OPERATING_SYSTEM.md) |

## 3. Clean-Room（Phase 1 起到永远）

| ID | 规则 | 来源 |
| --- | --- | --- |
| CR-001 | License 验证先行；非 MIT 参考不记录 license 不写任何行为证据 | [05](05_CLEAN_ROOM_POLICY.md) |
| CR-002 | Specifier 车道**可读**非 MIT 源；Implementer 车道只读 spec | [05 §Lane Definitions](05_CLEAN_ROOM_POLICY.md), [DR-000](process/decisions/DR-000-clean-room-methodology.md) |
| CR-003 | Option C carve-out 区域：账号池路由 / 计费对账 / Provider 健康 — implementer 只能读 spec，**连 MIT 源都不读** | [DR-000](process/decisions/DR-000-clean-room-methodology.md) |
| CR-004 | 同 session 不能同时干两车道工作 | [DR-000](process/decisions/DR-000-clean-room-methodology.md) |
| CR-005 | 多 session 累积污染（R-LIC-002）：跨 session 也有风险 | [10 §R-LIC-002](10_RISK_REGISTER.md) |

## 4. CL-001..CL-010 Spec Leakage Checklist（强制每条审查）

| ID | 检查 | 来源 |
| --- | --- | --- |
| CL-001 | 不带上游函数名 / 方法名 / 配置常量名+值（如 `RUN_MODE=simple`） | [specs/_REVIEW_CHECKLIST.md](specs/_REVIEW_CHECKLIST.md) |
| CL-001a | 配置常量 name+value pair = upstream 指纹，必须释义 | 同上（2026-04-28 加） |
| CL-002 | **不抄上游 distinctive 文件结构 / 目录布局**（new-api 已违规过！） | 同上 |
| CL-003 | 不抄 schema 列名 / 表名 / 迁移文件名 | 同上 |
| CL-004 | 不抄 UI 源 / 独特组件名 / 独特类名 | 同上 |
| CL-005 | 不算法逐行翻译为本地词汇——必须重构为"保证形式" | 同上 |
| CL-006 | 每个 reference 引用对应 docs/07 中有 E-LIC-NNN 行；evidence ID 必须**真实存在** | 同上 |
| CL-007 | Lane mode 字段必填（Option B / Option C） | 同上 |
| CL-008 | Capability ID 在 docs/03 矩阵里**真实存在** | 同上 |
| CL-009 | Open Questions 节诚实记录，不伪装"全懂了" | 同上 |
| CL-010 | Source URL 不出现在 implementer-reachable 节（Normal Path 等）；只在 Sources 节 | 同上 |

## 5. 功能不缩水 / Disposition

| ID | 规则 | 来源 |
| --- | --- | --- |
| F-001 | 每个上游功能必须有 7 种合法处置之一（Implemented / Implemented Better / Merged Equivalent / Safe Equivalent / Plugin / Feature Flag / Mandatory Roadmap）；**不许 Dropped/Ignored/Out of Scope** | [03](03_FEATURE_PARITY_MATRIX.md) |
| F-002 | Disposition = target plan；Status = current state；两轴独立 | [03 §Disposition vs Status](03_FEATURE_PARITY_MATRIX.md) |
| F-003 | 锁定能力组（Gateway / Account / Channel / Pool / Edition / Quota / Billing / Admin / Health / Logs / Auth / Plugin / Test）不能静默裁掉 | [04](04_FEATURE_LOCK.md) |

## 6. Deep Mining Mandate（核心，已被 Owner 多次强调）

| ID | 规则 | 来源 |
| --- | --- | --- |
| M-001 | 每个 reference 进 Phase 2 前必须有 `_INVENTORY.md`，列尽所有功能 | [22](22_DEEP_MINING_MANDATE.md) |
| M-002 | 每个 L1/L2 功能必须有 prose decomposition 文件（不只是 ledger 行） | [22 §Owner Sharpening](22_DEEP_MINING_MANDATE.md) |
| M-003 | prose 文件必须含 7 字段（WHY / WHAT / INPUTS / FAILURES_HANDLED / FAILURES_NOT_HANDLED / KEEP-IMPROVE-AVOID / ATTRIBUTION） | [22](22_DEEP_MINING_MANDATE.md) |
| M-004 | 多 reference 共担一个功能时——**每个 reference 都要独立 prose 文件** | [22](22_DEEP_MINING_MANDATE.md) |
| M-005 | Phase 1 → Phase 2 退出门：每个 L1/L2 行有 `Released` 决议 | [DR-008](process/decisions/DR-008-methodology-choice-strict-authenticity.md) |

## 7. 互审制度（Owner 直接指令）

| ID | 规则 | 来源 |
| --- | --- | --- |
| MR-001 | 同样的工作 Claude 和 Codex **各自独立**做一份 | Owner 2026-04-28 |
| MR-002 | 双方互审对方产出（写到 `docs/process/reviews/`） | 同上 |
| MR-003 | PM 综合产出最终行动方案 | 同上 |
| MR-004 | reviewer-lane = **第三个不同 session**，跑 CL-001..010 | [22](22_DEEP_MINING_MANDATE.md) |
| MR-005 | spec Released 才能进 `docs/specs/` 给 implementer 用 | [DR-008](process/decisions/DR-008-methodology-choice-strict-authenticity.md) |

## 8. Decision 圆桌制度

| ID | 规则 | 来源 |
| --- | --- | --- |
| DR-R-001 | 跨切面架构决策走圆桌（DR），不走 Standard Flow | [21](21_DECISION_PROCESS.md) |
| DR-R-002 | DR 在 Discussion 状态超 7 天，PM 必须刷新 Context 后再请 Owner 决策 | [21 §Staleness Protocol](21_DECISION_PROCESS.md) |
| DR-R-003 | DR Decided 后必须执行 Propagation Checklist 才能 Implemented | [21](21_DECISION_PROCESS.md) |

## 9. 持续追踪（运维期约束）

| ID | 规则 | 来源 |
| --- | --- | --- |
| T-001 | 每个 reference 发新版 → 7 天内自审 + 写到 `docs/tracking/<ref>/<date>.md` | [24](24_REFERENCE_TRACKING_POLICY.md) |
| T-002 | 每月走 commit log，每季 Owner 战略复审 | 同上 |
| T-003 | 每条 upstream bug fix 给 HUAKAI 判定（VULNERABLE / SAFE-BY-DESIGN / SAFE-BY-CODE / UNKNOWN） | 同上 |
| T-004 | Phase 1 → Phase 2 退出前，**每个 reference 必须有 baseline 文件** | 同上 |

## 10. 技术栈约束（DR-003..006，已锁）

| ID | 规则 | 来源 |
| --- | --- | --- |
| TS-001 | 后端 Go (stdlib net/http + chi); 永禁 Fiber/fasthttp | [DR-003](process/decisions/DR-003-technology-stack.md), [DR-005](process/decisions/DR-005-go-http-framework.md) |
| TS-002 | 前端 TS + React + Next.js App Router + Tailwind | [DR-004](process/decisions/DR-004-frontend-framework.md) |
| TS-003 | 数据库 PostgreSQL + sqlc + Docker Compose；永禁 SQLite 上生产 | [DR-006](process/decisions/DR-006-database.md) |
| TS-004 | OpenAPI 是 contract source of truth，前端类型从此 codegen | [DR-003 Constraint 2](process/decisions/DR-003-technology-stack.md) |
| TS-005 | 命名跟 [18 术语表](18_GLOSSARY.md) 严格对齐；不许同义词 | [DR-003 Constraint 8](process/decisions/DR-003-technology-stack.md) |
| TS-006 | tenant_id 在每张主表 Day 1 就有 | [DR-001](process/decisions/DR-001-multi-tenancy.md) |
| TS-007 | Money 用 PostgreSQL `numeric(20, 8)`；永禁 float | [Quota+Billing 综合](decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md) |

## 11. Phase Gates

| ID | 规则 | 来源 |
| --- | --- | --- |
| P-001 | Phase 1 → 2: 每个 L1/L2 row 有 Released spec; baseline 文件齐全; 互审 cycle on 核心算法完成 | [DR-008](process/decisions/DR-008-methodology-choice-strict-authenticity.md) |
| P-002 | Phase 2 → 3: API contracts 锁定 + UI assumptions + 高风险文件识别 | [16](16_PHASED_DELIVERY_PLAN.md) |
| P-003 | Phase 3 → 4: Go 骨架 + OpenAPI codegen + provider-neutral streaming abstraction; DR-005/006 完成 | [16](16_PHASED_DELIVERY_PLAN.md) |
| P-004 | Phase 4-9 任何阶段不写未 Released 功能的代码 | [DR-008 §Constraints](process/decisions/DR-008-methodology-choice-strict-authenticity.md) |

## 12. PM Self-Check（每次 commit 之前）

提交前 PM 必须**逐条**对照 self-check，回答 yes/no/N-A：

- [ ] 本次改动触碰的所有 binding 规则我列出来了？
- [ ] CL-001..010 在我修改的每个文件里都通过？
- [ ] 引用的所有 evidence ID 在 docs/07 真实存在？
- [ ] 引用的所有 F-* 在 docs/03 真实存在？
- [ ] AGPL/LGPL/GPL 项目的目录结构/函数名没出现在公开文件？
- [ ] 互审制度是否需要触发（多 agent 都做的工作）？
- [ ] DR 受影响的 propagation checklist 是否同步更新？
- [ ] 持续追踪 (T-001..T-004) 状态是否需要更新？
- [ ] commit message 末尾 `Rules touched: <ID list>` 写了？

## 13. 已发生的违规登记

记录已发生的规则违反，避免重复犯：

| 日期 | 规则 | 违反方 | 发现方 | 修复 commit |
| --- | --- | --- | --- | --- |
| 2026-04-28 | CL-001a (config name+value 指纹) | Claude (E-S2A-005 写了 RUN_MODE=simple) | Codex Phase 1 audit | a308477 |
| 2026-04-28 | CL-002 (AGPL 目录结构) | Claude (new-api inventory 抄目录) | Codex symmetric review | faeeb14 |
| 2026-04-28 | CL-006 (3 个不存在的 evidence ID) | Claude (E-S2A-012 / E-NAI-010 / E-PK-011) | Codex symmetric review | faeeb14 |
| 2026-04-28 | CL-007 (Lane mode 缺) | Claude (layered-account-selection.md) | Codex symmetric review | faeeb14 |
| 2026-04-28 | MR-004 (互审 + reviewer 三方) | Claude (Phase 1 前 ~3 周零互审) | Owner 2026-04-28 直接指出 | fba4dcc |
| 2026-04-28 | M-001 (每个 reference 必须有 inventory) | Claude (Phase 1.1 完了无 inventory) | Owner "整体读完了吗"指出 | 13c0700 |
| 2026-04-28 | M-002 (prose decomposition not optional) | Claude (~30+ L1/L2 features 还只有 ledger 行) | Owner / Phase 1.2 mandate | 进行中 |
| 2026-06-04 | CR-002 (implementer 只读 spec，不读源) | Claude (codex prompt 指向 /home/ubuntu/refs 源码) | Owner "牢记clean-room禁止copy" | 杀 codex 重派 clean-room 盲实现 |

## 14. 规则数量审计（每月）

每月 PM 跑一次：本文件规则总数 vs 来源文件中的 binding 规则总数。差异 > 5 条 = 漏检红灯。

## 15. Owner 2026-06-04 固化要求（本轮指令全集，与旧条冲突以本节为准）

> 来源：Owner 2026-06-04 多条直接指令 + /goal。最高优先。

### 模型分配（MA）
| ID | 规则 |
| --- | --- |
| MA-001 | **sonnet 退役**：调研质量太差，**不再派 sonnet 做任何事**。("sonnet调研太差了，还是你和codex来! 多用codex") |
| MA-002 | **Claude/opus（PM 本人）= 调研 + 设计 + 核验 + 评审 + 接线 + 决策**；每个功能动手前**亲自**读借鉴真源码。`/home/ubuntu/refs/` 有 new-api/one-api/sub2api/CLIProxyAPI 全量真源码 + `wt-notif/docs/decompositions`。 |
| MA-003 | **codex = 实现**；**多用、尽量多开并行**。 |

### 融合升级法（FU，核心，强化 §6 Deep Mining）
| ID | 规则 |
| --- | --- |
| FU-001 | 做**每个**功能前，把四个借鉴项目同功能的**实现方法 / 逻辑 / 代码 / 算法**全部精读吃透——细致入微。 |
| FU-002 | **融合各家所长 → 做成比他们都强**（parity-or-better+；不是单家照搬）。 |
| FU-003 | 用融合法**回扫之前已做的功能模块**，逐个评估并升级到"更强"。(/goal 2026-06-04) |

### Clean-Room 强化（重申 CR-002/003；本轮违规已登记 §13）
| ID | 规则 |
| --- | --- |
| CR-R-001 | **实现者（codex）盲读**：PM 看过原码后用**自己的话**写 spec/分解，codex **只照 spec + HUAKAI 现有模式从零实现**；**codex prompt 严禁出现 `/home/ubuntu/refs` 路径或"读 XX 源码"指令**。 |
| CR-R-002 | 评审时**比对 codex 产物 vs 原码**：字段名/函数名/结构/目录/注释雷同即打回重写。 |

### 算力 / 执行（CP）
| ID | 规则 |
| --- | --- |
| CP-001 | **算力拉满**：codex 多开并行 + PM 持续设计/审/接线，不空转。 |
| CP-002 | **在服务器（kaifa）上执行**；能在服务器跑的就在服务器跑。 |

### 合规边界（CB，PM 立场 + 待决）
| ID | 规则 |
| --- | --- |
| CB-001 | **反检测/反封号规避工具不做**：冒充第一方客户端、绕过上游检测/访问控制（R7 `ApplyMimicryPlan` 请求体伪装）= **park**。D-R3-A 仍待 Owner 裁决；PM 立场为只走合规版（transport policy + 出站诊断，不做指纹复刻）。 |

## 16. Owner 2026-07-16 全局收益优先执行要求

> 来源：Owner “不要给自己界限设定的太死”“只要对我们有益处的，能将基础打扎实的都可以做”
> “真正会动数据库、资金或鉴权规则时……带上借鉴项目的做法以及你的计划和优缺点”
> “这些要求对全局生效”。本节适用于所有 agent、目标和工作树；不覆盖高风险确认与 clean-room。

| ID | 规则 |
| --- | --- |
| BE-001 | 有明确基础收益时，可主动扩大 HUAKAI 源码阅读和低/中风险修复面；不得用自设窄 scope 截断入口、装配、运行时、持久化、观测和恢复整条链路。 |
| BE-002 | 每项扩展阅读必须指向具体闭环：证据、修复、测试、风险记录或明确下一步；禁止无目标全仓扫描、重复调研和 token 空耗。 |
| BE-003 | 收益清楚、可测试、可回滚且不触及数据库结构、资金、鉴权核心、billing ledger、quota enforcement、真实密钥、运行时依赖或生产部署的低/中风险改动，直接实现、验证并记录。 |
| BE-004 | 计划、双计划、review、clean-room 等现有硬门继续生效，但产物和轮次按风险与改动规模配置，复用已有证据，禁止以流程为由拖延闭环。 |
| BE-005 | 要改变数据库 schema、资金路径、鉴权核心、billing ledger、强配额、运行时依赖或生产部署前，必须先给 Owner 决策包：HUAKAI 当前源码链路；借鉴项目当前源码做法及引用；优缺点和功能不缩水对比；至少两个选项；迁移、测试、回滚、风险；明确推荐；精确待决问题。 |
| BE-006 | 修复必须检查辐射模块；无法安全闭环的影响项必须记录，禁止只修表面报错或静默遗忘。 |

## 17. Owner 2026-07-16 PR 与主线合并门

> 来源：Owner “后续所有修复以及改动都需要作为 PR 提交。后面并入主线要我同意”。
> 本节对所有 agent、目标和工作树全局生效。

| ID | 规则 |
| --- | --- |
| PR-001 | 所有代码、测试、合同、规则和正式文档改动必须在独立分支完成，并通过 PR 提交；禁止直接向主线提交改动。 |
| PR-002 | PR 默认使用 Draft，必须写明改动、根因、影响、验证、风险、功能是否缩水和 Owner 待决事项。 |
| PR-003 | agent 不得自行合并 PR；进入 `main` 或其它主线前，必须取得 Owner 针对该 PR 的明确同意。创建或更新 PR 不等于合并授权。 |
| PR-004 | 长任务按可独立验证的闭环拆 PR，不得混入无关目标、其它 agent 改动或未核实问题。 |
