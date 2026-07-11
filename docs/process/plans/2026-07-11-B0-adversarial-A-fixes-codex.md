# 2026-07-11 B0 对抗审 A 类缺陷修复（Codex 独立计划）

> 独立性声明：本计划只依据 Owner 本轮指令、B0 对抗审裁定、HUAKAI 内部实现与测试独立形成；未读取任何同主题 Claude 计划。此文件只制定计划，不代表已经执行。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “现只修对抗审抓到的 4 个 A 类缺陷……不碰 DB schema/迁移，不碰 Reserve/hold 准入，不改结算金额口径”；另含 “S1-6 最低要求：settle+enqueue 双失败必须触发 P0 外部告警/摘 readiness” |
| Scope | 修复 S1-1、S1-2、S2-1、S2-2，并完成 S1-6 最低 P0 信号；只修改交付证据、恢复前审计引用校验、OTel 仪表类型、相应测试与最小生产接线。 |
| Out of scope | 不改数据库 schema/迁移/SQL 结构；不改 Reserve、hold 准入、余额扣减公式、结算金额或 usage 权威来源；不处理裁定 B 类 S1-3/S1-4/S1-5，也不扩展 S1-7 运维终局；不新增依赖；不执行 `git add/commit/push`。 |
| Success criteria | 五项修复均有判别测试；恢复缺审计证据时 worker 不调用 `Settle` 且恢复行保持 pending；图片恢复跑通真实 Claim→Handler→Settler→MarkDelivered；两个未决结算快照指标以 gauge 导出；双失败产生 `critical`/P0 且 `event_class=money_lost_double_fault`；全部指定门禁通过。 |
| Time estimate | 墙钟约 1.5–3 小时（主要取决于全量单测和逐包隔离 integration_pg）；有效 agent 工作约 60–100 分钟。 |
| Blast radius | 流式响应的 Abort/Settle 分流、post-delivery settlement worker、恢复 payload JSON、gateway 生产接线、OTel Prometheus 指标类型、chat/images 双故障告警。任何误改都可能造成误扣、漏扣、恢复行假完成或告警缺失。 |
| Failure modes | 见下文；每个门禁最多迭代 3 次，同一测试连续 3 次修复仍失败即停止并如实报告。 |
| Decision points | 当前授权足以执行下列低/中风险改动。若审计重验必须引入 schema、迁移、新运行时依赖，或必须改变 Reserve/hold/金额口径，立即停止并请 Owner 裁定，不自行扩 scope。 |

## 资金路径不变量

1. `DeliveredTokenCount` 继续保留为 usage/计量证据，但绝不充当客户端交付证据。
2. 流式 Abort 与 Settle 的选择只认至少一帧业务帧 `Write` 返回 `n == len(body)` 且 `err == nil`；零写、短写、完整长度带错都不能把该帧记为已交付。
3. 一旦先前已有整帧成功，后续短写不会抹掉历史交付事实，仍走结算恢复而不是 Abort。
4. 已交付请求的金额、token 与成本仍由现有上游权威 usage/现有定价逻辑决定；本轮不改金额算法。
5. 恢复 worker 只有在 payload 携带 inline 路径认可的审计引用证据时才可调用 `Settler.Settle`；缺失证据返回可重试错误，DLQ 行不得标 `delivered`。
6. 双故障 P0 事件只携带脱敏、allowlist 内的租户/claim/request 与错误分类，不记录原始上游响应、密钥或自由文本错误。

## 预期文件范围

- 流式交付实现与测试：
  - `internal/gateway/streamdelivery/writer.go`
  - `internal/gateway/streamdelivery/writer_test.go`（若用最小独立测试文件更清晰）
  - `internal/gateway/forwarder.go`（只复核或做必要的交付证据透传，不改 token 计量）
  - `internal/gateway/forwarder_clientadapter_test.go`
  - `internal/gatewayhttp/chatpipe/chatpipe.go`
  - `internal/gatewayhttp/chat_completions_stream.go`
  - `internal/gatewayhttp/chat_completions_stream_test.go` 或同包聚焦测试文件
- 恢复审计证据与 worker 测试：
  - `internal/settlementrecovery/payload.go`
  - `internal/settlementrecovery/payload_test.go`
  - `internal/settlementrecovery/handler.go`
  - `internal/settlementrecovery/handler_test.go`
  - `internal/settlementrecovery/*integration_test.go`
  - `cmd/gateway/wiring.go`
  - `cmd/gateway/wiring_test.go`（仅在可判别地覆盖共享 policy 接线时修改）
- 指标：
  - `internal/otelbridge/expvarbridge.go`
  - `internal/otelbridge/otelbridge_test.go`
- P0 双故障：
  - `internal/settlementrecovery/enqueue.go`
  - `internal/settlementrecovery/enqueue_test.go`

实际只触碰完成目标所需的最小子集；不会覆盖或清理工作树里既有 B0 改动。

## 失败模式与缓解

| 失败模式 | 缓解与判别证据 |
| --- | --- |
| 删除 token 兜底后仍被 `Attempt.State.Chargeable` 或 `AmbiguousUsage` 绕过 | 将 Abort/Settle 分流显式门控到 `BusinessFrameDelivered`；首帧零写/短写端到端断言 `Settle=0, Abort=1`，恢复 `|| DeliveredTokenCount>0` 变异时测试必须红。 |
| raw 路径在真正写入前预累计 token 污染交付事实 | 保留 `delivered += eventDelivered` 的 token 计量，但只用 `WriteBusinessAndFlush` 的整帧成功结果累计 `BusinessFrameDelivered`；raw 与翻译路径分别注入零写、短写。 |
| 后续短写错误地抹掉已经成功交付的前帧 | 用“首帧整写成功、次帧短写”断言最终仍为已交付并走结算。 |
| 恢复 payload 只保存 DLQ ref，丢失持久 ledger ID+签名指纹 | 在 payload JSON 中持久化可校验的审计引用字段（非 DB schema），`FromCompletionEvent`/编解码 round-trip 判别测试覆盖两种合法证据。 |
| worker 依赖 nil policy 导致生产重验被静默禁用 | 生产接线注入与 inline/eventbus 相同的 `AuditRefPolicy`；handler 对生产 policy 调同一个 `eventbus.ValidateMoneyPathAuditRef`。缺证据错误保持可重试。 |
| 图片 worker 测试只验证 spy，没有真实状态机 | integration_pg 使用真实 `dlq.Store/Service/Worker` 与真实 `billing.Settler`，断言 claim/余额/hold、usage/billing 行、恢复行终态与精确金额。 |
| gauge 修复误把其它 counter 改成 gauge | 指标描述增加显式 instrument kind，默认仍为 counter，只给两个目标指标标 gauge；Prometheus `# TYPE` 同时断言目标为 gauge、控制 counter 仍为 counter，并验证快照值可下降。 |
| P0 事件仍只是普通 ERROR 或字段被 privacy redactor 阻断 | `EnqueueFailure` 的双失败汇聚点发 `SeverityCritical`、`priority=P0`、指定 event class；单测捕获结构化 slog，断言 critical/P0/class 且不含原始错误秘密。 |
| 修改撞到用户工作树未提交内容 | 每次补丁前复读目标 diff；只做窄 patch；不还原、不格式化无关文件；最终报告列出本轮实际文件。 |

## 具体执行顺序

1. 记录基线：保存 `git status --short`、目标文件 diff、相关单测基线；不暂存。
2. 先写 S1-1 失败测试：底层 writer 的零写/短写/整写带错/整写成功；raw 与翻译端到端首帧零写、首帧短写、先成功后短写；确认当前代码至少在判别点失败。
3. 最小修改整帧判定、`DeliveredStreamAttempt` 与上层分流；定向跑 `streamdelivery`、`gateway`、`gatewayhttp` 测试。
4. 先写 S1-2 handler 判别测试，扩充恢复 payload 的审计引用 bundle，worker 在 `Settle` 前调用共享校验，生产接线注入同一 policy；定向跑 `settlementrecovery` 与 `cmd/gateway`。
5. 增加两个 integration_pg 场景：缺审计证据保持 pending/不扣 hold；`SourceImagesDelivered` 真 worker 成功扣精确金额并标 delivered。先用脚本能够执行的单包标签测试快速定位，再以官方全脚本为最终证据。
6. 将 OTel bridge 改为 counter/gauge 分流，只标记两个 delivered_unsettled 指标；增加导出类型与下降值判别测试。
7. 在统一 `EnqueueFailure` 双失败点发 P0 critical 事件；补 chat/images 共用汇聚点的单元判别，并确认现有调用都不吞掉事件产生条件。
8. 对所有本轮修改文件执行 `gofmt`，确认 `gofmt -l` 输出为空；检查 `git diff --check` 与代码预算。
9. 按最多三轮纪律依次跑：定向测试、`go build ./...`、`go vet ./...`、`go test ./...`、`HUAKAI_IT_RACE="" bash scripts/integration-pg.sh`、codebudget 标准门。每个门失败只允许修复后重跑最多三次。
10. 最终核对没有 schema/迁移/Reserve/hold 准入/金额口径改动，没有执行 git 暂存或提交；输出逐修复文件、核心逻辑、真实测试输出、变异红点、门禁和剩余风险。

## 执行前检查清单

1. [ ] Owner 已确认本 Codex 独立计划，并已存在经 Claude/Codex 差异讨论后的权威合成计划或明确授权以本计划执行。
2. [ ] 未读取同主题 Claude 独立计划，独立性仍成立。
3. [ ] 目标文件当前 diff 已复读，确认不会覆盖 Owner/B0 既有改动。
4. [ ] `git status` 基线已记录，后续不运行 `git add/commit/push`。
5. [ ] 测试 fixture 能区分“上游 token > 0”与“客户端整帧写成功”，避免 winner/loser 共享关键特征。
6. [ ] 审计证据校验复用 HUAKAI 现有 `eventbus` 口径，不复制 gatewayhttp 私有逻辑形成漂移。
7. [ ] integration_pg 只由官方脚本克隆纯净迁移库，最终不手工复用脏库。
8. [ ] 新增生产 Go 文件不会突破 codebudget；优先在已有内聚文件中做小改动，测试文件按职责组织。

