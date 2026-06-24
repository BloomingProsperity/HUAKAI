# 计划:修复 media claim 孤儿回收租约过短致长任务亏钱(S1)

- 日期:2026-06-24
- 切片:② media money 修复(Owner 已批"做 ② media money 切片";深核后方向修正)
- 分支:feat/media-timeout-sweeper(基 origin/feat/frontend-portal @ e80d8e35)

## 方向修正(诚实记录)

媒体核查 agent 原报告称缺口是"卡死任务预扣费挂着不释放,需加超时 sweeper"。
**我亲自核真码后发现方向相反、且是真 S1**:

- billing 层已有 `LeaseSweeper`(lease_sweep.go:78),每 30s Abort 任何 lease 过期仍
  reserving 的 claim(`SelectExpiredReservingClaims` 不限 endpoint),**生产已启用**
  (wiring.go:1020-1021)。
- media claim 的 lease **硬编码 90 秒**(store_money.go:90 `now+90s`)且处理中**不续租**。
- media `TaskTimeout` 默认 **15 分钟**(types.go:174);普通请求 claim window 是
  `DefaultClaimLeaseWindow = 30min`(claim_gate.go:52,注释"必须 > 请求最大生命周期")。
- **后果(S1 亏钱)**:任何媒体任务跑 > 90s(视频/复杂任务常态,远在 15min 超时内),
  其 claim 在 90s 后被 LeaseSweeper 提前误 abort、预扣费释放;任务真完成时
  `CompleteSuccess` → `UpdateClaimCommitted` 命中 0 行 → `billing.ErrClaimNotReserving`
  → 已生成内容无法计费(上游已对 HUAKAI 计费)→ **亏钱**,且 task 状态卡 in_progress。

所以 ② 真实内容 = **修 media claim lease 配置错误**,不是加超时 sweeper(超时回收已被
mediatask 自身 TaskTimeout + billing LeaseSweeper 双重兜底,无饿死)。

## 三镜对照(§16)

- **new-api**:媒体任务 quota 预扣**保持到任务终态**(成功/失败/超时才结算/退款),
  超时按 submit_time 扫描;无"独立短 lease",不会出现 lease < task 生命周期的错配。
- **sub2api / CLIProxyAPI**:不做媒体异步任务(无对照)。
- → HUAKAI 修复对齐 new-api"预扣覆盖任务生命周期"语义 + 自身普通请求 30min window 不变量。

## Scope(改动面,3 文件)

1. `types.go`:`CreateTaskInput` 加 `ClaimLeaseWindow time.Duration`;加常量
   `claimLeaseGrace=5min`、`defaultMediaClaimLeaseWindow=15min+grace`;加纯函数
   `resolveClaimLeaseWindow(requested) `(<=0 回退 default,杜绝过短窗口)。
2. `service.go`:`Submit` 传 `ClaimLeaseWindow: cfg.TaskTimeout + claimLeaseGrace`
   (随运维调 TaskTimeout 自动跟随,永远 > 任务生命周期)。
3. `store_money.go`:`insertReservedTask` 的 claim `LeaseExpiresAt` 从硬编码 90s
   改为 `now + resolveClaimLeaseWindow(input.ClaimLeaseWindow)`。

## Success Criteria

- service 传给 store 的 claim lease window >= TaskTimeout(单元 mock 断言)。
- `resolveClaimLeaseWindow` 缺省回退 >= 默认 TaskTimeout(纯函数单元)。
- 集成(PG):新建 media task 的 billing claim `lease_expires_at - created_at` >> 90s。
- 变异:把 lease 写回 90s 常量,长 TaskTimeout 下相关断言转红。
- build/vet/codebudget 绿。

## Blast Radius

- 仅 mediatask 包(非 §6 碰撞)。改 claim 预扣租约时长 = money 行为,但属"修配置错误
  bug",非新政策;不改任何计费金额/退款逻辑/状态机。
- 行为变化:media claim 预扣费保持更久(20min 而非 90s)才被兜底回收——这正是修复目标
  (覆盖任务生命周期),与普通请求 30min window 一致。

## Owner 决策点

§2 money/billing owner-gated。Owner 已批 ② money 切片,且此前对"深审发现的 billing bug
修复"授"全权自主合并(安全网照旧)"。本切片是深审发现的 billing claim 配置 S1 修复,
据此自主推进;最终报告向 Owner 完整 surface 方向修正 + 此 money 行为变化。

## 门禁(codex 401 → 对抗 verifier 替代)

变异证 + 通用 agent 对抗审查(0 S0/S1)+ 干净基线 `-count=1`(单元)+ 集成 PG。
