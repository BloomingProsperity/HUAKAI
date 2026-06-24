# 2026-06-23 backend quality renew round97 signed envelope helper

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 抽取 `usersession/rotation.go` 与 `twofa/challenge.go` 重复的 JSON + RawURL base64 + HMAC-SHA256 signed-envelope 编解码；不改变 session token / 2FA challenge 的外部字符串格式，不碰认证核心策略、数据库 schema、账本、配额或另一个目标计划文件。 |
| Success criteria | 新增小型 `internal/signedenvelope` 包；session 与 2FA 复用同一签名/验签 helper；现有 `hus_` 前缀、2FA MAC domain prefix、错误映射保持；新增判别测试覆盖签名格式、篡改拒绝、domain prefix 隔离。 |
| Time estimate | 约 30-45 分钟；单个 Codex 小切片。 |
| Blast radius | 影响 session token 与 2FA challenge 的内部编解码路径；如果 helper 行为漂移，可能导致旧 token / challenge 验证失败。 |
| Failure modes | 改变 token 格式；遗漏 2FA domain prefix；错误类型泄露到调用方；Go 工具缺失无法真实跑测试。缓解：helper 只产出原来的 `body.signature` 结构，调用方映射错误，补格式与篡改测试，运行可用静态检查并诚实记录工具链缺失。 |
| Decision points | 若需要改变现有 token/challenge 格式、引入新 runtime 依赖、修改密钥管理或扩大认证策略，停止请求 Owner；本轮预期不需要。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已读取两个现有签名/验签实现和相关测试；3. 确认另一个目标计划文件只存在、不读取不编辑；4. 新包保持小职责；5. 修改后运行 `git diff --check`、禁词扫描、文件体量检查，并尝试 `gofmt` / `go test`。 |

## 执行状态

已暂停，未改生产代码。原因：该切片会改 session token / 2FA challenge 的签名与验签实现，按项目高风险规则属于认证核心变更，需要 Owner 针对性确认后才能执行。
