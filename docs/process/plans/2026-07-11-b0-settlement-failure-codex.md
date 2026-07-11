# 2026-07-11 B0 交付后结算与未决恢复保护（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “先交付、后按上游权威用量计量、已交付永不反悔”；保留 Reserve/hold 准入闸；不建第二持久环；完整业务体成功写出才算交付，部分写或零写走 Abort，keepalive 裸换行不算交付。 |
| 定稿依据 | `docs/process/reviews/2026-07-10-B0-settlement-failure-design.md` 的“2026-07-11 Owner 裁决 + 官方计费模型调研 → 定稿方案”及本轮 Owner 实施指令。 |
| Scope | 修复 chat 非流式、chat 流式、过期 claim/槽清扫、图片同步响应的交付后结算时序；复用现有 `usage_record_dlq` 的 `post_delivery_settlement` 行；补判别性单元/PG 集成/协作回归测试与观测。 |
| Out of scope | 不改 DB schema/迁移、Reserve/hold 准入、上游 usage 解析、金额算法、SSRF/auth、第二持久环、运行时依赖和 `LICENSE`；不执行 `git add/commit/push`。 |
| Success criteria | 七类判别测试覆盖并能说明旧实现为何必红；所有改动文件 `gofmt -l` 空输出；`go build ./...`、`go vet ./...`、`go test ./...`、真实 PG 的 `go test -tags integration_pg ./...`、`internal/codebudget` 全绿，或如实记录唯一外部阻塞。 |
| Time estimate | 只读核实与双计划约 30–45 分钟；实现与定向测试约 2–4 小时；全量门禁和真 PG 约 1–3 小时，取决于现有测试耗时与数据库状态。 |
| Blast radius | 计费 claim/hold、quota reservation、provider slot、DLQ worker、chat/图片客户端响应；错误会造成误扣、白吃、冻结槽、重复结算或已发响应被错误改写。 |
| Failure modes | 写体短写被误判成功；header 已提交后继续写 5xx；已交付流被 Abort；零帧流误入恢复；未决恢复行被 sweep；恢复达阈值停止；图片来源不能通过 payload 校验；DLQ 双失败泄露原始错误；committed claim 被盲 Abort。缓解见下方逐项守卫和测试。 |
| Decision points | 无新增产品/架构决策。Owner 已明确交付定义、持续重试、不建第二环和两项测试反转；若现有 schema 无法表达未决状态才停，但只读核实已确认现有行可表达。 |
| Pre-execution checklist | 1. 读取定稿段；2. 核实工作树与并行锁；3. 核实四条生产路径、现有恢复 payload/worker、sweep SQL、codebudget；4. Claude/Codex 各自独立计划并合并；5. 合并计划无新增决策点后再编辑代码。 |

## 参考项目范围与 clean-room 边界

- `REFERENCE PROJECTS IN SCOPE`: CLIProxyAPI、sub2api、new-api；官方协议/计费语义为 Anthropic、OpenAI、Gemini。
- 三镜行为调研已经由定稿设计的 specifier lane 完成并写入上述定稿文档。本计划属于 implementer lane，只读取 HUAKAI 内部定稿与 HUAKAI 代码，不读取或复述参考项目源码。
- 实现从 HUAKAI 现有类型、SQL 查询和测试夹具出发；生产代码注释只描述 HUAKAI 自身语义，不出现借鉴项目名。

## 运行形态清单

| 路径 | 交付判定 | 交付后失败 | 未交付失败 |
| --- | --- | --- | --- |
| Chat 非流式 | 完整客户端业务体一次写完，`n == len(body)` 且 `err == nil`；此前 keepalive 不计 | 结算失败写入现有恢复队列，HTTP 结果不反悔 | 短写/零写立即 Abort，不结算、不入恢复、不再尝试写错误体 |
| Chat 流式 | 至少一个业务帧已写；keepalive 不计；部分业务帧写入等不确定状态按已交付 | ledger/结算失败不 Abort，流状态保持真实非 Failed，走结算恢复并标 deferred | 零业务帧失败继续 Abort |
| 图片同步 | 完整 JSON 业务体一次写完，短写视未完整交付 | 以 `SourceImagesDelivered` 保存原 `SettleRequest`，三证 worker 重放 | 立即 Abort，不结算、不入恢复 |
| Lease/slot/quota 清扫 | `usage_record_dlq` 中同 tenant/claim、`event_kind='post_delivery_settlement'` 且非 `delivered` 即未决 | claim、provider slot 与相关 quota 孤儿清扫不得释放 | 无未决恢复行的普通孤儿维持原行为 |
| 恢复 worker | 正常成功或三证证明已提交才转 `delivered` | 可重试失败持续 pending，指数退避封顶；超过告警阈值只告警不停止 | 结构损坏仍进入显式隔离态，但隔离态仍算“未解决”并受 sweep 保护 |

## 预计文件与结构预算

不新建生产包，不上调 `internal/codebudget/baseline.json`。优先小改既有职责文件；SQL 变更通过 `sqlc generate` 同步生成文件。

- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gatewayhttp/chat_completions_attempt.go`（仅在需要表达“已写完”成功态时小改）
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_billing_test.go` 或现有相邻判别测试文件
- `backend/internal/gatewayhttp/chat_completions_stream_test.go`
- `backend/internal/imageshttp/handler.go`、`attempt.go`、`billing.go`、`handler_test.go`
- `backend/internal/settlementrecovery/payload.go` 及测试
- `backend/internal/dlq/service.go`、`retry.go` 及测试（以事件种类选择持续重试策略）
- `backend/internal/billing/lease_sweep.go` 与 PG 集成测试
- `backend/sql/queries/balance_holds.sql`、`pool_slot_acquisitions.sql`、必要时 `quota.sql`
- 相应 `backend/internal/db/**` sqlc 生成文件
- `backend/cmd/gateway/routes.go` 与装配测试
- 如运行逻辑文档已有对应页，只做必要同步；不扩写无证据内容。

## 具体执行顺序

1. 先补/反转判别测试：chat 非流式成功交付后 settle 失败、chat 写错误、流式 ledger 双失败、流式零帧、图片 settle 失败、图片写错误。
2. 实现一个明确检查 `n == len(body)` 的 buffered 写入原语；只设置 header 后调用 `Write`，不预先 `WriteHeader`。写失败后只 Abort/记录内部错误，不再试图改变已可能提交的 HTTP 状态。
3. Chat 非流式在完整写成功后用脱离请求取消且有上限的上下文结算；失败只记录并入恢复。成功后才维持现有 replay/cache 后续语义，避免扩大本切片。
4. 流式以“业务帧已交付”而非 header/keepalive 判断终局；已交付时 ledger 不可用也构造可重放结算事件，不把 `Attempt` 改成 `Failed`，不 Abort；零帧仍按原策略 Abort。
5. 为图片增加代码层 `SourceImagesDelivered`，复用 `FromSettleRequest`、现有 DLQ event kind 与三证 handler；在完整写成功后结算，失败只入恢复；写失败 Abort。
6. 查询所有非 `delivered` 的 post-delivery 行作为未决：过期 reserving claim、provider slot，以及实际会释放 reserving claim 资源的 quota 清扫均排除。SQL 主文件与生成文件必须一致。
7. 为该事件种类增加封顶退避持续重试策略；达到原 MaxAttempts/DLQAfter 阈值时仍 pending，并发出不含原始错误的 ERROR/WARN 观测。增加未决数量与最老年龄 gauge/log。
8. 跑定向单元测试与真 PG sweep 集成测试；对关键条件做临时反向变异、保存真实红点输出，再恢复正确实现。
9. 跑并发/模块协作回归，确认结算成功释放 slot/quota，恢复未决时不会被 sweep，普通孤儿仍可清。
10. 跑完整门禁、检查统一 diff 和工作树，确认没有迁移、依赖、baseline、`git add/commit/push` 痕迹。

## 判别测试与变异证

| 编号 | 正确断言 | 回退/变异后红点 |
| --- | --- | --- |
| 1 | 非流式 settle 失败仍是完整 200 body，恢复一次，Abort 零次 | 把 settle 移回写前：status/body 断言变成旧 500/空体 |
| 2 | 非流式写体短写/错误时 Abort 一次、Settle/恢复零次 | 忽略 `Write` 结果：Settle 或恢复调用数转非零 |
| 3 | 已发业务帧后 ledger+DLQ 双失败：非 Failed、Abort 零次、结算恢复被调用 | 恢复旧 `ledgerFailClosed` Abort：状态/Abort/恢复三断言至少一项红 |
| 4 | 零业务帧失败仍 Abort，恢复零次 | 把 header/keepalive 当交付：Abort 变零或恢复变一 |
| 5 | 有未决恢复行的过期 claim/slot 不被清；普通孤儿照旧清 | 删除 `NOT EXISTS`：受保护 claim/slot 被零成本释放 |
| 6 | 图片 settle 失败仍完整 200 JSON、恢复一次、Abort 零次；写错误 Abort 且不恢复 | 恢复 settle-before-write 或忽略写错：状态/体/调用次数红 |
| 7 | billing settle ↔ quota reconcile ↔ slot release 的成功链与并发容量不回归 | 去掉终态释放/恢复保护中的任一接线：状态、槽计数或下一请求容量断言红 |

## 风险守卫

- Header：buffered 路径不显式提前 `WriteHeader`；一旦 `Write` 返回短写/错误，不再向客户端写 5xx，只做脱钩 Abort 与内部日志。
- Claim 状态：写失败只对当前尚未结算的 reserving claim 调 Abort；交付后路径绝不 Abort。若 Settle 返回提交结果不明，统一交恢复三证判定，不猜测、不盲退。
- 恢复未决：`status <> 'delivered'` 全部视为未解决，包括 pending/inflight/operator_review/dlq/quarantined；只有 worker 成功或三证确认提交才解除 sweep 保护。
- 幂等：恢复仍使用 tenant+claim+request 的现有唯一键与同一个 event kind，不建立第二持久环。
- 隐私：DLQ `failure_reason` 与告警只记错误类别，不持久化原始 DB/上游错误文本。
- 竞争：SQL 在 tenant+claim 维度关联，并保留 `FOR UPDATE SKIP LOCKED`；测试同时覆盖恢复完成与 sweep 竞态的单终局。

## 门禁命令

```text
gofmt -l <全部改动 Go 文件>
go build ./...
go vet ./...
go test ./...
HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/huakai_e2e go test -tags integration_pg ./...
go test ./internal/codebudget
```

不执行任何 `git add`、`git commit` 或 `git push`；最终仅交付未暂存统一 diff 与真实测试输出。
