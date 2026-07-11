# settlement_intents 对账 sweeper(B 类阶段 2)亲检 + 真 PG 并发实测结果 — 2026-07-11

## 结论:亲检全绿,真 PG + -race 并发/故障注入通过,零 S0/S1

sweeper 让 fail-open 漏标终态的悬挂意图行追平权威 claim(committed→settled 复制权威金额、
aborted→aborted、reserving→跳过在途、claim 更高 attempt→superseded),只追平不推断金额、
不改主账本。三镜(sub2api leader+守卫式 UPDATE / new-api CAS+租约 fencing / CLIProxyAPI 无等价物)
+ 内部参照(PendingReconciliationWorker/LeaseSweeper)综合设计,计划见
docs/process/plans/2026-07-11-settlement-intent-sweeper-claude.md。

## 亲检(独立复跑,不采信 codex 报告)

- gofmt / go build ./... / go vet 全 exit 0;sweeper.go 376 行(codebudget ≤600 绿)。
- unit:settlementintent / gatewayhttp / cmd/gateway / config / db/billing 全 ok。
- **真 PostgreSQL + -race** 跑 integration_pg:PASS 1.33s(纯净迁移库,12 goroutine CAS 竞争 +
  attempt 复活 + 创建宽限)。
- 阶段 1 正向 Mark* 生命周期不回归(独立断言,version 依次递增、不触发 stale 写)。

## 变异证(真跑,每处对应测试变红)

| 变异 | 结果 |
|---|---|
| attempt proof 判断 `claim.AttemptSeq > intent.AttemptSeq` 改永假 | 「更高 attempt 只能 superseded」+「ReconcilesByAttemptAndStatus」红 |
| created grace `now-createdGrace` 改 `now`(去宽限) | 「created_at 宽限排除新意图」+「Cutoffs」红 |
| 删 AbortedIfStale 的 `status IN` 守卫 | 「多副本并发单胜者」红 |
| 删 SettledIfStale 的 `status IN` 守卫 | 补断言后「并发单胜者」line203 红 |
| 删 SupersededIfStale 的 `status IN` 守卫 | 补断言后「并发单胜者」line206 红 |

## 亲检抓到并修补的测试 gap(codex 报告未提)

codex 的并发单胜者测试只锁了三条守卫式 CAS 中的 **AbortedIfStale**;删 SettledIfStale /
SupersededIfStale 的 `status IN` 守卫时测试不红(纵深防御守卫无判别锁定)。虽属纵深防御
(ListStale 已过滤终态 + version CAS 挡并发,删守卫不影响正常路径正确性,非 S1/S2),但按测试
完备性纪律,三条守卫应对称锁定。已补两个对称断言:对并发终态后的意图用当前 version 调
SettledIfStale / SupersededIfStale,断言返回 no rows;变异证两者删守卫后各自真红。

## 设计要点(亲检确认正确)

- **attempt proof 优先于 claim 当前状态**:主账本进入更高 attempt 时旧意图只能 superseded,
  绝不拿新 attempt 的终态/金额冒充旧证据。
- **守卫式 CAS 单胜者**:`WHERE id AND version AND status IN (悬挂态)`,与正向 hook 或另一副本
  撞车时 ErrNoRows 让位,天然幂等无需幂等键表。
- **三层 panic 隔离**:定时轮(注入 logger 自身 panic 时改用进程默认 logger 防二次触发)/轮次/
  单条,任一 panic 不 crash、不阻断其余、下一周期继续。
- **双阈值抗误判**:staleAfter 10min(须长于最长请求生命周期,防长流式误判)+ createdGrace 10s
  (防事务已分配时间戳但提交可见性晚于扫描边界被跨过)。
- **默认关**:env-gate 未开时不构造/不 Start、不侵入热路径;优雅启停(running 幂等 + done channel)。

## 已知边界(Owner-gated 后续,非阻塞)

- 未做「在途续期 bump updated_at」:当前靠 staleAfter 足够长兜底长请求;更强防线(处理协程周期
  刷新 updated_at)增复杂度,留后续可选。
- leader 选主(复用 PostgresLeaderLock)减多副本重复扫描:本切片靠守卫式 CAS 保正确性(与
  LeaseSweeper 现状一致),leader 优化留后续。
- 运维人工裁决 UI:仍属后续强制切片。
