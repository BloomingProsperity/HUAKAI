# Owner 主链微步骤分析 — 评估与对齐(Claude 独立稿)

> 【2026-06-02 已更新】本文关于前端“搁置到 Rust 四波后”的判断是 2026-05-21 历史；
> landing commit `bcc4f5d` 已记录 “frontend Next 14->15; Owner lifted frontend freeze”，前端
> wire-up 已解锁。以下为历史评估。

- 日期: 2026-05-21
- 性质: CLAUDE.md #10 平行评估的 Claude 稿。独立起草,未看 codex 平行稿(`2026-05-21-mainchain-analysis-eval-codex.md`)
- 评估对象: Owner 提供的「请求主链 12 微步骤 + 功能 12 项 + 3 周排班」差异分析(基线 commit 880b443)
- 输入: `2026-05-21-direction-1.md`、`2026-05-21-account-to-api-gap-analysis.md`、`2026-05-21-phase1-design-synthesis.md`、全景进度报告

## 1. 评估判断

**质量**: 高。这份分析的贯穿主线是「每个决策出 reason-code、每一跳可追踪、request→attempt→receipt 能单条串起来」。它不是又一版 gap 清单,而是把 HUAKAI 的信任链/透明差异化([[project_core_trust_chain_differentiator]] 的 F-TRUST/F-AUDIT)**下沉到 retry/routing 主链**。值得采纳。

**准确性**: 对 sub2api / cliproxyapi 的论断,与本仓库 gap 分析 §4 已有 file:line 引证方向一致(sub2api 强 pool-routing / wait plan / attempt 可审计;cliproxyapi round-robin/fill-first/onboarding 强)。无明显事实错误。但全部是泛泛论断、无 file:line —— 若要据某条做架构决策,该条需回源码核实(CLAUDE.md #12)。

**跟现状脱节处**:
- 基线 880b443 旧了。其后已 +PR1(router 多候选)+PR2(错误 taxonomy),**#一.3 路由策略、#一.9 重试切换、#二.8 error class 已动工**。
- **#一.11 通道健康判断偏重**。Owner 这份说 HUAKAI「monitor 与 cooldown 一致性还要收敛」;但 gap 分析定性 channelhealth 是 HUAKAI 强项、「无大缺口 Go 强」(active/degraded/cooling/ramping 状态机完整,ramp 渐进恢复已有)。这条不该算缺口。
- **倾向「什么都建表」**。request_attempt 表、四元索引、绑定变更链 …… 若全落 schema,迁移面很大。要甄别哪些真需要新表、哪些复用现有(billing_events / audit ledger / usage_records)。

## 2. 24 项对齐表

档位: ✅已做 / 🔄在做(Phase 1 在改) / 📋已规划(方向1框架内有) / 🆕真新增(框架未覆盖)

### 一、请求主链 12 步

| # | Owner 诉求 | 档位 | 依据 |
|---|---|---|---|
| 1 | 用户身份入口 + session affinity 来源矩阵可量化可回归 | 🆕 | 身份/租户入口✅;但「affinity 来源矩阵」方向1无对应任务 |
| 2 | API Key 账户绑定解析 + 绑定变更可追踪链(版本/失效原因/切换事件) | 🆕 | 绑定解析✅(registry/binding);变更审计链无 |
| 3 | 路由策略 strategy_id + 决策出 reason-code | 🔄+🆕 | 多候选 planner PR1 已落、AttemptPlan.Reason 是 reason 雏形;strategy_id 枚举无 |
| 4 | 并发槽位 slot 四态(queued/limited/assigned/timed_out)+ 耗时分位 | 🆕 | pool slot 机制✅;显式四态状态机 + 分位归因无 |
| 5 | Sticky 会话中止条件(首字节/工具调用/上下文断层) | 🔄+🆕 | sticky✅;Phase 1 流式硬规则覆盖「首字节」边界;工具调用/上下文断层无 |
| 6 | 凭证租约 lease(lease_id+token_version+token_status 绑 attempt) | 🆕 | 凭证刷新📋(洞④/Phase2);lease 级建模无 |
| 7 | Credential Injecter 与路由尝试解耦 + 接口测试 | 🔄+🆕 | Phase 1 runAttempt 抽取含 resolveCredential;显式 CredentialInjector 接口无 |
| 8 | 协议适配器统一 protocol_loss schema + 前后端/审计可见 | 🆕 | 协议翻译 + loss 记录✅(HCSF);统一 schema + 可见性无 |
| 9 | 重试/失败切换每跳 {attempt_n,reason,from,to,terminal_class} | 🔄+🆕 | 洞② Phase 1 核心;每跳**持久化**轨迹无(Phase 1 设计是内存态,见 §4) |
| 10 | 失败计入用量 + usage 四元索引(binding+account+attempt+reason) | 🆕 | billing✅、usage_records 有 attempt_seq/account;binding+reason 维度索引无 |
| 11 | 通道健康评分周期 + 恢复坡度 + 禁用恢复触发 | ✅ | channelhealth 状态机完整 + ramp 渐进恢复已有,HUAKAI 强项 |
| 12 | Ops Trace request→attempt→receipt 单条串联视图 | 🆕 | 审计/六跳链✅;单条「用户可读+运维可排查」串联视图无 —— Owner 称核心短板,同意 |

### 二、功能 12 项

| # | Owner 诉求 | 档位 | 依据 |
|---|---|---|---|
| 1 | 账户资产模型加容量/状态/租约/路由标签元数据 | 🆕 | provider_accounts 有 in_flight/健康;lease+路由标签无 |
| 2 | API 密钥到路由绑定变更 pending/apply/active/disabled | 🆕 | 绑定✅;变更原子性+回滚四态无 |
| 3 | 账户组/池策略 strategy_id 可灰度 | 🆕 | Pool✅;策略枚举可灰度无(同一.3) |
| 4 | 多租户隔离 — edge path 错误信息泄漏跨租户 | 🆕 | 隔离基础✅;edge 错误泄漏是缺陷,归 F-PRIV |
| 5 | 凭证存储接口抽象(可插拔+审计) | 🆕 | 现 PG;接口抽象方向1未列(洞④明说不改 store 契约) |
| 6 | 登录/凭证导入 create→callback/cancel/finalize | 📋 | F-CRED-001/F-AUTH-006 spec-only |
| 7 | 刷新风暴控制 3-scope + singleflight + jitter | 📋 | 洞④/Phase 2 明确「复用 storm controller + singleflight 语义」 |
| 8 | 路由状态码 class map 版本化 + 回归测试 | 🔄+🆕 | PR2 已建 error class;版本化(provider override)无 |
| 9 | 会话来源矩阵(6 提取源 + 兼容映射) | 🆕 | 同一.1 |
| 10 | OpenAPI 与实现一致 — 接口契约红线测试 | 🆕 | openapi.yaml 有,但有 drift;契约测试无 |
| 11 | 前端 6 主面板可读 | 📋 | 前端有骨架,Owner 已决「搁置到 Rust 四波后」([[project_frontend_state_2026_05_21]]) |
| 12 | 安全脱敏 — 日志分级 + 脱敏白名单 + 错误体积限制 | 🔄 | F-PRIV-001,internal/privacy + internal/redact 已存在,在做 |

## 3. 真新增项 + 进 Phase 建议

🆕 约 13 项,共同主题分两类:

**A. 可观测/可追踪类**(reason-code 贯穿、ops trace 单条视图、usage 四元索引、protocol_loss schema、slot 四态)
- 建议: 方向 1 **新增一个横切 Phase「主链可观测/可追踪强化」**(暂记 Phase 3.5)。这些不该硬塞进 Phase 1-3(那是补 7 个功能洞),它们是 retry/routing 主链稳定后的一层「可追踪皮肤」,与 F-TRUST/F-AUDIT 一脉。Phase 1 的 attempt 模型 + PR2 的 reason-code 已经是这层的地基。

**B. 建模升级类**(strategy_id 枚举、credential lease、绑定变更四态、凭证存储接口抽象)
- 建议: 分别并入相关洞,不单独立 Phase —— strategy_id 并洞⑥ 后续 ranking;credential lease 并洞④(Phase 2);绑定四态属 registry/admin;凭证存储接口抽象优先级低、可延后。

**C. 独立项**
- OpenAPI 契约红线测试: 独立小工程,任何时候可插,建议尽早(防 drift 继续累积)。
- edge path 跨租户错误泄漏: 是缺陷不是 feature,归 F-PRIV,按严重度定优先级 —— 若真能跨租户泄漏,P0。

## 4. request_attempt 表 vs 复用 —— 我的判断

Owner 第 1 周列「request_attempt 表」(持久化 attempt 轨迹);Phase 1 当前设计是 attempt 走内存 + 复用 `claim.attempt_seq`。

**判断: 不必新建 request_attempt 表,复用现有 billing 实体。** 理由:
- Phase 1 retry **功能本身**不需要持久化轨迹 —— retry loop 在单请求生命周期内,内存维护 failed accounts + attempt 序号足够。Phase 1 内存态是对的。
- 但 Owner 真正诉求是「ops trace 可事后排查」——那需要轨迹落库。而**轨迹其实已经在落库**: Phase 1 PR4 设计让每个失败 attempt 写一条 `claim_aborted` billing event(zero-cost evidence),最终成功写 `usage_records`(带 attempt_seq)。同一 `claim_id` 下的 N 条 `claim_aborted` + 1 条 committed,本身就是完整 attempt 轨迹。
- 新建 request_attempt 表会与 billing_events 的 attempt evidence **重复**,且多一处需对账的真相源。
- 正解: 「单条串联视图」建成一个**只读查询/视图**,按 `request_id`/`claim_id` 把 billing_events + usage_records + audit ledger 串起来。零新表、零写入冗余。

→ 这条要进 §3 的「可观测 Phase」:不是建表,是建视图。

## 5. 风险 / 盲点

| 风险 | 说明 |
|---|---|
| 两套计划框架打架 | Owner 这份「12+12+3周」与方向1「7洞+8Phase」是同片地两种切法。必须并成一套,不能平行存在 |
| 3 周排班过乐观 | 第 1 周 5 项含 request_attempt 表/三链绑定/lease trace —— 强度 ≈ 整个 Phase 1(5 个 PR)。3 周排不完 |
| 与 Phase 1 PR4 撞车 | Owner 第 1 周的「attempt→usage→invoice 三链绑定」直接落在 billing 高风险确认门,需与 PR4 合并而非另起 |
| 「什么都建表」倾向 | 见 §1、§4。要逐项甄别新表必要性 |
| 这份分析未经 codex 平行 | Owner 已说明此分析是外部工具所做;本评估补上 codex 平行(`-codex.md`) |

## 6. 一句话结论

采纳这份分析的**精神**(可观测/可追踪下沉主链),但不采纳它的**框架**(12+12+3周)。落地方式: 把 13 个🆕项按 §3 归类 —— 可观测类立一个新横切 Phase、建模类并入相关洞、契约测试独立插、跨租户泄漏当缺陷修。request_attempt 表改为「视图」。

---
本稿 lane: evaluator — Claude 独立评估,未看 codex 平行稿。未读外部参考项目源码。agent: Claude (claude-opus-4-7)。UTC 2026-05-21。
