# 2026-06-24 后端质量刷新 round130：retry budget 测试 SA4000 修复

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-6：测试质量必须判别式，staticcheck baseline 不应保留可直接修复的恒等表达式告警。 |
| Scope | 仅修复 `backend/cmd/gateway/retry_budget_wiring_test.go` 与 `backend/internal/retrybudget/budget_test.go` 中连续 `Allow(7)` 写在同一 `||` 表达式导致的 SA4000；同步删除 `backend/scripts/staticcheck-baseline.txt` 对应两条豁免。不改 retry budget 生产逻辑。 |
| Success criteria | 两个测试都用显式变量承接第一次、第二次 `Allow` 结果；失败信息能区分第几次 retry 不符合预期；staticcheck baseline 不再包含这两个 SA4000 条目。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操 1 个小闭环。 |
| Blast radius | 两个测试文件和 staticcheck baseline。失败时可能改变测试消费 budget 的顺序或误删其他 baseline。 |
| Failure modes | 保留重复表达式导致 SA4000 未消除：用变量承接结果；误改测试语义：保持调用顺序仍为同 tenant 的第 1、2、3 次；baseline 删除过宽：只删精确两条 SA4000。 |
| Decision points | 无需 Owner 中途确认；本轮不触碰支付、账本、配额、认证核心、数据库 schema 或部署脚本。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取两个测试文件；3. 已确认 `Allow` 是有状态方法，原测试意图是前两次 allow、第三次 deny；4. 已确认 baseline 精确命中两条 SA4000。 |

## 执行顺序

1. 将两个 `!budget.Allow(7) || !budget.Allow(7)` 展开成两个变量。
2. 删除 `staticcheck-baseline.txt` 中对应两条 SA4000。
3. 运行 scoped whitespace、clean-room 词、SA4000 残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
