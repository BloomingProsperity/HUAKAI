# 2026-06-24 后端质量刷新 round131：email sender factory SA4023 修复

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-6：测试质量必须判别式，staticcheck baseline 不应保留可直接修复的永不成立比较。 |
| Scope | 仅修复 `backend/internal/email/sender_factory_test.go` 中 `EmailSender` 接口装箱后再判 nil 导致的 SA4023，并删除 `backend/scripts/staticcheck-baseline.txt` 对应豁免。不改 SMTP 生产发送逻辑。 |
| Success criteria | 测试先检查 `NewSMTPSender` 返回的具体指针不为 nil，再显式赋给 `EmailSender` 接口证明实现关系；staticcheck baseline 不再包含 `internal/email/sender_factory_test.go` 的 SA4023。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操 1 个小闭环。 |
| Blast radius | 单个 email 测试文件和 staticcheck baseline。失败时可能把原本的接口实现检查删弱。 |
| Failure modes | 修复后仍有永不成立比较：避免对已装箱接口做 nil 比较；测试变弱：保留具体指针非 nil 和接口赋值两层检查；baseline 删除过宽：只删精确 SA4023 条目。 |
| Decision points | 无需 Owner 中途确认；本轮不触碰发送实现、配置存储、credential、auth、billing、quota 或 schema。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 `EmailSender`、`SMTPSender`、`NewSMTPSender` 定义；3. 已确认 `NewSMTPSender` 返回 `*SMTPSender`；4. 已确认 staticcheck baseline 精确命中该测试。 |

## 执行顺序

1. 将 `TestAT_EMAIL_008_SMTPSenderImplementsEmailSender` 改成先检查具体 `*SMTPSender`。
2. 保留 `var sender EmailSender = smtpSender` 赋值，证明接口实现关系。
3. 删除 staticcheck baseline 中该 SA4023 条目。
4. 运行 scoped whitespace、clean-room 词、SA4023 残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
