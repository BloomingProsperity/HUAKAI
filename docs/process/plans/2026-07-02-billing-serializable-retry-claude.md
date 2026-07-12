# 缺口③ billing 预扣 hold 并发 500——Serializable 重试配套(Layer 1)

- 日期:2026-07-02
- 作者:Claude(三 S1 缺口 fusion-upgrade 第一片,Owner 批「全权按次序推进三片」+「批准 Layer1 做法」)
- 分支:`fix/billing-serializable-retry`(基于 origin/feat/fe-wire-users-mod @ 10eaed36)

## 背景(真码 + 实测)

2026-07-02 P0 细粒度并发 E2E 实测:同一用户并发多个请求争抢同一 `user_balances` 行时,
`ClaimGate.Reserve`(`claim_gate.go:90` 起 `pgx.Serializable` 事务 + `SELECT FOR UPDATE` 条件扣)
抛 40001 序列化失败;`claim_gate.go` 只把 23505 唯一约束冲突映射成 `ErrClaimRace`,**40001/40P01
无分支**,落到 `Reserve` 末尾通用 `err != nil`,经端点映射成 **HTTP 500 `reserve_error`**,无重试、无
Retry-After。症状:单用户并发大多数秒级 500。钱是安全的(事务干净回滚不扣费),但可用性烂。

## 三镜对照(§15/§16)

| 维度 | sub2api@0b8e5eec | new-api@52858ad1 | CLIProxyAPI@cde9336b |
|---|---|---|---|
| 隔离级 | 刻意不用 Serializable;转发后 READ COMMITTED 单行条件 UPDATE,行锁排队不抛 40001(`usage_billing_repo.go:116-121`) | 不用 Serializable;Redis 原子递减 + 异步 DB(`billing_session.go:184-230`) | 无 billing 包(纯 relay) |
| 超支 | **允许**:条件 UPDATE 返 0 行→回退无守卫 UPDATE,余额可负标 `BalanceOverdrafted`(`usage_billing_repo.go:177-207`) | **允许**:DB 可为负(`user.go:939-962`) | — |
| 对外 | 并发全 200,超支事后追 | 并发全过 | 无等价物 |

**关键**:三镜靠**放弃 Serializable + 允许超支**绕开并发冲突。HUAKAI 是唯一用严格预扣 hold
(Serializable + 条件 UPDATE)在扣费前防超支的——这是强项,只是缺 Serializable 的教科书重试配套。

## 范围(Layer 1,不倒退超支防护)

1. `internal/billing/retry.go`(新,非碰撞):`retryReserve` 有限重试 + decorrelated-jitter 退避
   (base 2ms / cap 50ms / max 5);`isReserveSerializationConflict` 只认 40001/40P01;业务哨兵
   (ErrClaimRace/ErrFingerprintConflict/ErrInsufficientBalance)立即返回不重试;ctx 早退;退避
   sleep 在事务外(连接已归还);耗尽映射 `ErrClaimRace` 并打一条 `slog.Warn`(运营 grep 日志定位
   高并发争用,比不透明 500 好定位;不引入无消费者的导出计数器 getter,避免死代码/堆砌)。
2. `internal/billing/claim_gate.go`:`Reserve` 拆成外层(算幂等键 + retryReserve 包裹)+ `reserveOnce`
   (原事务体);struct 加可注入 `reserveSleep/reserveRand`(生产 nil→默认,单测注入确定性)。
3. **5 端点补 `ErrClaimRace`→409+Retry-After 分支**(`{audio,completions,embeddings,images,rerank}http/billing.go`,
   均非碰撞区):此前只有 chat 有该分支,5 端点的 `ErrClaimRace` 落进通用 500——**顺带修好这 5 端点
   「reserving 幂等命中(`claim_gate.go:113` 返 ErrClaimRace)今天就误报 500」的潜伏 bug**。

## 三维 delta(§12)

- **算法**:40001/40P01 归可重试瞬时错误 + decorrelated-jitter 指数退避——强于三镜(无重试),
  也强于项目内既有 mediatask/dispatcher 的「立即无退避重试」(惊群下同步再撞)。
- **生态**:序列化重试/耗尽计数器让运营可观测真实争用;耗尽契约从不透明 500 升级为**可重试的
  409+Retry-After**(使用者可自动退避重试=更好用)。
- **架构**(留 Layer 2,单独 gate):per-(tenant,user) advisory xact lock 把「检测-中止-重试」改「排队-直行」。

## 功能等价(不缩水)+ money 安全

- 凡现在能成功的请求改后仍成功;并发下的 500 改成 200(重试成功)或可重试 409(耗尽,使用者自动重试)。
- **余额永不为负**——保留条件 UPDATE `WHERE (balance-held)>=cost` 防超支,不像 sub2/new-api 允许负余额。
- 重试 = 回滚后重跑干净事务(reserveOnce 纯事务、无外部副作用/回调),不重复扣不漏扣。

## 成功标准 / 测试

- `go build ./... && go vet` 绿;全量 `go test ./...` 绿;quality-gate PASS。
- §14 变异全证红:①去重试判别→「重试后成功」单测红 ②耗尽不映射 ErrClaimRace→「耗尽映射」单测红
  ③去 retryReserve 包裹→并发集成测试泄漏 40001 红(3/3 稳定)。均已完成。
- 并发集成测试(真 PG):16 goroutine 同用户各建独立 claim,断言无原始 40001 泄漏 + 余额不为负 + held 一致。

## 影响面 / Owner 门

- 全落非碰撞区(internal/billing + 5 端点 http 包);**零 schema、零隔离级变更、零默认行为翻转**(纯 500→200/409)。
- Owner 门:属 billing 核心 money 路径,逻辑上纯修 bug 不动扣费正确性,已获 Owner 批准 Layer1 做法。
- 后续:Layer 2(advisory lock ± READ COMMITTED)单独排期,Layer 1 上线观测争用指标后再决定是否需要。
