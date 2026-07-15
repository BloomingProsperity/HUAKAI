# HUAKAI 文档归并全库分类 Manifest

> 建档日期：2026-07-15（UTC）
>
> 本文件是第一波“全库分类 manifest，零删除”的唯一产物。它不是实现真伪结论、不是删除授权，也不是最终项目 SSOT。任何 `git rm` 前必须把逐领域删除清单交 Claude 亲检；遇到 Owner-gated、DEFERRED、TODO、未接线或状态存疑，一律停下，不据此判删。

## 1. 口径、边界与方法

- 基线发现：在创建本文件前，`docs/**.md` 恰好 1272 份；已逐份成功打开并读取标题、路径日期和前 30 行，失败 0 份。
- 本文件也在下方自登记，因此明细表共 1273 行；全局“基线状态计数”仍只统计原始 1272 份，避免把控制文件混入待治理母集。
- `rg` 只用于枚举和定位文件；没有把命中或未命中当成功能存在/不存在的证据。
- 本波没有据散文档判断实现真假，也没有以“计划自称完成”替代代码核验。凡含关键实现断言且无明确治理保护/取代关系者，标 `NEEDS-CODE-VERIFY`。
- 日期列口径：优先取文件名中的 `YYYY-MM-DD`；没有路径日期时取该 tracked 文件最近一次 git 变更日期；仍无则写“未标注”。它不是作者承诺日期。
- 领域归属按“路径 + 文首标题”判定；跨领域文档只放一个主领域，备注不代表排除次级影响。
- 状态是“文档治理状态”，不是功能实现状态。`SUPERSEDED` 必须给出实际存在的取代者；`HISTORICAL-DELETE` 仍只是待 Claude 亲检候选。

### 状态定义

| 状态 | 本清单中的含义 | 是否允许本波删除 |
| --- | --- | --- |
| `CURRENT` | 当前治理/契约/决策入口，或 Owner 明确保护、专门处理的文档 | 否 |
| `SUPERSEDED` | 有可指名、实际存在的综合稿/SSOT/无后缀稿取代 | 否；先经 Claude 逐项核验 |
| `HISTORICAL-DELETE` | 文件名明确为已结束 final-review 的纯过程候选 | 否；先确认发现项已迁移且无遗漏 |
| `NEEDS-CODE-VERIFY` | 含实现/运行现状断言，或计划是否执行/废弃无法靠文档自述确认 | 否；后续必须真读实现代码和决策链 |

## 2. 全局统计

### 2.1 原始 1272 份基线

| 指标 | 数量 |
| --- | ---: |
| 原始 Markdown 基线 | 1272 |
| 成功打开并读取前 30 行 | 1272 |
| `CURRENT` | 350 |
| `SUPERSEDED` | 175 |
| `HISTORICAL-DELETE` | 8 |
| `NEEDS-CODE-VERIFY` | 739 |
| 后续建议删除候选（排除全部受保护证据） | 159 |
| 建议保留/待核验 | 1113 |

加入本 manifest 自记录后，明细总数 1273；全表状态计数为 `CURRENT=351`、`SUPERSEDED=175`、`HISTORICAL-DELETE=8`、`NEEDS-CODE-VERIFY=739`。

### 2.2 明确保护与专门处理的族

| 族 | 数量 | 本波处置 |
| --- | ---: | --- |
| `docs/decompositions/` | 88 | clean-room 镜像/分解证据；不删不归并 |
| `docs/research/` | 47 | 原始研究/抓包；不删不归并 |
| `docs/process/research/` | 28 | 研究过程证据；不删不归并 |
| `docs/reference_delta/` | 18 | 上游差异证据；不删不归并 |
| `docs/process/evidence/` | 1 | 证据工件；不删不归并 |
| 上述受保护证据合计 | 182 | 其中 24 份虽有明确后继，仍不列入建议删除 |
| trust-chain / receipt / audit-ledger 专门族 | 45 | 41 份独立列入 trust 领域，另 4 份已包含在受保护证据族；本波不判删 |
| `docs/architecture/egress-tls-mimicry-SSOT.md` | 1 | 已完成 SSOT，仅登记；旧于 2026-07-15 的散计划/评审可指名由它取代 |

### 2.3 各领域计数总览（含本 manifest 自记录）

| 领域 | 总数 | CURRENT | SUPERSEDED | HISTORICAL-DELETE | NEEDS-CODE-VERIFY | 建议删 | 建议保留 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 项目治理 / clean-room | 328 | 61 | 32 | 0 | 235 | 32 | 296 |
| relay-gateway 转发链 | 32 | 2 | 5 | 0 | 25 | 5 | 27 |
| protocol-openapi-models 协议 / 契约 / 模型 | 34 | 7 | 11 | 3 | 13 | 14 | 20 |
| billing-pricing-payment 计费 / 定价 / 支付 | 107 | 8 | 10 | 1 | 88 | 11 | 96 |
| quota-rate-concurrency 配额 / 限流 / 并发 | 36 | 2 | 2 | 1 | 31 | 3 | 33 |
| auth-session-rbac 登录 / 鉴权 / 会话 | 53 | 4 | 5 | 0 | 44 | 5 | 48 |
| account-pool-dispatch 账号池 / 选号 / 调度 | 23 | 2 | 4 | 0 | 17 | 4 | 19 |
| credentials 凭证 / OAuth / 刷新 | 63 | 10 | 7 | 0 | 46 | 7 | 56 |
| egress-tls-mimicry 出口 / TLS / 指纹 | 65 | 5 | 53 | 0 | 7 | 53 | 12 |
| provider-adapters 厂商适配 | 63 | 10 | 5 | 0 | 48 | 5 | 58 |
| reseller-distribution 分销 / 商户 | 4 | 2 | 0 | 0 | 2 | 0 | 4 |
| frontend 前端 | 52 | 5 | 4 | 0 | 43 | 4 | 48 |
| hermes 运维助手 | 34 | 9 | 6 | 0 | 19 | 6 | 28 |
| observability-logging 可观测 / 日志 | 22 | 0 | 0 | 0 | 22 | 0 | 22 |
| trust-chain-audit 信任链 / 收据 / 审计账本 | 41 | 41 | 0 | 0 | 0 | 0 | 41 |
| media 媒体 | 9 | 0 | 0 | 0 | 9 | 0 | 9 |
| deployment 部署 / 运维 | 16 | 2 | 0 | 0 | 14 | 0 | 16 |
| reference-decompositions 镜像调研 / 分解证据 | 182 | 158 | 24 | 0 | 0 | 0 | 182 |
| database-schema 数据库 / schema | 17 | 5 | 4 | 0 | 8 | 4 | 13 |
| testing-release-quality 测试 / 评审 / 发布质量 | 92 | 18 | 3 | 3 | 68 | 6 | 86 |

## 3. 最需要 Claude 人工裁定的 TOP 清单

1. **项目总览竞争源**：`docs/00-MASTER-PLAN.md`、`docs/PROJECT_MASTER_PLAN.md`、`docs/PHASE_3_SKELETON.md` 都含大范围现状断言；须逐领域核代码后决定哪些内容进入未来 `PROJECT-SSOT-INDEX.md`，不能按日期直接判旧。
2. **最新代码盘点候选底稿**：`docs/architecture/backend-feature-inventory-codex.md` 与 `docs/architecture/deprecated-schema.md` 自称近期核验，仍需 Claude 复读其所引代码和调用链后，才能作为领域 SSOT 输入。
3. **出口同日增量冲突**：`docs/process/plans/2026-07-15-tenant-default-egress.md`、`docs/process/plans/2026-07-15-tenant-default-egress-codex.md` 与既有出口 SSOT 同日；必须人工判断是已吸收内容还是 SSOT 之后的新决策。
4. **分销/商户最新三件套**：`docs/process/plans/2026-07-15-reseller-phase1-codex.md`、`docs/process/plans/2026-07-15-reseller-arc-final-model.md`、`docs/process/plans/2026-07-15-coadmin-and-merchant-tenant-arc-claude.md` 涉及 Owner-gated 商业模型，绝不能按“尚未实现”判过期。
5. **同日活跃综合稿与独立稿**：并发缺陷、model registry admin CRUD、前端功能树、MVP blockers 等 2026-07-15 文档可能仍在推进；只有有明确综合稿的独立稿才标 `SUPERSEDED`，综合稿本身保留。
6. **739 份 `NEEDS-CODE-VERIFY`**：优先顺序建议为钱路径 → auth/session/RBAC → quota/concurrency → account-pool/dispatch → provider/credential → relay/protocol → UI/ops；每份必须打开实现文件并追调用链，不能用搜索命中裁定。
7. **所有 DEFERRED/PENDING 与 Owner-gated 记录**：必须先对照 `docs/10_RISK_REGISTER.md` 和 DR；功能没建、TODO、未接线、默认关闭都不构成删除理由。
8. **159 份建议删除候选**：只是“明确后继或 final-review 过程”集合；任何 `git rm` 前都要按领域导出清单给 Claude 逐份亲检。
9. **182 份受保护证据**：即使其中 24 份有明确 source-verified/综合后继，也因 Owner 本次范围排除而保留。
10. **trust-chain 专门族 45 份**：本波只登记，后续若治理必须另开专门波，不能夹带进普通历史清理。

## 4. 全量逐文件分类

### 4.1 项目治理 / clean-room

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/00-MASTER-PLAN.md` | 2026-06-07 | 文首主题：HUAKAI 项目总纲 (MASTER PLAN)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/00_PM_OPERATING_SYSTEM.md` | 2026-04-27 | 文首主题：PM Operating System。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/01-STANDARD-PROCESS.md` | 2026-06-07 | 文首主题：HUAKAI 标准操作流程 (STANDARD OPERATING PROCESS)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/01_PROJECT_BRIEF.md` | 2026-05-19 | 文首主题：Project Brief。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/02_CAPABILITY_CONTRACT.md` | 2026-05-19 | 文首主题：Capability Contract。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` | 2026-06-23 | 文首主题：HUAKAI 融合架构 — 项目逻辑框架。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/03_FEATURE_PARITY_MATRIX.md` | 2026-06-23 | 文首主题：Feature Parity Matrix。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/04_FEATURE_LOCK.md` | 2026-05-19 | 文首主题：Feature Lock。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/05_CLEAN_ROOM_POLICY.md` | 2026-05-28 | 文首主题：Clean-Room Policy。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/06_REFERENCE_PROJECTS.md` | 2026-05-28 | 文首主题：Reference Projects。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/07_REFERENCE_EVIDENCE_LEDGER.md` | 2026-07-14 | 文首主题：Reference Evidence Ledger。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/08_REAL_WORLD_SCENARIOS.md` | 2026-05-22 | 文首主题：Real-World Scenarios。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/09_BUG_PATTERN_LIBRARY.md` | 2026-04-27 | 文首主题：Bug Pattern Library。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/10_RISK_REGISTER.md` | 2026-07-11 | 文首主题：Risk Register。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/11_ACCEPTANCE_TEST_MATRIX.md` | 2026-07-15 | 文首主题：Acceptance Test Matrix。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/12_AGENT_WORKFLOW.md` | 2026-05-28 | 文首主题：Agent Workflow。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/15_RELEASE_GATES.md` | 2026-06-09 | 文首主题：Release Gates。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/16_PHASED_DELIVERY_PLAN.md` | 2026-05-19 | 文首主题：Phased Delivery Plan。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/17_FEATURE_LEVEL_MATRIX.md` | 2026-05-19 | 文首主题：Feature Level Matrix。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/18_GLOSSARY.md` | 2026-04-28 | 文首主题：Glossary。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md` | 2026-05-19 | 文首主题：Clean-Room Methodology Options。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/21_DECISION_PROCESS.md` | 2026-05-19 | 文首主题：Decision Process (Round-Table Mode)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/22_DEEP_MINING_MANDATE.md` | 2026-05-19 | 文首主题：Deep Mining Mandate。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/24_REFERENCE_TRACKING_POLICY.md` | 2026-05-28 | 文首主题：Reference Tracking and Continuous Learning Policy。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/PHASE_3_SKELETON.md` | 2026-05-19 | 文首主题：Phase 3 Skeleton — Status & Map。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/PROJECT_MASTER_PLAN.md` | 2026-05-28 | 文首主题：HUAKAI 项目总规划 / Project Master Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/RULES-DIGEST.md` | 2026-06-14 | 文首主题：HUAKAI 项目规则全集。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/RULES.md` | 2026-06-03 | 文首主题：HUAKAI 规则清单（Rules Manifest）。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/architecture/DOC-CONSOLIDATION-MANIFEST.md` | 2026-07-15 | 文首主题：HUAKAI 文档归并全库分类 Manifest。 | `CURRENT` | 本清单 | 控制文件；不在 1272 基线内。 |
| `docs/architecture/runtime-logic/README.md` | 2026-07-11 | 文首主题：运行逻辑 / 模块间配合文档(runtime-logic)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/2026-05-24-ref-anchor.md` | 2026-05-24 | 文首主题：2026-05-24 Ref-Anchor Ledger (CLAUDE.md #12 First-Cite Recency Check)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/decisions/DR-000-clean-room-methodology.md` | 2026-05-19 | 文首主题：DR-000: Clean-Room Methodology For HUAKAI。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-003-technology-stack.md` | 2026-05-19 | 文首主题：DR-003: Technology Stack For Phase 2-9 Personal Edition。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-005-go-http-framework.md` | 2026-05-19 | 文首主题：DR-005: Go HTTP Framework。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-007-product-positioning-and-breadth.md` | 2026-05-19 | 文首主题：DR-007: HUAKAI Product Positioning — "Sub2API Plus Breadth"。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-008-methodology-choice-strict-authenticity.md` | 2026-05-19 | 文首主题：DR-008: Methodology Choice — Strict Authenticity Over Speed。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-009-algorithm-upgrade-policy.md` | 2026-05-19 | 文首主题：DR-009: Algorithm Upgrade Policy — 8 Decisions + Client Transparency + Seller…。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-009-boring-license-clear.md` | 2026-05-19 | 文首主题：DR-009: Boring License Clearance。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-010-auth-last-used-telemetry.md` | 2026-05-31 | 文首主题：DR-010: Auth Last-Used Telemetry Carve-Out。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/_TEMPLATE.md` | 2026-05-19 | 文首主题：DR-NNN: <Short Title>。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/feature-tree/README.md` | 2026-05-30 | 文首主题：HUAKAI 功能树 · 推进 + 修复 状态地图。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/REFRESH-2026-06-14.md` | 2026-06-14 | 文首主题：HUAKAI 特性树状态刷新 — 2026-06-14。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/accounts-auth.md` | 2026-06-07 | 文首主题：Feature-Tree Audit: accounts-auth。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/admin-ops-platform.md` | 2026-06-03 | 文首主题：Admin-Ops-Platform Feature Tree。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/benchmark-2026-06-06.md` | 2026-06-06 | 文首主题：HUAKAI 项目标杆功能树 (BENCHMARK · 2026-06-06)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/content-features.md` | 2026-06-03 | 文首主题：Content-Features Domain Audit。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/gap-roadmap.md` | 2026-06-15 | 文首主题：Feature-tree gap closure — PM roadmap (verify-then-residual)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/growth-ux.md` | 2026-06-03 | 文首主题：Growth-UX 域特性审计。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/routing-loadbalance.md` | 2026-06-03 | 文首主题：Feature-Tree Audit: routing-loadbalance。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/notifications.md` | 2026-06-03 | 文首主题：Gap Critique: Notification System。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/ops-suite.md` | 2026-06-03 | 文首主题：Critique: Gap Design ops-suite。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/notifications.md` | 2026-06-03 | 文首主题：Gap Design: Notification System。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/platform-settings.md` | 2026-06-03 | 文首主题：Gap Design: Admin Platform-Settings Consolidation。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/content-moderation.md` | 2026-06-03 | 文首主题：Gap Spec: Content Moderation (F-CONTENT-MOD-001)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/per-key-controls.md` | 2026-06-03 | 文首主题：Gap Spec: Per-API-Key Controls。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/platform-settings.md` | 2026-06-03 | 文首主题：Gap Spec: Admin Platform-Settings Consolidation。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-verification-report-codex.md` | 2026-06-03 | 文首主题：HUAKAI Survey Gap Verification Report。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-deep-decomposition-plan.md` | 2026-04-29 | 文首主题：2026-04-29 Deep Decomposition Plan — 7 reference projects。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-envoy-topology-r3.md` | 2026-04-29 | 文首主题：2026-04-29 envoy topology R3 source-verified decomposition。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-helicone-cost-routing-r3.md` | 2026-04-29 | 文首主题：2026-04-29 helicone cost routing R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-integration-sprint-plan.md` | 2026-04-29 | 文首主题：2026-04-29 Integration Sprint Plan — make HUAKAI run end-to-end。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-oneapi-channel-auto-disable-r3.md` | 2026-04-29 | 文首主题：2026-04-29 one-api channel auto-disable R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-30-n4-l0-minimum-codex.md` | 2026-04-30 | 文首主题：2026-04-30 N+4 L0 Minimum - Codex Independent Plan。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n4-l0-minimum.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n4-l0-minimum.md` | 2026-04-30 | 文首主题：2026-04-30 N+4 L0 Minimum — Synthesized Plan。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-04-30-n5b-handler-rewrite-claude.md` | 2026-04-30 | 文首主题：N+5b — Chat handler rewrite + escape hatch deletion (Claude independent draft)。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5b-handler-rewrite.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5b-handler-rewrite-codex.md` | 2026-04-30 | 文首主题：2026-04-30 N+5b Chat Handler Rewrite + Escape Hatch Deletion - Codex Independ…。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5b-handler-rewrite.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5b-handler-rewrite.md` | 2026-04-30 | 文首主题：N+5b — Chat Handler Rewrite + Escape Hatch Deletion (Synthesized Plan)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-01-n4b-admin-keys-claude.md` | 2026-05-01 | 文首主题：N+4b — Admin API-key Issuance + Ledger FK Backfill (Claude independent draft)。 | `SUPERSEDED` | `docs/process/plans/2026-05-01-n4b-admin-keys.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-01-n4b-admin-keys-codex.md` | 2026-05-01 | 文首主题：1. Evidence Read。 | `SUPERSEDED` | `docs/process/plans/2026-05-01-n4b-admin-keys.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-01-n4b-admin-keys.md` | 2026-05-01 | 文首主题：N+4b — Admin API-key Issuance + Ledger FK Backfill (Synthesized)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-02-accapi-spine-claude.md` | 2026-05-02 | 文首主题：Account-to-API spine — Claude plan (CLAUDE.md #10 parallel-draft)。 | `SUPERSEDED` | `docs/process/plans/2026-05-02-accapi-spine.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-02-accapi-spine-codex.md` | 2026-05-02 | 文首主题：2026-05-02 Account-to-API Spine 0011 Codex Parallel Draft。 | `SUPERSEDED` | `docs/process/plans/2026-05-02-accapi-spine.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-02-accapi-spine.md` | 2026-05-02 | 文首主题：Account-to-API spine 0011 — Claude/Codex synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-02-huakai-algo-upgrade-claude.md` | 2026-05-02 | 文首主题：HUAKAI 算法升级计划 — Claude 平行版。 | `SUPERSEDED` | `docs/process/plans/2026-05-02-huakai-algo-upgrade-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-02-huakai-algo-upgrade-codex.md` | 2026-05-02 | 文首主题：2026-05-02 HUAKAI Algorithm Upgrade Plan - Codex。 | `SUPERSEDED` | `docs/process/plans/2026-05-02-huakai-algo-upgrade-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-02-huakai-algo-upgrade-synthesis.md` | 2026-05-02 | 文首主题：HUAKAI 算法升级 — 平行版 Synthesis（Claude × Codex）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-02-huakai-improvements-codex.md` | 2026-05-02 | 文首主题：2026-05-02 HUAKAI Improvements - Codex Independent Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-02-huakai-reverse-proxy-core-claude.md` | 2026-05-02 | 文首主题：HUAKAI 反向代理核心模块细化清单 (Claude side, parallel-draft)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-02-huakai-reverse-proxy-core-codex.md` | 2026-05-02 | 文首主题：2026-05-02 HUAKAI reverse proxy core refinement list。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-04-a22-codeparallel-synthesis.md` | 2026-05-04 | 文首主题：A22 Health Hysteresis FSM — Code-Parallel Synthesis (third under 2026-05-04 r…。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-04-r6-codeparallel-synthesis.md` | 2026-05-04 | 文首主题：R6 Error Normalization — Code-Parallel Synthesis (first under 2026-05-04 rule…。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-06-readme-legal-boundaries-draft.md` | 2026-05-06 | 文首主题：README + LEGAL.md 边界草案（R3 启动前置）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-07-remote-linux-env-codex.md` | 2026-05-07 | 文首主题：2026-05-07 Remote Linux Env Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-cpa-cliproxyapi-reference-scan.md` | 2026-05-08 | 文首主题：2026-05-08 CPA / CLIProxyAPI 参考项目扫描。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade1-binding-aware-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Upgrade #1 — binding-aware filter (claude lane plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade1-binding-aware-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #1 — binding-aware filter。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade6-client-identity-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Upgrade #6 — client identity detector (claude lane plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade6-client-identity-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #6 — Client Identity Detector Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade6-u6d-design-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #6 U6-D Atomic Design — Codex Lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade6-u6d-design-sonnet.md` | 2026-05-08 | 文首主题：U6-D atomic — identity → adapter mapping 策略（设计 / sonnet lane）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade6-u6d-synthesis.md` | 2026-05-08 | 文首主题：2026-05-08 U6-D 双 lane synthesis (sonnet + codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-upgrade7-passthrough-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Upgrade #7 — 上游字段 passthrough 完整性矩阵 (claude lane plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade7-u7e-codex.md` | 2026-05-08 | 文首主题：2026-05-08 U7-E FieldMatrix 字段级 Verdict Matrix Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-upgrade7-u7e-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-upgrade7-u7e-synthesis.md` | 2026-05-08 | 文首主题：2026-05-08 Upgrade #7 U7-E — 双 lane 综合。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-vertical-closure-claude.md` | 2026-05-08 | 文首主题：2026-05-08 纵向闭环计划 — Claude 草案。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-vertical-closure-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-vertical-closure-codex.md` | 2026-05-08 | 文首主题：2026-05-08 Vertical Closure Codex 独立计划。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-vertical-closure-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-market-research-codex.md` | 2026-05-09 | 文首主题：2026-05-09 Market Research Codex Lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-09-next-pivot-claude.md` | 2026-05-09 | 文首主题：Next Pivot — Claude Lane Independent Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-next-pivot-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-next-pivot-codex.md` | 2026-05-09 | 文首主题：2026-05-09 Next Pivot Plan - Codex Independent Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-next-pivot-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-next-pivot-synthesis.md` | 2026-05-09 | 文首主题：Next Pivot — Claude × Codex 综合。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-09-p0c-followup-plan-synthesis.md` | 2026-05-09 | 文首主题：P-0c Follow-up Plan — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-09-three-directions-claude.md` | 2026-05-09 | 文首主题：三方向差异化评估 — Claude 独立草案。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-three-directions-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-three-directions-codex.md` | 2026-05-09 | 文首主题：三方向差异化评估 — Codex 独立草案。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-three-directions-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-three-directions-synthesis.md` | 2026-05-09 | 文首主题：三方向差异化评估 — 综合（含源码证据）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-10-feature-parity-audit-codex.md` | 2026-05-10 | 文首主题：2026-05-10 feature parity audit codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-p1-capability-payload-refinement-claude.md` | 2026-05-12 | 文首主题：P-1 Capability Graph IR Payload 细化 — Claude lane plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-12-p1-capability-payload-refinement-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-12-p1-capability-payload-refinement-codex.md` | 2026-05-12 | 文首主题：2026-05-12 P-1 Capability Graph IR Payload 细化独立计划（Codex）。 | `SUPERSEDED` | `docs/process/plans/2026-05-12-p1-capability-payload-refinement-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-12-p1-capability-payload-refinement-synthesis.md` | 2026-05-12 | 文首主题：P-1 Capability Graph IR Payload 细化 — synthesis 决议。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-12-p2-client-adapter-plan-claude.md` | 2026-05-12 | 文首主题：P-2 ClientAdapter — Claude lane plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-12-p2-client-adapter-plan-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-12-p2-client-adapter-plan-codex.md` | 2026-05-12 | 文首主题：2026-05-12 P-2 ClientAdapter 切片计划（Codex 独立草案）。 | `SUPERSEDED` | `docs/process/plans/2026-05-12-p2-client-adapter-plan-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-12-p2-client-adapter-plan-synthesis.md` | 2026-05-12 | 文首主题：P-2 ClientAdapter — Synthesis（Claude lane + Codex lane）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-13-ref-deep-mining-brief.md` | 2026-05-13 | 文首主题：Reference Project Deep Mining — 通用 Brief（T1 dir skeleton）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-t5-user-verify-codex.md` | 2026-05-13 | 文首主题：2026-05-13 T5 user-facing verify endpoint + huakai-verify CLI。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-codex-cli-request-signature-codex.md` | 2026-05-14 | 文首主题：2026-05-14 Codex CLI Request Signature Source Analysis - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-m2-admin-pools-crud-codex.md` | 2026-05-14 | 文首主题：2026-05-14 M2 admin pools CRUD Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-ref-borrow-gap-matrix-codex.md` | 2026-05-14 | 文首主题：2026-05-14 ref borrow gap matrix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-t7-tokencheck-codex.md` | 2026-05-14 | 文首主题：2026-05-14 T7 tokencheck。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-t8-redact-audience-codex.md` | 2026-05-14 | 文首主题：2026-05-14 T8 Redact Audience。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-cache-001-l2-cache-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-CACHE-001 简单 L2 响应缓存 (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-cache-001-l2-cache-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-CACHE-001 L2 response cache Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-cred-001-synthesis-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-CRED-001 Synthesis Plan — Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-mandatory-roadmap-priority-codex.md` | 2026-05-15 | 文首主题：2026-05-15 Mandatory Roadmap Priority Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-round2b-e2e-smoke-codex.md` | 2026-05-15 | 文首主题：2026-05-15 Round 2-B E2E Smoke。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-at-matrix-status-sync-plan-claude.md` | 2026-05-16 | 文首主题：AT Matrix Status Sync Plan (Claude Lane Plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-auth-007-r2-high-security-fixes-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-AUTH-007 R2 High Security Fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md` | 2026-05-16 | 文首主题：2026-05-16 F-CRED-001 OCAW Answers (Claude 主笔)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-cred-001-phase-a-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CRED-001 Phase A Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-cred-001-phase-b-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CRED-001 Phase B Codex Executor Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-priv-001-spec-claude.md` | 2026-05-16 | 文首主题：F-PRIV-001 Privacy / No User Data Logs Spec — Claude Lane Draft。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-priv-001-spec-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-PRIV-001 Privacy / No User Data Logs Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-email-smtp-backend-codex.md` | 2026-05-17 | 文首主题：2026-05-17 Email SMTP Backend Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-f-auth-007-r2-med-followup-codex.md` | 2026-05-17 | 文首主题：2026-05-17 F-AUTH-007 R2 MED Follow-up。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-f-priv-1-implementation-codex.md` | 2026-05-17 | 文首主题：2026-05-17 F-PRIV-1 privacy implementation codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-p1-wave2-backend-contract-fixes-codex.md` | 2026-05-17 | 文首主题：2026-05-17 P1 Wave 2 Backend Contract Fixes Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-receipt-p2-fixes-codex.md` | 2026-05-17 | 文首主题：2026-05-17 receipt P2 fixes codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-f-comm-001-phase1-codex.md` | 2026-05-18 | 文首主题：2026-05-18 F-COMM-001 Phase 1 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-receipt-sequence-codex.md` | 2026-05-18 | 文首主题：2026-05-18 receipt sequence P1 fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-refund-replica-verify-degrade-codex.md` | 2026-05-18 | 文首主题：2026-05-18 refund replica and verify degrade。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-docs-tree-process-cleanup-codex.md` | 2026-05-19 | 文首主题：2026-05-19 docs tree process cleanup。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-wave-2a-chat-completions-refactor-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Wave 2-A Chat Completions Handler Refactor。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-wave-3b-pool-subpackages-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Wave 3-B pool subpackages。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-cleanup-batch3-codex.md` | 2026-05-20 | 文首主题：2026-05-20 cleanup-batch3-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-code-cleanup-claude.md` | 2026-05-20 | 文首主题：代码清理计划 (Claude)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-renew-p2-fixes-codex.md` | 2026-05-20 | 文首主题：2026-05-20 renew P2 fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md` | 2026-05-21 | 文首主题：账号转 API 链路 — 功能完整性 gap 分析。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-audit-b-codex.md` | 2026-05-21 | 文首主题：2026-05-21 audit-b-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-direction-1-claude.md` | 2026-05-21 | 文首主题：方向 1 执行计划 — Go 管线作大脑 / Rust 作传输层(Claude 稿)。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-direction-1.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-direction-1-codex.md` | 2026-05-21 | 文首主题：2026-05-21 方向 1 执行计划（Codex 独立稿）。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-direction-1.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-direction-1.md` | 2026-05-21 | 文首主题：方向 1 执行计划 — 权威稿(Claude × codex 综合)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-21-full-audit-claude.md` | 2026-05-21 | 文首主题：2026-05-21 HUAKAI 全面自查计划（Claude 独立草案）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-full-audit-codex.md` | 2026-05-21 | 文首主题：2026-05-21 HUAKAI 全面自查独立评估与执行计划 - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-juice-model-degradation-codex-eval.md` | 2026-05-21 | 文首主题：2026-05-21 juice model degradation codex eval。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-juice-transparency-refcompare-codex.md` | 2026-05-21 | 文首主题：2026-05-21 juice-transparency-refcompare-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-juice-web-crawl-codex.md` | 2026-05-21 | 文首主题：2026-05-21 juice-web-crawl-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-mainchain-analysis-eval-claude.md` | 2026-05-21 | 文首主题：Owner 主链微步骤分析 — 评估与对齐(Claude 独立稿)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-mainchain-analysis-eval-codex.md` | 2026-05-21 | 文首主题：2026-05-21 Owner 主链分析评估 — Codex 独立稿。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-phase1-design-codex.md` | 2026-05-21 | 文首主题：2026-05-21 Phase 1 详细实施设计 - Codex 独立稿。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-phase1-design-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-phase1-design-synthesis.md` | 2026-05-21 | 文首主题：Phase 1 详细实施设计 — 综合稿(Claude × codex × Owner 决策)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-21-pr2-attempt-error-taxonomy-codex.md` | 2026-05-21 | 文首主题：2026-05-21 PR2 Attempt Error Taxonomy Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-pr3-handler-attempt-loop-codex.md` | 2026-05-21 | 文首主题：2026-05-21 PR3 handler attempt loop skeleton。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-22-audit-remediation-wave-claude.md` | 2026-05-22 | 文首主题：2026-05-22 全仓深度审计 — 总清单 + 补救波计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-22-audit-remediation-wave-codex.md` | 2026-05-22 | 文首主题：2026-05-22 HUAKAI full-codebase deep audit remediation-wave plan - Codex para…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-22-audit-remediation-wave.md` | 2026-05-22 | 文首主题：2026-05-22 全仓深度审计 — 补救波权威计划(合成稿)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-22-w3-public-error-model.md` | 2026-05-22 | 文首主题：W3 公开错误安全模型 —— 实施 spec。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-go-vertical-closure-synthesis.md` | 2026-05-23 | 文首主题：2026-05-23 Go 后端「树向闭环补齐」synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md` | 2026-05-23 | 文首主题：W5 计划（Claude lane）—— audit 原子化敏感变更审计。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md` | 2026-05-23 | 文首主题：W5 audit 原子化敏感变更审计 Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-w5-audit-atomicity-synthesis.md` | 2026-05-23 | 文首主题：2026-05-23 W5 Audit 原子化敏感变更 综合计划。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-24-decisions-locked.md` | 2026-05-24 | 文首主题：账号转 API — D 决策固化(Owner 2026-05-24)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-s2-refresh-outcome-wiring-codex.md` | 2026-05-24 | 文首主题：2026-05-24 S2 refresh outcome wiring - Codex independent plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-slice2-5-round2-internal-hmac-codex.md` | 2026-05-26 | 文首主题：2026-05-26 Slice 2.5 Round 2 Internal HMAC S0/S1 Fix Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-chatgpt-r2-flow-id-fix-codex.md` | 2026-05-27 | 文首主题：2026-05-27 ChatGPT R2 admin flowid fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-early-heartbeat-codex.md` | 2026-05-27 | 文首主题：2026-05-27 Early Heartbeat Reference Research。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-fix-tool-call-id-validator-codex.md` | 2026-05-28 | 文首主题：2026-05-28 toolcallid 校验修复。 | `SUPERSEDED` | `docs/process/plans/2026-05-28-fix-tool-call-id-validator.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-28-fix-tool-call-id-validator.md` | 2026-05-28 | 文首主题：2026-05-28 toolcallid 校验修复。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-28-s1-013-durable-hold-claude.md` | 2026-05-28 | 文首主题：2026-05-28 S1-013 Durable Atomic Balance Hold — Claude independent draft (#10)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1-013-durable-hold-codex.md` | 2026-05-28 | 文首主题：Plan: S1-013 Durable Held Balance + Atomic Pre-deduction。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-waveb-A-evidence-codex.md` | 2026-05-28 | 文首主题：2026-05-28 WaveB-A Evidence Plan (Codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-r-sub-wire-claude.md` | 2026-05-29 | 文首主题：R-SUB-WIRE 实施计划（Claude 独立草案）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-s2163-s1029-shared-fu-claude.md` | 2026-05-29 | 文首主题：S2-163/S1-029 shared follow-up — 计划（Claude）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-30-role-panel-switch-codex.md` | 2026-05-30 | 文首主题：2026-05-30 Role Panel Switch Codex Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-30-role-panel-switch-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-31-money-loop-claude.md` | 2026-05-31 | 文首主题：钱闭环闭合计划 — Claude 稿(2026-05-31)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-money-loop-codex.md` | 2026-05-31 | 文首主题：Using clean-room specifier lane only. I verified the local gap before plannin…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-s1-005-codex.md` | 2026-05-31 | 文首主题：2026-05-31 S1-005 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-s1-024-refresh-health-filter-codex.md` | 2026-05-31 | 文首主题：2026-05-31 S1-024 refresh health filter。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-s2-002-codex.md` | 2026-05-31 | 文首主题：2026-05-31 S2-002 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-s2-006-codex.md` | 2026-05-31 | 文首主题：2026-05-31 S2-006 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-money-5-admin-credit-codex.md` | 2026-06-01 | 文首主题：2026-06-01 MONEY-5 admin manual balance adjustment。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-money-6-balance-enforcement-codex.md` | 2026-06-01 | 文首主题：2026-06-01 MONEY-6 balance enforcement mandatory。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s1-006-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S1-006 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s1-028-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S1-028 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s1-029-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S1-029 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-003-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-003 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-004-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-004 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-005-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-005 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-008-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-008 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-013-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-013 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-014-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-014 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-docs-state-sync-codex.md` | 2026-06-02 | 文首主题：2026-06-02 docs-state-sync-codex。 | `SUPERSEDED` | `docs/process/plans/2026-06-02-docs-state-sync.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-06-02-docs-state-sync.md` | 2026-06-02 | 文首主题：2026-06-02 docs-state-sync。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-02-mixed-channel-risk-codex.md` | 2026-06-02 | 文首主题：2026-06-02 mixed-channel-risk Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-model-auto-sync-codex.md` | 2026-06-02 | 文首主题：2026-06-02 model-auto-sync Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-money-5-fix-codex.md` | 2026-06-02 | 文首主题：2026-06-02 money-5 admin balance adjustment fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-admin-overview-snapshotcache-codex.md` | 2026-06-03 | 文首主题：2026-06-03 admin overview snapshotcache codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-admin-usage-leaderboard-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Admin Usage Leaderboard Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-admin-usage-overview-codex.md` | 2026-06-03 | 文首主题：2026-06-03 admin usage overview codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-apikey-ip-allowlist-codex.md` | 2026-06-03 | 文首主题：2026-06-03 API Key IP Allowlist - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-audit001-cost-disputes-codex.md` | 2026-06-03 | 文首主题：2026-06-03 audit001-cost-disputes Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-auth-secret-encryption-codex.md` | 2026-06-03 | 文首主题：2026-06-03 auth-secret encryption codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-captcha-turnstile-codex.md` | 2026-06-03 | 文首主题：2026-06-03 captcha-turnstile Codex execution plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-content-moderation-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Content Moderation Slice 1 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-f-sec-005-header-firewall-codex.md` | 2026-06-03 | 文首主题：2026-06-03 F-SEC-005 Header Firewall Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-fu-003-admin-analytics-cache-codex.md` | 2026-06-03 | 文首主题：2026-06-03 FU-003 admin analytics snapshot cache (Codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-gap-closure-multiagent-waves.md` | 2026-06-03 | 文首主题：Plan: feature-tree gap-closure — multi-agent implementation waves。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-generation-endpoint-codex.md` | 2026-06-03 | 文首主题：2026-06-03 generation-endpoint-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-gitleaks-allowlist-codex.md` | 2026-06-03 | 文首主题：2026-06-03 gitleaks allowlist。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-models-capabilities-codex.md` | 2026-06-03 | 文首主题：2026-06-03 models capabilities codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-notifications-worker-stats-codex.md` | 2026-06-03 | 文首主题：2026-06-03 notifications worker stats first slice。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-per-key-controls-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Per-Key Controls Slice 1 - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-platform-settings-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 platform-settings slice1。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-usage-granularity-codex.md` | 2026-06-03 | 文首主题：2026-06-03 usage granularity。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-usage-performance-codex.md` | 2026-06-03 | 文首主题：2026-06-03 usage-performance codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-content-moderation-chat-wire-codex.md` | 2026-06-04 | 文首主题：2026-06-04 Content Moderation Chat Wire Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-embeddings-codex.md` | 2026-06-04 | 文首主题：2026-06-04 embeddings-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-gotify-priority-codex.md` | 2026-06-04 | 文首主题：2026-06-04 gotify-priority-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-notifications-slice2-codex.md` | 2026-06-04 | 文首主题：2026-06-04 notifications-slice2-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-rpm-tpm-budget-codex.md` | 2026-06-04 | 文首主题：2026-06-04 RPM/TPM Budget Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-settings-resilient-read-codex.md` | 2026-06-04 | 文首主题：2026-06-04 settings-resilient-read。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-wire-resilience-codex.md` | 2026-06-04 | 文首主题：2026-06-04 Wire Resilience Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-c1-refund-codex.md` | 2026-06-05 | 文首主题：2026-06-05 C1 Refund Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-c5-cancel-renew-codex.md` | 2026-06-05 | 文首主题：2026-06-05 C5 Cancel-Renew Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-refund-approval-codex.md` | 2026-06-05 | 文首主题：2026-06-05 Refund Approval Closed Loop - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-refund-available-balance-guard-codex.md` | 2026-06-05 | 文首主题：2026-06-05 refund available balance guard - Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-subscription-reminder-dedup-codex.md` | 2026-06-05 | 文首主题：2026-06-05 subscription reminder dedup codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-admin-users-read-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Admin Users Read Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-announcements-codex.md` | 2026-06-06 | 文首主题：2026-06-06 announcements Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-apikey-usage-summary-codex.md` | 2026-06-06 | 文首主题：2026-06-06 api-key usage summary。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-audit-proof-export-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Audit Proof Export Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-controlhttp-g3-codex.md` | 2026-06-06 | 文首主题：2026-06-06 controlhttp G3 package consolidation plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-daily-checkin-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Daily Check-In Reward Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-delete-zero-import-packages-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Delete Zero-Import Packages。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-dispute-admin-list-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Admin Dispute List Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-financial-export-codex.md` | 2026-06-06 | 文首主题：2026-06-06 financial export CSV endpoints。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-fold-g4-single-caller-packages-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Fold G4 Single-Caller Packages Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-fold-thin-helper-packages-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Fold Thin Helper Packages Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-moderation-bulk-import-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Moderation Bulk Import Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-moderation-external-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Moderation External Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-notif-broadcast-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Admin Broadcast Notifications + User Inbox。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-public-rankings-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Public Rankings Endpoint。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-rerank-endpoint-codex.md` | 2026-06-06 | 文首主题：2026-06-06 rerank endpoint slice-1。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-riskctl-admin-codex.md` | 2026-06-06 | 文首主题：2026-06-06 riskctl admin visibility and unban。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-subscription-admin-ops-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Subscription Admin Ops Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-subscription-change-plan-codex.md` | 2026-06-06 | 文首主题：2026-06-06 subscription-change-plan-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-subscription-progress-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Subscription Progress Endpoint Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-usage-apikey-dim-codex.md` | 2026-06-06 | 文首主题：2026-06-06 usage api-key dimension。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-user-profile-update-codex.md` | 2026-06-06 | 文首主题：2026-06-06 User Profile Update。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-user-usage-export-codex.md` | 2026-06-06 | 文首主题：2026-06-06 USER Self-Service Usage CSV Export。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-a-keyexpiry-codex.md` | 2026-06-07 | 文首主题：2026-06-07 A key expiry sweep worker。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-auth-037-email-send-ip-limit-codex.md` | 2026-06-07 | 文首主题：2026-06-07 AUTH-037 Email Send Per-IP Limit Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-auth-124-admin-unlock-codex.md` | 2026-06-07 | 文首主题：2026-06-07 AUTH-124 Admin Unlock Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-codex-responses-ingress-codex.md` | 2026-06-07 | 文首主题：2026-06-07 Codex Responses Ingress。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-module-a-auth-067-068-codex.md` | 2026-06-07 | 文首主题：Module A AUTH-067/068 Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-module-a-auth-156-157-codex.md` | 2026-06-07 | 文首主题：2026-06-07 module A AUTH-156 AUTH-157 Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-module-a-auth-email-policy-codex.md` | 2026-06-07 | 文首主题：2026-06-07 module-a auth email policy。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-module-a-policy-adapter-wiring-codex.md` | 2026-06-07 | 文首主题：2026-06-07 module-a policy adapter wiring。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-user-auditlog-codex.md` | 2026-06-07 | 文首主题：2026-06-07 user-auditlog-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-15-mvp-closure-plan.md` | 2026-06-15 | 文首主题：HUAKAI MVP 闭环计划（树状图）— 2026-06-15。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-slice-account-proxy-binding.md` | 2026-06-17 | 文首主题：切片计划草案:账号↔代理绑定写路径(#2+#4)— 2026-06-17。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-apikey-expiry-update.md` | 2026-06-18 | 文首主题：Plan — 用户自助 API-key expiry 更新写路径 (inert-gap 切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-capability-binding-upsert.md` | 2026-06-18 | 文首主题：Plan — 模型能力绑定 upsert 写端点 (inert-gap 切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-notify-extra-emails.md` | 2026-06-18 | 文首主题：Plan — 通知设置 extraemails 双向接线 (inert-gap 切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-system-health-runtime-snapshot.md` | 2026-06-18 | 文首主题：Plan — 系统健康端点补 runtime 资源快照 (F-GW-003 Phase 1: 测量半)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-tenant-inherit-global-catalog.md` | 2026-06-18 | 文首主题：Plan — 租户全局目录继承(inheritglobalcatalog)写端点 (inert-gap 切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-activate-l2-cache.md` | 2026-06-19 | 文首主题：Plan — 激活 L2 响应缓存(F-CACHE-001 默认翻转 ON)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-ftree-closure-wave1.md` | 2026-06-19 | 文首主题：功能树后端闭环 —— 第 1 波(2026-06-19)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-usage-overview-counts.md` | 2026-06-19 | 文首主题：Plan — /usage/overview 补 raw successcount + errorcount (生态 parity 完整性切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-sm05-ambiguous-usage-undercharge.md` | 2026-06-20 | 文首主题：SM-05 修复:歧义用量(AmbiguousUsage)已交付内容永久漏收。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-orphan-reconcile.md` | 2026-06-24 | 文首主题：孤儿对账闭环(mediataskorphans)实施计划 — 2026-06-24。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-r7-identity-activation.md` | 2026-06-24 | 文首主题：R7 身份改写激活闭环 计划（2026-06-24）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-r7-identity-rewrite-wiring.md` | 2026-06-24 | 文首主题：R7 身份改写【接线】切片计划（默认关）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-backup-readonly-manifest.md` | 2026-06-26 | 文首主题：备份只读 manifest(rank6)— 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-proxy-probe-through.md` | 2026-06-26 | 文首主题：代理质检 probe-through(rank3)— 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-risk-overview-readonly.md` | 2026-06-26 | 文首主题：风控只读总览页(rank2)— 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-29-device-confirmation-flow-claude.md` | 2026-06-29 | 文首主题：新设备确认完整流 (default-dormant) — 实施计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-03-audit-remediation-claude.md` | 2026-07-03 | 文首主题：颗粒度模块配合缺陷 · 修复计划(2026-07-03)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-audit-remediation-batch-a-codex.md` | 2026-07-05 | 文首主题：2026-07-05 audit-remediation-batch-a-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-batch-b-codex.md` | 2026-07-05 | 文首主题：2026-07-05 batch-b-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-c2-manual-first-reprice-codex.md` | 2026-07-05 | 文首主题：2026-07-05 C-2 Manual-First 补价 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-codebudget-six-overlimit-split-codex.md` | 2026-07-05 | 文首主题：2026-07-05 codebudget 历史超标六项拆分 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-deadcode-cleanup-codex.md` | 2026-07-05 | 文首主题：2026-07-05 deadcode cleanup。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-four-domain-s3-tail-fix-codex.md` | 2026-07-05 | 文首主题：2026-07-05 四域审计 S3 尾批 fix-in-place。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-legacy-completions-c4-c5-c6-codex.md` | 2026-07-05 | 文首主题：2026-07-05 legacy completions C-4/C-5/C-6 对齐计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-pool-failover-s2-codex.md` | 2026-07-05 | 文首主题：2026-07-05 pool-failover S2 配合修 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-queue-wait-deadcode-routing-policy-cache.md` | 2026-07-05 | 文首主题：2026-07-05 queuewait deadcode 与 routing policy 缓存收尾。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-queue-wait-executor-claude.md` | 2026-07-05 | 文首主题：queuewait 排队执行层补建——Claude 草案(2026-07-05)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-queue-wait-executor-codex.md` | 2026-07-05 | 文首主题：2026-07-05 queuewait 排队执行层实现计划（Codex 独立轨）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-reprice-logical-clear-codex.md` | 2026-07-05 | 文首主题：2026-07-05 补价逻辑清除收尾 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-codex-account-responses-live-slice1-codex.md` | 2026-07-06 | 文首主题：2026-07-06 codex 账号 Responses 直通片1 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-codex-full-translation-layer-claude.md` | 2026-07-06 | 文首主题：codex 账号全量翻译层接通 — Claude 实现计划(2026-07-06,Owner 已拍板全量翻译层)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-codex-cli-global-hardening-codex.md` | 2026-07-07 | 文首主题：片2f 弧:codex-cli 全局加固层 — Codex 独立计划(specifier lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-per-account-codex-cli-only-claude.md` | 2026-07-07 | 文首主题：片2e:每账号 codex-cli-only 收紧开关 — Claude 计划草案。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-per-account-codex-cli-only-codex.md` | 2026-07-07 | 文首主题：片2e:每账号 codex-cli-only 收紧开关 — Codex 独立计划(specifier lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-response-format-text-format-codex.md` | 2026-07-07 | 文首主题：2026-07-07 responseformat 到 Responses text.format 转换修复 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-account-import-conversion-reference.md` | 2026-07-10 | 文首主题：账号→API:导入格式 × 转换方式 跨系统参考(sub2api + CLIProxyAPI)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-admin-user-usage-codex.md` | 2026-07-10 | 文首主题：2026-07-10 管理端按用户下钻明细用量（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-10-admin-user-usage.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-10-admin-user-usage.md` | 2026-07-10 | 文首主题：2026-07-10 管理端按用户下钻明细用量（合成执行计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-10-final-remediation-roadmap-claude.md` | 2026-07-10 | 文首主题：最终修复+建设路线图 —— Claude 平行草案(2026-07-10)。 | `SUPERSEDED` | `docs/process/plans/2026-07-10-final-remediation-roadmap.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-10-final-remediation-roadmap-codex.md` | 2026-07-10 | 文首主题：2026-07-10 HUAKAI 最终修复 + 建设路线图：Codex 独立平行计划。 | `SUPERSEDED` | `docs/process/plans/2026-07-10-final-remediation-roadmap.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-10-final-remediation-roadmap.md` | 2026-07-10 | 文首主题：2026-07-10 HUAKAI 最终修复+建设路线图（合成终版·无后缀）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-10-r0-serving-capability-closure-codex.md` | 2026-07-10 | 文首主题：2026-07-10 R0 薄能力闭合闸（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-B0-adversarial-A-fixes-codex.md` | 2026-07-11 | 文首主题：2026-07-11 B0 对抗审 A 类缺陷修复（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-f1-custom-endpoint-a1-downgrade-claude.md` | 2026-07-11 | 文首主题：2026-07-11 F1 允许 apikey 自定义上游地址 + A1 五厂降级 — 实施计划(Claude spec)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-backend-b2-b4-codex.md` | 2026-07-12 | 文首主题：2026-07-12 后端切片 B2+B4 独立 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-batch5-pages-spec-claude.md` | 2026-07-13 | 文首主题：第五批全站剩余页密度重构 Spec(38 页 · 11 个 codex 分派批)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-operator-overview-1to1-spec-claude.md` | 2026-07-13 | 文首主题：Operator 总览大屏 1:1 复刻 — 部件级 Spec。 | `SUPERSEDED` | `docs/process/plans/2026-07-13-operator-overview-1to1-spec.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-13-operator-overview-1to1-spec-codex.md` | 2026-07-13 | 文首主题：2026-07-13 运营总览页部件级重构（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-13-operator-overview-1to1-spec.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-13-operator-overview-1to1-spec.md` | 2026-07-13 | 文首主题：2026-07-13 运营总览页部件级重构（综合执行计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-14-admin-console-ia-codex.md` | 2026-07-14 | 文首主题：2026-07-14 运营管理台信息架构与用户台设计（Codex 独立稿）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p0b-proxy-group-chain-claude.md` | 2026-07-14 | 文首主题：2026-07-14 P0-b 代理组全链修复(Claude 独立草案 + 交叉综合)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p0b-proxy-group-chain-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P0-b 代理组全链修复（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p1a-four-switch-wiring-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P1-a 四个“配了没用”开关真接线（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p2a-account-advanced-config-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P2-a 账号高级配置通用化（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-14-p2a-account-advanced-config.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-14-p2a-account-advanced-config-independent-2.md` | 2026-07-14 | 文首主题：2026-07-14 P2-a 账号高级配置通用化通电（独立计划 2）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p2a-account-advanced-config.md` | 2026-07-14 | 文首主题：2026-07-14 P2-a 账号高级配置通用化（合成执行计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/parallel-window-prompts-2026-05-18.md` | 2026-05-18 | 文首主题：HUAKAI 多窗口并行开场 prompt (2026-05-18)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/README.md` | 2026-05-19 | 文首主题：Specs。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/_TEMPLATE.md` | 2026-04-28 | 文首主题：<Feature ID>: <Short Behavior Title>。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/_invariants/cross-module-boundaries.md` | 2026-05-31 | 文首主题：Cross-Module Boundaries — HUAKAI Architecture Invariants。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无覆盖全项目的单一 Markdown SSOT；规则导航暂以 `docs/RULES.md` 为入口，项目现状仍待代码核实。
- 建议删除：32 份；建议保留：296 份。
- 需真读代码裁定：235 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.2 relay-gateway 转发链

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/runtime-logic/relay-forwarding.md` | 2026-07-02 | 文首主题：Relay 转发链 运行逻辑 / 模块间配合。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-litellm-cooldown-retry-r3.md` | 2026-04-29 | 文首主题：2026-04-29 litellm cooldown retry R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-portkey-streaming-handler-r3.md` | 2026-04-29 | 文首主题：2026-04-29 portkey streaming handler R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-m1-responses-streaming-ledger-codex.md` | 2026-05-14 | 文首主题：2026-05-14 M1 responses streaming ledger。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-pol-1-upstream-policy-monitor-codex.md` | 2026-05-16 | 文首主题：2026-05-16 POL-1 upstream policy monitor。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-streaming-idempotent-replay-codex.md` | 2026-05-20 | 文首主题：2026-05-20 流式幂等重放补齐方案（Codex 独立草案）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-audit-w1-phase1-retry-failover-codex.md` | 2026-05-21 | 文首主题：2026-05-21 audit-w1-phase1-retry-failover Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-phase1-design-claude.md` | 2026-05-21 | 文首主题：Phase 1 详细实施设计 — 洞 ②⑥ 单请求内重试 / failover / 跨池(Claude 独立稿)。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-phase1-design-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-pr5-retry-failover-codex-execution.md` | 2026-05-21 | 文首主题：2026-05-21 PR5 Retry Failover Codex Execution Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1-017-buffered-sse-codex.md` | 2026-05-28 | 文首主题：2026-05-28 S1-017 Buffered SSE Fallback Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-30-routeadmin-crud-claude.md` | 2026-05-30 | 文首主题：routes admin CRUD — 补全 S1b 可运维性(Claude 自选板块)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-30-s1b-group-routing-claude.md` | 2026-05-30 | 文首主题：S1b 分组路由激活 — Claude 独立草案。 | `SUPERSEDED` | `docs/process/plans/2026-05-30-s1b-group-routing-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-30-s1b-group-routing-codex.md` | 2026-05-30 | 文首主题：S1b 分组路由激活 — Codex 独立草案。 | `SUPERSEDED` | `docs/process/plans/2026-05-30-s1b-group-routing-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-30-s1b-group-routing-synthesis.md` | 2026-05-30 | 文首主题：S1b 分组路由激活 — 双草交叉综合 + 终稿(R-SUB-WIRE-1 第二阶段)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-03-gap-route-wiring-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Gap Route Wiring - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-openrouter-small-features-codex.md` | 2026-06-03 | 文首主题：2026-06-03 openrouter-small-features-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-tenant-retry-budget-codex.md` | 2026-06-03 | 文首主题：2026-06-03 tenant retry budget - Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-model-fallback-codex.md` | 2026-06-04 | 文首主题：2026-06-04 model-fallback-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-cred-f1-revoked-legacy-fallback-codex.md` | 2026-06-06 | 文首主题：2026-06-06 CRED-F1 revoked legacy fallback guard - Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-model-fallback-validate-codex.md` | 2026-06-06 | 文首主题：2026-06-06 model fallback validate。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-module-b-inbound-routes-codex.md` | 2026-06-07 | 文首主题：2026-06-07 module-b-inbound-routes-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-14-route-121-active-rpm-tpm-precheck-claude.md` | 2026-06-14 | 文首主题：2026-06-14 ROUTE-121 主动 RPM/TPM 预算滑窗预检 (claude)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-routes-enable-disable.md` | 2026-06-18 | 文首主题：Plan — routes.enabled 启用/停用 admin 写路径 (inert-gap 切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-routing-wizard.md` | 2026-06-18 | 文首主题：Plan — 路由可视化向导（数据层 + 后端能力补强）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-meusage-stream-fields.md` | 2026-06-19 | 文首主题：Plan — self-service usage record: stream / streamterminatedreason / requested…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-capacity-retry-after.md` | 2026-06-20 | 文首主题：容量耗尽时精确 Retry-After(用池最早恢复时刻替硬编码 5 秒)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-23-relay-capacity-limits.md` | 2026-06-23 | 文首主题：计划:relay 数据面容量上限做成可配 + 抬默认(消除付费用户 413/砍流)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-routing-weighting-activation.md` | 2026-06-24 | 文首主题：路由加权激活闭环(routing weighting activation)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-binding-fallback-class-claude.md` | 2026-07-14 | 文首主题：绑定级 fallbackclass 激活 · Claude 独立计划(双计划我方稿)。 | `SUPERSEDED` | `docs/process/plans/2026-07-14-binding-fallback-class.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-14-binding-fallback-class-codex.md` | 2026-07-14 | 文首主题：2026-07-14 绑定级 fallbackclass 降级类别真生效（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-14-binding-fallback-class.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-14-binding-fallback-class.md` | 2026-07-14 | 文首主题：绑定级 fallbackclass 激活 · 综合裁定(双计划交叉讨论结论)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/specs/streaming-forwarder.md` | 2026-06-11 | 文首主题：F-GW-002: Streaming Forwarder + Usage Accounting。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；待后续逐链路读实现代码后建立领域 SSOT。
- 建议删除：5 份；建议保留：27 份。
- 需真读代码裁定：25 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.3 protocol-openapi-models 协议 / 契约 / 模型

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/13_API_CONTRACTS.md` | 2026-05-19 | 文首主题：API Contracts。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/openapi/SYNTHESIS.md` | 2026-04-29 | 文首主题：OpenAPI Synthesis — 3 Independent Drafts → 1 Unified Contract。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/.codex-prompt-n5-slice2-v2.md` | 2026-05-19 | 文首主题：Codex Round-2 Plan Refinement — N+5 Slice 2 (Model Registry)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/.codex-prompt-n5-slice2.md` | 2026-05-19 | 文首主题：Codex Independent Plan Draft — N+5 Slice 2 (Model Registry)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-30-n5-model-registry-claude-v2.md` | 2026-04-30 | 文首主题：N+5 Slice 2 — Model Registry — Claude Round-2 Plan。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5-model-registry.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5-model-registry-claude.md` | 2026-04-30 | 文首主题：N+5 Slice 2 — Model Registry (Claude independent draft)。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5-model-registry.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5-model-registry-codex-v2.md` | 2026-04-30 | 文首主题：2026-04-30 N+5 Slice 2 Model Registry - Codex Round 2 Reference-Pattern Plan。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5-model-registry.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5-model-registry-codex.md` | 2026-04-30 | 文首主题：2026-04-30 N+5 Slice 2 Model Registry - Codex Independent Plan。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n5-model-registry.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-n5-model-registry.md` | 2026-04-30 | 文首主题：N+5 Slice 2 — Model Registry — Authoritative Synthesized Plan。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md` | 2026-05-09 | 文首主题：HCSF Canonical 选型综合 — Claude × Codex × Issue Mining 三 lane。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-09-hcsf-v04-implementation-claude.md` | 2026-05-09 | 文首主题：HCSF v0.4 Phased Delivery Plan — Claude (sonnet) Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-hcsf-v04-implementation-codex.md` | 2026-05-09 | 文首主题：HCSF v0.4 Phased Delivery Plan — Codex Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md` | 2026-05-09 | 文首主题：HCSF v0.4 Phased Delivery — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-09-p0c-followup-plan-claude.md` | 2026-05-09 | 文首主题：HCSF v0.4 P-0c Follow-up Plan — Claude (sonnet) Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-p0c-followup-plan-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-p0c-followup-plan-codex.md` | 2026-05-09 | 文首主题：HCSF v0.4 P-0c Follow-up Plan — Codex Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-p0c-followup-plan-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-28-S1-025-protocol-loss-audit-fix-claude.md` | 2026-05-28 | 文首主题：2026-05-28 S1-025 Protocol-loss evidence持久化修复（Claude 计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-S1-025-protocol-loss-audit-fix-codex.md` | 2026-05-28 | 文首主题：2026-05-28 S1-025 Protocol-loss evidence持久化修复（Codex 计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-s1025-protocol-loss-fu-claude.md` | 2026-05-29 | 文首主题：S1-025 follow-up — 4×P2 protocol-loss completeness — Claude plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-s1025-protocol-loss-fu-codex.md` | 2026-05-29 | 文首主题：2026-05-29 S1025 Protocol-Loss FU Codex Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-s1025-protocol-loss-fu-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-s1025-protocol-loss-fu-synthesis.md` | 2026-05-29 | 文首主题：S1-025 follow-up — synthesis of Claude + Codex parallel plans (CLAUDE.md #10)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-01-s2-016-realtime-runtime-guard-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-016 Realtime Runtime Guard。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-protocol-completions-counttokens-codex.md` | 2026-06-06 | 文首主题：2026-06-06 protocol completions count-tokens Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-openapicheck-security-dimension.md` | 2026-06-19 | 文首主题：openapicheck 补 security 维度校验(防 IDOR 类契约漂移复发)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-hcsf-default-flip-forensics.md` | 2026-06-26 | 文首主题：HCSF 默认翻转取证(rank4)— 证据 + 给 Owner 的决策。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-models-protocol-family-check-hunyuan-e2e-codex.md` | 2026-07-05 | 文首主题：2026-07-05 models protocolfamily CHECK 与混元 E2E 修复。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-model-registry-admin-crud-claude.md` | 2026-07-15 | 文首主题：模型主体(model registry)Admin CRUD 补口 · C③ · Claude 规划稿。 | `SUPERSEDED` | `docs/process/plans/2026-07-15-model-registry-admin-crud.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-15-model-registry-admin-crud-codex.md` | 2026-07-15 | 文首主题：2026-07-15 模型主体运维 CRUD（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-15-model-registry-admin-crud.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-15-model-registry-admin-crud.md` | 2026-07-15 | 文首主题：2026-07-15 模型主体运维 CRUD 综合执行计划。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/reviews/2026-04-28-codex-protocol-translation-synthesis-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - Protocol Translation Synthesis。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-04-28-codex-protocol-translation-synthesis-v2-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-PROTO-002 Protocol Translation Synthesis…。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-04-29-codex-openapi-final-review.md` | 2026-04-29 | 文首主题：HUAKAI Phase 2.2 OpenAPI Final Review。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/DEFERRED-S1-025-protocol-loss.md` | 2026-05-28 | 文首主题：DEFERRED — S1-025 protocol-loss evidence completeness (follow-up slice)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/api-contract.md` | 2026-04-29 | 文首主题：Phase 2.2: API Contract — Released。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/model-substitution.md` | 2026-05-19 | 文首主题：F-MODEL-SUBSTITUTION-001: Model Substitution Engine (A29)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无单一 Markdown SSOT；契约真源仍是非 Markdown 的 `docs/openapi/openapi.yaml`，本领域散文档待核实归并。
- 建议删除：14 份；建议保留：20 份。
- 需真读代码裁定：13 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.4 billing-pricing-payment 计费 / 定价 / 支付

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/runtime-logic/settlement-intent-reconciliation.md` | 2026-07-11 | 文首主题：settlementintents 持久结算意图 运行逻辑。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/billing-quota.md` | 2026-06-23 | 文首主题：Billing & Quota Feature Tree — HUAKAI Audit。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/model-catalog-pricing.md` | 2026-06-03 | 文首主题：Feature Tree: model-catalog-pricing。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/payment-monetization.md` | 2026-06-23 | 文首主题：Feature-Tree Audit: payment-monetization。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/tiered-billing.md` | 2026-06-03 | 文首主题：Gap Critique: Tiered/Expression Billing DSL + Funding-Source Switch。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/content-moderation.md` | 2026-06-03 | 文首主题：Gap Design: Content Moderation + Violation-Fee Billing。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/pricing-catalog.md` | 2026-06-03 | 文首主题：Gap design: Pricing catalog — per-group ratios + upstream preset sync。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/tiered-billing.md` | 2026-06-03 | 文首主题：Gap Design: Tiered/Expression Billing DSL + Funding-Source Switch。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/pricing-catalog.md` | 2026-06-03 | 文首主题：Gap spec: pricing-catalog (residual-verified)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/tiered-billing.md` | 2026-06-03 | 文首主题：Gap Spec: Tiered/Expression Billing DSL + Funding-Source Switch。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-new-api-cache-billing-reasoning-r3.md` | 2026-04-29 | 文首主题：2026-04-29 new-api cache billing reasoning R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-phase-b5-settler.md` | 2026-04-29 | 文首主题：Phase B.5 — Real Tx2 Settler against PostgreSQL。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-003-billing-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-OBS-003 4-state failed-stream billing (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-003-billing-codex.md` | 2026-05-15 | 文首主题：=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05CLEANROOMPOLICY) ===。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-bill-002-voucher-system-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-BILL-002 Voucher System Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-bill-002-voucher-system-implementation-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-BILL-002 Voucher System Implementation Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-p1-wave2-plan-claude.md` | 2026-05-17 | 文首主题：P1 Wave 2 Plan (Pools fields + Voucher GetBatch + Channel Health list)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-billing-refund-atomic-codex.md` | 2026-05-18 | 文首主题：2026-05-18 billing refund atomic transaction codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-pricing-public-scope-v2-codex.md` | 2026-05-18 | 文首主题：2026-05-18 pricing public scope v2。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-audit-billing-med-codex.md` | 2026-05-19 | 文首主题：2026-05-19 audit billing MED codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-admin-audit-billing-setting-check-codex.md` | 2026-05-20 | 文首主题：2026-05-20 admin audit billing setting check。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-billing-settings-audit-atomic-p1-codex.md` | 2026-05-20 | 文首主题：2026-05-20 billing settings audit atomic P1。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-case-c-billing-setting-claude.md` | 2026-05-20 | 文首主题：Case-C 计费策略可配置面板设置 — Claude 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-case-c-billing-setting-codex.md` | 2026-05-20 | 文首主题：2026-05-20 Case C Billing Setting Plan - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-case-c-billing-setting-p2-fixes-codex.md` | 2026-05-20 | 文首主题：2026-05-20 Case C Billing Setting P2 Fixes - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-case-c-billing-settings-admin-api-codex.md` | 2026-05-20 | 文首主题：2026-05-20 case-c-billing-settings-admin-api-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-l2-cache-hit-usage-settlement-claude.md` | 2026-05-20 | 文首主题：2026-05-20 l2-cache-hit-usage-settlement-claude。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-l2-cache-hit-usage-settlement-codex.md` | 2026-05-20 | 文首主题：2026-05-20 l2-cache-hit-usage-settlement-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-stream-billing-settlement-codex.md` | 2026-05-20 | 文首主题：2026-05-20 stream billing settlement。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-pr4-billing-claim-retry-atomicity-codex.md` | 2026-05-21 | 文首主题：2026-05-21 PR4 billing/claim retry atomicity。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-w4c-settle-bypass-claude.md` | 2026-05-23 | 文首主题：W4c 计划（Claude lane）—— Settle 旁路堵塞。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-w4c-settle-bypass-codex.md` | 2026-05-23 | 文首主题：2026-05-23 W4c settle 旁路堵塞 / B-12 Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-23-w4c-settle-bypass-synthesis.md` | 2026-05-23 | 文首主题：2026-05-23 W4c Settle 旁路堵塞综合计划。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-24-post-delivery-settle-recovery-claude.md` | 2026-05-24 | 文首主题：2026-05-24 P2/P3 流式 post-delivery settle 失败 durable 兜底 — Claude lane plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-post-delivery-settle-recovery-codex.md` | 2026-05-24 | 文首主题：2026-05-24 post-delivery-settle-recovery-codex (落档 Claude 代写)。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md` | 2026-05-24 | 文首主题：2026-05-24 P2/P3 流式 post-delivery settle 失败 durable 兜底 — Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-28-quota-b2b-settle-claude.md` | 2026-05-28 | 文首主题：配额 B2b settle/release/cache-hit 实施计划 — Claude 独立稿 (2026-05-28)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-b2b-settle-codex.md` | 2026-05-28 | 文首主题：2026-05-28 HUAKAI 配额子系统切片 B2b settle/release/cache-hit 实施计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1-015-cache-tier-pricing-codex.md` | 2026-05-28 | 文首主题：2026-05-28 S1-015 cache-tier pricing Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1-034-request-id-money-path-codex.md` | 2026-05-28 | 文首主题：2026-05-28 S1-034 Request ID Money Path Fix - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1029-p1-provisional-overcharge-claude.md` | 2026-05-28 | 文首主题：S1-029 Round-2 [P1] — chunk-count provisional pricing — Claude independent po…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-waveb-B-billing-codex.md` | 2026-05-28 | 文首主题：2026-05-28 waveb B — Billing fix plan (S1-015 + S1-029)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-waveb-billing-evidence-claude.md` | 2026-05-28 | 文首主题：2026-05-28 Wave B (S1-025 / S2-163 / S1-015 / S1-029) — Claude independent dr…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-money-path-worker-claude.md` | 2026-05-29 | 文首主题：money-path worker slice — 计划（Claude，独立草案）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-money-path-worker-codex.md` | 2026-05-29 | 文首主题：Money-Path Worker Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-payment-p1-claude.md` | 2026-05-29 | 文首主题：支付子系统 Slice P1 实施计划 — Claude 独立稿 (2026-05-29)。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p1-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p1-codex.md` | 2026-05-29 | 文首主题：2026-05-29 HUAKAI 支付子系统切片 P1 Codex 独立实施计划。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p1-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p1-synthesis.md` | 2026-05-29 | 文首主题：支付子系统 Slice P1 — 平行计划交叉综合 (2026-05-29)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-29-payment-p2a-claude.md` | 2026-05-29 | 文首主题：支付子系统 Slice P2a 实施计划 — Claude 独立稿 (2026-05-29)。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p2a-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p2a-codex.md` | 2026-05-29 | 文首主题：HUAKAI 支付子系统 P2a 自动入账回调独立实施计划。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p2a-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p2a-synthesis.md` | 2026-05-29 | 文首主题：支付 P2a 综合稿 (Claude ∥ Codex 交叉 + Owner 决策) — 2026-05-29。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-29-payment-p3-claude.md` | 2026-05-29 | 文首主题：支付子系统 Slice P3「订阅」实施计划 — Claude 独立稿 (2026-05-29)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-payment-p3-codex.md` | 2026-05-29 | 文首主题：2026-05-29 Payment P3 Subscription Implementation Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p3-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p3-synthesis.md` | 2026-05-29 | 文首主题：支付 P3「订阅」综合稿 (Claude ∥ Codex 交叉 + Owner 决策) — 2026-05-29。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-29-payment-p3a-impl-claude.md` | 2026-05-29 | 文首主题：P3a 订阅子系统 实现计划 (refined) — Claude — 2026-05-29。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-payment-p3b-claude.md` | 2026-05-29 | 文首主题：支付 P3b 实现计划 — Claude 独立稿 — 2026-05-29。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p3b-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p3b-codex.md` | 2026-05-29 | 文首主题：=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05CLEANROOMPOLICY) ===。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-payment-p3b-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-payment-p3b-synthesis.md` | 2026-05-29 | 文首主题：支付 P3b 综合定稿 — Claude∥Codex 平行交叉 — 2026-05-29。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-29-payment-p3b3-voucher-claude.md` | 2026-05-29 | 文首主题：P3b-3 兑换码购订阅 — Claude 实现计划 — 2026-05-29。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-payment-p3b4-order-claude.md` | 2026-05-29 | 文首主题：P3b-4 订单购订阅 — Claude 实现计划 — 2026-05-29。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-s1015-cache-stream-fu-claude.md` | 2026-05-29 | 文首主题：S1-015-fu — 纯缓存流结算闸门 + lease-sweep 测试隔离。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-money-1-voucher-balance-bridge-codex.md` | 2026-05-31 | 文首主题：2026-05-31 MONEY-1 voucher balance bridge。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-31-money-3-recharge-orders-codex.md` | 2026-05-31 | 文首主题：2026-05-31 MONEY-3 Recharge Orders。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-money-4-payment-callback-codex.md` | 2026-06-01 | 文首主题：MONEY-4 Payment Callback Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-payment-loop-slice-a-codex.md` | 2026-06-02 | 文首主题：2026-06-02 payment-loop-slice-a-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-billingdsl-settle-codex.md` | 2026-06-03 | 文首主题：2026-06-03 billingdsl settle codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-models-pricing-codex.md` | 2026-06-03 | 文首主题：2026-06-03 models pricing discovery codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-pricing-catalog-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 pricing-catalog slice1 Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-tiered-billing-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 tiered-billing slice1 Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-group-ratio-billing-codex.md` | 2026-06-04 | 文首主题：2026-06-04 group ratio billing。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-pricing-ratio-signed-audit-codex.md` | 2026-06-04 | 文首主题：2026-06-04 pricing-ratio signed audit Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-admin-payment-panel-codex.md` | 2026-06-05 | 文首主题：2026-06-05 admin-payment-panel-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-cache-price-override-live-codex.md` | 2026-06-05 | 文首主题：2026-06-05 cache price override live billing plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-payment-expire-sweeper-codex.md` | 2026-06-05 | 文首主题：2026-06-05 payment expire sweeper。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-payment-provider-psp-reconcile-contract-codex.md` | 2026-06-06 | 文首主题：2026-06-06 payment provider PSP reconcile contract。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-pricing-public-page-codex.md` | 2026-06-06 | 文首主题：2026-06-06 pricing public page endpoint。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-completions-settle-recovery.md` | 2026-06-17 | 文首主题：Plan：/v1/completions 交付后结算恢复（S1-2 + S1-3 修复）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-completions-stream-settle-money-fix.md` | 2026-06-19 | 文首主题：completionshttp 流式交付后全额退款修复(wave-2 / 审计 wy94u3tn9 两个 S1)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-pricingpublic-catalog-fields.md` | 2026-06-19 | 文首主题：Plan — public pricing page catalog-metadata parity (ownedby / mode / maxoutpu…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-anthropic-ping-keepalive.md` | 2026-06-20 | 文首主题：修复:Anthropic 上游 ping 保活帧被判未知事件 → 整流截断并计费(S1)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-claim-lease-window-vs-request-lifecycle.md` | 2026-06-20 | 文首主题：修复:claim 租约 90s 远短于请求生命周期(600s)→ LeaseSweeper 腰斩活流/慢settle 致亏钱+超并发(S1)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-payment-config-non-secret.md` | 2026-06-20 | 文首主题：paymentproviderconfig 改判为非密钥配置(Owner 拍板"放开")。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-tool-surcharge-leak-fix.md` | 2026-06-24 | 文首主题：工具附加费止漏装配(NAPI-BILLING-01 Stage A 收尾)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-29-subscription-auto-renewal-worker-claude.md` | 2026-06-29 | 文首主题：订阅自动续费 worker(扫到期→扣钱包余额→续期)— 实现计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-02-billing-serializable-retry-claude.md` | 2026-07-02 | 文首主题：缺口③ billing 预扣 hold 并发 500——Serializable 重试配套(Layer 1)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-billing-settle-abort-serializable-retry-codex.md` | 2026-07-05 | 文首主题：2026-07-05 billing Settle/Abort Serializable 重试。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-B-class-durable-settlement-intent-claude.md` | 2026-07-11 | 文首主题：B 类根治:durable settlement intent(持久结算意图)— 设计 + Owner schema 决策 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-B-class-durable-settlement-intent-phase1-codex.md` | 2026-07-11 | 文首主题：2026-07-11 B 类持久结算意图阶段 1 Codex 执行计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-B-class-durable-settlement-intent-phase1-remediation-codex.md` | 2026-07-11 | 文首主题：2026-07-11 B 类持久结算意图阶段 1 对抗修复 Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-B-class-durable-settlement-intent-phase1-round2-remediation-codex.md` | 2026-07-11 | 文首主题：2026-07-11 B 类持久结算意图阶段 1 第 2 轮修复（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-b0-settlement-failure-claude.md` | 2026-07-11 | 文首主题：B0 结算失败四缺口修复 — 实施计划(Claude 独立车道)— 2026-07-11。 | `SUPERSEDED` | `docs/process/plans/2026-07-11-b0-settlement-failure.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-11-b0-settlement-failure-codex.md` | 2026-07-11 | 文首主题：2026-07-11 B0 交付后结算与未决恢复保护（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-11-b0-settlement-failure.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-11-b0-settlement-failure.md` | 2026-07-11 | 文首主题：2026-07-11 B0 交付后结算与未决恢复保护（合并计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-11-settlement-intent-sweeper-claude.md` | 2026-07-11 | 文首主题：settlementintents 对账 sweeper(B 类阶段 2)— 计划(Claude)— 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-f5-reprice-trust-frontend-codex.md` | 2026-07-12 | 文首主题：2026-07-12 F5 计费重算与信任验证中心前端计划（Codex）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-batch3-pages-spec-claude.md` | 2026-07-13 | 文首主题：第三批四页密度重构 Spec(/admin/subscriptions /vouchers /pricing /orders)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-codex-observability-synthesis-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-OBS-001 Observability + Atomic Billing S…。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-07-10-B0-settlement-failure-design.md` | 2026-07-10 | 文首主题：B0 结算失败四终局——设计与 Owner 决策点 — 2026-07-10。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-10-official-api-module-audit.md` | 2026-07-10 | 文首主题：官方 API 模块全链审计(采集→key 物化→网关转发→计费)— 2026-07-10。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-B-class-phase1-real-upstream-result.md` | 2026-07-11 | 文首主题：B 类阶段 1 settlementintents 真上游实测结果 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-B0-adversarial-review-verdict.md` | 2026-07-11 | 文首主题：B0 结算失败补偿——codex 对抗审裁定 + Claude 亲核 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-settlement-intent-sweeper-result.md` | 2026-07-11 | 文首主题：settlementintents 对账 sweeper(B 类阶段 2)亲检 + 真 PG 并发实测结果 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/DEFERRED-S1-015-cache-tier-pricing.md` | 2026-05-28 | 文首主题：DEFERRED — S1-015 cache-tier pricing (follow-up)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/runbooks/p2-p3-post-delivery-settle-recovery-runbook.md` | 2026-06-01 | 文首主题：P2/P3 Post-Delivery Settle Recovery — Runbook。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md` | 2026-04-29 | 文首主题：F-OBS-001 §Tx2 Invariant Checklist。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/observability-billing.md` | 2026-04-29 | 文首主题：F-OBS-001: Observability + Atomic Billing Settlement。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/voucher-system.md` | 2026-06-01 | 文首主题：F-BILL-002: Voucher System。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；钱路径断言须逐文件读代码、迁移和调用链后建立领域 SSOT。
- 建议删除：11 份；建议保留：96 份。
- 需真读代码裁定：88 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.5 quota-rate-concurrency 配额 / 限流 / 并发

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/process/gap-critiques/app-rate-limit.md` | 2026-06-03 | 文首主题：Gap Critique: Application-Level Per-User / Per-Group Rate Limiting (F-RL-APP-…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/app-rate-limit.md` | 2026-06-03 | 文首主题：Gap Design: Application-Level Per-User / Per-Group Rate Limiting。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/per-key-controls.md` | 2026-06-03 | 文首主题：Gap Design: Per-API-Key Quota Cap + Group + Batch Revoke + Secure Reveal。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade5-quota-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Upgrade #5 — 二阶段 quota with tiermaxmultiplier (claude lane plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade5-quota-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #5 — Two-Stage Quota With tiermaxmultiplier。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-b2a-reserve-codex.md` | 2026-05-28 | 文首主题：2026-05-28 Quota B2a Reserve Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-b2a-review-fixes-codex.md` | 2026-05-28 | 文首主题：2026-05-28 Quota B2a Review Fixes Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-b2b-review-fixes-codex.md` | 2026-05-28 | 文首主题：2026-05-28 quota B2b review fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-slice-a-s1-fixes-codex.md` | 2026-05-28 | 文首主题：2026-05-28 quota slice A S1 fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-subsystem-claude.md` | 2026-05-28 | 文首主题：配额子系统实施计划 — Claude 独立稿 (2026-05-28)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-subsystem-codex.md` | 2026-05-28 | 文首主题：2026-05-28 配额子系统实施计划（Codex independent draft）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-quota-subsystem-slice-b-codex.md` | 2026-05-28 | 文首主题：2026-05-28 Quota Subsystem Slice B Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-quota-reconciler-codex.md` | 2026-05-29 | 文首主题：2026-05-29 quota reconciler。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-commercial-quota-reconcile-codex.md` | 2026-06-02 | 文首主题：2026-06-02 commercial quota reconcile Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-quota-fail-open-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Quota Fail Open Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-quota-hotpath-enforce-codex.md` | 2026-06-03 | 文首主题：2026-06-03 Quota Hot Path Enforce Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-quota-policy-crud-frontend.md` | 2026-06-17 | 文首主题：Wave2 切片计划 — 配额策略 admin CRUD（前端接线）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-quota-multimetric-windows.md` | 2026-06-18 | 文首主题：Plan — 自助 /quota 多维窗口读 (F-OPS-001 parity L2→L3)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-18-quota-view-priority.md` | 2026-06-18 | 文首主题：Plan — 自助 per-key quota 视图补 priority 字段 (inert-gap read-surfacing)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-bridge-budget-failopen.md` | 2026-06-19 | 文首主题：Plan — 预算/限流 fail-open 计数器接入告警/指标 (observability tight slice)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-keyquota-window-end.md` | 2026-06-19 | 文首主题：Plan — per-key 配额视图补 windowend 重置边界 (生态 parity 完整性切片)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-quota-window-kind.md` | 2026-06-20 | 文首主题：配额拒绝透出窗口种类(quotawindow)— parity buildnow。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-23-quota-default-perkey-limits.md` | 2026-06-23 | 文首主题：1 修复:新建 API key 种保守默认配额(RPM+并发),堵"单 key 烧池子"。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-account-slot-e2e-concurrency-codex.md` | 2026-07-05 | 文首主题：2026-07-05 账号并发槽全链路端到端并发补测。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-per-model-ratelimit-cooldown-claude.md` | 2026-07-07 | 文首主题：片3a:429 限速冷却下沉到 per-model 格(账号×模型二维粒度)— Claude 计划草案。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-per-model-ratelimit-cooldown-codex.md` | 2026-07-07 | 文首主题：2026-07-07 429 限速冷却下沉到 per-model 格 Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-binding-concurrency-limit-claude.md` | 2026-07-14 | 文首主题：2026-07-14 绑定级 MaxParallelRequests 并发上限 · Claude 综合裁定。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-binding-concurrency-limit-codex.md` | 2026-07-14 | 文首主题：2026-07-14 绑定级并发硬上限独立计划（Codex）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-burst-hard-cap-calendar-month.md` | 2026-07-15 | 文首主题：burst 硬上限接 enforce + calendarmonth 白名单放开 · 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-concurrency-defects-fix-claude.md` | 2026-07-15 | 文首主题：三个既有重并发降级缺陷修复 · Claude 独立计划(双计划我方稿)。 | `SUPERSEDED` | `docs/process/plans/2026-07-15-concurrency-defects-fix.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-15-concurrency-defects-fix-codex.md` | 2026-07-15 | 文首主题：2026-07-15 重并发降级缺陷独立修复计划（Codex）。 | `SUPERSEDED` | `docs/process/plans/2026-07-15-concurrency-defects-fix.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-15-concurrency-defects-fix.md` | 2026-07-15 | 文首主题：三个既有重并发降级缺陷修复 · 综合裁定稿。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-15-increment-k-deferred-budget-slot-race-codex.md` | 2026-07-15 | 文首主题：2026-07-15 增量 K：Deferred 预算与槽终结并发。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/reviews/2026-04-28-codex-rate-limiting-synthesis-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-RATE-001 Rate Limiting + Cooldown Synthe…。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-07-15-concurrency-defects-increment-a.md` | 2026-07-15 | 文首主题：2026-07-15 并发缺陷修复增量 A 实施报告。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/rate-limiting.md` | 2026-05-06 | 文首主题：F-RATE-001: Upstream Rate-Limit Detection + Provider Account Cooldown。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；配额、限流与并发须逐 gate/存储/释放链路核实后建立领域 SSOT。
- 建议删除：3 份；建议保留：33 份。
- 需真读代码裁定：31 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.6 auth-session-rbac 登录 / 鉴权 / 会话

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/plans/2026-05-19-provider-session-reversal-codex.md` | 2026-05-19 | 文首主题：2026-05-19 provider session reversal assertions。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/REFRESH-2026-06-15.md` | 2026-06-15 | 文首主题：Feature-tree refresh — 2026-06-15 (session delta)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/totp-2fa.md` | 2026-06-03 | 文首主题：Adversarial Review: totp-2fa.md (F-AUTH-008 / F-AUTH-009)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/totp-2fa.md` | 2026-06-03 | 文首主题：Gap Design: TOTP 2FA / Passkey / Step-Up Gate。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-06-vendor-session-adapters-codex.md` | 2026-05-06 | 文首主题：2026-05-06 vendor session adapters codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-scheduler-algorithm-options-codex.md` | 2026-05-08 | 文首主题：HUAKAI 自有调度算法 — 候选与推荐 (codex lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-004-async-processor-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-OBS-004 async processor chain (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-004-async-processor-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-OBS-004 async processor chain Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-auth-007-f-session-001-implementation-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-AUTH-007 + F-SESSION-001 Implementation。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-auth-007-f-session-001-review-fix-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-AUTH-007 + F-SESSION-001 Review Fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-user-auth-session-spec-codex.md` | 2026-05-16 | 文首主题：2026-05-16 User Auth + Session Spec。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-f-comm-001-invitation-referral-spec-claude.md` | 2026-05-17 | 文首主题：F-COMM-001 邀请/推荐系统 Spec 草拟 — Claude 平行计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-f-comm-001-invitation-referral-spec-codex.md` | 2026-05-17 | 文首主题：2026-05-17 F-COMM-001 Invitation Referral Spec - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-provider-session-reversal-codex.md` | 2026-05-19 | 文首主题：2026-05-19 provider session reversal assertions。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-s1-remediation-claude.md` | 2026-05-21 | 文首主题：§1 用户与权限 收口计划(Claude 独立草案)。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-s1-remediation.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-s1-remediation-codex.md` | 2026-05-21 | 文首主题：2026-05-21 §1 用户与权限 remediation plan - Codex parallel draft。 | `SUPERSEDED` | `docs/process/plans/2026-05-21-s1-remediation.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-21-s1-remediation.md` | 2026-05-21 | 文首主题：2026-05-21 §1 用户与权限 remediation synthesized plan。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-24-placeholder-session-adapters-claude.md` | 2026-05-24 | 文首主题：Placeholder Session Adapter × 6 实落 + 默认启用 — Claude Lane Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Placeholder Session Adapters Codex Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-placeholder-session-adapters-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-placeholder-session-adapters-synthesis.md` | 2026-05-24 | 文首主题：6 Placeholder Session Adapter — Synthesis (Claude × Codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-30-role-panel-switch-claude.md` | 2026-05-30 | 文首主题：登录按角色自动切面板 — Claude 独立计划草稿。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-30-role-panel-switch-synthesis.md` | 2026-05-30 | 文首主题：登录按角色切面板 — 双草 synthesis(Claude × Codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-31-s2-048-login-throttle-refix-claude.md` | 2026-05-31 | 文首主题：S2-048 重修实现计划(Claude 独立稿)。 | `SUPERSEDED` | `docs/process/plans/2026-05-31-s2-048-login-throttle-refix-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-31-s2-048-login-throttle-refix-codex.md` | 2026-05-31 | 文首主题：S2-048 重修实现计划(Codex 独立草案)。 | `SUPERSEDED` | `docs/process/plans/2026-05-31-s2-048-login-throttle-refix-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-31-s2-048-login-throttle-refix-synthesis.md` | 2026-05-31 | 文首主题：S2-048 重修 — Claude×Codex 平行计划综合(执行版)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-31-s2-082-responses-sessionhash-codex.md` | 2026-05-31 | 文首主题：2026-05-31 S2-082 Responses SessionHash。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-011-session-refresh-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-011 Session Refresh。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-012-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-012 Registration Policy And Invitation Binding - Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-comm001-referral-qualify-codex.md` | 2026-06-03 | 文首主题：2026-06-03 COMM001 Referral Qualify Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-twofa-totp-codex.md` | 2026-06-03 | 文首主题：2026-06-03 twofa-totp-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-05-c6-referral-reward-codex.md` | 2026-06-05 | 文首主题：2026-06-05 C6 Referral Reward Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-passkey-webauthn-codex.md` | 2026-06-06 | 文首主题：2026-06-06 PASSKEY / WebAuthn passwordless login。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-referral-records-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Referral Records Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-referral-reward-issuance-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Referral Reward Issuance Slice 1。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-a-2fa-adoption-stats-codex.md` | 2026-06-07 | 文首主题：2026-06-07 A 2FA Adoption Stats Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-auth-182-validate-invite-codex.md` | 2026-06-07 | 文首主题：2026-06-07 AUTH-182 validate invitation code endpoint。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-auth-unlink-logout-codex.md` | 2026-06-07 | 文首主题：2026-06-07 AUTH-086 AUTH-140 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-audit-export-idor.md` | 2026-06-19 | 文首主题：audit 导出未认证跨租户泄露(IDOR)修复(wave-2 审计 wy94u3tn9 S0)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-twofa-totp-replay.md` | 2026-06-19 | 文首主题：twofa TOTP 防重放(wave-2 审计 wy94u3tn9 S1,auth-core + additive schema)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-23-soften-session-key-dev-autogen.md` | 2026-06-23 | 文首主题：计划:session 签名 key —— 非生产模式自动生成临时 key(Owner 拍:dev-only ephemeral)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-referral-deadcode-removal.md` | 2026-06-26 | 文首主题：referralreward 死代码裁决(community/invitation 平行实现)— 删除计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-30-telegram-login-wire-and-elevate.md` | 2026-06-30 | 文首主题：Telegram 登录:接线 + 升华(fusion-upgrade)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-01-role-based-auth-migration-claude.md` | 2026-07-01 | 文首主题：HUAKAI role 制单登录迁移计划(auth-core + schema)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-chatgpt-session-relay-e2e-claude.md` | 2026-07-05 | 文首主题：chatgpt/codex session 账号转 API 全链路 e2e + endpoint 可配 — Claude 切片计划(2026-07-05)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-fe-auth-refresh-wiring-codex.md` | 2026-07-05 | 文首主题：2026-07-05 前端鉴权刷新接线修复 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-chatgpt-codex-session-e2e-codex.md` | 2026-07-06 | 文首主题：2026-07-06 chatgpt/codex session 账号转 API e2e + endpoint 可配 Codex 执行计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-auth-event-source-metadata-codex.md` | 2026-07-10 | 文首主题：2026-07-10 登录审计事件来源 IP 与 User-Agent。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-email-template-override-claude.md` | 2026-07-12 | 文首主题：B6 鉴权邮件模板覆盖(零 schema + fail-safe)— Claude 计划 2026-07-12。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-hide-auth-tenant-input-codex.md` | 2026-07-13 | 文首主题：2026-07-13 隐藏登录相关页面租户输入（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-setup-wizard-role-nav-claude.md` | 2026-07-13 | 文首主题：/setup 首装向导 + 登录角色分流 + 单侧栏角色导航(sub2api 形态照抄)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/community-invitation-referral.md` | 2026-05-19 | 文首主题：F-COMM-001: Community Invitation And Referral System。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/session-management.md` | 2026-05-19 | 文首主题：F-SESSION-001: Platform Session Management。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/user-authentication.md` | 2026-06-03 | 文首主题：F-AUTH-007: User Authentication。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；登录、会话和 RBAC 须逐中间件、存储与路由链核实后建立领域 SSOT。
- 建议删除：5 份；建议保留：48 份。
- 需真读代码裁定：44 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.7 account-pool-dispatch 账号池 / 选号 / 调度

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/process/plans/2026-05-08-pasr-lite-v2-claude.md` | 2026-05-08 | 文首主题：2026-05-08 PASR-lite v2 — HUAKAI 自有调度算法 (Claude lane)。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-pasr-lite-v2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-pasr-lite-v2-codex.md` | 2026-05-08 | 文首主题：2026-05-08 PASR-lite v2 — codex-lane 独立设计。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-pasr-lite-v2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-pasr-lite-v2-synthesis.md` | 2026-05-08 | 文首主题：2026-05-08 PASR-lite v2 — Synthesis (claude × codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-pasr-mainwire-claude.md` | 2026-05-08 | 文首主题：2026-05-08 PASR-lite Main-Wire — Claude lane 独立设计。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-pasr-mainwire-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-pasr-mainwire-codex.md` | 2026-05-08 | 文首主题：2026-05-08 PASR-lite main.go 集成计划（Codex lane）。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-pasr-mainwire-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-pasr-mainwire-synthesis.md` | 2026-05-08 | 文首主题：2026-05-09 PASR-lite Main-Wire — Synthesis (claude × codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-scheduler-algorithm-options-claude.md` | 2026-05-08 | 文首主题：2026-05-08 HUAKAI 自有调度算法 — 4 选项 (Claude lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-09-pasr-cache-aware-claude.md` | 2026-05-09 | 文首主题：2026-05-09 PASR-lite cache-aware 调整 (Claude lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-09-pasr-shadow-realtraffic-sop.md` | 2026-05-09 | 文首主题：PASR-lite Shadow 实战操作 SOP (2026-05-09)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-q4-dispatcher-hcsf-codex.md` | 2026-05-14 | 文首主题：2026-05-14 Q4 Dispatcher HCSF。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-ch-002-channel-health-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CH-002 Channel Health Auto-Disable。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-ch-002-channel-health-implementation-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CH-002 Channel Health Implementation - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-ch-002-code-review-fix-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CH-002 channel health code review fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-auth-expired-dispatcher-health-state-codex.md` | 2026-05-25 | 文首主题：2026-05-25 authexpired dispatcher healthstate S3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-p0-account-health-codex.md` | 2026-06-02 | 文首主题：2026-06-02 P0 Account Health Endpoint Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-channel-health-summary-codex.md` | 2026-06-06 | 文首主题：2026-06-06 channel-health fleet summary endpoint - Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-account-health-probe-wiring.md` | 2026-06-24 | 文首主题：计划:account health probe 死开关接线(写 lastprobeat)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-binding-weighted-pool-selection-claude.md` | 2026-07-14 | 文首主题：2026-07-14 绑定级 Weight 选号加权 · Claude 综合裁定。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-binding-weighted-pool-selection-codex.md` | 2026-07-14 | 文首主题：2026-07-14 绑定级 Weight 选号加权 Codex 独立计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-account-module-sub2-gap-analysis.md` | 2026-07-15 | 文首主题：账号模块 · sub2api ↔ HUAKAI 全面差距分析。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/channel-health-auto-disable.md` | 2026-05-19 | 文首主题：F-CH-002: Channel Health Auto-Disable。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/client-identity.md` | 2026-05-19 | 文首主题：F-CLIENT-IDENTITY-001: Client Identity Detection, Persistence, and Sticky Cac…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/pool-routing.md` | 2026-07-15 | 文首主题：F-POOL-001: Provider Account Pool Selection。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；账号池、选号和调度须逐候选加载、过滤、评分、等待/回退链核实后建立领域 SSOT。
- 建议删除：4 份；建议保留：19 份。
- 需真读代码裁定：17 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.8 credentials 凭证 / OAuth / 刷新

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/runtime-logic/claude-oauth-session-serving.md` | 2026-07-10 | 文首主题：Claude OAuth/session 官方直发运行逻辑。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/architecture/runtime-logic/credential-acquisition.md` | 2026-07-11 | 文首主题：credential 采集流状态机 运行逻辑。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/2026-06-30-external-creds-for-live-test.md` | 2026-06-30 | 文首主题：全栈实测·需要 Owner 提供的真实凭证清单。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/multi-oauth.md` | 2026-06-03 | 文首主题：Gap Critique: Multi-provider OAuth login (multi-oauth)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/multi-oauth.md` | 2026-06-03 | 文首主题：Gap Design: Multi-provider OAuth login (WeChat / DingTalk / LinuxDo / OIDC)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-specs/multi-oauth.md` | 2026-06-03 | 文首主题：Gap Spec: Multi-provider OAuth login (multi-oauth)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-29-all-api-hub-credential-vault-r3.md` | 2026-04-29 | 文首主题：2026-04-29 all-api-hub credential vault R3。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-credential-refresh-worker-codex.md` | 2026-05-14 | 文首主题：2026-05-14 credential refresh worker。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-l2-credentialworker-refresh-adapters-codex.md` | 2026-05-14 | 文首主题：2026-05-14 L2 credentialworker refresh adapters。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-auth-005-credential-mgmt-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-AUTH-005 upstream credential management (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-auth-005-credential-mgmt-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-AUTH-005 upstream credential management Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-cred-001-acquisition-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-CRED-001 自动凭证获取流程 (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-cred-001-acquisition-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-CRED-001 automated credential acquisition flow — Codex SPECIFIER…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-audit-key-rotation-codex.md` | 2026-05-18 | 文首主题：2026-05-18 audit key rotation codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-anthropic-oauth-inversion-claude.md` | 2026-05-24 | 文首主题：Anthropic Pro/Max OAuth 反转 — Claude lane 独立 plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-anthropic-oauth-inversion-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-anthropic-oauth-inversion-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Anthropic Pro/Max OAuth Inversion Plan - Codex Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-anthropic-oauth-inversion-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-anthropic-oauth-inversion-synthesis.md` | 2026-05-24 | 文首主题：Anthropic Pro/Max OAuth 反转 — Synthesis (Claude × Codex Cross-Discuss)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-24-oauth-callback-registry-fix-codex.md` | 2026-05-24 | 文首主题：2026-05-24 OAuth Callback Registry Fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-openai-codex-oauth-refresh-codex.md` | 2026-05-24 | 文首主题：2026-05-24 openaicodex OAuth refresh Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-refresh-oauth-endpoint-hardening.md` | 2026-05-24 | 文首主题：2026-05-24 Refresh OAuth Endpoint Hardening。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-token-refresh-worker-closure-claude.md` | 2026-05-24 | 文首主题：Token Refresh Worker 闭环 + 账号采集 Handler 实落 — Claude Lane Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-token-refresh-worker-closure-codex.md` | 2026-05-24 | 文首主题：2026-05-24 token refresh worker closure — Codex independent plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-token-refresh-worker-closure-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-token-refresh-worker-closure-synthesis.md` | 2026-05-24 | 文首主题：Token Refresh Worker 闭环 — Synthesis (Claude × Codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-25-vendor-credential-lane-b-codex.md` | 2026-05-25 | 文首主题：2026-05-25 vendor credential Lane B Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-anthropic-claude-oauth-claude.md` | 2026-05-26 | 文首主题：2026-05-26 Anthropic claudeaioauth 真 OAuth 接通 — Claude Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-26-anthropic-claude-oauth-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-26-anthropic-claude-oauth-codex.md` | 2026-05-26 | 文首主题：2026-05-26 Anthropic claudeaioauth 真 OAuth 接通 — Codex Lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-anthropic-claude-oauth-synthesis.md` | 2026-05-26 | 文首主题：2026-05-26 Anthropic claudeaioauth 真 OAuth 接通 — Synthesis (合并稿)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-26-credentialworker-oauth-mode-adapters-codex.md` | 2026-05-26 | 文首主题：2026-05-26 credentialworker OAuth mode adapters Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-chatgpt-oauth-claude.md` | 2026-05-27 | 文首主题：ChatGPT OAuth (vendor=openai, AuthMode=chatgptoauth) — Claude lane plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-chatgpt-oauth-codex.md` | 2026-05-27 | 文首主题：2026-05-27 ChatGPT OAuth Codex Lane Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-27-chatgpt-oauth-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-27-chatgpt-oauth-synthesis.md` | 2026-05-27 | 文首主题：ChatGPT OAuth (vendor=openai, AuthMode=chatgptoauth) — Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-27-gemini-oauth-claude.md` | 2026-05-27 | 文首主题：2026-05-27 Gemini codeassist + googleone 真 OAuth 接通 — Claude Lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-gemini-oauth-codex.md` | 2026-05-27 | 文首主题：2026-05-27 Gemini codeassist + googleone 真 OAuth 接通 — Codex Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-27-gemini-oauth-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-27-gemini-oauth-gem1-gem2-round2-codex.md` | 2026-05-27 | 文首主题：2026-05-27 GEM-1+2 Round 2 S1 修复。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-gemini-oauth-gem1-gem2-spec.md` | 2026-05-27 | 文首主题：GEM-1 + GEM-2 实施 Spec — Gemini codeassist + googleone OAuth 接通。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-gemini-oauth-gem3-spec.md` | 2026-05-27 | 文首主题：GEM-3 实施 Spec — Gemini Refresh Path SSRF + S1-D 闭合。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-27-gemini-oauth-synthesis.md` | 2026-05-27 | 文首主题：2026-05-27 Gemini codeassist + googleone 真 OAuth 接通 — 合并稿 (Claude PM synthesi…。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-28-dual-mode-oauth-callback-spec.md` | 2026-05-28 | 文首主题：双模式 admin OAuth callback spec (loopback + 远程 web) — S1-002 + S2-059。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-oauth-web-1-r2-gemini-allowlist-codex.md` | 2026-05-28 | 文首主题：2026-05-28 OAUTH-WEB-1 R2 Gemini Allowlist Strictness。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-oauth-web-2-browser-callback.md` | 2026-05-28 | 文首主题：OAUTH-WEB-2 (S1-002a) browser callback 无 Bearer 实施计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-oauth-web-3-wiring-allowlist.md` | 2026-05-28 | 文首主题：OAUTH-WEB-3 (S1-002b) production wiring 接 allowlist 构造函数 + operator config。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-30-s2-045-three-scope-storm-wiring.md` | 2026-05-30 | 文首主题：S2-045 — Wire three-scope refresh-storm control into the credential worker。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-010-credential-acq-composite-fk-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-010 credential acquisition composite FK - Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-multi-oauth-slice1-codex.md` | 2026-06-03 | 文首主题：2026-06-03 multi-oauth slice1 Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-oauth-providers-slice2-codex.md` | 2026-06-04 | 文首主题：2026-06-04 OAuth Providers Slice2 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-oauth-state-cookie-provider-errors-codex.md` | 2026-06-04 | 文首主题：2026-06-04 OAuth State Cookie And Provider Error Hardening。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-credentialacq-finalize-race-codex.md` | 2026-06-06 | 文首主题：2026-06-06 credentialacq finalize race。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-kimi-oauth-codex.md` | 2026-06-07 | 文首主题：2026-06-07 Kimi OAuth Provider。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-auth-credential-acq-fixes-codex.md` | 2026-07-05 | 文首主题：2026-07-05 auth 采集流两处修 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-credential-materialization-fixes-codex.md` | 2026-07-05 | 文首主题：2026-07-05 credential 物化三处修 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-openai-compat-oauth-grok-s1-codex.md` | 2026-07-10 | 文首主题：2026-07-10 openai-compat OAuth Grok S1（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-10-openai-compat-oauth-grok-s1.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-10-openai-compat-oauth-grok-s1.md` | 2026-07-10 | 文首主题：2026-07-10 openai-compat OAuth Grok S1（合成执行计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-12-antigravity-oauth-client-to-env-claude.md` | 2026-07-12 | 文首主题：Antigravity 内置 OAuth clientid/secret 改 env 提供(消除公开仓 secret)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-antigravity-oauth-client-to-env-codex.md` | 2026-07-12 | 文首主题：2026-07-12 Antigravity OAuth 客户端环境变量覆盖（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-frontend-credentials-ops-gaps-codex.md` | 2026-07-12 | 文首主题：2026-07-12 前端凭证与运维缺口切片（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-f-cred-001-preservation-codex-review.md` | 2026-05-15 | 文首主题：2026-05-15 F-CRED-001 acquisition preservation review。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-10-R1A-codex-adversarial-verdict.md` | 2026-07-10 | 文首主题：R1A(Claude OAuth/session 端到端 serving)对抗审查裁定 — 2026-07-10。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/DEFERRED-anthropic-claude-oauth-ant1-2.md` | 2026-05-26 | 文首主题：DEFERRED — Anthropic claudeaioauth ANT-1 + ANT-2 S2 留尾。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-anthropic-claude-oauth-ant3.md` | 2026-05-27 | 文首主题：DEFERRED — Anthropic claudeaioauth ANT-3 留尾。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-gemini-storage-handler.md` | 2026-05-24 | 文首主题：DEFERRED — Gemini credentialacq mode/credentialstore handler 缺失。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-windsurf-storage-handler.md` | 2026-05-24 | 文首主题：DEFERRED — Windsurf credentialstore handler 缺失。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/credential-acquisition.md` | 2026-05-19 | 文首主题：F-CRED-001: Credential Acquisition。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/upstream-credential-management.md` | 2026-05-19 | 文首主题：F-AUTH-005: Upstream Provider Account Credential Management。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；凭证获取、存储、刷新和审计须逐模式追踪后建立领域 SSOT。
- 建议删除：7 份；建议保留：56 份。
- 需真读代码裁定：46 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.9 egress-tls-mimicry 出口 / TLS / 指纹

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/egress-tls-mimicry-SSOT.md` | 2026-07-15 | 文首主题：出口 / TLS 指纹 / mimicry —— 唯一权威文档(SSOT)。 | `CURRENT` | 本文件即既有 SSOT | 既有 SSOT；仅登记。 |
| `docs/dev/owner-setup-fingerprint-and-pg.md` | 2026-05-19 | 文首主题：Owner 本机 Setup 指引: PG 集成测试 + Provider 指纹抓取。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/tls-fp-crud.md` | 2026-06-03 | 文首主题：Critique: TLS Fingerprint Profile Admin CRUD (tls-fp-crud)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/tls-fp-crud.md` | 2026-06-03 | 文首主题：Gap Design: TLS Fingerprint Profile Admin HTTP CRUD。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-l2-a2-tls-clienthello-capture-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A2 TLS ClientHello Capture。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a2-tls-parser-negative-tests-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A2 TLS Parser Negative Tests。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a3-capture-diff-normalizer-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A3 Capture Diff Normalizer。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a5-1-openssl-profile-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A5.1 OpenSSL profile cipher suites and ALPN injection。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a5-2-openssl-groups-sigalgs-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A5.2 OpenSSL supportedgroups and signaturealgorithms。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a5-3-ec-point-formats-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A5.3 EC point formats injection。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a5-4-extension-22-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A5.4 OpenSSL extension 22 profile preflight。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a5-5-extension-list-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A5.5 OpenSSL Extension List Preflight。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-l2-a6-http2-fork-adapter-codex.md` | 2026-05-15 | 文首主题：2026-05-15 L2-A6 HTTP/2 fork adapter。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-3-r-d-claude.md` | 2026-05-15 | 文首主题：2026-05-15 R-3 R-D 端到端验真 Claude 平行计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-3-r-e-mainline-claude.md` | 2026-05-15 | 文首主题：2026-05-15 R-3 R-E Mainline Rust 数据面接入 Claude 平行计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-3-r-e-mainline-codex.md` | 2026-05-15 | 文首主题：2026-05-15 R-3 R-E Mainline Rust Data Plane Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md` | 2026-05-15 | 文首主题：2026-05-15 R-C Lane 2 transport backend architecture - Codex。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md` | 2026-05-15 | 文首主题：2026-05-15 R-C Lane 2 L2-A0 依赖与许可审计 - Codex。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-c-lane2-l2-a8-codex.md` | 2026-05-15 | 文首主题：2026-05-15 R-C Lane 2 L2-A8 Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-15-r-d-smoke-scaffold-codex.md` | 2026-05-15 | 文首主题：2026-05-15 R-D Smoke Scaffold Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-16-all-vendor-subscription-anti-detection-roadmap-claude.md` | 2026-05-16 | 文首主题：2026-05-16 全 Vendor 订阅转 API 反封禁 + 主动对抗 Roadmap (Claude 主笔)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-16-antigravity-anti-detection-roadmap-claude.md` | 2026-05-16 | 文首主题：2026-05-16 Antigravity 反封禁技术栈 Roadmap (Claude 主笔)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-16-github-anti-detection-survey-sonnet.md` | 2026-05-16 | 文首主题：2026-05-16 Github 反检测/对抗 成熟项目调研 (Sonnet lane)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-16-r-3-r-e-ocaw-answers-claude.md` | 2026-05-16 | 文首主题：2026-05-16 R-3 R-E Mainline OCAW Answers (Claude 主笔)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-16-r-e-a-plus1-tls-codex.md` | 2026-05-16 | 文首主题：2026-05-16 R-E-A+1 TLS Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-17-r-3-a-fix-2-deeper-codex.md` | 2026-05-17 | 文首主题：2026-05-17 R-3-A-fix-2-deeper Codex Executor Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-18-r-3-a-fix-3-deeper-claude.md` | 2026-05-18 | 文首主题：R-3-A-fix-3-deeper — 3 vendor JA3 mismatch 根因。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-18-r-3-a-fix-3-deeper-codex.md` | 2026-05-18 | 文首主题：2026-05-18 R-3-A-fix-3-deeper Codex。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-18-r-3-a-fix-4-deeper-codex.md` | 2026-05-18 | 文首主题：2026-05-18 R-3-A-fix-4-deeper。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-19-fp-pool-claude.md` | 2026-05-19 | 文首主题：F-FP-POOL Plan (Claude) — TLS 指纹池 + 代理池 一键部署化。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-fork-backend-claude.md` | 2026-05-24 | 文首主题：L1 TLS BoringSSL fork backend — Claude Lane Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-fork-backend-codex.md` | 2026-05-24 | 文首主题：2026-05-24 BoringSSL Fork Backend Implementation Plan — Codex Lane。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-fork-backend-synthesis.md` | 2026-05-24 | 文首主题：L1 TLS BoringSSL fork backend — Synthesis (Claude × Codex)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-phase-2-5-tls-sidecar-codex.md` | 2026-05-24 | 文首主题：2026-05-24 boringssl phase 2.5 tls-sidecar codex。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-phase-4-5-claude.md` | 2026-05-24 | 文首主题：2026-05-24 BoringSSL Phase 4 ECH + Phase 5 PQ - Claude Lane Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-phase-4-5-codex.md` | 2026-05-24 | 文首主题：2026-05-24 BoringSSL R-3-A-fix-2..5 Status Review + Phase 4-5 Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-boringssl-phase-4-5-synthesis.md` | 2026-05-24 | 文首主题：BoringSSL Phase 4-5 Synthesis (Claude x Codex)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-h2-settings-phase3-codex.md` | 2026-05-24 | 文首主题：2026-05-24 H2 SETTINGS Phase 3 Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-05-24-ja4-alpn-parity-codex.md` | 2026-05-24 | 文首主题：2026-05-24 JA4 ALPN parity fix。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-02-r-sidecar-002-h2-bridge-codex.md` | 2026-06-02 | 文首主题：2026-06-02 R-SIDECAR-002 H2 bridge Codex plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-02-sidecar-contract-harden-codex.md` | 2026-06-02 | 文首主题：2026-06-02 Sidecar Contract Harden Codex Plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-02-sidecar-sigalgs-codex.md` | 2026-06-02 | 文首主题：2026-06-02 sidecar sigalgs codex plan。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-23-deploy-caddy-auto-tls.md` | 2026-06-23 | 文首主题：部署:Caddy 反代 + 自动 HTTPS(单租户 MVP 上线最后一道)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-24-mimicry-global-switch.md` | 2026-06-24 | 文首主题：计划:出站 TLS 指纹伪装 全局一键关运维开关(默认开)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-06-24-rust-egress-proxy-tunnel.md` | 2026-06-24 | 文首主题：Rust sidecar 代理穿透(②-3 CONNECT/SOCKS5)实现计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-03-egress-relay-quality-observability-plan-claude.md` | 2026-07-03 | 文首主题：出口(Go↔Rust sidecar)+ Relay 质量验收 + 可观测性 实现计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-03-r7-mimicry-full-activation-claude.md` | 2026-07-03 | 文首主题：R7 强伪装全套激活 + 默认开 实现计划(claude)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-05-comment-cn-batch2-codex.md` | 2026-07-05 | 文首主题：2026-07-05 存量英文注释转中文批2。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-05-mimicry-export-s2-robustness-codex.md` | 2026-07-05 | 文首主题：2026-07-05 mimicry 出口两处 S2 健壮性修 Codex 计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-07-codex-cli-global-hardening-claude.md` | 2026-07-07 | 文首主题：片2f 弧:codex-cli 全局加固层(黑白名单/版本门/引擎指纹/app-server)— Claude 计划草案。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-10-claude-oauth-serving-mimicry-claude.md` | 2026-07-10 | 文首主题：Claude OAuth serving 路径接线 + body 拟真(system/billing 注入)—— Claude 平行计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-10-claude-oauth-serving-mimicry-codex.md` | 2026-07-10 | 文首主题：2026-07-10 Claude OAuth 账号到 API serving 接线与出站 body 拟真：Codex 独立平行计划。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-13-batch2-pages-spec-claude.md` | 2026-07-13 | 文首主题：第二批三页密度重构 — 部件级 Spec(/accounts /users /keys)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-14-p2c-deadtable-writeports-claude.md` | 2026-07-14 | 文首主题：P2-c 死表写口+模型主体+租户默认出口+配额 · Claude 独立计划(双计划我方稿)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/plans/2026-07-15-tenant-default-egress-codex.md` | 2026-07-15 | 文首主题：2026-07-15 租户默认出口写口与前端激活（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | SSOT 同日增量；待 Claude 裁定。 |
| `docs/process/plans/2026-07-15-tenant-default-egress.md` | 2026-07-15 | 文首主题：2026-07-15 租户默认出口写口与前端激活（综合执行计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | SSOT 同日增量；待 Claude 裁定。 |
| `docs/process/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md` | 2026-05-15 | 文首主题：L2-A5.4 Retrospective Cross-Review (Codex)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/reviews/2026-05-15-l2-a5-5-codex-review.md` | 2026-05-15 | 文首主题：L2-A5.5 Per-Commit Cross-Review (Codex)。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/process/reviews/2026-05-15-round2a-r-e-switch-deny-codex-review.md` | 2026-05-15 | 文首主题：scope。 | `SUPERSEDED` | `docs/architecture/egress-tls-mimicry-SSOT.md` | 旧散计划/评审已由出口 SSOT 收口。 |
| `docs/runbooks/r-d-smoke-runbook.md` | 2026-05-15 | 文首主题：R-D Smoke Runbook。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/runbooks/r-e-transport-baseline-switch.md` | 2026-05-15 | 文首主题：R-E Transport Baseline Switch Runbook。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/active-anti-detection.md` | 2026-05-19 | 文首主题：Active Anti-Detection (L6 主动对抗) — F-ADV-001 Spec。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/device-fingerprint-binding.md` | 2026-05-19 | 文首主题：Device Fingerprint Binding (L3 反封禁层) — F-FP-001 Spec。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/outbound-ip-pool.md` | 2026-05-19 | 文首主题：Outbound IP Pool (L5 IP 池) — F-NET-001 Spec。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/request-pacing-mimicry.md` | 2026-05-19 | 文首主题：Request Pacing Mimicry (L4 节奏伪装) — F-PACE-001 Spec。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |

**节末统计**

- 当前唯一权威源：`docs/architecture/egress-tls-mimicry-SSOT.md`（Owner 明确指定的既有 SSOT；本波不重做）。
- 建议删除：53 份；建议保留：12 份。
- 需真读代码裁定：7 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.10 provider-adapters 厂商适配

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/process/feature-tree/provider-account-mgmt.md` | 2026-06-03 | 文首主题：Feature-Tree Audit: provider-account-mgmt。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-06-openai-sse-codex-lane.md` | 2026-05-06 | 文首主题：2026-05-06 openai-sse-codex-lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-07-bedrock-a2a3-codex.md` | 2026-05-07 | 文首主题：2026-05-07 Bedrock A2+A3 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-07-bedrock-eventstream-claude.md` | 2026-05-07 | 文首主题：2026-05-07 Bedrock EventStream 接入 — Claude 视角。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-07-bedrock-eventstream-codex.md` | 2026-05-07 | 文首主题：2026-05-07 Bedrock EventStream 接入计划（Codex 独立版）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-bedrock-a4-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Bedrock A4 — proto.BedrockEventStreamAdapter（claude lane plan）。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-bedrock-a4-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-bedrock-a4-codex.md` | 2026-05-08 | 文首主题：HUAKAI Bedrock Plan A4 - BedrockEventStreamAdapter。 | `SUPERSEDED` | `docs/process/plans/2026-05-08-bedrock-a4-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-08-bedrock-a4-synthesis.md` | 2026-05-08 | 文首主题：2026-05-08 Bedrock A4 — 双 lane 综合（claude + codex）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-bedrock-a5a6-claude.md` | 2026-05-08 | 文首主题：2026-05-08 Bedrock A5+A6 合并 atomic — gateway 集成 (claude lane plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-frontend-gemini-brief.md` | 2026-05-12 | 文首主题：HUAKAI 前端原创设计 Brief — 交付给 Gemini。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-open-brief.md` | 2026-05-12 | 文首主题：HUAKAI P1 Dashboard — Gemini 开放式 brief。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 前端原创设计 Brief — 交付给 Gemini。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round2-prompt.md` | 2026-05-12 | 文首主题：P0-1：实装 NEXTPUBLICUSEMOCK 切换。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round3-prompt.md` | 2026-05-12 | 文首主题：Fix 1（P0-3 残留）：page.tsx 11 处英文 JSX 注释翻中文。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round4-prompt.md` | 2026-05-12 | 文首主题：HUAKAI P1 Dashboard — Gemini Round 4 (借鉴 SaaS Dashboard 美学，全面重设计)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round5-prompt.md` | 2026-05-12 | 文首主题：HUAKAI P1 Dashboard — Gemini Round 5（Round 4 minor closeout）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round6-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 前端 — Round 6。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round7-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 前端 — Round 7。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-gemini-p1-round8-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 前端 — Round 8。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-openai-chat-client-split-codex.md` | 2026-05-13 | 文首主题：2026-05-13 OpenAI Chat Client Split。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-openai-responses-client-split-codex.md` | 2026-05-13 | 文首主题：2026-05-13 openai responses client split。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-hcsf-graph-provider-request-codex.md` | 2026-05-14 | 文首主题：2026-05-14 HCSF graph to provider request。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-q1-admin-provider-account-codex.md` | 2026-05-14 | 文首主题：2026-05-14 Q1 Admin Provider Account API。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-gemini-sse-extras-codex.md` | 2026-05-18 | 文首主题：2026-05-18 Gemini SSE extras retention。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-wave-3a-proto-vendor-subpackages-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Wave 3-A proto vendor subpackages。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-hole3-anthropic-buffered-impl-codex.md` | 2026-05-21 | 文首主题：2026-05-21 hole3-anthropic-buffered-impl-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-21-hole3-anthropic-buffered-refcompare-codex.md` | 2026-05-21 | 文首主题：2026-05-21 hole3-anthropic-buffered-refcompare-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-antigravity-provider-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Antigravity Provider Skeleton。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-cursor-vendor-skeleton-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Cursor vendor skeleton Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-kiro-vendor-skeleton-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Kiro Vendor Skeleton。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-vendor-refreshers-wiring-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Vendor Refreshers Wiring - Codex independent plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-windsurf-vendor-skeleton-codex.md` | 2026-05-24 | 文首主题：2026-05-24 Windsurf Vendor Skeleton。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-cursor-vendor-claude.md` | 2026-05-26 | 文首主题：2026-05-26 Cursor Vendor 集成方案 — Claude Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-26-cursor-vendor-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-26-cursor-vendor-codex.md` | 2026-05-26 | 文首主题：2026-05-26 Cursor Vendor 集成方案 — Codex Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-26-cursor-vendor-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-26-cursor-vendor-synthesis.md` | 2026-05-26 | 文首主题：2026-05-26 Cursor Vendor 集成方案 — Synthesis (合并稿)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-27-gem3-r2-env-secret-antigravity-pause-codex.md` | 2026-05-27 | 文首主题：2026-05-27 GEM-3 R2 env secret + antigravity pause Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-s1-030-provider-family-routing-codex.md` | 2026-05-28 | 文首主题：2026-05-28 S1-030 provider-family routing fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-proto-transparency-openai-claude.md` | 2026-05-29 | 文首主题：PROTO 透明性 Slice 1 (OpenAI 流式) 实施计划 — Claude 独立稿 (2026-05-29)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-29-proto-transparency-openai-codex.md` | 2026-05-29 | 文首主题：PROTO 透明性 Slice 1 OpenAI 流式适配器实施计划 — Codex 独立稿。 | `SUPERSEDED` | `docs/process/plans/2026-05-29-proto-transparency-openai-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-29-proto-transparency-openai-synthesis.md` | 2026-05-29 | 文首主题：PROTO 透明性 Slice 1 (OpenAI) — 平行计划交叉综合 (2026-05-29)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-02-provider-account-test-endpoint-codex.md` | 2026-06-02 | 文首主题：2026-06-02 provider account test endpoint。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-provider-channel-catalog-codex.md` | 2026-06-02 | 文首主题：Provider/Channel Catalog Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-openai-compat-passthrough-fold-codex.md` | 2026-06-06 | 文首主题：2026-06-06 OpenAI-Compatible Passthrough Adapter Fold。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-vendor-catalog-crud-codex.md` | 2026-06-06 | 文首主题：2026-06-06 vendor catalog CRUD Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-b-gemini-native-v1beta-codex.md` | 2026-06-07 | 文首主题：2026-06-07 B Gemini Native v1beta。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-captcha-multiprovider-codex.md` | 2026-06-07 | 文首主题：2026-06-07 captcha-multiprovider Codex execution plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-mediatask-orphan-persistence.md` | 2026-06-19 | 文首主题：mediatask 孤儿 providerTaskID 持久化(闭合审计 #71 残留,money 安全)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-anthropic-mergeusage-preserve-cache.md` | 2026-06-20 | 文首主题：修复:Anthropic 流式 mergeUsage 整段替换抹掉 cache 字段 → 流式 cache 观测丢失。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-02-officialkey-provider-expansion-claude.md` | 2026-07-02 | 文首主题：官 key 厂商扩容:Grok + 国内大厂 apikey 接入(存储约束放行)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-chat-anthropic-codex-slice2c-codex.md` | 2026-07-06 | 文首主题：2026-07-06 chat/anthropic 客户端接 openaicodex 上游片 2c Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-antigravity-multivendor-spec.md` | 2026-07-10 | 文首主题：Antigravity 多厂商中转 spec(经 Owner Google AI Pro 号亲登实证 2026-07-10)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-11-antigravity-reversal-lane-claude.md` | 2026-07-11 | 文首主题：Antigravity 反转车道补全 — 计划(Claude)— 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-antigravity-project-ref-b1-codex.md` | 2026-07-12 | 文首主题：2026-07-12 Antigravity projectref B1（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-antigravity-lane-real-account-result.md` | 2026-07-11 | 文首主题：Antigravity 反转车道 G1-G5 真账号实测结果 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/DECISION-2026-05-26-cursor-c1-partial-revert.md` | 2026-05-26 | 文首主题：Decision: Cursor C1 Partial Revert (2026-05-26)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-S2-163-reasoning-folding-crosscheck.md` | 2026-05-29 | 文首主题：DEFERRED — reasoning-aware token 交叉校验（逐 provider folding 建模）。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-proto-toolcall-roundtrip.md` | 2026-06-17 | 文首主题：DEFERRED（S3）：严格 OpenAI 兼容上游的 tool-call id 多轮 round-trip 前缀未闭合。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-vendor-devicecode-standard-alias.md` | 2026-05-25 | 文首主题：Deferred Review Findings: Vendor Device-Code Standard Alias。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-vendor-kiro-license-blocked.md` | 2026-05-25 | 文首主题：DEFERRED vendor kiro license blocked。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-vendor-windsurf-source-clone-blocked.md` | 2026-05-25 | 文首主题：DEFERRED vendor windsurf source clone blocked。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-windsurf-refresher-audit-outcome.md` | 2026-05-24 | 文首主题：DEFERRED — Windsurf refresher 不带 RefreshAuditOutcome wrapper。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/specs/capacity-graph.md` | 2026-05-19 | 文首主题：F-CAPACITY-GRAPH-001: Cross-Vendor Capacity Graph — Forecast, Restock, and Fa…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/protocol-translation.md` | 2026-04-29 | 文首主题：F-PROTO-002: Protocol Translation Across Provider Pairs。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；各厂商适配器须逐注册、装配、请求/响应与错误路径核实后建立领域 SSOT。
- 建议删除：5 份；建议保留：58 份。
- 需真读代码裁定：48 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.11 reseller-distribution 分销 / 商户

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/process/decisions/DR-002-product-editions.md` | 2026-05-19 | 文首主题：DR-002: Personal Edition First, SaaS Edition After Feedback。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/plans/2026-07-15-coadmin-and-merchant-tenant-arc-claude.md` | 2026-07-15 | 文首主题：平台协管员 + 入驻商家(白牌多租户)大 arc · Claude 独立稿。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-reseller-arc-final-model.md` | 2026-07-15 | 文首主题：分销商(二级管理员)arc · 最终锁定模型(权威底稿)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-reseller-phase1-codex.md` | 2026-07-15 | 文首主题：HUAKAI 分销商 Phase 1 独立实现计划（Codex）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |

**节末统计**

- 当前唯一权威源：无；`docs/process/decisions/DR-002-product-editions.md` 只覆盖 edition 决策，不等于分销领域 SSOT。
- 建议删除：0 份；建议保留：4 份。
- 需真读代码裁定：2 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.12 frontend 前端

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/14_UI_CONTRACTS.md` | 2026-04-28 | 文首主题：UI Contracts。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/architecture/backend-feature-inventory-codex.md` | 2026-07-13 | 文首主题：HUAKAI 后端功能全景 × 前端覆盖盘点(codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/2026-06-24-源码梳理与前端编写方案.md` | 2026-06-24 | 文首主题：HUAKAI 全项目源码梳理 + 三家竞品对比 + 前端编写方案。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/2026-06-25-页面清单-三镜对齐.md` | 2026-06-25 | 文首主题：HUAKAI 前端页面清单 · 三镜对齐草图(2026-06-25)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/ASK-HERMES-DESIGN-v1.md` | 2026-06-16 | 文首主题：Ask Hermes —— 嵌入式诊断助手设计方案 v1。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/BUILD-SPEC.md` | 2026-06-10 | 文首主题：HUAKAI Frontend Build Spec (for Claude Design)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/FUSION-LAYOUT-PLAN-v3.md` | 2026-06-15 | 文首主题：前端融合布局具体方案 v3 —— 实测版。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/IA-PROPOSAL-v2-2026-06-14.md` | 2026-06-14 | 文首主题：HUAKAI 前端信息架构（IA）融合提案 v2 — “深度版”。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/PAGE-PROMPTS.md` | 2026-07-05 | 文首主题：HUAKAI 前端逐页提示词（for Claude Design）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/SUB2API-FRONTEND-REUSE-DRILL-2026-06-15.md` | 2026-06-15 | 文首主题：经验:拿 sub2api 前端做底座 + 补缺失 —— 可行性演习记录。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/frontend/WIRING-COVERAGE-MATRIX.md` | 2026-06-15 | 文首主题：前端接线测试覆盖矩阵。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/decisions/DR-004-frontend-framework.md` | 2026-06-02 | 文首主题：DR-004: Frontend Framework。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/gap-designs/usage-dashboard.md` | 2026-06-03 | 文首主题：Gap Design: Usage Analytics Dashboard (Admin + User Self-Serve)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-vertical-closure-synthesis.md` | 2026-05-08 | 文首主题：2026-05-08 纵向闭环 synthesis（claude × codex 双 lane + Owner 前端 directive）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-12-frontend-brief-market-codex.md` | 2026-05-12 | 文首主题：2026-05-12 frontend brief market codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-frontend-round9-codex-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 前端 — Round 9（Codex 接手）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-feature-parity-sub2api-vs-round10-codex.md` | 2026-05-13 | 文首主题：2026-05-13 frontend feature parity sub2api vs Round 10。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-round10-codex-prompt.md` | 2026-05-13 | 文首主题：HUAKAI 前端 — Round 10（Codex 接手 v2，scope 收窄）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-round9-codex-execution.md` | 2026-05-13 | 文首主题：2026-05-13 Frontend Round 9 Codex Execution。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-research-codex-brief.md` | 2026-05-13 | 文首主题：HUAKAI 前端 UI 美学调研 — Codex Brief。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-v2-codex-brief.md` | 2026-05-13 | 文首主题：HUAKAI 前端 UI 美学调研 v2 — Codex Brief（非蓝绿强约束）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-v3-codex.md` | 2026-05-13 | 文首主题：2026-05-13 frontend-ui-aesthetic-v3-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-t6-frontend-audit-codex.md` | 2026-05-14 | 文首主题：2026-05-14 T6 Frontend Audit Page。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-renew-page-fix-claude.md` | 2026-05-20 | 文首主题：/renew 页面修复 — Claude 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-renew-page-fix-codex.md` | 2026-05-20 | 文首主题：2026-05-20 /renew 页面真实接线修复计划 - Codex 独立草案。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md` | 2026-05-25 | 文首主题：2026-05-25 AE-D3 Admin UI Detailed Cause Claude Lane Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-codex.md` | 2026-05-25 | 文首主题：2026-05-25 AE-D3 admin UI detailed cause — Codex lane plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-synthesis.md` | 2026-05-25 | 文首主题：2026-05-25 AE-D3 Admin UI Detailed Cause Synthesis (Claude x Codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-02-frontend-dashboard-real-codex.md` | 2026-06-02 | 文首主题：2026-06-02 frontend-dashboard-real。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-16-frontend-fusion-ia-blueprint.md` | 2026-06-16 | 文首主题：前端融合布局蓝图(IA)+ 前后端一起补 执行计划 — 2026-06-16。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-alert-rules-crud-frontend.md` | 2026-06-17 | 文首主题：Wave2 — 告警规则 alert-rules 写 CRUD 前端（计划留痕）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-alert-silences-crud-frontend.md` | 2026-06-17 | 文首主题：Wave2 — 告警静默 alert-silences admin CRUD 前端（计划留痕）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-announcement-crud-frontend.md` | 2026-06-17 | 文首主题：Wave2 — 公告 announcements admin CRUD 前端（计划留痕）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-channel-test-template-crud-frontend.md` | 2026-06-17 | 文首主题：Wave2 切片计划 — 渠道测试模板 admin CRUD（前端接线）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-model-sync-trigger-frontend.md` | 2026-06-17 | 文首主题：Wave2 — model-sync 触发 admin 前端（计划留痕）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-ops-data-panel-frontend.md` | 2026-06-17 | 文首主题：Wave2 切片计划 — 运维数据面（audit-events / DLQ / cache-L2）前端接线。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-17-wave2-subscription-lifecycle-ops-frontend.md` | 2026-06-17 | 文首主题：Wave2 切片计划 — 订阅生命周期 admin 写操作（前端接线）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-frontend-spa-migration.md` | 2026-06-19 | 文首主题：Plan — Frontend migration: Next.js → React + Vite SPA (single-binary embed)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-frontend-spa-kickoff.md` | 2026-06-24 | 文首主题：前端 SPA 重建启动计划(2026-06-24)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-25-frontend-embed-single-binary.md` | 2026-06-25 | 文首主题：前端 SPA 嵌入单二进制 —— 构建链补齐 + Dockerfile gated 改动。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-frontend-user-security-usage-depth-codex.md` | 2026-07-12 | 文首主题：2026-07-12 前端用户安全与用量深度（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes-claude.md` | 2026-07-13 | 文首主题：2026-07-13 Operator 首页大屏真数据缺陷修复（Claude 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes-codex.md` | 2026-07-13 | 文首主题：2026-07-13 Operator 首页真数据缺陷修复（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes.md` | 2026-07-13 | 文首主题：2026-07-13 Operator 首页真数据缺陷修复（合成权威计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-07-13-ui-polish-claude.md` | 2026-07-13 | 文首主题：UI 质感整改波(Claude 独立稿,#10 与 codex 稿交叉)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-ui-polish-codex.md` | 2026-07-13 | 文首主题：2026-07-13 HUAKAI 前端「UI 质感整改波」Codex 独立实施计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p1b-ui-schema-ghost-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P1-b UI 欺骗控件摘除与 schema 幽灵标注（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-frontend-function-tree.md` | 2026-07-15 | 文首主题：HUAKAI 前端功能树 · 逐页设计交付蓝图。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-frontend-redesign-knowledge-tree.md` | 2026-07-15 | 文首主题：HUAKAI 前端重设计知识树 · 第三方 AI 逐页设计蓝图。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-model-routing-overrides-codex.md` | 2026-07-15 | 文首主题：2026-07-15 C② 激活模型路由强制 pin 写口与前端分签。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-sidebar-review-owner-decisions.md` | 2026-07-15 | 文首主题：左侧栏逐项评审 · Owner 决定实录。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-17-admin-coverage-audit.md` | 2026-06-17 | 文首主题：Admin 端点 × 前端覆盖审计（2026-06-17）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；多个设计、覆盖矩阵和当前页面实现尚待逐路由/请求/状态流核实。
- 建议删除：4 份；建议保留：48 份。
- 需真读代码裁定：43 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.13 hermes 运维助手

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/plans/2026-05-24-hermes-native-integration.md` | 2026-05-24 | 文首主题：Hermes Native Integration Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice1-3-discriminating-tests-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Phase-1 Slice 1.3 Discriminating Tests Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice1-claude.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 1 Implementation Plan — Claude Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice1-codex.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 1 — codex lane plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 1 — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice2-1-jwt-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Phase-1 Slice 2.1 JWT Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice2-claude.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 2 Implementation Plan — Claude Lane。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-hermes-phase-1-slice2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice2-codex.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 2 — codex-lane Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-25-hermes-phase-1-slice2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-25-hermes-phase-1-slice2-synthesis.md` | 2026-05-25 | 文首主题：Hermes Phase-1 Slice 2 — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-25-hermes-slice1-1-round2-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 1.1 Round 2 Codex Fix Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-slice1-1-round3-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 1.1 Round 3 Codex Fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-slice1-2-docker-compose-runner-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 1.2 docker-compose hermes-runner。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-slice1-schema-gate-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 1 Schema Gate Codex Fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-slice2-1-round2-fixes-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 2.1 Round 2 S1 Fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-hermes-slice2-1-round3-s1-fixes-codex.md` | 2026-05-25 | 文首主题：2026-05-25 Hermes Slice 2.1 Round 3 S1 Fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-hermes-bridge-file-split.md` | 2026-05-26 | 文首主题：2026-05-26 Hermes Bridge File Split。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-claude.md` | 2026-05-26 | 文首主题：Hermes phase-1 Slice 2.2 — Claude lane plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-codex.md` | 2026-05-26 | 文首主题：Hermes Phase 1 Slice 2.2 - Codex Independent Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-26-hermes-phase-1-slice2-2-synthesis.md` | 2026-05-26 | 文首主题：Hermes phase-1 Slice 2.2 — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-26-hermes-slice2-3-codex-execution.md` | 2026-05-26 | 文首主题：2026-05-26 hermes Slice 2.3 Codex execution。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-hermes-slice2-5-hmac-transition-cleanup-codex.md` | 2026-05-26 | 文首主题：2026-05-26 Hermes Slice 2.5 HMAC Transition Cleanup Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-28-hermes-audit-tx-integrity-codex.md` | 2026-05-28 | 文首主题：2026-05-28 Hermes message+audit fail-closed tx integrity (Codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-13-hermes-ops-assistant-alignment-claude.md` | 2026-06-13 | 文首主题：Hermes 运维助手全面对齐 — 架构方案 (Claude draft)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-hermes-account-health-eta.md` | 2026-06-19 | 文首主题：Plan — accounthealthdiagnose: surface healthstateuntil + 5h session window st…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-hermes-cred-expiry-fields.md` | 2026-06-19 | 文首主题：Plan — credentialdiagnose: surface accessexpiresat / refreshbeforeat / lastre…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-hermes-diagnose-model-fields.md` | 2026-06-19 | 文首主题：Plan — requestdiagnose: surface requestedmodel / upstreammodel (model rewrite…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-hermes-deployment-architecture-codex.md` | 2026-07-15 | 文首主题：2026-07-15 Hermes 部署架构多角度调研（Codex 独立稿）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/DEFERRED-hermes-bridge-scanner-buffer.md` | 2026-05-26 | 文首主题：Deferred Review Finding: Hermes bridge SSE scanner buffer。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-hermes-enable-empty-body.md` | 2026-05-25 | 文首主题：DEFERRED Hermes Enable Empty Body。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-hermes-runner-hash-lock.md` | 2026-05-26 | 文首主题：Deferred Review Finding: Hermes Runner Hash Lock。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-hermes-runner-key-rotation.md` | 2026-05-26 | 文首主题：Deferred Review Finding: Hermes Runner Key Rotation 路径未接通。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-hermes-runner-sse-disconnect-cancel.md` | 2026-05-26 | 文首主题：DEFERRED Hermes Runner SSE Disconnect Cancel。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-hermes-slice2-1-round3-tail.md` | 2026-05-26 | 文首主题：DEFERRED — Hermes Slice 2.1 Round 3 Review Tail。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/TEST-EVIDENCE-2026-05-26-hermes-phase-1-e33d940.md` | 2026-05-26 | 文首主题：Test Evidence: claude/hermes-phase-1 @ e33d940 Docker Verify。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；部署、权限、会话、加密、保留期和 runner 现状尚未收口到单一文档。
- 建议删除：6 份；建议保留：28 份。
- 需真读代码裁定：19 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.14 observability-logging 可观测 / 日志

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/runtime-logic/runtime-log-sink.md` | 2026-07-12 | 文首主题：运行日志入库链(logsink)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/observability-analytics.md` | 2026-06-03 | 文首主题：Feature Tree: observability-analytics。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/relay-log.md` | 2026-06-03 | 文首主题：Gap Critique: Per-request relay log (relay-log.md)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/ops-suite.md` | 2026-06-03 | 文首主题：Gap Design: Ops Suite — Alert Rules + Proactive Monitor + Scheduled Tests。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-designs/relay-log.md` | 2026-06-03 | 文首主题：Gap Design: Per-request relay log subsystem。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-m3-observability-admin-codex.md` | 2026-05-14 | 文首主题：2026-05-14 M3 Observability Admin API。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-005-dlq-priority-claude.md` | 2026-05-15 | 文首主题：2026-05-15 F-OBS-005 DLQ + priority + dual-write (Claude 独立 plan)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-f-obs-005-dlq-priority-codex.md` | 2026-05-15 | 文首主题：2026-05-15 F-OBS-005 DLQ + priority + dual-write - Codex independent plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s2-021-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S2-021 Codex Security Log Redaction Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-obs002-otel-codex.md` | 2026-06-03 | 文首主题：2026-06-03 OBS002 OpenTelemetry Metrics Bridge。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-alert-eval-loop-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Alert Eval Loop Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-alert-rules-codex.md` | 2026-06-06 | 文首主题：2026-06-06 alert rules engine。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-w2-alert-delivery-codex.md` | 2026-06-07 | 文首主题：2026-06-07 W2 alert delivery quick win。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-07-w2-composite-alertmetrics-codex.md` | 2026-06-07 | 文首主题：2026-06-07 W2 composite alertmetrics。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-bridge-l2-cache-metrics.md` | 2026-06-19 | 文首主题：Plan — L2 响应缓存指标接入 Prometheus+告警快照 (F-CACHE-001 激活可观测性)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-19-runtime-alert-metrics.md` | 2026-06-19 | 文首主题：Plan — runtime 资源指标接既有告警引擎 (F-GW-003 Phase 2: 阈值/告警半)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-02-logging-observability-plan-claude.md` | 2026-07-02 | 文首主题：日志可观测性 fusion-upgrade 计划(三镜+成熟项目调研)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-02-slog-facade-unification-claude.md` | 2026-07-02 | 文首主题：日志片 D:slog 门面统一 + /loglevel 联动(修 S1 双栈割裂缺口⑤)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-runtime-log-sink-claude.md` | 2026-07-12 | 文首主题：B7 运行日志入库 + 运营台日志查询(实时轮询)— Claude 计划 2026-07-12。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-batch4-pages-spec-claude.md` | 2026-07-13 | 文首主题：第四批八页密度重构 Spec(/ops /usage-records /usage /health /admin/logs /activity /admi…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-security-monitoring-module-claude.md` | 2026-07-15 | 文首主题：运维安全监测 + 日志模块 · Claude 规划稿。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/privacy-no-user-data-logs.md` | 2026-06-05 | 文首主题：Privacy / No User Data Logs — F-PRIV-001 Spec。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；日志、指标、事件与 DLQ 断言尚待逐生产调用链核实。
- 建议删除：0 份；建议保留：22 份。
- 需真读代码裁定：22 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.15 trust-chain-audit 信任链 / 收据 / 审计账本

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/process/decisions/2026-05-27-trust-chain-simplification-codex-eval.md` | 2026-05-27 | 文首主题：2026-05-27 Trust Chain Simplification — Codex Evaluation。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-13-t3-trust-chain-wiring-codex.md` | 2026-05-13 | 文首主题：2026-05-13 T3 Trust Chain Wiring（Codex）。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-13-trust-chain-feature-family-claude.md` | 2026-05-13 | 文首主题：HUAKAI 核心差异化 — 信任链 / 透明 / 反掺水 Feature Family (Claude lane)。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-13-trust-chain-feature-family-codex.md` | 2026-05-13 | 文首主题：2026-05-13 Trust Chain Feature Family — Codex Lane。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-13-trust-chain-github-survey-codex.md` | 2026-05-13 | 文首主题：2026-05-13 trust-chain GitHub survey codex。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-14-trust-chain-t11-codex.md` | 2026-05-14 | 文首主题：2026-05-14 trust-chain-t11-codex。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-16-f-audit-001-cost-transparency-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-AUDIT-001 Cost Transparency Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-16-f-audit-001-spec-claude.md` | 2026-05-16 | 文首主题：F-AUDIT-001 User Consumption Transparency Spec — Claude Lane Draft。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-16-f-priv-f-audit-spec-wave-plan-claude.md` | 2026-05-16 | 文首主题：F-PRIV-001 + F-AUDIT-001 Spec Wave Plan (Claude Lane)。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-16-f-trust-001-spec-claude.md` | 2026-05-16 | 文首主题：Trust Chain User-Verifiable Ledger — F-TRUST-001 Spec。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-17-f-audit-1-implementation-plan-claude.md` | 2026-05-17 | 文首主题：2026-05-17 F-AUDIT-1 实施 Plan — Claude。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-17-f-trust-1-b-append-only-ed25519-codex.md` | 2026-05-17 | 文首主题：2026-05-17 F-TRUST-1-B append-only ed25519。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-18-f-audit-1-b-impl-claude.md` | 2026-05-18 | 文首主题：F-AUDIT-1-B 5 endpoint impl — Claude 平行计划。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-18-f-audit-1-b-impl-codex.md` | 2026-05-18 | 文首主题：2026-05-18 F-AUDIT-1-B Impl Codex。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-18-f-audit-1-b-p2-3-rate-table-tenant-scope-codex.md` | 2026-05-18 | 文首主题：2026-05-18 F-AUDIT-1-B P2 #3 rate table tenant scope。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-18-f-audit-1-b-review-fixes-codex.md` | 2026-05-18 | 文首主题：2026-05-18 F-AUDIT-1-B Review Fixes Codex。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-18-f-audit-1-c-mismatch-refund-codex.md` | 2026-05-18 | 文首主题：2026-05-18 F-AUDIT-1-C Mismatch Refund Worker Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-22-w4-trust-ledger.md` | 2026-05-22 | 文首主题：W4 强制账本引用与完整性 —— 实施 spec。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-23-receipt-owner-isolation-claude.md` | 2026-05-23 | 文首主题：P0-1 Receipt 租户内 user 隔离 — Claude lane plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-23-receipt-owner-isolation-codex.md` | 2026-05-23 | 文首主题：P0-1 Receipt 租户内 User 隔离 — Codex 独立 Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-23-receipt-owner-isolation-synthesis.md` | 2026-05-23 | 文首主题：2026-05-23 P0-1 Receipt 租户内 user 隔离 — Synthesis (3 lane 合并)。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-a-green-codex.md` | 2026-05-27 | 文首主题：2026-05-27 TRUST-A Green Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-b-2-codex.md` | 2026-05-27 | 文首主题：2026-05-27 TRUST-B-2 Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-b-claude.md` | 2026-05-27 | 文首主题：TRUST-B Claude Lane Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-b-codex.md` | 2026-05-27 | 文首主题：2026-05-27 TRUST-B Codex Lane Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-b-synthesis.md` | 2026-05-27 | 文首主题：TRUST-B Synthesis。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-chain-ab-claude.md` | 2026-05-27 | 文首主题：信任链 A+B 合一 (UX 面板字段 + lite ed25519 签名) — Claude lane plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-chain-ab-codex.md` | 2026-05-27 | 文首主题：2026-05-27 信任链 A+B 合一切片 Codex Lane Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-chain-ab-synthesis.md` | 2026-05-27 | 文首主题：信任链 A+B 合一 — Synthesis。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-27-trust-chain-simplification-codex.md` | 2026-05-27 | 文首主题：2026-05-27 trust-chain simplification Codex evaluation plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-28-trust-b-3-codex.md` | 2026-05-28 | 文首主题：2026-05-28 TRUST-B-3 Codex Implementation Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-28-trust-b-4-codex.md` | 2026-05-28 | 文首主题：2026-05-28 TRUST-B-4 Codex Implementation Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-28-trust-b-4-r1-fix-codex.md` | 2026-05-28 | 文首主题：2026-05-28 TRUST-B-4 R1 Fix Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-05-28-trust-b-5-docs-codex.md` | 2026-05-28 | 文首主题：2026-05-28 TRUST-B-5 docs closure Codex plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-06-02-f-audit-001-me-usage-codex.md` | 2026-06-02 | 文首主题：2026-06-02 F-AUDIT-001 Me Usage API Codex Plan。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/plans/2026-06-19-audit-ledger-crl.md` | 2026-06-19 | 文首主题：audit-ledger 验签忽略 key 吊销(CRL)修复(wave-2 审计 wy94u3tn9 最后一个 S1)。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/reviews/2026-07-11-cost-receipt-inputs-not-found-rootcause.md` | 2026-07-11 | 文首主题：cost-receipt「receipt inputs not found」根因定位 — 2026-07-11。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/process/reviews/TRUST-AB-summary.md` | 2026-05-28 | 文首主题：TRUST-A+B Summary。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/runbooks/p0-1-receipt-owner-isolation-runbook.md` | 2026-05-23 | 文首主题：P0-1 Receipt Owner Isolation — Migration Runbook。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/specs/trust-chain-user-verifiable-ledger.md` | 2026-05-28 | 文首主题：Trust Chain User-Verifiable Ledger — F-TRUST-001 Spec。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |
| `docs/specs/user-consumption-transparency.md` | 2026-05-19 | 文首主题：User Consumption Transparency — F-AUDIT-001 Spec。 | `CURRENT` | Owner 范围排除；`docs/10_RISK_REGISTER.md` | 专门处理族；本波不判删。 |

**节末统计**

- 当前唯一权威源：本波范围明确排除，不宣布新的唯一权威源；现有 spec、风险登记和代码留待专门治理波。
- 建议删除：0 份；建议保留：41 份。
- 需真读代码裁定：0 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.16 media 媒体

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/architecture/runtime-logic/media-task.md` | 2026-07-11 | 文首主题：media 任务生命周期 运行逻辑。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-audio-endpoints-codex.md` | 2026-06-04 | 文首主题：2026-06-04 Audio Endpoints Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-04-image-audio-slice1-codex.md` | 2026-06-04 | 文首主题：2026-06-04 image-audio-slice1 Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-async-media-task-slice1-codex.md` | 2026-06-06 | 文首主题：2026-06-06 Async Media Task Slice-1 Codex Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-24-media-claim-lease-fix.md` | 2026-06-24 | 文首主题：计划:修复 media claim 孤儿回收租约过短致长任务亏钱(S1)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-media-orphan-backcharge-codex.md` | 2026-07-05 | 文首主题：2026-07-05 media 孤儿追扣与 claim 状态机配合修 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-openai-realtime-audio-research.md` | 2026-07-10 | 文首主题：OpenAI Realtime 语音 — 形态存档 · 三镜对照 · HUAKAI 缺口与延后决策。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-f3b-media-compatible-console-codex.md` | 2026-07-12 | 文首主题：2026-07-12 F3b 媒体兼容端点专属控制台（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-12-frontend-playground-media-console-codex.md` | 2026-07-12 | 文首主题：2026-07-12 前端多协议调试台与媒体创建台 Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；媒体协议、上传、存储与厂商能力须逐调用链核实后建立领域 SSOT。
- 建议删除：0 份；建议保留：9 份。
- 需真读代码裁定：9 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.17 deployment 部署 / 运维

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/01_APPLOCKER_DEFENDER_RESOLUTION.md` | 2026-04-30 | 文首主题：01 — AppLocker / Defender / Smart App Control: Test Binary Block Resolution。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/dependency-policy.md` | 2026-05-15 | 文首主题：依赖许可证政策。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/deploy/go-live-readiness.md` | 2026-07-12 | 文首主题：HUAKAI 上线就绪说明(运营者视角)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/deploy/production-bootstrap.md` | 2026-07-05 | 文首主题：生产部署与首启引导(自用 / API-only)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/ops/2026-05-08-bedrock-anthropic-cli-setup.md` | 2026-05-08 | 文首主题：Anthropic CLI / Claude Code → AWS Bedrock 部署指南。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/ops/anthropic-prompt-cache-ttl.md` | 2026-07-12 | 文首主题：Anthropic 提示缓存 TTL。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/ops/remote-dev-setup.md` | 2026-05-19 | 文首主题：HUAKAI 远程开发环境（GCP Linux Server）接入指南。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-14-l1-prod-wiring-codex.md` | 2026-05-14 | 文首主题：2026-05-14 L1 production wiring Codex plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-01-s1-019-release-mode-codex.md` | 2026-06-01 | 文首主题：2026-06-01 S1-019 Release Mode Gate。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-03-auth006-bootstrap-ttl-codex.md` | 2026-06-03 | 文首主题：2026-06-03 auth006 bootstrap TTL。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-20-b0-email-gate-exclude-system-tenant.md` | 2026-06-20 | 文首主题：B0 修复:生产 email 门排除系统伪租户(tenant 0)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-23-deploy-no-domain-direct-option.md` | 2026-06-23 | 文首主题：计划:无域名 / IP 直连部署形态(作为选项,不强制域名)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-23-soften-email-gate.md` | 2026-06-23 | 文首主题：计划:production 邮箱门惰性化(软化为选项,默认不拦启动)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-15-mvp-launch-blockers.md` | 2026-07-15 | 文首主题：MVP 整体测试上线 · blocker 清单(三路真码摸底汇总)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-pre-launch-s1-verification.md` | 2026-07-11 | 文首主题：上线前 S1 现状核实 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/runbooks/upstream-policy-monitor-runbook.md` | 2026-05-16 | 文首主题：Upstream Policy Monitor Runbook。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无；部署说明与真实 compose/启动门/环境变量仍需逐项核实。
- 建议删除：0 份；建议保留：16 份。
- 需真读代码裁定：14 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.18 reference-decompositions 镜像调研 / 分解证据

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/decompositions/README.md` | 2026-04-28 | 文首主题：Decompositions。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_TEMPLATE.md` | 2026-04-28 | 文首主题：<reference> — <Feature title>。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C1-oneapi-channel-auto-disable.md` | 2026-04-29 | 文首主题：Critic Review of one-api Channel auto-disable on permanent-error pattern。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C2-portkey-streaming-handler.md` | 2026-04-29 | 文首主题：Critic Review of portkey Streaming response handler (SSE forwarder lifecycle)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C3-helicone-cost-routing.md` | 2026-04-29 | 文首主题：Critic Review of helicone Cheapest-provider routing + custom-rule routing。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C4-litellm-cooldown-retry.md` | 2026-04-29 | 文首主题：Critic Review of litellm Cooldown handler + retry policy hierarchy。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C5-newapi-cache-reasoning.md` | 2026-04-29 | 文首主题：Critic Review of new-api Cache-aware billing + reasoning-effort handling。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C6-aah-credential-vault.md` | 2026-04-29 | 文首主题：Critic Review of all-api-hub Multi-account credential vault + cross-source pr…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_critics/C7-envoy-topology.md` | 2026-04-29 | 文首主题：Critic Review of envoy-ai-gateway Outer/inner gateway topology + AI Route CRD。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/auth-token-codex.md` | 2026-04-29 | 文首主题：HUAKAI F-AUTH-001 Provider OAuth Token Refresh - Codex Parallel Pass。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/auth-token-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/auth-token-synthesis.md` | 2026-05-19 | 文首主题：Provider-Side OAuth Token Management — Synthesis (Source-Verified)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/channel-health.md` | 2026-05-19 | 文首主题：Cross-Cutting - Channel Health Auto-Disable。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/credential-acquisition.md` | 2026-05-19 | 文首主题：Cross-Cutting — Credential Acquisition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/observability-synthesis.md` | 2026-05-19 | 文首主题：Observability + Atomic Billing — Synthesis (Source-Verified)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/pool-selection-claude-v2.md` | 2026-05-19 | 文首主题：Provider Account Pool Selection — Claude v2 (Source-Verified Rewrite)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/pool-selection-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/pool-selection-claude.md` | 2026-05-19 | 文首主题：Provider Account Pool Selection — Claude's Independent Pass。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/pool-selection-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/pool-selection-codex.md` | 2026-05-19 | 文首主题：F-POOL-001 Provider Account Selection Algorithm (Codex pass)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/pool-selection-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/pool-selection-synthesis-v2.md` | 2026-05-19 | 文首主题：Provider Account Pool Selection — Synthesis v2 (Source-Verified)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/pool-selection-synthesis.md` | 2026-05-19 | 文首主题：Provider Account Pool Selection — Synthesis & Final Action Plan。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/protocol-translation-synthesis.md` | 2026-05-19 | 文首主题：Protocol Translation — Synthesis (Source-Verified, Regenerated as F-PROTO-002)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/quota-billing-claim-gate-claude.md` | 2026-05-19 | 文首主题：Quota Atomic Reservation + Billing Idempotent Claim Gate (Claude pass)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/quota-billing-claim-gate-codex.md` | 2026-04-28 | 文首主题：Quota Atomic Reservation + Billing Idempotent Claim Gate (Codex pass)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md` | 2026-05-19 | 文首主题：Quota Atomic Reservation + Billing Claim Gate — Synthesis & Final Action Plan。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/rate-limiting-codex.md` | 2026-04-29 | 文首主题：F-RATE-001 Rate Limiting + Cooldown Computation (Codex independent pass)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md` | 2026-05-19 | 文首主题：Rate Limiting + Cooldown — Synthesis (Source-Verified)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/streaming-forwarder-claude-v2.md` | 2026-05-19 | 文首主题：Streaming Forwarder + Usage Accounting — Claude v2 (Source-Verified Rewrite)。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/streaming-forwarder-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/streaming-forwarder-claude.md` | 2026-05-19 | 文首主题：Streaming Forwarder + Usage Accounting — Claude's Independent Pass。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/streaming-forwarder-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/streaming-forwarder-codex.md` | 2026-05-19 | 文首主题：F-GW-002 Streaming Forwarder + Usage Accounting - Codex Specifier Pass。 | `SUPERSEDED` | `docs/decompositions/_cross-cutting/streaming-forwarder-synthesis.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_cross-cutting/streaming-forwarder-synthesis.md` | 2026-06-11 | 文首主题：Streaming Forwarder + Usage Accounting — Synthesis (Source-Verified)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/user-auth-session.md` | 2026-05-19 | 文首主题：Cross-Cutting — User Authentication And Platform Session Boundary。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_cross-cutting/voucher-system.md` | 2026-05-19 | 文首主题：Voucher System Cross-Cutting Decomposition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_mechanism_questions/2026-04-30-external-architecture-comparison-codex.md` | 2026-04-30 | 文首主题：2026-04-30 外部架构提案 vs HUAKAI 当前状态 - Codex 独立评估。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_mechanism_questions/2026-04-30-five-axes-claude.md` | 2026-04-30 | 文首主题：5-Axis × 7-Projects Mechanism Question Matrix — Claude Independent Draft。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_mechanism_questions/2026-04-30-five-axes-codex.md` | 2026-04-30 | 文首主题：2026-04-30 Five-Axis Mechanism Question Matrix - Codex。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/_superseded-round1/all-api-hub-credential-vault-comparison-round1.md` | 2026-04-29 | 文首主题：All API Hub - Multi-Account Credential Vault + Cross-Source Comparison。 | `SUPERSEDED` | `docs/decompositions/all-api-hub/credential-vault-comparison-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/envoy-ai-gateway-topology-crd-round1.md` | 2026-04-29 | 文首主题：Envoy AI Gateway - Outer/Inner Gateway Topology + AI Route CRD。 | `SUPERSEDED` | `docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/helicone-cost-aware-routing-round1.md` | 2026-04-29 | 文首主题：Helicone - Cost-Aware Routing and Operator Rule Chains。 | `SUPERSEDED` | `docs/decompositions/helicone/cost-aware-routing-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/litellm-cooldown-retry-hierarchy-round1.md` | 2026-04-29 | 文首主题：LiteLLM - Cooldown Handler + Retry Policy Hierarchy。 | `SUPERSEDED` | `docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/new-api-cache-billing-reasoning-round1.md` | 2026-04-29 | 文首主题：New API - Cache-aware billing + reasoning-effort handling。 | `SUPERSEDED` | `docs/decompositions/new-api/cache-billing-reasoning-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/one-api-channel-auto-disable-round1.md` | 2026-04-29 | 文首主题：one-api - Channel Auto-Disable on Permanent-Error Pattern。 | `SUPERSEDED` | `docs/decompositions/one-api/channel-auto-disable-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round1/portkey-streaming-handler-round1.md` | 2026-04-29 | 文首主题：Portkey ／ Streaming Response Handler。 | `SUPERSEDED` | `docs/decompositions/portkey/streaming-handler-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/all-api-hub-credential-vault-comparison-round2.md` | 2026-04-29 | 文首主题：all-api-hub - Multi-account credential vault, comparison, and source recognit…。 | `SUPERSEDED` | `docs/decompositions/all-api-hub/credential-vault-comparison-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/envoy-ai-gateway-topology-crd-round2.md` | 2026-07-05 | 文首主题：1. WHY (motivation / context)。 | `SUPERSEDED` | `docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/helicone-cost-aware-routing-round2.md` | 2026-04-29 | 文首主题：helicone - Cost-aware routing chains, cost forecast, and tier vectors。 | `SUPERSEDED` | `docs/decompositions/helicone/cost-aware-routing-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/litellm-cooldown-retry-hierarchy-round2.md` | 2026-04-29 | 文首主题：LiteLLM - Cooldown Handler and Retry Policy Hierarchy。 | `SUPERSEDED` | `docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/new-api-cache-billing-reasoning-round2.md` | 2026-04-29 | 文首主题：new-api - Cache-aware billing buckets + reasoning-effort handling。 | `SUPERSEDED` | `docs/decompositions/new-api/cache-billing-reasoning-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/one-api-channel-auto-disable-round2.md` | 2026-04-29 | 文首主题：one-api - Channel auto-disable on permanent-error pattern。 | `SUPERSEDED` | `docs/decompositions/one-api/channel-auto-disable-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/_superseded-round2/portkey-streaming-handler-round2.md` | 2026-04-29 | 文首主题：portkey - Streaming response handler。 | `SUPERSEDED` | `docs/decompositions/portkey/streaming-handler-source-verified.md` | 受保护；即使有后继也不删。 |
| `docs/decompositions/all-api-hub/_INVENTORY.md` | 2026-04-28 | 文首主题：all-api-hub — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/all-api-hub/credential-vault-comparison-claude-deep.md` | 2026-04-29 | 文首主题：all-api-hub — Multi-Account Credential Vault + Cross-Source Comparison (Claud…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/all-api-hub/credential-vault-comparison-source-verified.md` | 2026-04-29 | 文首主题：all-api-hub credential vault / comparison decomposition - source verified R3。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/envoy-ai-gateway/_INVENTORY.md` | 2026-04-28 | 文首主题：envoy-ai-gateway — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/envoy-ai-gateway/topology-crd-claude-deep.md` | 2026-04-29 | 文首主题：envoy-ai-gateway — Outer/Inner Topology + AI Route CRD (Claude deep decomposi…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/envoy-ai-gateway/topology-crd-claude-draft.md` | 2026-04-29 | 文首主题：envoy-ai-gateway — Outer/Inner Gateway Topology + AI Route CRD (Claude draft)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md` | 2026-04-29 | 文首主题：envoy-ai-gateway topology and CRD reconciliation source-verified decomposition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/helicone/_INVENTORY.md` | 2026-04-28 | 文首主题：helicone — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/helicone/cost-aware-routing-claude-deep.md` | 2026-04-29 | 文首主题：helicone — Cost-Aware Routing + Custom Rule Chain (Claude deep decomposition)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/helicone/cost-aware-routing-claude-draft.md` | 2026-04-29 | 文首主题：helicone — Cost-Aware Routing + Custom Rule Routing (Claude draft)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/helicone/cost-aware-routing-source-verified.md` | 2026-04-29 | 文首主题：Helicone cost-aware routing + custom routing R3 source-verified decomposition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/helicone/observability-source-verified.md` | 2026-04-29 | 文首主题：1. Helicone Observability Ingestion Architecture。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/litellm/_INVENTORY.md` | 2026-04-28 | 文首主题：LiteLLM — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/litellm/_INVENTORY_codex.md` | 2026-04-28 | 文首主题：LiteLLM — Feature Inventory (Codex parallel take)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/litellm/cooldown-retry-hierarchy-claude-deep.md` | 2026-04-29 | 文首主题：litellm — Cooldown Handler + Retry Policy Hierarchy (Claude deep decompositio…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md` | 2026-04-29 | 文首主题：LiteLLM Cooldown Handler + Retry Policy Hierarchy - Round 3 Source-Verified。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/litellm/pool-fallback-source-verified.md` | 2026-04-29 | 文首主题：LiteLLM Pool / Fallback Source Verification for F-POOL-001。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/new-api/_INVENTORY.md` | 2026-05-19 | 文首主题：new-api — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/new-api/_INVENTORY_codex.md` | 2026-04-28 | 文首主题：new-api — Feature Inventory (Codex parallel take)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/new-api/cache-billing-reasoning-claude-deep.md` | 2026-04-29 | 文首主题：new-api — Cache-Aware Billing + Reasoning-Effort Handling (Claude deep decomp…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/new-api/cache-billing-reasoning-claude-draft.md` | 2026-04-29 | 文首主题：new-api — Cache-Aware Billing + Reasoning-Effort Pass-Through (Claude draft)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/new-api/cache-billing-reasoning-source-verified.md` | 2026-04-29 | 文首主题：new-api Cache-Aware Billing Buckets + Reasoning-Effort Handling。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/one-api/_INVENTORY.md` | 2026-04-28 | 文首主题：one-api — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/one-api/_INVENTORY_codex.md` | 2026-04-28 | 文首主题：one-api — Feature Inventory (Codex parallel take)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/one-api/channel-auto-disable-claude-deep.md` | 2026-04-29 | 文首主题：one-api — Channel Auto-Disable on Permanent-Error Pattern (Claude deep decomp…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/one-api/channel-auto-disable-source-verified.md` | 2026-04-29 | 文首主题：one-api Channel Auto-Disable Source-Verified Decomposition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/one-api/quota-billing-source-verified.md` | 2026-04-29 | 文首主题：one-api Quota + Billing Source Verification。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/portkey/_INVENTORY.md` | 2026-04-28 | 文首主题：Portkey — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/portkey/_INVENTORY_codex.md` | 2026-04-28 | 文首主题：Portkey — Feature Inventory (Codex parallel take)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/portkey/protocol-translation-source-verified.md` | 2026-04-29 | 文首主题：Portkey Protocol Translation - Source-Verified (F-PROTO-001 Cross-Reference)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/portkey/streaming-handler-claude-deep.md` | 2026-04-29 | 文首主题：portkey — Streaming Response Handler (Claude deep decomposition)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/portkey/streaming-handler-source-verified.md` | 2026-04-29 | 文首主题：Portkey Streaming Response Handler - Source-Verified Decomposition。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/_INVENTORY.md` | 2026-05-16 | 文首主题：Sub2API — Feature Inventory。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/auth-token-source-verified.md` | 2026-04-29 | 文首主题：Sub2API Provider-Side Auth + Token Refresh — Source-Verified (F-AUTH-001)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/layered-account-selection.md` | 2026-05-19 | 文首主题：Sub2API — Layered Provider Account Selection。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/observability-source-verified.md` | 2026-04-29 | 文首主题：Sub2API Observability + Atomic Billing — Source-Verified (F-OBS-001 + correct…。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/protocol-translation-source-verified.md` | 2026-05-19 | 文首主题：Sub2API Protocol Translation — Source-Verified (F-PROTO-001)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/protocol-translation.md` | 2026-04-28 | 文首主题：Sub2API — Protocol Translation Pipeline。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/rate-limiting-source-verified.md` | 2026-04-29 | 文首主题：Sub2API Rate Limiting — Source-Verified (F-RATE-001)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/decompositions/sub2api/streaming-forwarder.md` | 2026-05-19 | 文首主题：Sub2API — Streaming Forwarder Hot Path。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/evidence/cursor-real-traffic-template.md` | 2026-05-26 | 文首主题：Cursor IDE 真实流量证据采集模板 — C0 Slice。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-A.md` | 2026-05-21 | 文首主题：HUAKAI 全面自查 LANE A - 身份与账号。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-B.md` | 2026-05-21 | 文首主题：2026-05-21 Audit B - 网关与路由。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-C.md` | 2026-05-21 | 文首主题：2026-05-21 HUAKAI 全面自查 Lane C：钱与信任。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-D.md` | 2026-05-21 | 文首主题：2026-05-21 HUAKAI 全面自查 Lane D：运维与安全。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-E.md` | 2026-05-21 | 文首主题：2026-05-21 HUAKAI 全面自查 Lane E。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-audit-w1-phase1-retry-failover.md` | 2026-05-21 | 文首主题：2026-05-21 W1 Phase 1 retry/failover 回溯审计。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-full-audit-tree.md` | 2026-05-21 | 文首主题：HUAKAI 全面自查 —— 标满状态的功能树 + parity 缺失总表。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-hole3-anthropic-buffered-refcompare.md` | 2026-05-21 | 文首主题：2026-05-21 洞3 Anthropic Messages buffered 响应参照对比。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-juice-dig-techniques-sonnet.md` | 2026-05-21 | 文首主题：HUAKAI juice 检测技术深度调研报告。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-juice-market-survey.md` | 2026-05-21 | 文首主题：Juice 功能市场调研报告 — 模型算力/质量检测专项。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-juice-model-degradation-detection.md` | 2026-05-21 | 文首主题：Juice / 模型降算力检测 调研报告。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-juice-transparency-refcompare.md` | 2026-05-21 | 文首主题：2026-05-21 juice 透明版模型映射/替换透明度参照对比。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-21-juice-web-crawl-codex.md` | 2026-05-21 | 文首主题：LLM 模型降算力 / 静默替换 / 模型验真全网调研。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-deep-audit-billing-proto.md` | 2026-05-22 | 文首主题：2026-05-22 deep audit: billing/proto/eventbus/auditledger。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-deep-audit-gatewayhttp.md` | 2026-05-22 | 文首主题：2026-05-22 深度审计 — Zone A: gatewayhttp(HTTP 层)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-deep-audit-routing-auth.md` | 2026-05-22 | 文首主题：2026-05-22 routing/auth 深度代码审计。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-deep-audit-rust.md` | 2026-05-22 | 文首主题：2026-05-22 Rust Core Gateway 深度审计。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-external-ai-critique-eval.md` | 2026-05-22 | 文首主题：2026-05-22 外部 AI 对比 critique — Claude+codex 交叉评估。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-w1-ref-recompare.md` | 2026-05-22 | 文首主题：W1 closeout reference-recompare（specifier lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-w2-ref-recompare.md` | 2026-05-22 | 文首主题：2026-05-22 W2 收尾对照 — L2 精确响应缓存键 vs 参照项目。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-w3-ref-recompare.md` | 2026-05-22 | 文首主题：2026-05-22 W3 收尾对照 — 公开错误安全模型 vs 参照项目。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-worktree-audit-claude.md` | 2026-05-22 | 文首主题：2026-05-22 HUAKAI 工作树结构审计 — Claude 独立稿。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-22-worktree-audit-codex.md` | 2026-05-22 | 文首主题：2026-05-22 HUAKAI work-tree 结构审计（Codex 独立版）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-23-owner-cloud-review-verification.md` | 2026-05-23 | 文首主题：Owner Cloud Review Source Verification。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-23-owner-cloud-review.md` | 2026-05-23 | 文首主题：source: "Owner chat 2026-05-23"。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-23-receipt-owner-isolation-prestudy.md` | 2026-05-23 | 文首主题：2026-05-23 Receipt Owner Isolation Prestudy。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-23-w4-ref-recompare.md` | 2026-05-23 | 文首主题：W4 收尾对照参照项目 trust ledger / audit chain。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/process/research/2026-05-23-w5-ref-prestudy.md` | 2026-05-23 | 文首主题：W5 参考项目 prestudy — audit 原子化模式。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/_INDEX.md` | 2026-05-02 | 文首主题：Reference feature delta index。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md` | 2026-05-19 | 文首主题：Account-to-API mainline audit。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/ai-gateway.md` | 2026-05-02 | 文首主题：ai-gateway reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/all-api-hub.md` | 2026-05-02 | 文首主题：All API Hub reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/claude-reviewer-notes.md` | 2026-05-19 | 文首主题：Claude reviewer-lane synthesis on Codex's reference deep dives。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/codename-mapping.md` | 2026-05-19 | 文首主题：HUAKAI 内部代号 ↔ 公开项目名 映射表。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/feature-backlog-insertions.md` | 2026-05-02 | 文首主题：Feature backlog insertions。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/framing-revision-competitive.md` | 2026-05-19 | 文首主题：Framing 修正：HUAKAI 竞赛立场（Owner 2026-05-02）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/helicone.md` | 2026-05-02 | 文首主题：Helicone reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/huakai-creative-strengthening.md` | 2026-05-03 | 文首主题：HUAKAI creative strengthening — beyond fusion (Owner critique 2026-05-02)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/huakai-fusion-and-strengthening.md` | 2026-05-03 | 文首主题：HUAKAI fusion + strengthening strategy。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/litellm.md` | 2026-05-02 | 文首主题：LiteLLM reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/new-api.md` | 2026-05-02 | 文首主题：New API reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/one-api.md` | 2026-05-02 | 文首主题：one-api reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/portkey-gateway.md` | 2026-05-02 | 文首主题：Portkey Gateway reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/readme-ack-draft.md` | 2026-05-03 | 文首主题：README "Reference Projects & Usage Acknowledgement" — draft。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-02/sub2api.md` | 2026-05-02 | 文首主题：Sub2API reference delta。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/reference_delta/2026-05-06/vendor-drift-audit.md` | 2026-05-06 | 文首主题：Vendor Documentation Drift Audit — 2026-05-06。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-08-cache-hit-audit-codex-lane.md` | 2026-05-08 | 文首主题：HUAKAI cache hit-rate audit。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-08-cache-hit-audit.md` | 2026-05-08 | 文首主题：2026-05-08 HUAKAI Cache Hit-Rate Audit (sonnet lane)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-08-client-shape-evidence.md` | 2026-05-08 | 文首主题：客户端 wire-shape 证据表 (HUAKAI Upgrade #6 U6-D-1)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-axis3-huakai-current-state.md` | 2026-05-09 | 文首主题：HUAKAI Axis-3 (协议转换) Current State Audit (no upstream reads)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-axis3-protocol-translation-envoy.md` | 2026-05-09 | 文首主题：Axis-3 协议翻译 — envoy-ai-gateway 源读 (specifier lane)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-axis3-protocol-translation-litellm.md` | 2026-05-09 | 文首主题：LiteLLM Protocol Translation Mechanism — Specifier Lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-axis3-protocol-translation-portkey.md` | 2026-05-09 | 文首主题：Portkey Gateway — Axis 3 Protocol Translation Specifier Read。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-codex-feature-parity-audit.md` | 2026-05-09 | 文首主题：Codex Feature Parity Missing-Feature Audit — 2026-05-09。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-codex-full-renew-pass.md` | 2026-05-09 | 文首主题：Codex Full-Chain Renew Review — 2026-05-09 P-0 + P-0c plan。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-helicone-chain-reverify.md` | 2026-05-09 | 文首主题：Helicone Chain-of-Responsibility Reverification。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-issue-mining-cross-repo.md` | 2026-05-09 | 文首主题：Reference Project Issues 调研 — Cross-Repo Analysis。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-market-research-claude.md` | 2026-05-09 | 文首主题：AI 网关市场独立调研 — Claude (sonnet) Lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-market-research-codex.md` | 2026-05-09 | 文首主题：AI 网关市场独立调研 — Codex Lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md` | 2026-05-09 | 文首主题：Source-read research: Helicone, Envoy AI Gateway, All API Hub。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md` | 2026-05-09 | 文首主题：Source Read: one-api / Portkey-AI gateway / LiteLLM。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-source-read-sub2api-newapi.md` | 2026-05-09 | 文首主题：Source-Read Summary: sub2api + new-api (specifier lane)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-09-uncommitted-changes-review-sonnet.md` | 2026-05-09 | 文首主题：Uncommitted Changes Review — Sonnet Lane (Codex sandbox 挂掉的 backup)。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-frontend-brief-huakai-summary.md` | 2026-05-12 | 文首主题：HUAKAI 前端工程 Brief — Gemini 项目总览（2026-05-12）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-frontend-brief-market-codex.md` | 2026-05-12 | 文首主题：2026-05-12 HUAKAI 前端市场 brief（Codex lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-frontend-brief-market-sonnet.md` | 2026-05-12 | 文首主题：HUAKAI 前端市场调研 brief — Sonnet lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-review-codex.md` | 2026-05-12 | 文首主题：2026-05-12 Gemini P1 Dashboard Codex Lane Review。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-review-sonnet.md` | 2026-05-12 | 文首主题：Gemini P1 Dashboard 原型 — Sonnet UX/Usability/A11y Review。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round2-review-codex.md` | 2026-05-12 | 文首主题：2026-05-12 Gemini P1 Dashboard Round 2 Codex Review。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round2-review-sonnet.md` | 2026-05-12 | 文首主题：Gemini P1 Dashboard 原型 — Round 2 Sonnet UX/A11y Read-Only Verify。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round3-review-codex.md` | 2026-05-12 | 文首主题：2026-05-12 Gemini P1 Dashboard Round 3 Codex Review。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round3-review-sonnet.md` | 2026-05-12 | 文首主题：Gemini P1 Dashboard 原型 — Round 3 Sonnet 收尾 Verify。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round3open-review-codex.md` | 2026-05-12 | 文首主题：2026-05-12 Gemini P1 Dashboard Round 3 Open-Brief Codex Review。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-gemini-p1-round3open-review-sonnet.md` | 2026-05-12 | 文首主题：Gemini P1 Dashboard — Round 3 Open-Brief Sonnet UX/A11y Read-Only Verify。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-12-sub2api-frontend-decomposition.md` | 2026-05-12 | 文首主题：Sub2API Frontend UI 风格 Decomposition 文档。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：2026-05-13 all-api-hub 目录骨架深挖（Codex lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-frontend-feature-parity-sub2api-vs-round10-codex.md` | 2026-05-13 | 文首主题：2026-05-13 HUAKAI Round 10 Dashboard vs sub2api Dashboard 功能展示差距清单。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-frontend-ui-aesthetic-codex.md` | 2026-05-13 | 文首主题：2026-05-13 HUAKAI 前端 UI 美学调研 - Codex。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-frontend-ui-aesthetic-v2-codex.md` | 2026-05-13 | 文首主题：2026-05-13 HUAKAI 前端 UI 美学调研 v2 - Codex。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-frontend-ui-aesthetic-v3-codex.md` | 2026-05-13 | 文首主题：2026-05-13 HUAKAI 前端 UI 美学调研 v3 - Codex。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-helicone-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：2026-05-13 Helicone 目录骨架拆解（Codex Lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-litellm-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：LiteLLM T1 顶层目录骨架拆解（Codex lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-new-api-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：2026-05-13 new-api 顶层目录骨架拆解（Codex lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-portkey-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：2026-05-13 Portkey Dir Skeleton Codex Lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-ref-borrow-gap-matrix-codex.md` | 2026-05-13 | 文首主题：HUAKAI ref 项目借鉴 vs 缺失 gap analysis 总表（Codex）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-sub2api-dir-skeleton-codex.md` | 2026-05-13 | 文首主题：2026-05-13 sub2api 目录骨架深挖（Codex lane）。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-13-trust-chain-github-survey-codex.md` | 2026-05-13 | 文首主题：HUAKAI trust-chain GitHub survey - Codex lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-14-codex-cli-request-signature-codex.md` | 2026-05-14 | 文首主题：2026-05-14 openai/codex CLI 请求签名源码分析。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-14-gemini-cli-request-signature.md` | 2026-05-14 | 文首主题：2026-05-14 Gemini CLI 请求签名抓包分析。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-14-kiro-cli-request-signature.md` | 2026-05-14 | 文首主题：2026-05-14 kiro CLI 请求签名抓包分析。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-14-vendor-login-mechanisms.md` | 2026-05-14 | 文首主题：2026-05-14 三 vendor 登录机制汇总。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-16-anti-detection-project-deep-verify-sonnet.md` | 2026-05-16 | 文首主题：反封禁 Top 5 项目 Deep Verify Report。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |
| `docs/research/2026-05-16-vendor-fingerprint-data-sonnet.md` | 2026-05-16 | 文首主题：Vendor Fingerprint Data — Sonnet Lane。 | `CURRENT` | Owner 范围排除 | 受保护证据；本波不归并。 |

**节末统计**

- 当前唯一权威源：不新建归并 SSOT；该族是 Owner 明确保留的原始研究、镜像分解与 clean-room 证据集合。
- 建议删除：0 份；建议保留：182 份。
- 需真读代码裁定：0 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。
- 受保护但有明确后继：24 份；不计入建议删除。

### 4.19 database-schema 数据库 / schema

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/19_DOMAIN_MODEL.md` | 2026-05-19 | 文首主题：Domain Model。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/architecture/deprecated-schema.md` | 2026-07-14 | 文首主题：无运行时消费的 schema 保留清单。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/decisions/DR-001-multi-tenancy.md` | 2026-05-19 | 文首主题：DR-001: Tenant-Aware Schema From Day 1。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/decisions/DR-006-database.md` | 2026-05-19 | 文首主题：DR-006: Database。 | `CURRENT` | 本文件即决策出处 | 正式决策记录。 |
| `docs/process/plans/2026-04-30-n4-l0-minimum-claude.md` | 2026-04-30 | 文首主题：2026-04-30 N+4 — L0 Minimum：apikeys + users schema + 退役 SmokeAuthResolver。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-n4-l0-minimum.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-p0-schema-spec-claude.md` | 2026-05-09 | 文首主题：P-0 Schema Spec — HCSFEnvelope v0.4 Go Type 锁定 (Claude Lane)。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-p0-schema-spec-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-p0-schema-spec-codex.md` | 2026-05-09 | 文首主题：P-0 Schema Spec — HCSFEnvelope v0.4 Go Type 锁定 (Codex Lane)。 | `SUPERSEDED` | `docs/process/plans/2026-05-09-p0-schema-spec-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-09-p0-schema-spec-synthesis.md` | 2026-05-09 | 文首主题：P-0 Schema Spec — Claude × Codex Synthesis。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-19-wave-3c-db-subpackages-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Wave 3-C db sqlc subpackages。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-auth-expired-schema-gate-claude.md` | 2026-05-24 | 文首主题：Auth Expired Outcome Schema Gate — Claude Lane Plan。 | `SUPERSEDED` | `docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-05-24-auth-expired-schema-gate-codex.md` | 2026-05-24 | 文首主题：2026-05-24 authexpired outcome schema gate — Codex lane plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md` | 2026-05-24 | 文首主题：Auth Expired Outcome Schema Gate — Synthesis (Claude × Codex)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-06-23-soften-auto-migrate.md` | 2026-06-23 | 文首主题：计划:迁移加 HUAKAIAUTOMIGRATE 单机自迁移开关(默认关)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-13-sub2-seed-map-codex.md` | 2026-07-13 | 文首主题：2026-07-13 sub2 测试数据映射 SQL（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p0a-sql-stopgap-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P0-a SQL 层两处止血修复（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-30-env-config-to-admin-settings-backlog.md` | 2026-06-30 | 文首主题：env 配置 → 后台设置 迁移 backlog(2026-06-30)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/schema/README.md` | 2026-05-19 | 文首主题：Phase 2 Schema Lock。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |

**节末统计**

- 当前唯一权威源：无单一 Markdown SSOT；迁移与查询代码才是事实基线，散文档须在后续波逐项对照。
- 建议删除：4 份；建议保留：13 份。
- 需真读代码裁定：8 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

### 4.20 testing-release-quality 测试 / 评审 / 发布质量

| 文件路径 | 日期 | 主题一句话 | 状态 | 取代者/决策出处 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `docs/dev-tests.md` | 2026-06-26 | 文首主题：Backend 测试矩阵。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/feature-tree/coverage-verification.md` | 2026-06-03 | 文首主题：Coverage Verification — Gap Design vs Real Backend Code。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/gateway-core.md` | 2026-06-23 | 文首主题：Gateway-Core Feature Tree。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/feature-tree/security-abuse.md` | 2026-06-03 | 文首主题：Security-Abuse Feature Tree — HUAKAI AI Gateway。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/gap-critiques/per-key-controls.md` | 2026-06-03 | 文首主题：Adversarial Review: per-key-controls.md。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-04-30-phase-c-gateway-wiring-claude.md` | 2026-04-30 | 文首主题：2026-04-30 Phase C — Gateway DI wiring + first real /v1/chat/completions。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-phase-c-gateway-wiring.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-phase-c-gateway-wiring-codex.md` | 2026-04-30 | 文首主题：2026-04-30 Phase C Gateway Wiring - Codex Independent Plan。 | `SUPERSEDED` | `docs/process/plans/2026-04-30-phase-c-gateway-wiring.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-04-30-phase-c-gateway-wiring.md` | 2026-04-30 | 文首主题：2026-04-30 Phase C — Gateway Wiring (synthesized)。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/plans/2026-05-08-upgrade1-u1a-prereview-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #1 U1-A PRE-REVIEW。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-08-upgrade5-u5a-prereview-codex.md` | 2026-05-08 | 文首主题：HUAKAI Upgrade #5 U5-A PRE-REVIEW。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-09-sprint-c-day1-red-tests-spec.md` | 2026-05-09 | 文首主题：Sprint C Day 1 — Red Tests Spec。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-test-strategy-codex-prompt.md` | 2026-05-12 | 文首主题：HUAKAI 项目测试方案起草 — Codex Lane。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-12-test-strategy-codex.md` | 2026-05-12 | 文首主题：2026-05-12 HUAKAI 测试策略（Codex）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-15-per-commit-review-fix-codex.md` | 2026-05-15 | 文首主题：2026-05-15 per-commit review fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-f-cred-phase-b-code-review-fix-codex.md` | 2026-05-16 | 文首主题：2026-05-16 F-CRED Phase B Code Review Fix。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-16-post-deep-review-p0-wave-plan-claude.md` | 2026-05-16 | 文首主题：Post Deep-Review P0 Wave Plan (Claude PM Lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-17-codex-review-fixes-claude.md` | 2026-05-17 | 文首主题：Codex Review 修复批 — Claude 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-p2-2-refund-receipt-sink-codex.md` | 2026-05-18 | 文首主题：2026-05-18 P2#2 Refund Receipt Sink Gateway Wire。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-18-wave7-audit-refund-review-fixes-codex.md` | 2026-05-18 | 文首主题：2026-05-18 wave7 audit refund review fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-adminhttp-handler-tests-codex.md` | 2026-05-19 | 文首主题：2026-05-19 adminhttp handler tests codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-codex-review-v5-p1-cleanup-claude.md` | 2026-05-19 | 文首主题：2026-05-19 Codex Review v5 P1 Cleanup — Claude 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-codex-review-v5-p1-cleanup-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Codex Review v5 P1 Cleanup — Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-codex-review-v6-cleanup-codex.md` | 2026-05-19 | 文首主题：2026-05-19 codex review v6 cleanup codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-codex-review-v7-cleanup-codex.md` | 2026-05-19 | 文首主题：2026-05-19 codex-review-v7-cleanup-codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-go-test-infra-windows-python-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Go Test Infra Windows Python Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-group-c-review1-refund-gateway-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Group C Review 1 Refund Gateway Fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-group-c-review3-p2-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Group C Review 3 P2 Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-19-wave-2b-gateway-main-refactor-codex.md` | 2026-05-19 | 文首主题：2026-05-19 Wave 2-B gateway main refactor。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-renew-aggregate-endpoint-claude.md` | 2026-05-20 | 文首主题：/renew 方案 B — 跨租户续期状态聚合端点 · Claude 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-20-renew-aggregate-endpoint-codex.md` | 2026-05-20 | 文首主题：2026-05-20 renew aggregate endpoint Codex 独立执行计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-25-core-gateway-listener-test-bind-codex.md` | 2026-05-25 | 文首主题：2026-05-25 coregateway listener test bind remediation - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-05-26-slice-2-2-b-gateway-round2-s1-fixes-codex.md` | 2026-05-26 | 文首主题：2026-05-26 Slice 2.2.b Gateway Round 2 S1 fixes。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-02-f-priv-001-killrawlog-codex.md` | 2026-06-02 | 文首主题：F-PRIV-001 Kill Raw Gateway Logs Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-06-performance-gate-v2-codex.md` | 2026-06-06 | 文首主题：Performance Gate v2 Implementation Plan。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-06-26-promo-gate-a.md` | 2026-06-26 | 文首主题：promo 兑换码总开关(rank5,Owner 选 A 方案)— 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-comment-cn-gateway-gatewayhttp-codex.md` | 2026-07-05 | 文首主题：2026-07-05 存量英文注释转中文首批。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-05-hunyuan-upstream-e2e-codex.md` | 2026-07-05 | 文首主题：2026-07-05 混元真实上游 E2E 测试。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-codex-account-nonstream-aggregate-slice2a-codex.md` | 2026-07-06 | 文首主题：2026-07-06 codex 账号非流式聚合片2a Codex 计划。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-06-slice2c-hardening-codex.md` | 2026-07-06 | 文首主题：2026-07-06 片2c 加固补测试与流式 loss 修。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-07-chat-messages-codex-live-e2e-codex.md` | 2026-07-07 | 文首主题：2026-07-07 chat/messages 到 codex live e2e 子测试。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-10-r0-serving-capability-review-fixes-codex.md` | 2026-07-10 | 文首主题：2026-07-10 R0 serving capability 提交前 review 修复（Codex 独立计划）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p2b-channel-rewrite-gates-codex.md` | 2026-07-14 | 文首主题：2026-07-14 P2-b 渠道三门写口通电（Codex 独立计划）。 | `SUPERSEDED` | `docs/process/plans/2026-07-14-p2b-channel-rewrite-gates.md` | 并行/独立草案；已有指名后继。 |
| `docs/process/plans/2026-07-14-p2b-channel-rewrite-gates-independent-2.md` | 2026-07-14 | 文首主题：2026-07-14 P2-b 渠道三门写口通电独立计划（Codex independent-2）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/plans/2026-07-14-p2b-channel-rewrite-gates.md` | 2026-07-14 | 文首主题：2026-07-14 P2-b 渠道三门写口通电（合并权威计划）。 | `CURRENT` | 本文件的 synthesis / Owner gate | 综合稿或 Owner-gated；不得按过期处理。 |
| `docs/process/reviews/2026-04-28-claude-reviews-codex-phase1-v2.md` | 2026-04-28 | 文首主题：Review v2: Claude reviewing Codex Phase 1 outputs (deeper pass)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-claude-reviews-codex-phase1.md` | 2026-04-28 | 文首主题：Review: Claude reviewing Codex Phase 1 outputs。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-codex-auth-token-synthesis-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-AUTH-001 Auth Token Synthesis。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-04-28-codex-pool-synthesis-v2-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-POOL-001 Synthesis v2。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-04-28-codex-reviewer-cycle1-cycle2-cl011.md` | 2026-04-28 | 文首主题：Codex Reviewer-Lane Report — Cycle 1+2 Source-Verified Rewrites。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-codex-reviews-claude-phase1.md` | 2026-04-28 | 文首主题：Review: Codex reviewing Claude Phase 1 outputs。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-codex-source-verification.md` | 2026-04-28 | 文首主题：1. Codex Self-Verification。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-28-codex-streaming-forwarder-synthesis-final-review.md` | 2026-04-28 | 文首主题：Codex Final Reviewer-Lane Report - F-GW-002 Streaming Forwarder Synthesis。 | `HISTORICAL-DELETE` | 无；Claude 删除前核验 | final-review 过程候选；未授权删除。 |
| `docs/process/reviews/2026-04-28-source-truth-corrections.md` | 2026-04-28 | 文首主题：Source-Truth Corrections — F-POOL-001 + F-GW-002。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-29-codex-validation-of-self-audit.md` | 2026-04-29 | 文首主题：Codex Validation of Claude's 2026-04-29 Self-Audit。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-29-self-audit-after-slice-4.md` | 2026-04-29 | 文首主题：2026-04-29 Self-Audit — After Slice 4 (before slice 5 commit)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-29-slice-1-2-coverage-audit.md` | 2026-04-29 | 文首主题：2026-04-29 Codex Reviewer-Lane Audit: Slice 1 + 2 Acceptance-Test Coverage。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-29-slice-1-2-rereview-post-fix.md` | 2026-04-29 | 文首主题：2026-04-29 Codex Reviewer-Lane RE-Review (post-fix)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-04-29-slice-4-coverage-audit.md` | 2026-04-29 | 文首主题：2026-04-29 Codex Reviewer-Lane Audit: Slice 4 (F-GW-002)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-f-cred-001-preservation-sonnet-review.md` | 2026-05-15 | 文首主题：F-CRED-001 / F-AUTH-005 Feature Preservation Review (Sonnet lane)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-high-risks-mitigation-claude.md` | 2026-05-15 | 文首主题：2026-05-15 HIGH Risks (R-SEC-002 / R-TRANSPORT-001 / R-LIC-003) Mitigation Cl…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-high-risks-mitigation-codex.md` | 2026-05-15 | 文首主题：2026-05-15 HIGH Risks Mitigation Convergence - Codex。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-l2-lane2-retrospective-bulk-codex-review.md` | 2026-05-15 | 文首主题：R-C Lane 2 Retrospective Bulk Cross-Review。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-mandatory-roadmap-priority-claude.md` | 2026-05-15 | 文首主题：2026-05-15 Mandatory Roadmap Priority Claude 平行评审。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-mandatory-roadmap-priority-codex.md` | 2026-05-15 | 文首主题：2026-05-15 Mandatory Roadmap Priority Review (Codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-round1-cross-discuss-synthesis.md` | 2026-05-15 | 文首主题：2026-05-15 Round 1 Cross-Discuss Synthesis (Claude × Codex)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-round2-cross-discuss-synthesis.md` | 2026-05-15 | 文首主题：2026-05-15 Round 2 Cross-Discuss Synthesis (Claude × Codex — Round 2-B 5 Go f…。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-15-round2b-e2e-smoke-verification.md` | 2026-05-15 | 文首主题：2026-05-15 Round 2-B 5 features E2E module smoke verification。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-05-27-rust-hardening-c942a27-codex-review.md` | 2026-05-27 | 文首主题：Rust hardening c942a27 Codex review。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-16-cross-module-wiring-audit.md` | 2026-06-16 | 文首主题：跨模块接入协作逻辑审计 — 2026-06-16。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-16-thirdparty-claim-verification.md` | 2026-06-16 | 文首主题：第三方 AI 论断核实 — 「治理债 >> 实现债 / 可交付产品严重滞后」。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-17-account-to-api-deep-audit.md` | 2026-06-17 | 文首主题：账号→API 转换流水线 深度审计（account→API conversion pipeline）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-17-backend-completeness-audit.md` | 2026-06-17 | 文首主题：后端功能完整性审计（2026-06-17）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-24-backend-renew-codex-review.md` | 2026-06-24 | 文首主题：后端 renew 代码质量与架构审查（Codex，2026-06-24）。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-06-30-closed-loop-modules-e2e-audit.md` | 2026-06-30 | 文首主题：已闭环模块·端到端逻辑审计(2026-06-30)。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-10-two-module-remediation-status.md` | 2026-07-10 | 文首主题：两模块闭环 remediation 状态说明(账号转API relay + 官方API)— 2026-07-10。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/2026-07-11-E2E-real-upstream-result.md` | 2026-07-11 | 文首主题：端到端全链路真上游 E2E 结果 — 2026-07-11。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/process/reviews/DEFERRED-S1-015-lease-sweep-isolation.md` | 2026-05-29 | 文首主题：DEFERRED — S1-015-fu piece B：lease-sweep 测试隔离。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-S1-029-provisional-reconcile.md` | 2026-06-01 | 文首主题：S1-029 — streaming provisional cost fix (LANDED) + no-usage reconcile worker …。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-S2-163-tokencheck-wire.md` | 2026-05-29 | 文首主题：DEFERRED — S2-163 token cross-check wiring (follow-up)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-apikey-expiry-followups.md` | 2026-06-18 | 文首主题：DEFERRED — API-key expiry 更新写路径 (PR pending) 审查后续。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-audit-outcome-severity-mapping.md` | 2026-05-24 | 文首主题：DEFERRED — audit outcome severity mapping for new 4-class outcomes。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-bughunt-tail-2026-06-24.md` | 2026-06-24 | 文首主题：对抗猎 bug 尾部延后项(2026-06-24)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-burst-33-39.md` | 2026-06-18 | 文首主题：Deferred follow-ups — 跨切片整合审计 (burst PR #33–39)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-capability-binding-followups.md` | 2026-06-18 | 文首主题：Deferred follow-ups — capability-binding 切片 + inert-gap 死字段 roadmap。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-inherit-catalog-followups.md` | 2026-06-18 | 文首主题：DEFERRED — inheritglobalcatalog 写端点 (PR #43) 审查后续。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-logfacade.md` | 2026-07-03 | 文首主题：DEFERRED — logfacade(slog 门面统一片 D)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-quality-gate-baseline-2026-06-25.md` | 2026-06-25 | 文首主题：DEFERRED:quality-gate baseline 存量债与排清计划(2026-06-25)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-r0-serving-capability.md` | 2026-07-10 | 文首主题：R0 serving capability 延后 review 事项。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/DEFERRED-s2-045-storm-precision.md` | 2026-05-30 | 文首主题：DEFERRED — S2-045 storm-precision refinements (codex R2, both S2)。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/process/reviews/SLICE0-baseline-clean.md` | 2026-05-25 | 文首主题：Slice 0 Baseline Clean Gate。 | `NEEDS-CODE-VERIFY` | `docs/10_RISK_REGISTER.md` + 相关 DR + 实现代码 | 后续必须真读代码与决策链。 |
| `docs/specs/_REVIEW_CHECKLIST.md` | 2026-05-19 | 文首主题：Spec-Leakage Review Checklist。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |
| `docs/templates/codex-reviewer.md` | 2026-04-29 | 文首主题：Codex Reviewer-Lane Prompt Template。 | `CURRENT` | 本文件 / 治理入口 | 当前治理/契约/模板入口。 |

**节末统计**

- 当前唯一权威源：无；`docs/11_ACCEPTANCE_TEST_MATRIX.md` 与 `docs/15_RELEASE_GATES.md` 是治理入口，但不能替代测试代码事实。
- 建议删除：6 份；建议保留：86 份。
- 需真读代码裁定：68 份，即本节表内全部 `NEEDS-CODE-VERIFY` 行。

## 5. 后续停线门

- 本波结束时 `git rm` 次数必须为 0；本文件中的建议删除计数不授权执行删除。
- 下一波先由 Claude 按领域核 manifest、纠正领域或状态；再选一个领域逐文档真读代码并建立代码核实过的 SSOT。
- 某领域形成实际删除清单后，必须再次停下交 Claude 逐份亲检；未收到明确核准，不得删除。
- 任何条目若命中 Owner-gated、DEFERRED、Mandatory Roadmap、TODO、未启用、未接线或风险登记，默认保留并升级人工裁定。
- 后续关键实现断言必须附亲自读过的 `file:line`，且需追到实际调用/装配链；搜索工具只能定位，不能作真假证据。

## 6. 本波审计结论

- 原始 1272 份 Markdown 全部进入分类母集，无漏项；本 manifest 另有自记录 1 行。
- 本波仅修改本文件，没有删除、移动或改写任何既有文档，也没有修改代码逻辑。
- 当前建议删除候选 159 份；建议保留/待核验 1113 份。候选集中 175 份状态为 `SUPERSEDED`（其中 24 份受保护、不得删），8 份为明确 final-review 过程候选。
- 最大未决量是 739 份；这正是后续逐领域真读实现代码的工作队列，本波不伪装成已核实。
