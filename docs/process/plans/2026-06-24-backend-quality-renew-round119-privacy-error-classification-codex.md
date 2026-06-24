# 2026-06-24 后端质量刷新 round119：privacy 错误分类误判收敛

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理 `internal/privacy` 中 `SanitizeError` 依赖子串匹配造成的错误分类误判；允许调整同包单元测试；不改日志 sink 合约、不改审计/账本/auth/schema/LICENSE/deploy。 |
| Success criteria | `ErrUnsafePayload` 等隐私哨兵仍归类为 `privacy_guard_hit`；timeout、rate limit、forbidden、panic、invalid request、credential、upstream 等常见类别保持可识别；`pirate limit`、`panicky`、`credentialed` 这类子串误命中不会被错误分类。 |
| Time estimate | 10-20 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 影响隐私日志与系统日志中的 `error_class` 归类，不改变 payload 脱敏、blocked payload 格式或业务状态机。 |
| Failure modes | token 化过严导致常见短语漏分；token 化过松仍误判。缓解：同时覆盖正向短语与负向近似词，并保留隐私哨兵 `errors.Is` 优先级。 |
| Decision points | 若要改变对外错误码、日志 schema 或审计 sink 字段，停止并请求 Owner 确认；本轮预计不需要。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 读取 production-scenario-review 技能；3. 核实 `SanitizeError` 实现和测试；4. 执行 `git diff --check`、禁词扫描、静态核验；5. 尝试 Go 测试并如实记录工具链缺失。 |

## 执行顺序

1. 核对 `default_redactor.go` 是否已从 `strings.Contains` 子串链切换为 token / token-pair 分类。
2. 核对 `redactor_test.go` 是否覆盖隐私哨兵、正向常见类别和负向近似词。
3. 必要时做最小补丁，不扩大到日志 schema 或 sink 行为。
4. 运行静态检查并记录无法运行的工具链命令。
