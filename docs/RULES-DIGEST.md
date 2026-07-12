# HUAKAI 项目规则全集

> 本文件汇总 HUAKAI 项目里 Owner 制定的**全部规则**,来源:`AGENTS.md`、`CLAUDE.md`、`docs/RULES.md`(规则清单宪法)、`docs/00-MASTER-PLAN.md`,以及 PM 记忆里的 Owner 口头准则。
> **规则原文以来源文件为权威**;本文件是导航 + 去重 + 把「被最新指令覆盖的旧规则」标出来。
> 整理日期:2026-06-11(当前 landing = `fix/h-fixes`)。**冲突时:后面的日期覆盖前面的;§16 列出所有已被覆盖的旧规则。**

---

## 0. 北极星目标(绝对约束)
| ID | 规则 |
|---|---|
| G-001 | 商业目的=赚钱(成功后再开源);不接受「降低真实度换速度」 |
| G-002 | 在 Sub2API 基础上做**更全面更好**;接入广度=差异化护城河 |
| G-003 | **必须真实**——inventory≠理解,spec≠实现;禁虚标 |
| G-004 | 慢无所谓(250-500 工程小时预期);不缩 scope,只加并行 |
| G-005 | 持续追踪上游更新→自审→修 |
| G-006 | 两商业模式并行:Personal(卖 API)+ SaaS(卖给运营方) |

## 1. 启动门 + 风险分级
| ID | 规则 |
|---|---|
| S-001 | Owner 未给「开始/proceed/开干」信号前,不推进实现 |
| S-002 | 给信号后主动跑:**低险直接做 / 中险记录原因继续 / 高险停下问** |
| 风险-高危清单 | LICENSE、生产密钥、真实凭据、支付逻辑、认证核心、计费账本、配额执行、DB schema、部署脚本、破坏性迁移、删文件、新增运行时依赖、生产部署 = 高危,改前问 Owner |
| 不过度阻塞 | 规则若挡住真实产品需求:解释冲突→提安全路径→能则用 safe equivalent 继续→高危部分标记待确认→**绝不静默删功能** |

## 2. Clean-room / 许可证 / 禁抄袭(永久)
| ID | 规则 |
|---|---|
| 非谈判项 | 参照项目=证据,不是源码来源;**禁抄** AGPL/GPL/LGPL 的源码/文件结构/注释/schema/UI 源/独特实现 |
| CR-001 | License 先验证;非 MIT 参照不记 license 不写任何行为证据 |
| CR-002 | Specifier 车道**可读**非 MIT 源;Implementer 车道**只读 spec** |
| CR-004 | 同 session 不能同时干两车道 |
| CR-005 | 多 session 累积污染也算风险 |
| CL-001..010 | Spec 防泄漏清单:不带上游函数名/常量名+值/目录结构/schema 列名/UI 类名;不逐行翻译;evidence ID 必须真实存在;source URL 只在 Sources 节 |
| Vendoring | **MIT/Apache-2.0** 参照可直接 vendor 到 `backend/vendor/`(保留 LICENSE+NOTICE+MODIFICATIONS);**LGPL/AGPL/GPL 禁 vendor**,只能释义机制 |
| 架构自研 | 架构完全可自研(3-tier Router/Pool/Executor 等是 HUAKAI 原创);**只是功能不能少** |
| 抄袭审查(2026-06-10) | copyleft 仓只读机制+cite,**禁逐字搬码**;注释写「ported from / 逐行 Source:」即红旗;clean-room=从 wire-format 事实独立表达 + parity-or-better |

## 3. 功能不缩水(Feature Preservation)
| ID | 规则 |
|---|---|
| F-001 | 每个上游功能必须有 **7 种合法处置之一**:Implemented / Implemented Better / Merged Equivalent / Safe Equivalent / Plugin / Feature Flag / Mandatory Roadmap;**禁 Dropped/Ignored/Out-of-Scope** |
| F-002 | Disposition(目标)与 Status(现状)两轴独立 |
| F-003 | 锁定能力组不能静默裁掉 |
| 风险≠删功能 | License/安全风险只能改**实现方式/灰度/默认值**,不能移除功能 |

## 4. 调研先行 + 融合升级(核心)
| ID | 规则 |
|---|---|
| #16 三镜默认 | 写**每个**功能前必调研三个默认镜:`sub2api` + `new-api` + `CLIProxyAPI`(其它域参照叠加);数路径/模式,不只确认存在 |
| #16 shape inventory | 设计前先列全功能的所有 path/mode/state/actor(防像支付那样漏掉一整条路径) |
| sub2api 默认裁决 | 三镜分歧且必须选一时,**默认按 sub2api**(最成熟),再叠 HUAKAI 融合 delta;money/security/schema 高危仍 surface Owner |
| #12 source-must-read | 任何「X 项目做/不做 Y」「机制是…」「parity 判定」类断言**必须读源码**,带 `<repo>@<sha>:<file>:<line>` cite;记忆/训练印象/README 不算证据;源码与文档冲突以源码为准 |
| 首引近期性 | 首次引用某参照必查 archived=false + 90 天内有 push + HEAD SHA;cite 须在生产代码非 tests/ |
| FU 融合升级 | 精读各家同功能实现→**融合各家所长做成比他们都强**(parity-or-better+);用融合法**回扫**已做模块逐个升级 |
| 三维升级 | 每个 delta 归类:**架构升级 / 算法升级 / 生态升级**;差异表必带「delta vs upstream」+维度列,不能只 ✓/✗ |
| #15 决策带对照 | PM 给 Owner 做决策(AskUserQuestion/方案)时,每个选项必带 ≥2 个参照项目的处理方法 file:line 对照;无对照=违规 |

## 5. 冻结包 → 软预算 + 模块化(2026-06-11 修订)
| ID | 规则 |
|---|---|
| 模块化硬规则 | 代码**按职责组织**;新功能区→新建合适粒度的包;**绝不**默认堆进大包 |
| ~~硬冻结~~(退役) | ~~`internal/{gatewayhttp,gateway,proto}` 禁新增文件~~ → **2026-06-11 退役**(实测只挡新文件、反把逻辑逼进旧文件致膨胀) |
| 软预算门(现行) | `backend/internal/codebudget`(跑在 unit 门里):单非测试文件 ≤600 行、单包 ≤6000 行/≤20 文件;存量 grandfather 在 baseline.json + 5% 增长余量,超标即红→**拆子包修**(范本 `internal/provider` 9 核心+13 子包),禁把 baseline 调高 |
| 加新协议 | 落**新子包**(像 `proto/<family>/`、`provider/<vendor>/`)+ 既有文件加性 edit;禁 shim hack;需内部能力就导出它 |
| code-modularity | 文件 <~500-600 行、函数 <~80 行;review 门强制(等同判别测试严重度) |

## 6. 测试质量 + 门
| ID | 规则 |
|---|---|
| #14 测试必判别 | 每个测试:① 一句话说清它抓哪个回归 ② **mutation check**:引入缺陷必须变红,否则 fixture 非判别须重设计 ③ 判别 fixture(broken 代码产出必须不同)④ 打真实风险(钱/泄露/损坏)用真触发,非 nil stub |
| commit-first | **先 commit** → 再 mutation 验红(compile-safe 变异)→ 还原 |
| 双门 | unit(`go vet ./... && go test ./...`)+ 真库集成(fresh `huakai_gate` + `migrate up` + `go test -tags=integration_pg -p 1 ./...`,**必须 -p1** 否则多包共享库触发 40001 假失败) |
| perf-skip | unit 全量门必带 `HUAKAI_SKIP_PERF_LATENCY_GATE=1`(否则 TestChatCompletionsMixedLoadP95 负载下 flaky) |
| 闭环顺序 | commit-first → mutation 验红 → 双门绿 → FF-push → reap |
| 接口变更=全门 | 闭环若改共享接口,必须跑**全量** `go test ./...`(别处 mock 不更会挂),不能只跑改动包 |

## 7. 评审 / 计划 / 自验
| ID | 规则 |
|---|---|
| #8 per-commit codex review | 每次 commit 前 staged diff 过 codex review;**severity-based 门**(S0/S1 必修才 commit,S2/S3 记录排后续);默认 ≤2 轮;Codex HIGH/MED/LOW → 归类 HUAKAI S0/S1/S2/S3 |
| #7 slice cross-review | 每个 vertical slice 完成后跑 `/cross-review`,REJECT 不得推进 |
| #9 plan-before-execute | 非平凡动作(codex 批量派发/>200 行代码/schema 迁移/删除/多步)前写 plan artifact 到 `docs/process/plans/` surface Owner;平凡动作豁免 |
| #10 双方独立定计划+交叉讨论 | 重大决策 Claude 和 Codex **各自独立**先定计划再比对;Owner 说「你定/由你决定」= 委托这一个决策免 codex 并行 |
| PM 3 轮自验 | 决策/派发前自我反驳+自验 3 轮(R1 冲突/重复 R2 依赖/波次 R3 规格/前提) |
| PM self-check | 每次 commit 前逐条对照 self-check(触碰规则/CL 清单/evidence 真实/互审是否触发/commit message 末尾写 `Rules touched:`) |
| MR 互审 | 同样的工作 Claude+Codex 各做一份→互审→PM 综合;reviewer-lane=第三个不同 session |

## 8. 角色分工(2026-06-10 保留 codex 模式)
| 角色 | 职责 |
|---|---|
| Claude(opus,PM) | PM-编排 + 首席架构 + 调研 + 设计 + 核验 + 评审 + 接线 + 决策;每功能动手前亲读参照真源码;**不可自批自己的实现免 review** |
| Codex | 生产评审 + 场景测试 + parity 审计 + 小安全补丁 + **多并行实现**(≤2-3 并发);非大功能主实现者除非明确指派 |
| Gemini | 前端 UI + 运营看板;**禁碰**后端核心(provider 路由/配额/计费/auth/schema/LICENSE/密钥) |
| 拓扑 | 中央 PM=claude;codex worker 模式保留;PM 现可直接在 kaifa 上跑(文件本地原生) |

## 9. 技术栈(已锁)
| ID | 规则 |
|---|---|
| TS-001 | 后端 Go(stdlib net/http + chi);**永禁** Fiber/fasthttp |
| TS-002 | 前端 TS + React + Next.js App Router + Tailwind |
| TS-003 | DB PostgreSQL + sqlc + Docker Compose;**永禁** SQLite 上生产 |
| TS-004 | **OpenAPI 是 contract 真相源**,前端类型从它 codegen,禁手写 shape |
| TS-006 | tenant_id 每张主表 Day-1 就有 |
| TS-007 | Money 用 `numeric(20,8)`;**永禁** float |

## 10. 真相源 / 文档纪律
| ID | 规则 |
|---|---|
| 三件套活产物 | ① 总纲 `docs/00-MASTER-PLAN.md` ② 态势看板 HTML ③ 功能树 `huakai-feature-tree.html`(生成器解析 `/home/ubuntu/benchmark/`,六级禁虚标) |
| RULES.md 宪法 | 任何 binding 规则增改,PM 必须同步刷 `docs/RULES.md` + commit message 末尾 `Rules updated:` |
| 实时标记 | 每闭环一个→标杆行改「已完成」+证据列前缀 `🆕<sha>`→重生成功能树 |
| 报告归拢(2026-06-10) | 报告/台账都在 `/home/ubuntu/reports/`(benchmark/audit-runs/MIGRATION 留兼容软链,旧绝对路径不破) |

## 11. ⚠ 反封禁姿态(三连反转,现行=最新)
| 时间 | 立场 |
|---|---|
| 6-08 | 「主动反检测必须开着」 |
| 6-10 早 | 「反封禁不要做」/ 合规免责优先(覆盖 6-08) |
| **6-10 晚(现行)** | **只要 sub2api/new-api/CLIProxyAPI 之一 demonstrably 有的,即便涉及反封禁也要建**;判定锚点=「三 refs 有没有」不是「是不是反封禁」 |
| 仍不做 | ① 三 refs 都没有的 novel 主动 anti-ban ② 真实 PSP 支付通道 |
| 既有 mimicry | R3 传输伪装/uTLS/device-profile/Rust tls-sidecar 保留,default-off + operator-gated;**合规免责块已加 README**(ToS 风险+仅供学习研究) |

## 12. 钱/auth/schema/security 高危
| 规则 |
|---|
| money 账本/认证核心/破坏性迁移=高危,**逐项 Owner 批 + full reviewer-lane + 强判别测试** |
| **parity-complete 授权(2026-06-10)**:三 refs 有的 parity 功能全建、park 解封;但触及 money/auth-语义/破坏性 schema 仍 Owner-gated。加性/可空 schema 过 PM 门即可(非冻结) |
| Owner delegated authority(2026-06-02):无需逐动作审批,照流程自主决策(land/push/architect);只**不可逆生产动作** inform-not-ask |
| 日志禁记凭据/原始 payload(CMB-5) |

## 13. 运行环境 / 协调
| 规则 |
|---|
| 所有活在 kaifa 跑(8vCPU/15G/242G);能并行大力并行 |
| **claude 迁到 kaifa 直接跑(2026-06-10)**:Claude 桌面端 SSH 直连,cwd=repo 根,文件/测试本地原生,不再 ssh 包裹 |
| 并行编辑协调:多 AI 同树并发会互相覆盖,改共享文件前走 `.coordination/`(check→claim→release) |
| 编辑/commit 脚本一律 Write 工具写本地再 `cat\|ssh` 传,**绝不内联 heredoc 带引号/反引号**;传文件用 `cat\|ssh` 非 `scp C:\` |

## 14. Owner 总结要求(每个任务完成后中文输出)
做了什么 · 改了哪些文件 · 为什么这样做 · 有没有功能缩水 · 有没有 clean-room 风险 · 有没有安全风险 · 哪些需 Owner 确认 · 下一步建议

## 15. 质量准则(常青)
- 质量第一、先懂再改、**不改不懂的代码**;PM 严格 review 门
- 派任务前**通读整个项目**
- 真相第一(truth-first):宁可说「没读够/不确定」,不硬判;断言带证据

---

## 16. ⚠ 已被覆盖的旧规则(避免照旧规则误操作)
| 旧规则 | 现状 |
|---|---|
| **CB-001**「反检测/反封号不做、park」 | **已被 6-10 晚反转覆盖**:三 refs 有的即便反封禁也建(见 §11) |
| **MA-001**「sonnet 退役、必须 opus」 | 模型现由 Owner 自己 `/model` 切(本会话用 fable-5);workflow/agent 不写死 model,继承主循环即可 |
| **硬冻结**「proto/gateway/gatewayhttp 禁新文件」 | 已退役为**软预算门 codebudget**(见 §5) |
| **park money/auth/schema 全停** | parity-complete 授权后:ref-parity 功能要建;仅真高危(账本/auth 语义/破坏迁移)仍 Owner-gated(见 §12) |
| landing 分支 hermes-phase-1 | 现 landing = `fix/h-fixes` |

---

*权威以来源文件为准:`AGENTS.md`(745 行,agent 操作规则)· `CLAUDE.md`(16 条编号工作流规则 + charter)· `docs/RULES.md`(规则清单宪法)· `docs/00-MASTER-PLAN.md`(总纲)· PM 记忆 `memory/`。本文件是导航+去重+订正,定期随规则变更刷新。*
