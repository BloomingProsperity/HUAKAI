# 2026-06-23 backend-quality-renew-round94-budget-error-label-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| --- | --- |
| Scope | 修 `backend/internal/budget/service.go` 的 `errType`，避免按字节截断 `err.Error()` 造成 UTF-8 破坏与 metric label 高基数；补对应单元测试。 |
| Out of scope | 不改预算放行/拒绝策略，不改 Redis/memory store，不改 billing/quota/schema，不调整 fail-open 语义。 |
| Success criteria | `errType` 返回稳定、低基数、ASCII label；动态错误正文不进入 label；中文错误不会产生无效 UTF-8；`nil` 仍返回空字符串。 |
| Time estimate | 约 20-30 分钟。 |
| Blast radius | 仅日志/metric label helper 与测试；不影响预算计数、扣减、回滚或结算。 |
| Failure modes | 过度归一化导致排障信息不足：保留错误具体类型而非固定常量；类型名含特殊字符：统一转成小写 ASCII 下划线；`fmt.Errorf("%w")` 包装错误只显示 wrapper 类型：这是可接受的低基数选择，避免正文泄漏。 |
| Decision points | 若 Owner 要把预算 fail-open 策略本身统一到单点 policy，需要另开计划；本轮不扩大到策略层。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 production-scenario-review 技能；3. 已核 `errType` 当前为 `err.Error()` 字节截断；4. 已确认调用点只用于 `slog.String("error_type", ...)`。 |

## 执行顺序

1. 将 `errType` 改成基于错误类型的稳定标签。
2. 添加 label 清洗 helper，限制输出字符集与长度。
3. 在 `service_test.go` 增加中文错误、动态文本、`nil` 的判别式测试。
4. 运行可用检查；若 Go 工具链缺失，记录真实限制。
