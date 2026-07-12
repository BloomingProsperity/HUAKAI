# 修复:claim 租约 90s 远短于请求生命周期(600s)→ LeaseSweeper 腰斩活流/慢settle 致亏钱+超并发(S1)

> 日期:2026-06-20 · 切片:核心资金链 bug(money 链二次深猎 wust32t4z 确认 CONC-1+LEAK-1,各 3/3 refuter;本人真码核根因)
> 基线 `feat/frontend-portal` @ dde537b4 · 落点 `backend/internal/billing/claim_gate.go` + `backend/cmd/gateway/wiring.go`(均非 proxies 碰撞写面)

## 1. 缺陷(本人真码核实)

claim 的 `lease_expires_at` 在 reserve 时设为 `now + LeaseWindow`,`LeaseWindow` 默认 **90s**(`claim_gate.go:59` 构造默认、`:233` fallback),注释自承"Lease window for claim row orphan-sweep recovery"。grep 确认**请求生命周期内 claim 租约全程无续租/心跳**(`lease_expires_at=` 写路径只有 slot 表 + reserve 时 insert)。

而 `cmd/gateway/middleware.go:262` `TotalStreamTimeout` 默认 **600s**,且 AI relay 路径豁免连接级总超时(为不砍长跑推理/agentic)。`LeaseSweeper`(30s tick,`wiring.go` 无条件 Start)的 `SelectExpiredReservingClaims` 仅按 `status='reserving' AND lease_expires_at < NOW()` 捞,`lease_sweep.go:93` 无条件 `Abort(reason='lease_expired')`,**无任何活性/已交付检查**。

→ **任何跑超 90s 的合法流(大输出/慢上游/长 tool-use,Claude 流式常见)在仍在传输时,claim 已过期被 sweeper Abort。** 请求在上游转发期间不持 DB 行锁(Tx1 已 commit),故 `FOR UPDATE SKIP LOCKED` 能锁到活 claim。

## 2. 后果(CONC-1 + LEAK-1,同根因)

- **CONC-1**:活流被中途 Abort → claim reserving→aborted、释放 hold、in_flight 减 1。流真正结束调 Settle → `GetClaimForSettle` 要 `status='reserving'` → 已 aborted → ErrClaimNotReserving → settlementrecovery DLQ → 重放仍 ErrClaimNotReserving(claim 非 committed)→ quarantine 需人工。净效果:**客户拿到完整响应却永不自动计费(亏钱)** + in_flight 在流仍活时减 1 → 账号可被再次选中**超 cap_concurrency**。
- **LEAK-1**:已上游成功交付但 settle 慢(DLQ 退避)的 reserving claim 被 lease-sweep 抢先 **0 成本 Abort**(`settler.go` Abort 硬置 actual_cost=Zero)→ 真实用量永久 0 成本入账(确定性 undercharge),重放不可纠正(claim 已 aborted)。

严重度 **S1**(money-coupled:亏钱 + 容量不变性破坏;常见路径;零测试覆盖)。

## 3. 三家对照(#16)

claim 级"reserve→DB 租约→后台 sweeper 回收孤儿"是 HUAKAI 自有的 Tx1/Tx2 DB claim 机制。`~/refs/sub2api`(单相后付,无 reserve/无租约)、`~/refs/new-api`(两相但锚点是内存 session flag,无 DB 租约 sweeper)、`~/refs/CLIProxyAPI`(纯 relay 无计费)均**无等价的 claim 租约 sweeper**。故无可直接对照实现,本缺陷是 HUAKAI 自身 lease 尺寸错配。三家共性:它们都不会"在请求仍在跑时回收其计费占位"——HUAKAI 应保证同样性质。

## 4. 修法(放大 claim 租约,env 可配,不动 slot 租约)

根因:90s 是给 **slot 抢占窗口**(空 slot 快速回收)设的,误用到了**必须活过整个请求**的 claim 上。

- `claim_gate.go`:新增 `const DefaultClaimLeaseWindow`,设为显著大于最大流式请求生命周期 + settle/DLQ 余量(默认 **30min**);构造默认(`:59`)与 fallback(`:233`)都改用它;修正误导注释。
- `cmd/gateway/wiring.go`:claim gate 构造改为 env 可配(`HUAKAI_BILLING_CLAIM_LEASE`,默认 `DefaultClaimLeaseWindow`,复用 `streamDurationEnv` 通用 duration 解析)——**默认翻转留 env 开关**(满足硬规则),可逆。

**为何不动 slot 租约/不加心跳**:
- slot(90s 租约)受"NOT EXISTS reserving claim"守卫:claim 仍 reserving 时其 slot 不被孤儿清扫(二次深猎已证此守卫成立,驳回了"reserving claim 的 slot 被误清扫"候选)。放大 claim 租约后,活流的 claim 整程 reserving → 其 slot 全程受保护。故**不需放大 slot 租约**(放大反而会延长 POOL-LEAK-01 的 count_tokens 泄漏窗口)。
- 加流式心跳续租需写 `gatewayhttp`/`gateway`(proxies 碰撞写面)+ 每心跳一次 DB 写,blast radius 大;放大租约只动 billing + wiring(非碰撞面),更小更安全。

**真孤儿(进程崩溃,claim 卡 reserving)**:在新租约窗口(默认 30min)后仍被回收,仅回收延迟变长(hold 多占一会),money 安全无丢失。

## 5. 强测试(变异证 RED,integration_pg + 单元)

- **单元**:`DefaultClaimLeaseWindow >= 15min`(且 > 默认 TotalStreamTimeout 600s)。变异:还原 90s → `90s < 15min` → RED。
- **integration_pg**(dev DB `HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable`,`-tags integration_pg`):reserve 一个 claim,从 DB 读其 `lease_expires_at`,断言 `lease_expires_at - reserveTime >= 10min`(覆盖最大请求生命周期)。变异:90s 窗口 → `~90s < 10min` → RED。
- 既有 `TestSettler_LeaseSweepAbortsExpiredClaims`(显式把 lease 设过期)不受窗口改动影响,继续守"真孤儿仍被回收"——确保没把 sweeper 功能改坏。

## 6. 成功标准 / blast radius / 风险 / 决策点

- 成功:build/vet 绿;新测试 GREEN 且变异 RED;billing + cmd/gateway 干净基线 `-count=1`(含 integration_pg)fail 0;对抗审查零 S0/S1。
- blast radius:`claim_gate.go` 常量+2处引用+注释,`wiring.go` 一处构造改 env 可配;无 schema 改、无新依赖。
- 风险:低。唯一行为变化=claim 孤儿回收从 90s 延到默认 30min(对真孤儿仅延迟回收,money 安全;对活流=修正,不再误 abort)。env 可回退 90s。
- 决策点:money-core,按 Owner 全权 + 安全网(变异测试+对抗审查+干净基线)自主落地;默认翻转留 env 开关已满足硬规则;无 schema/auth/deploy 改动。
