<!--
Owner directive (2026-05-08): "我们优化的放行也只是在同架构上进行优化，没有跳出框架之外！用我们自己的东西！" + "我是说核心调度算法框架以及逻辑"
Lane = planner / 独立思考 / 未读 Claude 同名 plan
推荐方案一句话 = "Lifetime-Budget Trajectory Scheduler" — 把每个账号当成一条**有限燃料的轨迹**，调度的原语不是"挑账号"而是"对账号未来 N 小时的剩余可用面积做一次形状再分配"。
-->

# HUAKAI 自有调度算法 — 候选与推荐 (codex lane)

## 1. 核心思考：sub2api 框架的本质 vs HUAKAI 跳出后是什么

**sub2api / one-api / new-api 框架本质 = 三层无状态过滤器 + 时点决策**：
1. **过滤层** (gates)：当前时刻账号能不能用 (cooldown / quota / health)。
2. **匹配层** (sticky / model-route)：当前请求的某个 key 能不能粘在某个账号上。
3. **排序层** (priority / load / RR)：上一步过滤后剩下的账号，谁排第一。

它的隐含假设：**每次请求是一个独立事件，调度只关心"当下"**。账号被抽象成**当前快照** (LoadRate / LastUsedAt / Priority)。"未来"只通过 cooldown 这种钝器表达。

**这是 stateless control plane 范式**——和 K8s scheduler / nginx upstream / envoy lb 同源。

**HUAKAI 跳出后的范式 = stateful、时间维、有限资源轨迹**：

账号不是"当前能不能用的开关"，而是 **一条有寿命、有形状、有指纹消耗速率的轨迹**：
- 每个账号有 24h / 7d 的**可用面积** (quota / 风控耐受 / 指纹寿命)。
- 每个请求**消耗这条轨迹的某个时刻 + 某段未来形状** (留下 cache footprint、留下指纹特征序列、留下风控记忆)。
- 调度的真正问题：**"给定未来 N 小时的请求洪流预测，怎么把每个请求 assign 到某条轨迹上的某个时刻，使总剩余面积最大、总命中率最高、总风控暴露最低？"**

换言之，HUAKAI 的调度不是 selector，而是 **scheduler in the OS sense + portfolio optimizer**——更像 CPU 时间片 + 投资组合再平衡，不是 LB。

---

## 2. 候选算法 (5 个)

### A. Lifetime-Budget Trajectory Scheduler (LBTS) ⭐推荐
- **一句话**：每个账号是一条有寿命预算的轨迹，调度即"在轨迹形状上预定时刻 + 预扣未来面积"。
- **类比**：航空公司收益管理 (yield management)——一个航班的座位是有限轨迹，每个 booking 不只占当前座位、还影响后续放票节奏。
- **数据结构**：`AccountTrajectory{ID, TotalBudget24h, ConsumedAreaSoFar, FutureReservations[time→bytes], FingerprintDecayState, CacheFootprintMap}` + 全局 `TrajectoryLedger`。
- **伪代码**：
  ```
  on Request(r):
    candidates = trajectories.filter(canHostShape(r.predictedShape))
    for c in candidates:
      score = projectedRemainingAreaAfter(c, r) - lossDueToFingerprintNoise(c, r)
              + cacheHitProjection(c, r) - reservationConflictPenalty(c)
    pick argmax(score)
    reserve(c, r.startTime, r.predictedDuration, r.predictedTokens)
    on completion: settle(c, actual) → update trajectory shape
  ```
- **收益**：天然解决 cache locality / 指纹寿命 / quota 形状 / 长尾请求 reservation conflict 一体。把"调度 + billing claim + cooldown + sticky"四个独立子系统统一成一个数学对象 (轨迹 + 预订)。
- **代价**：需要请求 shape 预测 (用历史 prefix → token 分布近似)；ledger 是有状态系统、需要崩溃恢复。
- **HUAKAI 独有**：✅ 现有项目 (sub2api / new-api / one-api / portkey / litellm / helicone) **没有**把账号建模成时间维轨迹的；它们都是 stateless filter + LB。

### B. Multi-Armed Bandit with Contextual Cache Reward (CCB)
- **一句话**：用 contextual bandit 在线学习"什么 prompt 形状路由到什么账号 cache 命中率最高"。
- **类比**：广告投放 CTR 在线学习。
- **数据结构**：`BanditArm{accountID, contextHash, alpha, beta}` (Beta 分布) + Thompson sampling。
- **伪代码**：
  ```
  arm = sample θ_a ~ Beta(α_a, β_a) for each a in eligible
  pick a* = argmax θ_a
  observe reward = cacheHit ? 1 : 0  + healthBonus
  α_a*, β_a* ← update
  ```
- **收益**：对未知账号性能自适应，不需要先验配置权重。
- **代价**：cold-start 慢；和 sticky 概念冲突需要重新设计 explore/exploit 边界；本质仍是"挑账号" (sub2api 范式延伸而非跳出)。
- **HUAKAI 独有**：⚠️ 部分独有——bandit 在 LB 领域用过 (Netflix / Meta CDN)，但用在 LLM cache routing 是新组合。整体仍偏"优化排序层"，没跳出三层框架。

### C. Workload-Cluster Affinity Field (WCAF)
- **一句话**：把所有进行中的请求建模成"工作负载簇" (按 prompt prefix + tool 集 + tenant 聚类)，每个簇有引力场吸住一个账号；新请求被吸进最近的簇。
- **类比**：流体力学 / particle-in-cell；或 Kafka consumer group rebalance。
- **数据结构**：`Cluster{centroid:promptEmbedding, attachedAccount, weight, lastActivity}` 列表 + `ClusterAffinityIndex`。
- **伪代码**：
  ```
  for r in requests:
    nearest = argmin clusterDistance(r.embedding, c.centroid)
    if nearest.weight > threshold: route(r, nearest.attachedAccount)
    else: spawnCluster(r) → bind to bestAccount via LBTS-lite
  rebalance every T: merge thin clusters, split heavy ones
  ```
- **收益**：cache locality 高于 prefix-hash 因为是连续相似而非 exact match；自动多租户隔离。
- **代价**：embedding 计算成本；簇漂移；和 LBTS 部分重叠 (LBTS 已隐式做时间簇)。
- **HUAKAI 独有**：✅ 是的——没有 AI gateway 项目把请求建模成持续演化的簇。但单独用价值不如做 LBTS 的子模块。

### D. Hedged Forecast Routing (HFR)
- **一句话**：基于"未来 5 分钟流量预测 + 账号风险预测" 做模型预测控制 (MPC)，每个请求 hedge 到 top-2 账号、谁先返回 token-1 谁赢。
- **类比**：HFT 双下单 + cancel；Google "tail at scale" hedged requests。
- **数据结构**：`ForecastModel(time→λ_per_account)` + `HedgeBudget` (并发 hedged 请求池)。
- **伪代码**：
  ```
  predicted_load = forecast(t+5min)
  primary = leastForecastedConflict(eligible, predicted_load)
  shadow = secondInLine
  fire(primary); after δms if no token-1: fire(shadow), kill loser
  ```
- **收益**：尾延迟显著降低；预测系统压力。
- **代价**：浪费上游配额 (一次请求成本 ×1.X)；和 vendor TOS 边界风险；clean-room 上 hedged 是 Google 公开论文范式不存在抄袭。
- **HUAKAI 独有**：⚠️ hedged 在 RPC 领域成熟，AI gateway 没人做但成本高、和"稳定 / 反风控"目标冲突——浪费 quota = 缩小总剩余面积。

### E. Account-Lifecycle State Machine + Reverse Quota Flow (ALSM)
- **一句话**：账号是有限状态机 (Fresh→Warm→Loaded→Cooling→Recovering→Retired)，调度按状态机相位选目标，请求从"配额池"反向拉账号而非账号被挑。
- **类比**：连接池 + reverse flow control (背压)。
- **数据结构**：`AccountFSM` per account + `RequestDemandQueue` 按 SLA 分桶 + `PhaseTransitionRules`。
- **伪代码**：
  ```
  every account ticks FSM (timer / event driven)
  when request r arrives:
    bucket = bySLA(r)
    bucket.demand() → pull from accounts in matching phase
    if no match: transit some Warm→Loaded to absorb
  ```
- **收益**：把"账号生命周期管理"和"请求路由"合并；指纹寿命自然落进 FSM 相位。
- **代价**：FSM 复杂度高；相位转换调参难；对突发流量响应慢。
- **HUAKAI 独有**：✅ 是的——把账号从"资源"提升到"主动状态机参与者"反向拉请求，未见于参考项目。但工程复杂度对 HUAKAI 当前规模过高。

---

## 3. 推荐：**A. Lifetime-Budget Trajectory Scheduler (LBTS)**

**强逻辑链**：

1. **Owner 要"跳出框架"**：A 是唯一彻底重定义调度原语 (从"挑账号"变成"在轨迹上预订时空")。B/D 仍在三层框架里优化，C 是子模块，E 工程复杂度过高。
2. **统一现有 4 个孤岛**：sticky (cache footprint 子集) + cooldown (轨迹相位子集) + claim_gate (面积预扣子集) + selector ranking (轨迹评分子集)。当前 4 套相互打架的逻辑可被一套覆盖。
3. **天然契合 HUAKAI 上下游事实**：
   - 上游 vendor 配额是**有时间形状**的 (5h 滚动窗口 / 24h reset / weekly cap) — 轨迹建模天然吻合。
   - prompt cache 是**有寿命**的 (5min/1h TTL) — footprint map 天然吻合。
   - 风控 / 指纹是**有累积曲线**的 — 衰减状态天然吻合。
   - sub2api 等都用单点快照表达这些 = 信息丢失 = HUAKAI 真正的护城河。
4. **clean-room 安全**：轨迹/收益管理 (yield management) 是航空业 1970s 公开数学，不与任何 LLM gateway 项目 IP 重叠。
5. **可渐进上线**：Phase 1 用 conservative trajectory (24h budget + simple shape) 即可替代现有 selector；Phase 2 加预测；Phase 3 加 reservation conflict resolver。每 phase 独立可验证。
6. **对 Owner "复杂度不要太高"边界友好**：核心数据结构 ≈ 200 LoC；shape 预测 v1 用 EWMA (无 ML 依赖)；只在 Phase 3 才需要更高级预测。

---

## 4. LBTS 实施 atomic 拆解 (8 原子，每个 < 500 LoC)

| # | 原子 | 内容 | 验收 |
|---|---|---|---|
| L1 | `trajectory/types.go` | `AccountTrajectory`, `Reservation`, `TrajectoryLedger` 类型 + 序列化 | 单测覆盖结构与 JSON 来回 |
| L2 | `trajectory/budget.go` | 24h/5h/weekly budget tracker (滚动窗口) + `consume()` / `reserve()` / `release()` | budget 不重不漏，崩溃恢复测试 |
| L3 | `trajectory/shape_predictor.go` | EWMA-based 请求 shape 预测 (tokens / duration / cache likelihood) | 预测误差 < baseline naive 30% |
| L4 | `trajectory/scoring.go` | `score(trajectory, request)` = remainingArea − fpNoise + cacheGain − conflictPenalty | 单测 4 评分子项独立 |
| L5 | `trajectory/scheduler.go` | 主入口 `Schedule(req)` 替代 `selector.Select` (保 interface 向后兼容) | 现有 selector 测试套件 100% 通过 |
| L6 | `trajectory/ledger_store.go` | DB-backed ledger (新表 `account_trajectories` + `trajectory_reservations`) | 整合测试 reserve→commit→settle 闭环 |
| L7 | `trajectory/migration.go` | 灰度开关：`feature_flag=use_lbts` 控制按 tenant 切流 | 双跑 shadow mode 一周指标对照 |
| L8 | `trajectory/observability.go` | Prom metrics + routing reason JSON 扩展 (新增 trajectory_id / projected_remaining_area / reservation_age) | grafana 看板就绪 |

---

## 5. 风险 (≥ 4)

1. **崩溃恢复 / split-brain**：ledger 是有状态系统；多副本 gateway 同时 reserve 同一账号会超额。**缓解**：所有 reservation 走 DB advisory lock + 心跳 lease；超时自动回滚；Phase 1 单实例先跑。
2. **Shape 预测错误放大调度错误**：预测偏差 → 错预订 → 真请求来时无可用面积 → 退化甚至比 RR 差。**缓解**：永远保留 20% 紧急面积 (绕过 reservation 的 fast lane)；预测置信度 < 阈值时退化 RR。
3. **复杂度蔓延 / 调试难**：轨迹 + 预订 + 衰减 + 评分 4 维状态；线上排障比 selector 难一个数量级。**缓解**：routing_reason JSON 必须包含完整决策栈快照；每决策可回放；shadow mode 长跑。
4. **clean-room 错觉**：虽然 yield management 是公开数学，**实施细节**仍可能撞 sub2api / new-api 某段逻辑。**缓解**：实施前 reference-project-miner 扫一遍这些项目的 selector 路径，确认行为差异，写入 ADR。
5. **billing 强一致性冲突**：现有 `claim_gate.Reserve` 假设瞬时；LBTS 引入"未来面积预扣"破坏前提。**缓解**：L6 引入 reservation 表的同时，把 claim_gate 改为引用 reservation_id 而非账号 ID；保持账本不变。
6. **Owner 容忍度风险**：Owner 明说"复杂度不要过高"。LBTS 整体不算简单。**缓解**：Phase 1 砍到只用 budget + simple ranking (≈ L1+L2+L4 简化版 + L5)，先验证 budget 模型正确性再叠 shape predictor / reservation。Phase 1 总 LoC < 1500。

---

## 6. clean-room 边界

| 是否需要读外部参考 | 内容 |
|---|---|
| **不读** | sub2api / new-api / one-api / portkey / litellm / helicone / cursor-api 的 selector / scheduler / pool / route 任何源代码 — LBTS 是不同范式，读了反而污染 |
| **可以读 (公开学术 / 行业标准)** | 1) 航空 yield management 公开教材 (PASSUR / Belobaba 1989 论文) 2) Google "tail at scale" 公开 paper 3) PostgreSQL advisory lock / row-level lock 文档 4) Anthropic / OpenAI 公开的 quota / cache TTL 文档 |
| **必须读 (HUAKAI 内部)** | 1) 现有 `pool/selector.go` `pool/db_sticky_store.go` `billing/claim_gate.go` — 为了接口兼容和迁移而非抄袭 2) `docs/RULES.md` 与升级 #1-#7 plan — 确保不破坏既有 invariant |
| **lane 安排** | 我 (codex) 是 specifier — 已写完本草案；下一步需要 Claude 同名 plan 横向对照 + Owner 决策；实施开始前 reviewer lane (单独 agent session) 用 reference-project-miner 反向证伪 |

---

## 备注

本草案在不读 Claude 同名 plan 前提下独立完成。如 Claude 草案给出与 LBTS 收敛/正交/冲突的方案，应在 synthesis 文档里 surface 三种走向 (合流 / 互补 / 二选一) 让 Owner 决策。我个人对 LBTS 信心较强，因为它是唯一**真正改变了"调度的原语"**的方案；其他都是更聪明地排序。
