# 订阅自动续费 worker(扫到期→扣钱包余额→续期)— 实现计划

日期: 2026-06-29 · 作者: Claude · 切片: P-AUTORENEW

## 调研(§16 三镜 + 真码)

- **真码已核**(worktree 根 backend/):
  - `internal/subscription/worker.go:21-122` ExpiryWorker(到期扫描 worker 范本)。
  - `internal/subscription/activation.go:41-126` `ActivateOrRenewTx`(事务式续期入口,payment 履约就调它;不 begin/commit,共享调用方事务)。
  - `internal/subscription/store_postgres.go:519-542` `ListDueExpiry`(扫到点 active 订阅范本)。
  - `sql/migrations/0060_user_balance_holds.up.sql:1-10` `user_balances` = 真正可变钱包表(numeric(20,8) USD,带 held/version)。
  - `internal/payment/store_postgres_refund.go:110-130` `debitAvailableRefundBalanceTx` = 余额扣减范本:`UPDATE user_balances SET balance=balance-$3 ... WHERE balance-held>=$3`,RowsAffected==0 即余额不足(原子守卫,无需单独 SELECT FOR UPDATE)。
  - `sql/migrations/0092_payment_refunds.up.sql:46-94` `billing_events` 被互斥 CHECK 硬堵:无 reason 列,每个 event_type 强绑特定链接列。**写 reason=subscription_auto_renew 的负向 billing_event 必须加迁移**。
  - `sql/migrations/0075...:79-122` `subscription_fulfillment_effects` 的 admin 源**无唯一幂等锚**(只 order/voucher 有)。

- **三镜对照(自动续费扣余额)**:
  - new-api: 订阅/额度走 quota 计数器扣减,无独立续费 worker;余额扣减是 amount counter 模型。
  - sub2api(默认裁决参照,最成熟):订阅窗口化 USD 上限 + worker 重置;续费动作走钱包余额扣减 + 幂等。
  - CLIProxyAPI:纯 relay account→API,**无 payment/subscription 模块**(显式 no-equivalent)。
  - HUAKAI delta(生态升级):续费扣款 + 续期收进**同一 DB 事务原子**,且用**专表幂等锚**(period_key)防 worker 重跑双扣;默认关 KNOB,opt-in 才续。

## 规格冲突与裁决(诚实记录)

派单要求"扣款写 billing_event,reason=subscription_auto_renew"+"避免 schema 迁移"。真码证明二者互斥:`billing_events` 互斥 CHECK 不允许该行,除非加迁移。同时 money 安全硬约束要求**持久化幂等键防双扣**,而现有表没有可复用的续费幂等锚 → 无论如何都需要一张表来当幂等锚。

**裁决**:加**一条 additive-only 迁移 0147**,建专表 `subscription_auto_renewal_charges`,它同时是:(a) 幂等锚(唯一索引 `(tenant_id, user_subscription_id, period_key)`),(b) 续费扣款的 money-movement 审计行(记 amount_cents/扣款时间/续期前后到期日)。钱包余额扣 `user_balances`(与退款同表同形态)。**不碰** `billing_events`/`payment_credits`/`user_balances` 的任何现有约束(纯加新表 + 新唯一索引)。DB schema 属 Owner-gated → 本计划 surface 给 Owner;但迁移 additive-only + worker 默认关 = 翻 KNOB 前零生产行为变。

## 文件清单(每文件目标包 + 预算)

- 新 `internal/subscription/auto_renewal.go`(subscription 包)— AutoRenewWorker(照 ExpiryWorker)+ `Service.ProcessAutoRenewal`。≤300 行。
- 新 `internal/subscription/store_postgres_auto_renewal.go`(subscription 包)— `ListAutoRenewDue` + `TryAutoRenewSubscription`(money 核心事务)。≤300 行。
- 改 `internal/subscription/store.go` — Store 接口 +2 方法。
- 改 `internal/subscription/store_memory.go` — memory 实现 +2 方法(满足接口;ProcessAutoRenewal 用真 PG 测)。
- 改 `cmd/gateway/wiring.go` — 默认关 KNOB `HUAKAI_SUBSCRIPTION_AUTO_RENEW_ENABLED` + worker Start。
- 新 `sql/migrations/0147_subscription_auto_renewal.{up,down}.sql` — 专表 + 唯一索引。

subscription 包当前 4071 非空行 / 14 文件,加 2 文件后远低于 6000 行 / 20 文件预算。每文件 ≤600 行。

## 续费事务不变量(TryAutoRenewSubscription,单 Serializable 事务)

1. `getSubscriptionForUpdateTx` 锁订阅行,重查仍 `status='active'` + `auto_renew=true` + `expires_at<=now`(并发/重复防护)。否则零副作用返回 skip。
2. 算 period_key = 本订阅当前 `expires_at` 的 RFC3339(同一到期窗口只续一次)。预查幂等锚命中 → 已续过,skip。
3. 取套餐(`getPlanTx`)算续费价 = `plan.price_cents`(锁价快照)。price<=0 视为免费续费(不扣款,直接续)。
4. price>0:条件 UPDATE `user_balances` 扣款(`balance-held>=amount`)。RowsAffected==0 → 余额不足 → 回滚整事务、记 skip(可选审计),**绝不扣款也不续期**。
5. INSERT `subscription_auto_renewal_charges`(幂等锚 + 审计)。撞唯一索引(23505)→ 并发重跑,回滚 skip。
6. 同事务调 `ActivateOrRenewTx(SourceKind=admin/system, EnforceUpgradeOnly=false)` 续期(延长 expires_at + 刷新 caps 策略)。
7. commit。扣钱 + 续期 + 幂等行三者同事务原子,不可分裂。

## 测试(§14,每条一句话防什么 + mutation)

集成测试(integration_pg,真 PG):
- T1 余额够 → 扣款精确 + expires_at 延长 + 幂等行写入。mutation: 拆事务(扣款后续期前 return)→ 钱扣了没续 → 红。
- T2 余额不足 → **不扣款 + 不续期**,余额不变 expires_at 不变。mutation: 去掉 `balance-held>=amount` 守卫 → 余额变负/续期了 → 红。
- T3 幂等:同一 period 跑两次 → 只扣一次只续一次。mutation: 去掉幂等锚预查/唯一索引 → 双扣 → 红。
- T4 `auto_renew=false` 不续。mutation: 去掉 auto_renew 过滤 → 续了 → 红。
- T5 ListAutoRenewDue 只返 active+auto_renew=true+due。mutation: 去 auto_renew 过滤 → 含 false 行 → 红。

单测(无 PG):
- T6 KNOB 默认关:`autoRenewEnabledFromEnv("")` 返 false → worker 不 Start。mutation: 默认改 true → 红。
- T7 worker Start/Stop 生命周期 + TickOnce 计数(照 ExpiryWorker 测法)。

## blast radius

- 默认关 KNOB,worker 不启动 = 现有 auto_renew=true 订阅行为完全不变。
- 迁移 additive-only,不动现有约束/数据。
- 只动 subscription/cmd/gateway,复用 payment 的 user_balances(不改 payment 码)。
- 不碰碰撞包(pool/registry/proxy/channel/gateway*/tlsfp*)。

## 决策点(Owner)

- D1: 续费扣款落 `user_balances`(本计划)vs 落 `billing_events` 不可变台账(需扩互斥 CHECK,更大迁移)。选前者:小迁移、与退款同表语义、money-safe。
- D2: 迁移 0147 是 DB schema = Owner-gated。本切片 worker 默认关,翻 KNOB 才激活自动扣费 = Owner 拍板点。
