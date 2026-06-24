# 2026-06-24 后端质量刷新 round116：budget 错误指标标签收敛

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理 `backend/internal/budget` 中 `errType` 将错误文本直接截断进入 metric label 的质量债；可新增或调整同包判别式测试；不触碰 security 专项计划、不改 quota/billing ledger/auth/schema/LICENSE/deploy。 |
| Success criteria | `errType(nil)` 保持空标签；普通错误和自定义错误输出稳定、低基数、ASCII、合法 UTF-8；错误文本中的租户、请求号、中文动态内容不进入标签；现有 budget 行为测试不因本改动语义漂移。 |
| Time estimate | 10-20 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 影响 `budget` 服务日志/指标标签字段 `error_type`，不改变额度预留、结算、释放状态机。 |
| Failure modes | 反射处理 typed error 不稳定；label 清洗过度导致空标签；测试只检查“不等于坏值”而未锁定好值。缓解：锁定标准库错误和自定义错误的期望标签，并检查 UTF-8 与允许字符集。 |
| Decision points | 若需要改变 budget fail-open/fail-closed 策略、ledger 语义或数据库结构，停止并请求 Owner 确认；本轮预计不需要。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 读取生产场景与验收测试技能规则；3. 只核对 `budget` diff 和本计划文件；4. 执行静态检查与可用测试；5. 记录 Go 工具链缺失造成的未运行项。 |

## 执行顺序

1. 核对 `backend/internal/budget/service.go` 中 `errType` 是否已从错误文本截断改为类型标签。
2. 核对 `backend/internal/budget/service_test.go` 是否有判别式测试覆盖动态错误文本、中文内容、UTF-8 与 ASCII label。
3. 必要时做最小补丁，不扩大到 budget fail-policy 或 ledger 状态机。
4. 运行 `git diff --check`、禁词扫描、可用的静态模拟；尝试 `gofmt` 与 `go test ./internal/budget` 并如实记录环境缺口。
