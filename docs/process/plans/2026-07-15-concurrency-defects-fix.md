# 三个既有重并发降级缺陷修复 · 综合裁定稿

日期:2026-07-15。双计划交叉:Claude 稿(-claude.md)+ Codex 稿(-codex.md)。Owner 已批「修」;方向性四决策点 Owner 已授权自主裁定(2026-06-26「你定吧」授权延续)。

## 交叉结论:两稿高度一致,以 Codex 稿为实施蓝本

- **一致①(S1 根因与修法完全相同)**:Reserve 事务闭包内吞错点把 40001/40P01 转成 fail-closed deny 且闭包返 nil,外层重试环永远看不到。修法=吞错前先判 `isPgRetryableTxConflict`,瞬时冲突原样 return err 交外层整事务重跑;其余错误 fail-closed 纵深不动。**Codex 稿多找出第三个吞错点**(released/expired 复活路径 service.go:614-623),采纳;共三点位,用极小 helper 统一。
- **一致②(S2 改判一致)**:两稿独立亲核后均推翻旧诊断——Release 已有 3 次重试+补偿入队+默认开启的分钟级 worker。残余真缺口两稿互补:Claude 稿指出「入队交接自身可失败」;Codex 稿进一步定位**生产调用 ReservationID=0 会让 reconciliation_needed 标记静默跳过**(settler.go:188-192 只传 tenant+claim;service_settle.go:671-679 仅 ID 非零才标记)+交接复用已取消的请求 ctx。采纳 Codex 四层修法:Release 独立预算/按 tenant+claim 守卫式恢复标记(顺带缩 lease)/bounded cleanup context 交接/标记成功即具 stale 清扫资格。
- **一致③(缺陷3 方向一致,Codex 机制更优)**:Claude 稿方向「降打穿率+缩冻结窗」;Codex 落成具体机制=Abort 独立重试预算 + 耗尽后守卫式 UPDATE 把 claim lease 缩至当前时间(仅 status='reserving'、不碰钱不改终态),交由既有 30 秒 lease sweeper 下一轮真 Abort。头契约、fail-closed、双 sweeper 全保留。采纳。
- **无冲突项**。测试矩阵采用 Codex 稿 AT-CD1/CD2/CD3 全 21 项+变异清单;执行顺序采用其增量 A-F。

## 四个决策点裁定(自主,依据两稿证据)

1. **重试预算**:接受 quota Release 6 次、billing Abort 9 次为初值;随附 attempt 分布低基数指标,调整须凭数据,四原则(独立策略/有限预算/仅两 SQLSTATE/整事务重跑)不退让。
2. **cleanup timeout**:1 秒(context.WithoutCancel+timeout;只做守卫标记+入队两类写,禁一切主业务)。
3. **恢复时限门**:接受 quota 125 秒 / billing 65 秒(各两个 worker tick+余量)作为「PG 正常」条件下的测试门;PG 持续故障明确排除、走告警。
4. **soak 门**:确定性注入=硬门(变异必红);自然负载 30 轮=无回归趋势门(基线为 0 时只做 no-regression,不强推 100 轮)。

## 执行安排

- 实施=全新 codex clean implementer 会话(不复用读过非 MIT 源码的 specifier 会话;实施只读 HUAKAI 本地码+综合计划)。
- 按增量 A(缺陷1)→B+C(缺陷2 热路径+恢复)→D+E(缺陷3)→F(整链 E2E+soak)分批派;codex 沙箱禁 socket,**真 PG integration/E2E 全部由 Claude 本机跑**,codex 只交付代码+unit+integration 测试文件。
- 每增量:测试先行证 RED→实现→亲手变异(Claude 复核 RED 记录)→CI 门。约束照 Codex 稿 §1.1/§1.2:不改 schema、不改对外错误码/头、不翻默认开关、不缩 30 分钟全局 lease、正常路径仍单事务。
