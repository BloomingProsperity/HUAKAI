# 2026-07-15 增量 K：Deferred 预算与槽终结并发

| 项目 | 内容 |
| --- | --- |
| Owner directive | “增量K:两件小事收官三缺陷arc”；K1 要求 `ErrReleaseDeferredForRevival` 不消耗终停预算，K2 要求补槽回收与 `Abort/Settle` 真 PostgreSQL 并发对撞证据。 |
| Scope | **In**：`internal/quota` reconciler 的终停判定及其判别单测；`internal/billing` 既有槽回收集成测试中的真 PostgreSQL 并发用例；必要的中文注释与本计划。**Out**：schema、SQL 生成物、对外错误码、默认开关、生产部署、参考项目源码、git commit。 |
| Success criteria | 1. 已耗到 `maxAttempts-1` 的 Deferred job 仍回 `queued`，`next_run_at` 是正常未来 backoff，claim 后续终态时同一 job 能成功解毒；2. invalidated 与普通预算耗尽仍终停；3. 槽回收与 `Abort`、`Settle` 同刻竞争若干轮后，每轮槽仅一个终态且 `in_flight_count` 恰减一次，调用方不见原始 `40001/40P01`，终结成功或幂等容忍；4. 指定 `vet/test` 通过，真 PG 用例至少在无数据库环境下完成编译验证。 |
| Time estimate | 约 45–75 分钟墙钟时间；单 agent，不派生并行 agent。 |
| Blast radius | K1 影响 quota 补偿 job 的失败状态机；误判会造成永久不解毒或无限重试错误类型。K2 仅增测试，但会验证 billing claim、槽状态及账号并发计数的事务一致性。 |
| Failure modes | Deferred 包装层未被识别：用 `errors.Is` 覆盖 `RetryableError.Cause`；例外扩到 invalidated/普通错误：补负向断言；测试 fixture 未真实竞争：用 goroutine barrier、独立 Serializable 事务和胜序计数；只看最终值掩盖双减：每轮独立 seed 且从固定 `in_flight_count` 断言；数据库不可达：仍用 `go test -run` 编译真 PG 文件并明确交给 Claude 本机运行。 |
| Decision points | 当前无 Owner 中途决策点：不改 schema、对外契约、默认参数或高风险生产逻辑；若现有 SQL 守卫无法表达要求或必须改 schema，立即停下请 Owner 确认。 |
| Pre-execution checklist | 1. 确认工作树与相邻实现；2. 记录 K1 现状 RED；3. 先写/加强判别测试；4. 以最小条件修改终停判定；5. 对 K1 做删除例外变异并确认红；6. 将 K2 放入既有 integration 文件避免扩大 billing 包文件预算；7. 编译及运行可用检查；8. 复核仅有预期 diff、中文注释、无 schema/错误码/默认开关变化。 |
| Concrete execution order | 1. 完整阅读 K1 fake store、K2 槽守卫与 seed helper；2. 写 K1 接近预算上限的 Deferred 恢复测试和负向测试；3. 运行定向 RED；4. 修改 `failRunningJob` 并跑 GREEN；5. 临时删除例外做变异、确认红后恢复；6. 写 Abort/Settle 两分支的真 PG 对撞测试与共享 helper；7. `gofmt`；8. 运行指定 `go vet`、普通 `go test`、integration 编译检查；9. 汇总真 PG 测试名与本机待跑命令。 |

## 独立性声明

本计划由 Codex 依据 Owner 本轮指令独立撰写，未读取同描述符的 Claude 计划。执行只使用 HUAKAI 本仓库规范与代码。

独立稿完成后对照了已批准的 `2026-07-15-concurrency-defects-fix.md` 综合计划：K1 延续 quota 解毒链“缩短异常冻结且不改终态守卫”的裁定，K2 补强 billing Tx2 与槽回收的并发证据；未发现 schema、外部契约、默认开关或事务边界冲突。Owner 本轮指令明确授权这两个收官增量，因此继续执行。
