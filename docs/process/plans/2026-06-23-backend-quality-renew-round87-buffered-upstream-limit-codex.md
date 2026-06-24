# 2026-06-23 buffered upstream 上限单一来源

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文件要求处理 gatewayhttp 与 gateway HCSF 两份 1MiB buffered 上游响应上限 |
| Scope | 仅统一非流式 buffered 上游响应读取上限的常量来源；涉及 `backend/internal/gateway/upstream_dispatcher_hcsf.go` 与 `backend/internal/gatewayhttp/chat_completions_handler.go`；不改读取策略、不改错误分类、不改重试/计费语义 |
| Success criteria | 1MiB 字面量只由 `gateway` 包导出常量承载；gatewayhttp legacy raw 路径复用该常量；现有 overflow 哨兵和截断行为不变；`git diff --check` 通过；若 Go 工具链可用则运行定向测试 |
| Time estimate | 约 10-15 分钟墙钟时间；单个 Codex 小补丁 |
| Blast radius | HCSF non-streaming 与 legacy raw buffered 两条上游响应读取路径；若常量接错，会影响 oversized 响应拒绝/截断边界 |
| Failure modes | 导出常量命名导致调用点漏改；legacy 测试仍引用旧本地常量；注释误导为两个不同上限 |
| Mitigation | 保留 gatewayhttp 本地兼容别名但让它等于 `gateway.MaxBufferedUpstreamResponseBytes`；不改 `LimitReader(limit+1)` 逻辑；保留现有 tests 的边界断言 |
| Decision points | 本轮不引入新共享包、不改 env 配置化、不调大/调小 1MiB；若 Owner 要配置化上限，另起计划 |
| Pre-execution checklist | 1. 已读取目标 objective；2. 已核对两个常量定义与测试引用；3. 已确认另一个目标 plan 不读不改；4. 编辑前记录本计划；5. 编辑后跑可用检查 |

