# HUAKAI 算法升级 — 平行版 Synthesis（Claude × Codex）

Date: 2026-05-02
Inputs:
- [docs/plans/2026-05-02-huakai-algo-upgrade-claude.md](2026-05-02-huakai-algo-upgrade-claude.md) — 28 项 (A01-A28), ~110h
- [docs/plans/2026-05-02-huakai-algo-upgrade-codex.md](2026-05-02-huakai-algo-upgrade-codex.md) — 26 项 (A01-A26), 272h

Method: CLAUDE.md #10 平行交叉法。Claude 写时未读 Codex 输出；本 synthesis 在两份 plan 都落地后做对比 + 取优。

## 0. 一句话结论

**两版同覆盖 12 算法域、同 A-编号布局，核心立意一致**（binding-first / lease-version / 3-scope storm / 2-phase quota / attempt DAG / error class table / versioned pricing / capacity forecast / cross-vendor graph / hysteresis FSM / identity priority / adaptive stream）。差异在角度和细化方向：Claude 偏数学不变量+伪代码深度；Codex 偏 schema 字段+测试 ID 落点+多 attempt 归因细节。**取并集，按"两版都说必做"=P0、"一边新颖+另一边可吸收"=P1 排**。

## 1. Agree（两版立意一致 — 直接进 backlog 必做）

下表每行 Claude 与 Codex 都覆盖、做法收敛。优先按 Codex 的 P 分级（更严，且其 effort 为生产估算）。

| ID | 主题 | 共识算法核心 | Priority | 选用版本 |
|---|---|---|---|---|
| A01 | Binding-aware scheduler | api_key → bindings → expand → filter → score | **P0** | Codex（含 wait_or_fail 整合点）+ Claude（候选去重 OrderedSet） |
| A06 | 凭据 lease 状态机 | VALID/LEASED/STALE/REVOKED + version CAS | **P0** | Codex（清晰状态图）+ Claude（grace_max hold 边界） |
| A07 | 3-scope refresh storm controller | (account, endpoint, global) token bucket + singleflight | **P0** | 收敛一致 |
| A09 | 2-phase quota reserve+settle | Tx1 reserve(scopes) + attempt records + Tx2 settle | **P0** | Codex（attempt-aware attribution policy）+ Claude（4-scope 列表 + binding tier multiplier） |
| A11 | Attempt DAG planner | edge typing：refresh / spill / model_subst / terminal | **P0** | Codex（DAG schema）+ Claude（期望时间最短路径数学） |
| A13 | Error normalization 决策表 | 版本化规则表 + provider override | **P0** | 收敛 |
| A15 | Versioned pricing vector | token_vector dot snapshot + decimal | **P0** | 收敛（Codex 字段更细：reasoning/audio/image/tool token） |
| A17 | Capacity forecast | EWMA + seasonal P95 → ETA | **P0** | Codex（公式更紧凑）+ Claude（STL 分解可作 P1 优化） |
| A19 | Cross-vendor min-residual graph | min-cut / min-residual_after | **P0** | Codex（fault_domain 抽象）+ Claude（max-flow 可视化） |
| A22 | Health hysteresis FSM | normal/degraded/cooling/needs_refresh + score decay | **P0** | Codex（更完整状态枚举）+ Claude（升降阈值不重合数学性质） |
| A23 | Client identity priority detector | 加权信号 + spoof penalty + ttl 化 | **P0** | Codex（spoof_penalty / hmac_hash / privacy）+ Claude（priority + confidence） |
| A25 | Adaptive stream buffer | 历史 P99 × 4 + 内存压力裁剪 | **P0** | Codex（memory_pressure 信号）+ Claude（AIMD 边界） |
| A26 | Stream drain 期望值决策 | E[value] vs E[cost] + budget cap | **P0** | Codex（forensic_value + incident_probability）+ Claude（三 budget 早停） |

13 项强收敛 P0，是必做底盘。

## 2. Conflict / 互补（取优合并）

### A02 — Pareto Band vs Welford z-score

- Claude：在线统计 z-score + softmax，量纲对齐
- Codex：Pareto band 排除被支配项 + risk_energy 加权随机

**冲突分析**：不冲突，是"如何给候选打分"的两阶段。
**合并方案**：
1. 用 Claude 的 Welford+EWMA 标准化原始信号（解决量纲）
2. 在标准化后的向量上跑 Codex 的 Pareto band 过滤（排除被支配）
3. 在 non-dominated 候选上用 risk_energy 软选择

**最终 A02**：Pareto band 主算法 + Welford 喂数据。Priority **P0**。Effort 14h。

### A03 — Strategy enum

- Claude：未单列
- Codex：A03 显式 strategy 枚举 (compat_priority_lru / round_robin / fill_first / risk_pareto)

**采纳 Codex A03**：操作员 visibility 非常有价值。Priority **P1**。Effort 8h。

### A04 — Sticky migration loss function

- Claude：migration manifest（含 cache_lost 美元差额头）
- Codex：损失函数（context_loss / cache_loss / load / cred / cooldown_near 加权）

**合并**：Codex 损失函数为决策核心；Claude manifest 为客户响应头公示。两者是计算+对外接口关系。
**最终 A04**：Codex 损失函数 + Claude `X-Huakai-Migration-Cache-Lost` 客户头。**P0**。Effort 14h。

### A05 — Sticky 稳定性

- Claude：HRW（Rendezvous）hashing — 解决账号 ±1 时大面积 reshuffle
- Codex：hotspot rebalance — 解决 Gini 不均

**两者解决不同问题，都要**。
- A05a HRW hashing — **P1** (1-2h)
- A05b Hotspot rebalance — **P1** (8h)

### A08 — 凭据 stale + lease lifecycle

- Claude：lease orphan sweep + heartbeat watermark（gateway crash 容错）
- Codex：stale-while-refresh admission guard（请求类按风险用 stale token）

**两者解决不同问题**。
- A08a Stale grace by request class（Codex）— **P1** (6h)
- A08b Lease orphan sweep + heartbeat（Claude）— **P1** (3-4h)

### A12 — Retry policy

- Claude：决策表 (error_class × attempt_n × budget) → action
- Codex：Stream-safe retry boundary FSM (BEFORE_FIRST_TOKEN / CONTENT_STARTED 不可 retry)

**两者解决不同层**。Codex 是"流式状态决定能不能 retry"；Claude 是"决定后选什么 action"。两者串行使用。
- A12a Stream-safe retry boundary（Codex）— **P0** (8h) — 防双扣费
- A12b Retry decision matrix（Claude）— **P1** (3-4h)

### A14 — 

- Claude：误分类反馈学习
- Codex：Retry-After 谐波 jitter + cooldown spread

**Codex 的更基础**（防 thundering herd 雪崩）。**优先 Codex A14**。Claude 的 misclassification feedback 降为 P3 后续。
- A14 Retry-After harmonizer（Codex）— **P1** (6h)
- A14b Misclassification learning（Claude）— **P3** (4-5h)

### A16 — Pricing post-process

- Claude：表达式 AST compile（性能 50-200us → 1us）
- Codex：Drift reconciliation（事后权威 usage 修正 → adjustment pair）

**Codex A16 更重要**（财务 immutability）。Claude 是性能优化。
- A16a Drift reconciliation（Codex）— **P1** (8h)
- A16b Expression compile（Claude）— **P2** (2h)

### A18 — Restock recommendation

- Claude：暴力枚举（账号种类 ≤5 时 ok）
- Codex：bounded knapsack

**采纳 Codex**（更 general，覆盖账号种类 >5 场景）。Priority **P1**. Effort 10h.

### A20 / A21 / A24

- A20 Fault-domain spillover：两版同；用 Codex（更完整 score 函数）。**P1**.
- A21 Risk-weighted probe：Codex 多信号 risk vector 比 Claude 单 AIMD 强。**采纳 Codex**。**P0**.
- A24 Identity cache drift：Claude 未列；Codex 有。**采纳 Codex**。**P1**.

## 3. Gaps（一方独有，需另一方决议是否纳入）

| Item | Source | 决议 |
|---|---|---|
| Claude A03 wait-plan vs fail 期望成本最小化 | Claude only | Codex A01 内含 `wait_or_fail(plan, ttl=min_recovery_eta)` — 已合并到 A01 |
| Claude A14 misclassification learning loop | Claude only | 降级 P3 — 价值有但非 launch blocker |
| Claude A16 expression compile | Claude only | 降级 P2 — 高 RPS 才显著 |
| Claude A27 stream 内 reserve 动态扩 | Claude only | **采纳新 ID A27** — 长流配额问题 — **P2** (3-4h) |
| Claude A28 跨 vendor fault-domain 故障感知 | Claude only | 与 Codex A20 合并 — Claude 的指数衰减 counter 用作 A20 的 residual 输入 |
| Codex A12a stream-safe boundary | Codex only | **必采纳** — 防双扣费 — **P0** |
| Codex A24 identity cache drift | Codex only | **采纳** — **P1** |

## 4. 合并后最终清单

### P0（必做底盘 — 14 项）

A01 Binding-aware scheduler ··· 14h
A02 Pareto band + Welford 标准化 ··· 14h
A04 Sticky migration loss function + manifest 头 ··· 14h
A06 Credential lease state machine ··· 10h
A07 3-scope refresh storm controller ··· 10h
A09 2-phase quota with attempt attribution ··· 14h
A11 Bounded multi-attempt DAG planner ··· 16h
A12a Stream-safe retry boundary FSM ··· 8h
A13 Provider error normalization rule table ··· 12h
A15 Versioned pricing vector evaluation ··· 12h
A17 Capacity depletion forecast ··· 12h
A19 Cross-vendor min-residual graph ··· 16h
A21 Risk-weighted probe scheduler ··· 12h
A22 Health hysteresis FSM ··· 12h
A23 Client identity priority detector ··· 10h
A25 Adaptive stream buffer ··· 10h
A26 Expected-value drain decision ··· 10h

P0 小计：**~206h**

### P1（强增益 — 11 项）

A03 Strategy enum (compat_priority_lru / round_robin / fill_first / risk_pareto) ··· 8h
A05a Sticky HRW hashing ··· 2h
A05b Sticky hotspot rebalance ··· 8h
A08a Stale-while-refresh admission guard ··· 6h
A08b Lease orphan sweep + heartbeat watermark ··· 4h
A10 Quantile reserve estimator ··· 10h
A12b Retry decision matrix ··· 4h
A14 Retry-After harmonizer ··· 6h
A16a Pricing drift reconciliation ··· 8h
A18 Knapsack restock recommendation ··· 10h
A20 Fault-domain spillover guard ··· 8h
A24 Identity cache drift detector ··· 6h

P1 小计：**~80h**

### P2（增益）

A16b Pricing expression compile ··· 2h
A27 Stream-time quota dynamic reserve ··· 4h

P2 小计：**~6h**

### P3（创新拓展）

A14b Misclassification learning loop ··· 5h

P3 小计：**~5h**

**总 effort：~297h**（Claude 110h 偏乐观，Codex 272h 偏保守；merge 后含两边的字段+测试落点 ≈ 中位数 297h，与 Codex 接近 +9% 是合并补丁开销）

## 5. 算法域覆盖回环检查

| 域 | A-IDs | 覆盖完整性 |
|---|---|---|
| 1 调度算法 | A01, A02, A03 | 完整 |
| 2 Sticky migration | A04, A05a, A05b | 完整（决策+稳定性+均衡） |
| 3 Lease + storm | A06, A07, A08a, A08b | 完整（FSM+storm+stale+orphan） |
| 4 二阶段 quota | A09, A10, A27 | 完整（reserve+quantile+stream-extend） |
| 5 Attempt DAG | A11, A12a, A12b | 完整（plan+stream-boundary+matrix） |
| 6 错误归一化 | A13, A14, A14b | 完整 |
| 7 Pricing snapshot | A15, A16a, A16b | 完整（vector+reconcile+compile） |
| 8 Capacity forecast | A17, A18 | 完整 |
| 9 跨 vendor capacity | A19, A20 | 完整（graph+spillover） |
| 10 Channel monitor | A21, A22 | 完整（probe scheduler+state FSM） |
| 11 Client identity | A23, A24 | 完整（detector+cache-drift） |
| 12 Stream forwarder | A25, A26 | 完整（buffer+drain） |

12 域全覆盖，无缺漏。

## 6. Owner Sign-off Decisions — 已定（2026-05-02）

Owner directive 2026-05-02: "完整升级 + 保证卖方的利益 + 用户端显示切换情况"。8 决议落定如下：

| # | 决策 | Owner 选项 | 卖方利益落点 | 客户透明度落点 |
|---|---|---|---|---|
| 1 | 账号 auto-disable | **C 分级**：铁证（OAuth invalid_grant / KYC / org_disabled / token_revoked / deactivated_workspace 等关键词命中）自动 permanent disable；模糊（连续 5xx / 边界 4xx）只挂红灯等 operator 点 | 自动减少误伤健康账号资产；保护账号库存价值 | 不直面客户（账号管理是后台） |
| 2 | 多 attempt 计费归因 | **C 双账**：客户账单 `succeeded_on`（只显示成功那次）；后台 analytics `dollar_weighted`（A+B+C 全显示） | 操作员完整看到失败 attempt 烧的成本，定价决策有依据 | 客户账单干净（不为失败 attempt 二次扣费） |
| 3 | Model substitution | **C 显式 opt-in + 头部标注**：默认禁；route policy 勾允许才换；换了必带 `X-Huakai-Model-Substituted: <from>→<to>` 响应头 + `X-Huakai-Substitution-Reason: <quota_exhausted\|model_unavailable>` | 客户不流失（model 全挂还能服务） | 客户头部明确知道被换了 model；可自行决定是否接受 |
| 4 | Quantile reserve | **C 分层**：新用户（无历史）P99 严；正常用户 P95；wallet 信用线/付费 tier P90 宽 | 新用户严防超卖；老用户高利用率不误报；信用线客户少打扰 | 响应头 `X-Huakai-Quota-Headroom-Usd: 4.21` 让客户知道余量 |
| 5 | Drain 隐私边界 | **A**：drain 决策只看 token usage 元数据，不读 prompt body | drain 保 billing capture（卖方收得到钱） | 客户隐私优先（合规） |
| 6 | Capacity graph 范围 | **B**：Personal Edition 单租户；SaaS Edition 强制租户隔离 | SaaS 防租户互窃容量；维护账号资产边界 | 不直面客户 |
| 7 | Sticky 算法 | **B HRW Rendezvous Hashing** | 账号增减时 sticky 命中率从 60% → 99%，减少 prompt cache 失效（卖方少多收 cache_creation 成本但客户 cache 体验稳定 — 长期续约率提升 > 短期 cache_creation 多收益） | 客户 prompt cache 不被账号扩缩容打断 |
| 8 | Stream 中途换账号 | **B opt-in 头**：默认不允许换（防双扣费骂战）；客户带 `Idempotent-Stream-Replay: true` 头才允许（自担风险） | 默认安全；高级 SLA 客户开 opt-in 后体验更稳 | 默认不换 = 客户不会看到重复内容；opt-in 是客户主动选择 |

### 6.5 客户透明度响应头清单（Owner directive: 用户端要显示切换情况）

每次请求若发生以下任一情况，必须带相应响应头（除非客户明确通过 `X-Huakai-Quiet: true` 关闭，默认开）：

| 响应头 | 触发场景 | 取值 | 关联 A-ID |
|---|---|---|---|
| `X-Huakai-Account-Failover-Count` | 经历了 ≥1 次账号 failover | int（attempt 数 -1） | A11 |
| `X-Huakai-Migration-Reason` | sticky 被打破，换了账号 | `lower_expected_loss \| hard_gate_fail \| account_disabled \| ...` | A04 |
| `X-Huakai-Migration-Cache-Lost` | sticky 换账号导致 prompt cache 失效（Vendor-X2） | `true / false` | A04 |
| `X-Huakai-Migration-Cost-Delta-Usd` | 迁移导致预估成本增加（cache_creation 重打） | `0.0042` 4 位小数 | A04 |
| `X-Huakai-Model-Substituted` | Q3 决策开启时 model 被偷换 | `gpt-4o→gpt-4o-mini` 形式 | 新增 A29 |
| `X-Huakai-Substitution-Reason` | 同上配套 | `quota_exhausted \| model_unavailable \| operator_route_policy` | 新增 A29 |
| `X-Huakai-Quota-Headroom-Usd` | 总有 | `4.21` 当前 binding 剩余配额 | A09 |
| `Retry-After` | wait_or_fail 决策 fail 但建议重试 | int 秒 | A01 wait_or_fail |
| `X-Huakai-Routing-Strategy` | A03 enum strategy 启用时 | `risk_pareto / round_robin / fill_first / compat_priority_lru` | A03 |
| `X-Huakai-Migration-Action` | A04 决策枚举 | `stay / migrate_silent / migrate_with_warning / fail_closed` | A04 |
| `X-Huakai-Stream-Boundary` | 仅当 A12a 阻止 retry 时（debug 模式） | `BEFORE_FIRST_TOKEN \| CONTENT_STARTED \| TOOL_SIDE_EFFECT_STARTED` | A12a |

**新增 A29 Model Substitution Engine [P1]**：实现 Q3 决策需要的算法。

```python
def substitute_model(req, account, route_policy):
    if not route_policy.allow_substitution:
        return None
    # 候选替代 model（同 family，能力下降但 schema 兼容）
    candidates = model_substitution_table[req.model]
    for candidate in candidates:
        if account.supports(candidate):
            return Substitution(
                from_model=req.model,
                to_model=candidate,
                reason="quota_exhausted" if quota_exhausted else "model_unavailable",
                client_headers={
                    "X-Huakai-Model-Substituted": f"{req.model}→{candidate}",
                    "X-Huakai-Substitution-Reason": reason,
                }
            )
    return None
```

数据结构：`model_substitution_table(from_model, to_model_priority_list)` 配置表 + `route_policies.allow_substitution bool`。

Effort：4-6h。加入 P1 总计：~84h（原 80h + A29 4h）。

### 6.6 卖方利益不可妥协的硬底线

| 不可让步 | 理由 |
|---|---|
| A09 二阶段 quota 必须 reserve（Tx1）+ settle（Tx2） | 否则高并发超卖 = 直接亏钱；sub2api 现状 settle-only 是 HUAKAI 必须升级的关键 |
| A26 drain 决策 = E[value] > E[cost] 仍 drain | 默认贪婪收 usage（卖方 billing capture）；只在恶意 upstream 三 budget 触发才停 |
| A11 attempt DAG 全 attempt 写 audit 行（即使失败） | 后台 analytics 看到完整成本（Q2 dollar_weighted） |
| A22 hysteresis FSM 不轻易 permanent_disable | 保护账号库存资产；只有 Q1 铁证关键词才自动；其他必经 cooldown_down 状态 |
| A07 3-scope storm controller | 防 OAuth refresh 自杀打爆 vendor endpoint 导致全账号挂 |
| A12a stream-safe boundary | 防止双扣费纠纷（已发字节后 retry 客户会投诉重复内容拒付） |
| A19 cross-vendor min-residual graph | 维持账号组合最大可用容量；不让单一 vendor drain 完才启动 fallback |

## 7. Phase 排序建议

```
Phase A (~80h, 立即可做，与 N+5b spine 协同):
  A01 binding-aware scheduler
  A06 credential lease FSM
  A07 3-scope storm
  A13 error normalization table
  A15 pricing vector
  A22 health hysteresis FSM

Phase B (~70h, P0 之 spine 之外):
  A02 Pareto + Welford
  A09 2-phase quota
  A11 attempt DAG
  A12a stream-safe boundary
  A23 identity detector

Phase C (~56h, capacity + stream):
  A17 forecast
  A19 cross-vendor graph
  A21 probe scheduler
  A25 adaptive buffer
  A26 drain decision
  A04 sticky migration manifest

Phase D (~80h, P1 提升):
  A03, A05a, A05b, A08a, A08b, A10, A12b, A14, A16a, A18, A20, A24

Phase E (~11h, P2/P3):
  A16b, A27, A14b
```

## 8. 一行总结

**Claude 提供"算法不变量 + 伪代码深度"，Codex 提供"schema 字段 + 测试 ID + attribution 完整性"，合并后 30 项 A-编号、3 phase ~210h P0 + 80h P1**，把 Commercial-Pool-Ref 的 5 层调度替换为 binding-first × Pareto-band × attempt-DAG × hysteresis-FSM × min-residual-graph 的"每决策都有数学不变量"路径，执行起点是 Phase A 6 项与 N+5b spine 协同落地。
