# 2026-07-05 audit-remediation-batch-a-codex

| Owner directive | "任务:裁定落地批A(六项,均已按三镜调研拍板...禁止 git commit/push...每个行为改动配判别测试并 §14 变异证红" |
| --- | --- |
| Scope | 落地 C-1b、PO-1、NT-1、PO-3、S3-4 一级、C-2 一级。同步必要测试、OpenAPI、compose 与部署文档。避开 Owner 明确禁改路径。 |
| Out of scope | 不提交 git commit/push；不改 `internal/config/`、`cmd/gateway/routes_moderation.go`、`internal/moderation/`、`internal/gatewayhttp/chat_completions_*`、`internal/payment/`、`internal/subscription/`；不做 C-2 自动补价 worker；不做 S3-4 二级 job_kind/冲减备忘迁移；不建前端页。 |
| Success criteria | 六项行为均有判别测试；关键测试在基线变绿；逐项变异能让对应测试变红并恢复；指定门禁尽量全跑并记录结果；新增/改动 Go 文件不超过 600 行。 |
| Time estimate | 预计 3-5 小时代理时间，取决于现有测试夹具与本地 PostgreSQL 可用性。 |
| Blast radius | 网关启动接线、通知广播受众、obs 死信管理 API、quota 退款观测、admin worker-stats 可见性。 |
| Failure modes | wiring 测试需要现有 worker seam；obs dlq 新包权限解析与路由接线不一致；integration_pg 数据夹具可能与本地库状态冲突；OpenAPI consistency 可能要求额外 schema 同步；变异恢复若不完整会污染后续门禁。 |
| Mitigation | 先亲读相关测试模式再改；新增小包复用既有 admin DLQ 鉴权口径；每次变异前 `cp` 备份并在测试后恢复；禁改路径只读不写；每项记录基线与变异命令。 |
| Decision points | 若必须触碰禁改路径、高风险 schema/auth/billing ledger/quota enforcement 核心、或新增运行时依赖，停止请求 Owner 确认。 |
| Pre-execution checklist | 1. 查 `git status` 避免覆盖他人改动；2. 亲读 C-1b/PO-1/NT-1/PO-3/S3-4/C-2 相关代码与测试；3. 定位部署文档与 compose 文件；4. 小步编辑；5. 分项跑判别测试；6. 逐项做 cp 备份变异证红并恢复；7. 跑指定门禁；8. 输出中文报告。 |

## 具体执行顺序

1. 盘点工作树、文件体量与现有测试模式。
2. 先做低耦合接线项:C-1b 与 PO-1,补 cmd/gateway wiring/middleware 测试。
3. 做 NT-1 store SQL 与 integration_pg 判别测试。
4. 做 PO-3:新增 `internal/obsdlqhttp`、`internal/obs/dlq/store_admin.go`、路由/OpenAPI/指标/Hermes 只读源与测试。
5. 做 S3-4 一级:ReverseCost skip 结果、退款 worker WARN+expvar 与测试。
6. 做 C-2 一级:worker-stats count+expvar、部署文档与测试。
7. 对每项执行 §14 变异证红,恢复后跑基线与指定门禁。
