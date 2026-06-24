# 2026-06-24 后端质量刷新 round129：auditledger acceptance 恒等断言修复

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-6：测试质量必须判别式，不能让恒等比较或假绿断言留在 baseline。 |
| Scope | 仅修复 `backend/internal/auditledger/acceptance_test.go` 中 `TestPostgresAdvisoryLockKeyIsTenantScoped` 的 SA4000 恒等比较，并删除 `backend/scripts/staticcheck-baseline.txt` 对应豁免。不改生产逻辑、不改审计 ledger schema、不改数据库迁移。 |
| Success criteria | 稳定性断言改为比较两个独立计算结果；tenant scoped 断言继续比较不同 tenant；staticcheck baseline 不再包含 `internal/auditledger/acceptance_test.go` 的 SA4000 条目。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操 1 个小闭环。 |
| Blast radius | 单个 auditledger 测试文件和 staticcheck baseline。失败时可能把测试改成仍然非判别式或误删其他 baseline。 |
| Failure modes | 断言仍被 staticcheck 识别为恒等比较：用变量承接两次函数调用；baseline 删除过宽：只删精确 `internal/auditledger/acceptance_test.go` 条目；Go 工具链不可用：记录并运行文本级检查。 |
| Decision points | 无需 Owner 中途确认；本轮不触碰 money/security 生产路径。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取测试源码；3. 已确认 baseline 精确命中 `acceptance_test.go` SA4000；4. 已确认修复只影响测试判别力。 |

## 执行顺序

1. 将 advisory lock key 稳定性检查改为两个独立变量比较。
2. 删除 staticcheck baseline 中 `internal/auditledger/acceptance_test.go` 的 SA4000 条目。
3. 运行 scoped whitespace、clean-room 词、符号/baseline 残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
