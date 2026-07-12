# 2026-07-11 B0 交付后结算与未决恢复保护（合并计划）

> 本计划由 `2026-07-11-b0-settlement-failure-claude.md` 与
> `2026-07-11-b0-settlement-failure-codex.md` 两份独立计划交叉讨论后合成。
> 定稿设计与本轮 Owner 指令已经给出全部产品决策，本计划无新增待决项。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “先交付、后按上游权威用量计量、已交付永不反悔”；保留 Reserve/hold；不建第二持久环；完整业务体写成功才算交付，部分/零写 Abort，keepalive 不算交付。 |
| Scope | Chat 非流式写后结算、chat 已交付流禁 Abort、未决恢复保护 claim/slot/quota sweep、`post_delivery_settlement` 持续重试与年龄观测、图片写后结算及三证恢复、七类判别测试与并发协作回归。 |
| Out of scope | DB schema/迁移、Reserve/hold 准入、usage/actualCost、SSRF/auth、第二持久环、新运行时依赖、`LICENSE`；禁止 `git add/commit/push`。 |
| Success criteria | 旧实现对应变异必红；定向与并发测试通过；`gofmt -l` 空输出；build/vet/unit/integration_pg/codebudget 全绿，外部阻塞如实报告。 |
| Time estimate | 实现与定向验证约 3–5 小时；全量门禁与真 PG 约 1–3 小时。 |
| Blast radius | billing claim/hold、quota reservation、provider slot、DLQ worker、chat/图片 HTTP 响应；失败会导致误扣、白吃、冻钱、重复结算或并发容量泄漏。 |
| Failure modes | 短写误判交付；header 已提交后写 5xx；audit-ref 拒绝器内部 Abort 已交付 claim；零帧误入恢复；未决行被 sweep；重试达阈值停止；committed claim 被盲 Abort；错误原文进入持久层/日志。 |
| Decision points | 无。`status <> 'delivered'` 表示全部未解决状态；已交付路径不得被 audit-ref 拒绝器 Abort；15 分钟沿用现有 DLQ age 阈值作首告警，随后封顶退避持续重试。 |
| Pre-execution checklist | 已读取定稿与内部代码；已确认现有 DLQ 行含 tenant/claim/event kind/status/failure time；已确认无需迁移；已完成双计划与合并；编辑前需重新认领全部目标文件。 |

## 参考范围与 clean-room

- `REFERENCE PROJECTS IN SCOPE`: CLIProxyAPI、sub2api、new-api；官方协议语义为 Anthropic、OpenAI、Gemini。
- 参考项目调研由 `docs/process/reviews/2026-07-10-B0-settlement-failure-design.md` 的 specifier lane 承接。本 implementer 车道只读 HUAKAI 定稿与内部实现，不读取参考项目源码。
- 生产与测试代码注释一律中文，只描述 HUAKAI 自身语义。

## 双计划交叉结论

### 共识

1. Chat 非流式与图片必须先完成业务体写入，再尝试结算；`n < len(body)` 才是未完整交付，`n == len(body)` 且同时报错属于交付不确定，按已交付保守处理。
2. 写失败时 claim 尚未执行 Settle，因此可安全 Abort；写后 Settle 返回提交不明时绝不 Abort，交三证恢复判定。
3. 流式交付必须按业务帧而不是 header/keepalive 判断；已交付流不得写成 `Failed`，不得 Abort。
4. `usage_record_dlq` 已能用 tenant/claim、event kind、status、failure time 表达未决恢复，无需新表。
5. `post_delivery_settlement` 需事件专用的封顶退避持续重试；其他 DLQ kind 保持原策略。
6. 两个锁错测试必须反转，并补零帧、短写、普通孤儿对照和并发协作回归。

### 一方补出的缺口

- Claude 计划发现 production audit-ref 校验失败会进入 `rejectMoneyPathAuditRef` 并内部 Abort。合并方案要求：`settleCompletionWithRecovery` 在 post-delivery 场景先做无副作用校验；缺审计引用时记录 ERROR 并直接落结算恢复，禁止进入带 Abort 的拒绝器。
- Codex 计划补出 provider slot 与 quota orphan 查询也必须按同一未决定义排除，避免只保护 claim、却提前释放其协作资源。
- Codex 计划补出 gatewayhttp 包已经接近 codebudget 允量，流式业务字节判定优先扩展既有 `chatpipe.DeliveryTracker`，不向 god-package 新增生产文件。

### 分歧与裁决

- Claude 建议按多个 commit 切片；Owner 明令禁用 `git add/commit/push`，本轮只保留工作树 diff，按测试阶段组织但不产生 commit。
- Claude 把 quarantined、audit-ref 放行与告警阈值列为微决策；本轮“所有未解决”“已交付永不反悔”“持续重试”已经覆盖前两项，第三项复用现有 15 分钟阈值以避免新增配置。
- 不新增外部 settlement-state header。流式 trailer 保持真实非 `Failed` 状态，审计 trailer 沿用 Deferred 语义；结算未决以结构化 ERROR 日志和恢复行表达，避免扩大公开协议。

## 实施顺序

1. 认领全部目标文件，复跑 codebudget 基线。
2. 先反转/新增判别测试，确认旧行为红点。
3. 缺口3：
   - claim、provider slot、quota orphan 查询加入同 tenant/claim 的未决 `post_delivery_settlement` 排除；
   - sqlc 生成文件与查询源同步；
   - DLQ 对该 kind 使用封顶退避持续 pending，并让历史非 delivered 状态重新可消费；
   - 输出未决数量、最老年龄秒数和达到 15 分钟后的结构化 ERROR。
4. 缺口2：以业务写入追踪器排除空白/keepalive；已交付且 ledger 不可用时保持真实流终态，跳过带 Abort 的 audit-ref 拒绝器并写入结算恢复；零业务帧继续 Abort。
5. 缺口1：准备完整 header 后直接 `Write(body)`，不预先 `WriteHeader`；零写/短写只做脱钩 Abort 与内部日志；完整长度写入后结算，失败只入恢复且 HTTP 200 不反悔。
6. 缺口4：增加 `SourceImagesDelivered`；图片完整写后以原 `SettleRequest` 结算/恢复；短写 Abort；生产 wiring 注入现有 DLQ service。
7. 跑定向单元、真 PG sweep、quota/slot/并发协作测试；逐个做临时反向变异并保存真实红点，再恢复正确代码。
8. 跑全量门禁，检查统一 diff、迁移目录、依赖文件、baseline 与 Git 状态。

## 判别测试与变异红点

| 编号 | 正确行为 | 回退后必红点 |
| --- | --- | --- |
| 1 | Chat 非流式 settle 失败仍完整 200 body；恢复 1、Abort 0 | settle 移回写前后变成 500/空体 |
| 2 | Chat 非流式零写/短写后 Abort 1；Settle/恢复 0 | 忽略写入长度会出现 Settle 或恢复 |
| 3 | 已交付流 ledger+DLQ 双失败后非 Failed；Abort 0；恢复 1 | 恢复旧 `ledgerFailClosed` 会 Failed+Abort |
| 4 | 流式零业务帧失败仍 Abort；恢复 0 | 把 header/keepalive 当交付会不 Abort |
| 5 | 未决恢复 claim/slot/quota 不清；普通孤儿照旧清 | 删除排除子查询会零成本释放受保护对象 |
| 6 | 图片 settle 失败仍完整 200 JSON；恢复 1、Abort 0；图片短写 Abort | settle-before-write 或忽略写错会改变状态/体/调用数 |
| 7 | 成功 settle 仍释放 billing/quota/provider slot；并发容量和 reconcile 不回归 | 删除任一协作接线后状态或容量断言红 |

## 关键守卫

- Header：buffered 路径只设置 header，不显式提前 `WriteHeader`；`Write` 后若失败，不再写任何错误响应。
- Claim：写失败发生在 Settle 前；交付后的任何错误都不能 Abort。提交结果不明统一进入三证恢复。
- Audit-ref：post-delivery 校验失败只记录并入恢复，不能调用会 Abort 的拒绝器。
- 未决：所有 `event_kind='post_delivery_settlement' AND status <> 'delivered'` 行均保护 claim 及协作资源。
- 重试：该 kind 的 handler 任何失败都回到 pending，退避不超过 CapBackoff；毒消息/缺 handler立即 ERROR，普通失败达到 MaxAttempts 或 15 分钟后 ERROR，均继续重试。
- 隐私：持久 `failure_reason` 与告警只写错误类别、tenant/claim/age/attempt，不写 prompt、响应、凭据或原始错误。
- 结构：不调大 baseline；gatewayhttp 生产净增控制在现有 +5% 余量内，必要的追踪能力放既有 `chatpipe` 子包。

## 预计文件

- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gatewayhttp/chat_completions_attempt.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chatpipe/chatpipe.go`
- 对应 gatewayhttp 测试文件
- `backend/internal/imageshttp/handler.go`、`attempt.go`、`billing.go`、`handler_test.go`
- `backend/internal/settlementrecovery/payload.go` 及测试
- `backend/internal/dlq/retry.go`、`service.go`、`store.go`、`depth.go` 及测试
- `backend/internal/billing/lease_sweep.go` 与 PG 集成测试
- `backend/sql/queries/balance_holds.sql`、`pool_slot_acquisitions.sql`、`quota.sql`
- 对应 `backend/internal/db/**` 生成文件
- `backend/cmd/gateway/routes.go`、`wiring_test.go`

## 门禁

```text
gofmt -l <改动 Go 文件>
go build ./...
go vet ./...
go test ./...
HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/huakai_e2e go test -tags integration_pg ./...
go test ./internal/codebudget
```

全程不执行 `git add`、`git commit`、`git push`；最终交付未暂存统一 diff与真实测试输出。
