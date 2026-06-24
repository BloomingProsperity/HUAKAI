# 2026-06-24 后端质量刷新 round117：上游 buffered 响应上限防漂移

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理非流式上游响应 buffered 读取上限的重复/漂移风险；检查 `internal/gateway` HCSF 路径与 `internal/gatewayhttp` legacy raw 路径是否共享同一上限；不做 gatewayhttp 拆包、不改计费/配额/auth/schema/LICENSE/deploy。 |
| Success criteria | `gatewayhttp` raw buffered 路径只能引用 `gateway.MaxBufferedUpstreamResponseBytes`，不得重新写本地 `1 << 20`；HCSF 私有常量仍从导出的统一常量派生；后续回退成双硬编码时 codebudget guard 会红。 |
| Time estimate | 10-20 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 仅新增静态 guard；生产行为不变。 |
| Failure modes | guard 过宽误伤无关 `1 << 20`；guard 过窄无法抓到 raw buffered 路径回退。缓解：只扫描两个确切源码文件，并绑定具体常量名与调用片段。 |
| Decision points | 若需要移动常量到新公共包、拆 `gatewayhttp` 或调整响应大小策略，停止并请求 Owner 确认；本轮预计不需要。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 核实当前源码是否已经共享常量；3. 新增最小 guard；4. 运行 `git diff --check`、静态模拟、禁词扫描；5. 尝试 Go 测试并如实记录工具链缺失。 |

## 执行顺序

1. 读取 `internal/gateway/upstream_dispatcher_hcsf.go` 与 `internal/gatewayhttp/chat_completions_handler.go` 的 buffered 上限定义。
2. 若当前已共享常量，不做生产行为改动，只新增 codebudget guard。
3. guard 钉住 `maxRawBufferedUpstreamBodyBytes = gateway.MaxBufferedUpstreamResponseBytes` 与 `maxBufferedUpstreamResponseBytes = MaxBufferedUpstreamResponseBytes`。
4. 静态检查并记录无法运行的工具链命令。
