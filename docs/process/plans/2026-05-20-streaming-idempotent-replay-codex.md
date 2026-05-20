# 2026-05-20 流式幂等重放补齐方案（Codex 独立草案）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI Go 后端任务 — 独立方案规划 (不要实现, 只写方案文档)。” |
| Scope | 只设计 HUAKAI Go 后端流式 SSE 幂等 replay 补齐方案；只读 HUAKAI 自有代码；不读参考项目源码；不实现代码。 |
| Out of scope | 不改 schema、不改 billing ClaimGate 语义、不改 StreamForwarder 协议解析、不新增运行时依赖。 |
| Success criteria | 带 `Idempotency-Key` 的成功流式请求在首次完成后写入 `idempotency_replay_records`；同 key 重试由 `serveIdempotentReplay` 返回原始 SSE 字节，状态 200，`Content-Type: text/event-stream`，保留幂等命中头。 |
| Time estimate | 实现约 1-2 小时；测试约 1-2 小时。 |
| Blast radius | `gatewayhttp` 流式响应路径、幂等 replay helper、handler 测试；不触碰数据库结构和核心计费表结构。 |

## 现状依据

- 非流式路径在成功结算后调用 `recordIdempotencyReplay(ex.reserveRes.ClaimID, http.StatusOK, clientBody)`，已有完整客户端响应体：`backend/internal/gatewayhttp/chat_completions_billing.go:70-72`。
- replay helper 当前固定记录 `application/json`，并用 `maxIdempotencyReplayBodyBytes = 1 MiB` 做超限跳过：`backend/internal/gatewayhttp/chat_completions_idempotency_replay.go:8-23`。
- `serveIdempotentReplay` 当前只按记录的 `content_type` 和 body 重放，额外写 `X-HUAKAI-Idempotency-Hit`：`backend/internal/gatewayhttp/chat_completions_idempotency_replay.go:41-68`。
- 流式路径在 `forwardSSEAndSettle` 设置 SSE headers 后，直接把 `http.ResponseWriter` 传给 `streamForwarder.Forward`，没有汇总 body：`backend/internal/gatewayhttp/chat_completions_stream.go:147-188`。
- `StreamForwarder` 的热路径通过 `writeAndFlush` 写 chunk，并依赖 `http.Flusher`：`backend/internal/gateway/forwarder.go:567-574`。
- `ReplayStore.Record` 已支持 `contentType string`，表里已有 `content_type` 和 `response_body`，无需扩表：`backend/internal/billing/replay_store.go:31-36`，`backend/sql/migrations/0044_idempotency_replay_records.up.sql:15-24`。
- ClaimGate 只有已 `committed` 的旧 claim 才返回 `IdempotencyHit=true`：`backend/internal/billing/claim_gate.go:91-99`。

## 设计结论

实现位置应放在 `gatewayhttp` handler 层，而不是改 `gateway.StreamForwarder`。理由是 replay 是 HTTP 幂等语义，依赖 `ex.idempotencyHeader`、`ex.reserveRes.ClaimID`、`ex.d.ReplayStore` 和最终 settlement 结果；这些信息都在 `chatExecution.forwardSSEAndSettle` 可见，而 `StreamForwarder` 应继续只负责流式协议转发和 usage draft。

新增一个只在需要 replay 时启用的 `http.ResponseWriter` 包装器：

- `Write(p []byte)` 先调用底层 writer，把实际成功写出的 `p[:n]` 追加到有界捕获器。
- 捕获器最多保留 `maxIdempotencyReplayBodyBytes`；一旦累计会超过上限，标记 `overLimit=true` 并释放已捕获 buffer，后续写入只透传、不再占内存。
- `Write` 必须返回底层 writer 的 `(n, err)`，捕获失败或超限不能改变客户端流式转发结果。
- 包装器必须实现 `Flush()` 并委托到底层 `http.Flusher`；底层不支持时 no-op。这样 `writeAndFlush` 的接口断言仍成立，不破坏 SSE chunk 及时 flush。
- 建议同时实现 `Unwrap() http.ResponseWriter`，便于后续 `http.ResponseController` 或测试继续触达底层 writer。当前热路径最关键的是 `http.Flusher`。

不要用 `io.MultiWriter` 或先把 upstream 流读入 buffer。捕获点必须是“已经写给客户端的字节”，这样跨协议 streaming adapter 生成的客户端 SSE 形状也会被原样记录，而不是误存 upstream 原始事件。

## 挂钩流程

在 `forwardSSEAndSettle` 中：

1. 继续先设置现有 SSE headers：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、stream billing trailers。
2. 构造 `streamForwarder` 后、调用 `Forward` 前判断是否需要捕获：
   - `ex.idempotencyHeader != ""`
   - `ex.d.ReplayStore != nil`
   - `ex.reserveRes != nil && ex.reserveRes.ClaimID != 0`
3. 条件满足时用 capture writer 包装原始 `w`，把包装后的 writer 传给 `streamForwarder.Forward`；条件不满足时仍传原始 `w`，避免无意义开销。
4. `Forward` 返回后，照旧生成 `streamAttempt`、写 stream billing headers、记录 channel health、调用 `settleCompletion`。
5. 只有以下条件全部成立时记录 replay：
   - `fwdErr == nil`
   - `settleCompletion` 返回 nil
   - 捕获器存在且未 `overLimit`
   - `streamAttempt.State != billing.StreamStateFailed`（更严格可写成 `streamAttempt.State == billing.StreamStatePartial`，与当前成功/可计费流式状态一致）
6. 调用新的 content-type aware helper 写入 ReplayStore：status `http.StatusOK`，content type `text/event-stream`，body 为捕获到的 SSE 原始字节，ttl 继续用 store 默认值。

记录应放在 settlement 成功之后，保持非流式路径“先成功结算、再写 replay”的语义。若 `CompletionBus.Emit` 只是成功入队，非流式目前也是同样语义；即使后续异步 settle 失败，ClaimGate 未 committed 前也不会暴露 `IdempotencyHit` 重放。

## Replay helper 调整

保留现有 `recordIdempotencyReplay(claimID, status, body)` 作为 JSON wrapper，降低非流式改动面。新增内部 helper，例如：

- `recordIdempotencyReplayWithContentType(claimID int64, status int, contentType string, body []byte)`
- `recordStreamingIdempotencyReplay(claimID int64, status int, body []byte)` 调用上面的 helper，并传 `text/event-stream`

所有 helper 继续复用现有门槛：

- 无 `Idempotency-Key`：直接 return。
- `ReplayStore` 未配置：直接 return。
- `claimID == 0`：直接 return。
- body 超过 `maxIdempotencyReplayBodyBytes`：直接 return。
- `ReplayStore.Record` 失败：best-effort 忽略，不影响已完成响应。

## `serveIdempotentReplay` 调整

需要对 SSE 内容类型做一个小分支：

- 继续从记录读取 `Content-Type`；为空时默认 `application/json`。
- 当 content type 为 `text/event-stream` 或带参数的 `text/event-stream; ...` 时，额外设置 `Cache-Control: no-cache`。
- 继续设置 `X-HUAKAI-Idempotency-Hit: true`。
- 不建议在 replay 路径强行设置 `Connection: keep-alive`：这是 hop-by-hop header，有限 body replay 由 `net/http` 管理连接即可。
- 可在写完 SSE replay body 后调用一次 `Flush()`（若 writer 支持），让 SSE 客户端立即收到 replay 结果；handler 随即返回，所以这不是功能依赖。

不需要把 stream billing trailers 持久化或重放。当前 replay schema 只保存 status、content type、body；非流式也没有重放完整 header 集。若未来要求完全重放 headers/trailers，应另立 schema 方案，不应混入本次缺口修复。

## 边界处理

| 场景 | 方案行为 |
| --- | --- |
| 成功流式、body <= 1 MiB、带 `Idempotency-Key`、ReplayStore 已配置 | 捕获并在 settlement 成功后写入 replay record；重试返回原 SSE body。 |
| `fwdErr != nil` | 不写 replay；保留当前 channel health 与 settlement 行为。若后续同 key 命中但无 record，仍回 409 `replay_without_cache`。 |
| 捕获 body 超过 1 MiB | 不中断流式响应；停止保留 buffer，最终跳过 replay record；后续同 key 命中无 record 时回 409，与非流式超限策略一致。 |
| 无 `Idempotency-Key` | 不包装 writer，不记录 replay。 |
| ReplayStore 未配置 | 不包装 writer，不记录 replay；命中路径仍按现有逻辑无法 lookup 时回 409。 |
| `settleCompletion` 返回错误 | 不写 replay，避免为未成功完成的 claim 提供可重放响应。 |
| `ReplayStore.Record` 写失败 | best-effort 忽略；首次响应不回滚。 |
| 客户端中途断开 | `Forward` 应返回 `ErrClientDisconnect` 或等价错误，`fwdErr != nil`，捕获到的部分字节不会写 replay。 |

## 需改文件清单

- `backend/internal/gatewayhttp/chat_completions_stream.go`
  - 在 `forwardSSEAndSettle` 内创建 capture writer 并传入 `streamForwarder.Forward`。
  - 在 `settleCompletion` 成功后调用 streaming replay 记录 helper。
- `backend/internal/gatewayhttp/chat_completions_idempotency_replay.go`
  - 增加 content-type aware 记录 helper。
  - 增加或引用 SSE replay content type 常量。
  - 调整 `serveIdempotentReplay` 对 `text/event-stream` 的 `Cache-Control` 和可选 flush。
- 可选新增 `backend/internal/gatewayhttp/chat_completions_stream_replay.go`
  - 放置 capture writer 与有界捕获器，避免 stream 主流程文件变重。
- `backend/internal/gatewayhttp/chat_completions_stream_test.go`
  - 增加流式 idempotent replay 主路径和边界测试。
- `backend/internal/gatewayhttp/chat_completions_idempotency_replay_test.go`（可选新文件）
  - 放置 capture writer 单元测试和 `serveIdempotentReplay` SSE header 测试。

不需要修改：

- `backend/internal/billing/replay_store.go`
- `backend/sql/migrations/*`
- `backend/sql/queries/idempotency_replay.sql`
- `backend/internal/gateway/forwarder.go`

## 测试清单

1. `TestStreamingIdempotencyReplayRecordsSSEAndReplays`
   - 第一次流式请求带 `Idempotency-Key`，ReplayStore 用 `billing.NewMemoryReplayStore()`。
   - 上游返回确定性 SSE fixture。
   - 断言首次响应 200，body 为 SSE，ReplayStore 中存在 claim record，`ContentType == "text/event-stream"`。
   - 第二次同 key 让 ClaimGate 返回 `IdempotencyHit=true`，Dispatcher 可设置为“被调用即失败”的 stub。
   - 断言第二次响应 200、`X-HUAKAI-Idempotency-Hit=true`、`Content-Type` 为 event-stream、`Cache-Control=no-cache`、body 与首次完全一致。
2. `TestStreamingIdempotencyReplaySkipsOverLimit`
   - 构造超过 `maxIdempotencyReplayBodyBytes` 的 SSE body。
   - 首次响应仍成功；ReplayStore lookup 不存在；第二次 hit 回 409 `replay_without_cache`。
3. `TestStreamingIdempotencyReplaySkipsForwardError`
   - 让 forwarder 返回错误或上游流截断触发 `fwdErr`。
   - 断言不写 replay record；不要求改变现有 streaming failure HTTP 行为。
4. `TestStreamingIdempotencyReplaySkipsWithoutKeyOrStore`
   - 无 key 或 ReplayStore nil 时不包装、不记录、不 panic。
5. `TestReplayCaptureWriterPreservesFlushAndCapturesWrittenBytes`
   - 用带 flush 计数的 ResponseWriter stub，断言 `Flush` 被委托，捕获字节等于实际写出字节。
6. `TestReplayCaptureWriterStopsAtLimitWithoutShortWrite`
   - 用小 limit 的捕获器单测，断言超限后底层 writer 仍收到完整 bytes，capture 标记 over-limit，`Write` 不返回短写。
7. 回归：现有非流式 `TestChatCompletionsIdempotentHitReplaysFromStore` 继续通过，JSON replay content type 不变。

建议实现后运行：

- `go test ./backend/internal/gatewayhttp`
- `go test ./backend/internal/billing`
- 若 capture writer 与 forwarder 交互有疑问，再跑 `go test ./backend/internal/gateway`

## Schema 结论

不需要 schema 变更。现有 `idempotency_replay_records` 已保存 `response_status`、`content_type`、`response_body`，能表达 JSON 与 SSE replay。新增字段保存 headers/trailers 会扩大迁移面，且不是修复当前缺口的必要条件。

## 风险点与缓解

- 内存风险：每个可记录流式请求最多占用 1 MiB buffer。缓解：只在 key/store/claim 都存在时包装；超限立即释放 buffer 并跳过记录。
- 流式语义风险：包装 writer 可能丢失 `http.Flusher`。缓解：包装器显式实现 `Flush` 并委托底层；测试覆盖 flush。
- 部分响应误记录风险：客户端断开或 forwarder 报错时可能已写出部分 chunk。缓解：必须要求 `fwdErr == nil` 且 settlement 成功才记录。
- 失败 claim 重放风险：某些流式 draft 可能无 forward error 但账务态为 failed。缓解：记录条件加 `streamAttempt.State == billing.StreamStatePartial` 或至少排除 failed。
- Header 不完全重放风险：ReplayStore 不保存完整 headers/trailers，SSE replay 只补 `Content-Type`、`Cache-Control` 和 idempotency hit header。缓解：本次明确不承诺完整 header replay；若产品需要 billing trailers replay，单独设计 schema。
- 隐私风险：新增把小于等于 1 MiB 的流式模型输出写入持久表。缓解：这与非流式 replay 的现有隐私边界一致；不要把捕获 body 写日志；继续依赖 TTL janitor 清理。
- 异步 settlement 风险：`CompletionBus.Emit` 成功不等于 DB 已 committed。缓解：与非流式现状一致；ClaimGate 只有 committed claim 才会返回 `IdempotencyHit`，未 committed 的 replay record 不会被命中路径暴露。

## 执行顺序

1. 新增 capture writer 与有界捕获器单测，先证明不破坏 `Write` / `Flush`。
2. 增加 content-type aware replay helper，保持旧 JSON helper API。
3. 修改 `serveIdempotentReplay` 的 SSE header 分支，并补 header 测试。
4. 在 `forwardSSEAndSettle` 挂 capture writer，settlement 成功后记录 SSE replay。
5. 增加流式 replay 主路径、超限、forward error、无 key/store 测试。
6. 运行 gatewayhttp/billing 测试；若 flush 或 writer 行为有交互风险，追加 gateway forwarder 测试。

## Owner 决策点

当前方案不需要 Owner 在实现前额外确认，因为不改 schema、不改高风险 billing ledger/ClaimGate、不新增依赖。若 Owner 希望“超 1 MiB 的流式 replay 也必须支持”或“完整重放 stream billing trailers/所有 headers”，那会触发 schema 或存储策略变更，应另开高风险确认。
