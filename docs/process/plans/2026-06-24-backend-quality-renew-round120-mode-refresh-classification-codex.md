# 2026-06-24 后端质量刷新 round120：mode refresh 失败分类类型化

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理 `internal/credentialworker` 中 mode refresh 失败分类依赖错误文案子串的问题；允许拆出小文件、补判别式单测；不改刷新事务、credential store schema、auth/billing/quota、真实 provider adapter 语义、LICENSE/deploy。 |
| Success criteria | `ClassifyRefreshErrorClass` 仍只暴露安全的七类失败结果；优先识别带 `RefreshFailureOutcome()` 的类型化错误；常见文本仍可兼容；`decryptology`、`jsonify`、`disabled accountant` 等近似词不再误分。 |
| Time estimate | 15-25 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 影响刷新失败落库/干跑返回的 failure class；不改变刷新成功/失败事务、冷却时间或审计写入。 |
| Failure modes | 分类过严导致真实上游错误落入 `temporary`；分类过松继续误判。缓解：类型化 outcome 优先，文本兼容只接受 token 或相邻 token 组合，并用正负样本锁定。 |
| Decision points | 若需要改变刷新状态机、账号健康策略或数据库字段，停止并请求 Owner 确认；本轮预计不需要。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 读取 production-scenario-review 与 acceptance-test-writer 技能；3. 核实当前实现与测试；4. 执行 `git diff --check`、禁词扫描、静态核验；5. 尝试 Go 测试并如实记录工具链缺失。 |

## 执行顺序

1. 核对旧 `mode_refresh.go` 是否已不再内联子串分类函数。
2. 核对 `refresh_failure_class.go` 是否承载类型化 outcome + token 分类。
3. 核对 `provider_account_test_test.go` 是否覆盖正向类别、负向近似词和 typed outcome。
4. 运行静态检查并记录无法运行的工具链命令。
