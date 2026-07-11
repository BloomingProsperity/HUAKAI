# settlement_intents 对账 sweeper(B 类阶段 2)— 计划(Claude)— 2026-07-11

## 背景
B 类阶段 1 已落地持久结算意图表 settlement_intents(状态机
pending→delivering→settling→settled/aborted/failed/superseded),正向生命周期由同步 hook
以 **fail-open** 推进(写意图失败绝不阻塞主结算)。fail-open 的代价:意图行可能**漏标终态**,
而权威主账本 billing_ledger_claims 早已达终态,两者产生漂移。阶段 2 补一个后台 sweeper,
把悬挂意图行追平权威 claim。

## 三镜 + 内部参照(§16 / §17)
- **sub2api**:支付订单周期对账 = leader 选主 + 外部网关为权威源 + `WHERE status=pending`
  守卫式 UPDATE 单胜者 + 置过期前再查一次防误杀;outbox 清理的 **10s 宽限期**(防事务内已分配
  id/时间戳但提交延迟被跨过漏读)。
- **new-api**:超时清扫 sweeper(100/轮)+ per-task CAS(`WHERE status=旧值`)+ DB 租约 fencing
  (写终态要求锁未过期);对账动作分级(仅标记 vs 真金差额补偿写账本日志)。
- **CLIProxyAPI**:纯 relay,无结算兜底等价物(grep 证据,无账本/订单表)。
- **HUAKAI 内部同域参照**:`billing.PendingReconciliationWorker`(grace cutoff + batch + Ticker +
  RunOnce)、`billing.LeaseSweeper`(30s tick,守卫式 `WHERE status=reserving`→abort,靠 CAS 单
  胜者、不靠 leader 保正确性)、`mediatask` orphan sweeper(分页 ListPending + 乐观 MarkReconciled)。
  已有 `alerting.PostgresLeaderLock` 单例抽象可复用。

## Scope(全部在 env-gate 默认关之后,不新触 money 语义)
sweeper 只做「拿悬挂意图 → 查主账本真相 → 追平意图终态」,**不推断金额、不改钱**;金额取自
已权威的 claim/usage,主账本始终是唯一真相源。

### S2-1 Store 扫描 + 守卫式对账写
- `ListStaleNonTerminal(ctx, staleCutoff, createdBefore, limit)`:扫
  `status IN ('pending','delivering','settling') AND updated_at < staleCutoff
   AND created_at < createdBefore` ORDER BY updated_at,LIMIT。`createdBefore = now - 宽限`
  (借 sub2api 10s 教训,防慢提交竞争漏读)。
- 追平写用**守卫式乐观锁**(复用阶段 1 version):
  - `MarkSettledIfStale(id, version, actualCost, settledAt)` →
    `UPDATE ... SET status='settled' ... WHERE id=? AND version=? AND status IN (悬挂态)`;
  - `MarkAbortedIfStale(id, version)`、`MarkSupersededIfStale(id, version)` 同构。
  - 受影响行=0 即让位(正常 hook 或另一副本已推进),天然幂等,无需幂等键表。

### S2-2 对账 worker(镜像 PendingReconciliationWorker)
- `NewSettlementIntentSweeper(store, claimAuthority, opts)`:Ticker(默认分钟级,如 60s)+
  RunOnce + panic recover + batch(默认 100)+ grace/宽限可注入。
- 每条 stale 意图,按 (tenant_id, claim_id, attempt_seq) 查权威 claim 状态:
  - **claim committed** 且 attempt_seq 一致 → `MarkSettledIfStale`(actual_cost 取自 claim/usage 权威值);
  - **claim aborted** → `MarkAbortedIfStale`;
  - **claim reserving**(在途)→ 跳过(LeaseSweeper 负责 claim 层,勿误杀在途请求);
  - **claim 当前 attempt_seq > 意图 attempt_seq**(claim 已复活重试)→ 旧意图 `MarkSupersededIfStale`
    (proof 绑 attempt:意图永远绑它被创建时的权威 attempt,过期 attempt 的意图作废而非误标 settled)。
- worker 整体 fail-open:单条对账错/panic 不影响其余、不 crash;结构化日志 + 轮次计数。

### S2-3 抗误判(§17 配合完整性关键)
- **grace 足够长**:staleCutoff = now − max(最长请求生命周期, settle 超时 30s + 上游 timeout)留足余量
  (先取保守值如 10min,注释说明依据),避免长流式在途请求被误判悬挂。
- (决策点,见下)是否补「在途续期 bump updated_at」:若阶段 1 的 delivering/settling 期间
  updated_at 不刷新,长请求靠 grace 兜;要不要加处理协程周期 bump,交 Owner/实现时权衡
  (sub2api 心跳刷新 score 是更强防线,但增复杂度)。

### S2-4 wire + 默认关
- 紧邻 `LeaseSweeper`/`PendingReconciliationWorker` 在 wiring.go 构造;env-gate 默认关
  (复用阶段 1 `HUAKAI_SETTLEMENT_INTENT_ENABLED` 或独立 `..._SWEEPER_ENABLED`,实现时定)。
- 多副本正确性靠 S2-1 守卫式 CAS(必需);leader 选主(复用 PostgresLeaderLock)作减重复扫描的
  可选优化,本切片可标注后续,不作正确性依赖(对齐 LeaseSweeper 现状)。

### S2-5 真 PG 并发故障注入测试(§17 硬规则:必测并发)
integration_pg 标签,纯净迁移库,判别性 + 并发:
1. claim committed 但意图卡 delivering → sweeper 追平 settled,断言金额=claim 权威值。
2. claim aborted 但意图卡 pending → 追平 aborted。
3. claim 复活到更高 attempt → 旧意图标 superseded(不误标 settled)。
4. claim reserving(在途)→ sweeper **不动**它(断言状态不变,防误杀)。
5. **并发竞争**:N 个 goroutine(sweeper × 正常 hook × 另一副本 sweeper)同时追平同一悬挂意图
   → 断言恰好一方成功、version 单调 +1、无重复终态、无重复金额。
6. 宽限期:created_at 在宽限窗内的新意图不被扫到(防慢提交漏读)。
每个断言配变异证(删守卫 WHERE / 去 grace / 错 attempt 匹配 → 对应测试变红)。

## Clean-room / 纪律
- 复用 HUAKAI 现有 worker 范式(PendingReconciliationWorker/LeaseSweeper),不逐字搬三镜。
- 不推断金额、不改 money 语义;不动阶段 1 已落地的正向 hook 行为(加不回归测试)。
- 注释中文;codex 实现,禁 commit,留 diff 给 Claude 亲检 + 真 PG 并发测试。

## 成功标准
- 单元 + integration_pg 全绿,S2-5 六类并发/判别测试变异全真红。
- 不回归阶段 1 正向生命周期(独立断言)。
- env 关时 sweeper 惰性不构造、不影响热路径;env 开时正确追平且并发单胜者。

## Blast radius / 决策点(Owner surface)
- **不触 money**:只追平意图行至已权威的 claim 终态,金额取自 claim,不新增扣费/退费。故判定
  **可自驱**(与阶段 1 同域授权),但仍 surface 两个决策点:
  1. 是否补「在途续期 bump updated_at」(更强抗误判 vs 复杂度);
  2. grace 具体阈值(保守 10min vs 更紧)。
- 先按保守默认(grace 10min、不补在途续期、靠守卫 CAS + grace 双保险)实现,决策点标注后续可调。
