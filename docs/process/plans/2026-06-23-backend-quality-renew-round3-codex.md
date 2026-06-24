# 2026-06-23 backend quality renew round3 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: HUAKAI 后端质量/架构 renew 第三轮静态审查,重点补查 provider session 测试质量、quota/budget/subscription 一致性、credential refresh 错误分类、缓存与钱账一致性、入口层预算盲区、可维护性债务。Out: security 专项目标、`docs/process/plans/2026-06-23-backend-security-scan-codex.md`、参考项目源码、业务代码修改、findings `.md` 报告。 |
| Success criteria | 输出中文 S0/S1/S2/S3 增量 findings,每条有证据、问题、修法/测试方向;明确哪些是新增、哪些是第二轮确认/降级;不把 goal 标 complete。 |
| Time estimate | 本轮 45-90 分钟静态审查;若环境缺 Go/Rust 工具,仅记录无法运行的验证命令。 |
| Blast radius | 只读代码与新增计划文件;不触碰生产逻辑、数据库 schema、auth/billing/quota 实现。 |
| Failure modes | 误碰另一个目标:通过路径排除避免;把推断写成事实:只写已读文件支持的结论;误报严重度:区分生产可达路径、测试缺口、结构债。 |
| Decision points | 若后续要实施 money path、worker lifecycle、CI 或 package split 修复,需要独立计划和 Owner 确认后再动代码。 |
| Pre-execution checklist | 1. 确认工作树和计划文件;2. 不读取/不修改 backend-security plan;3. 使用 API gateway risk review checklist;4. 先读测试与 wiring 再下结论;5. 最终报告说明验证限制。 |
| Concrete execution order | 1. Provider session 测试 skip/弱断言复核;2. Quota/budget/subscription 重复和 integration_pg 覆盖复核;3. Credential refresh/store 错误分类和 scanner 复核;4. Cache/money/eventbus 一致性补查;5. 汇总增量 findings 与下一轮优先级。 |
