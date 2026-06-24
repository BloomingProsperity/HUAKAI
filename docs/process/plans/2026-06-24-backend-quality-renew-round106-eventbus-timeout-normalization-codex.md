# 2026-06-24 backend quality renew round106 eventbus timeout normalization

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” 与 objective 中 “eventbus 异步 money 投递竞态 / worker 生命周期 / handler timeout” |
| Scope | 小修 `internal/eventbus` timeout 错误归一：handler 遵守 eventbus ctx 并返回 `context.DeadlineExceeded` 时，对外错误仍归一为 `ErrHandlerTimeout`；同步把 timeout 测试改成 ctx-aware handler，避免测试用 `time.Sleep` 模拟不响应取消 |
| Success criteria | `TestBusTimeoutAndPanicGoToDLQ` 与 `TestBusHandlerTimeoutUsesErrHandlerTimeoutSanitizedCode` 都使用 `<-ctx.Done()` 路径；`runner.go` 在 handler 返回 eventbus deadline 时返回稳定 `ErrHandlerTimeout`；DLQ/state 的 `handler_timeout` 语义保持不变 |
| Time estimate | 20-30 分钟；1 个中低风险小切片 |
| Blast radius | eventbus critical handler 超时错误口径；不改队列、worker 数、DLQ payload、billing settle 请求 |
| Failure modes | 误把业务主动返回的 `context.DeadlineExceeded` 归为 eventbus timeout：仅当 eventbus 派生 ctx 已经因 deadline 结束时才归一；handler 自己用其他 ctx 产生的错误不改写 |
| Decision points | 不处理“handler 完全无视 ctx 后 goroutine 继续运行”的深层结构问题；那需要更大设计，不在本小修内 |
| Pre-execution checklist | 已读取 objective；已读取 `production-scenario-review` 与 `api-gateway-risk-review` 技能；已核 `eventbus/runner.go` 和现有 timeout 测试；不读取不编辑另一个目标计划文件 |

## 执行顺序

1. 修改 `eventbus/runner.go`，在 handler 返回错误分支中识别 eventbus ctx 自身 deadline，并归一为 `ErrHandlerTimeout`。
2. 修改 timeout 测试，让 handler 等待 `ctx.Done()` 后返回 `ctx.Err()`。
3. 验证错误仍可被 `errors.Is(err, eventbus.ErrHandlerTimeout)` 命中，DLQ/state 仍是 `handler_timeout`。
4. 运行可用静态检查；尝试 `gofmt` / `go test ./internal/eventbus`，缺工具链则记录。
