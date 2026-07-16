# 可观测性与日志领域唯一权威文档（SSOT）

> 建档：2026-07-15（UTC）  
> 核验基线：分支 `feat/ui-density-overview`，代码基线 `0f7d6b69`。  
> 本文描述生产代码现状；`docs/specs/privacy-no-user-data-logs.md` 仍是规范目标，但不能把
> 其中尚未落地的机制写成当前事实。

## 1. 运行日志链

- 进程启动时创建异步 sink；zap 通过 Core 包装采集，slog 通过门面 tap 采集，均只旁路
  warn 及以上日志（`backend/cmd/gateway/main.go:31-65`；
  `backend/internal/logsink/capture.go:14-40,73-140`）。
- sink 的默认队列为 4096，按 100 条或 1 秒刷库；队列满、数据库失败或 panic 时丢弃并计数，
  不阻塞请求；停机时尽力 drain（`backend/internal/logsink/sink.go:29-33,72-175,178-206`）。
- 持久层写入 `ops_runtime_logs`，支持 level、component、request_id、before_id 与 limit 过滤，
  以及按时间清理（`backend/internal/logsink/store_postgres.go:13-54,67-125`）。
- 管理端列表、清理、健康端点均由平台管理员门控；清理先写审计再删除
  （`backend/internal/gatewayhttp/admin_runtime_logs_handler.go:21-52,75-190`；
  `backend/cmd/gateway/routes.go:965-986`）。
- 前端已经能查询、过滤、翻页和以 3 秒间隔轮询；没有清理按钮
  （`frontend/src/features/logsdiag/RuntimeLogsPanel.tsx:43-188`）。因此保留策略当前是 API 或
  外部调度驱动，不是自动 TTL。

## 2. 指标与告警

- Prometheus/OTel 指标桥默认关闭；只有 `HUAKAI_METRICS_PROMETHEUS=true` 才创建本地 registry
  和 meter provider（`backend/internal/otelbridge/provider.go:18-44`）。
- `/metrics` 与 `/debug/vars` 都在 platform-admin gate 后，不能当公共匿名 scrape 端点
  （`backend/cmd/gateway/middleware.go:114-126`）。
- 运行、计费、缓存等 expvar 被桥接成指标；指标 handler、告警复合快照和 scheduler 在 runtime
  装配（`backend/cmd/gateway/wiring.go:1540-1579`）。
- 告警 scheduler 周期评估启用租户并使用 leader lock；service 实现持续窗口、冷却、静默、触发、
  恢复与投递（`backend/internal/alerting/scheduler.go:94-160`；
  `backend/internal/alerting/service.go:273-351`）。
- 告警快照聚合用量的延迟/成本/请求量并叠加账号健康；读取失败采用可观测的降级，不阻断其他
  指标（`backend/internal/alertmetrics/composite.go:11-27,57-203`）。

## 3. 请求用量与关联

- 每请求日志不另建 `relay_request_logs`；`usage_records` 已承载 model、cost、status、provider、
  token、request_id 和信任提示。自助 API 由解析出的 tenant/API key 身份强制收敛，并使用
  键集分页（`backend/internal/meusagehttp/handler.go:58-88,105-163`）。
- 自助时间序列直接聚合已结算用量，窗口最多 31 天，tenant 与 API key 从身份取得而不是 query
  （`backend/internal/usageanalyticshttp/handler.go:1-25,86-145`）。
- 这是一项 **Merged Equivalent**：保留逐请求运营结果而不复制一套旁路表和写入链。

## 4. 隐私实现与规范差距

- 当前已有 System/UserAction/Security 三类事件接口和 allowlist redactor；user/security sink 未注入时
  不会自动持久化（`backend/internal/privacy/logger.go:21-67,69-180`；
  `backend/internal/privacy/redactor.go:15-62`）。
- HTTP privacy middleware 会在鉴权前把整个请求体读入内存、提取 metadata，再以可清零缓冲重放给
  后续 handler；这不是规范写的“forward-only zero-copy”
  （`backend/internal/privacy/middleware.go:39-75`）。
- slog 门面清洗 attrs，但保留原 message；panic 降级也保留 message
  （`backend/internal/logfacade/logfacade.go:57-87`）。标准库 `log` 被明确恢复为直写 stderr，
  不经过该门面或入库 sink（`backend/cmd/gateway/main.go:66-74`）。
- zap 入库路径会检查 message 与字段；slog 入库 tap 接收门面输出，门面没有扫描 message
  （`backend/internal/logsink/capture.go:19-40,100-159`）。因此“所有通道、所有消息、所有外部
  sink 均统一脱敏”目前只能作为规范目标，不能写成已完成。

## 5. 已确认风险与 Mandatory Roadmap

1. **日志 sink 与进程内迁移次序。** sink worker 在 `HUAKAI_AUTO_MIGRATE` 执行前启动；空库裸二进制
   启动时，早期批次可能因表尚不存在而 fail-open 丢弃
   （`backend/cmd/gateway/wiring.go:846-864`）。
2. **消息通道脱敏不闭合。** slog message 与标准库 log 可能绕开禁写扫描，见 §4。
3. **前端缺运行日志清理动作；后端没有自动 retention worker。** 现有 cleanup API 可由运维或外部
   调度调用，但不是自动生命周期策略。
4. **管理端 usage 按 request_id 单查未落地。** 运行日志可按 request_id 查，用户侧逐请求用量也可查；
   旧计划提出的 admin usage 单查端点没有挂载。
5. **主动探测与定时测试。** 旧 `ops-suite` 设计中的告警规则部分已经由现有告警引擎吸收，synthetic
   monitor 与 scheduled tests 仍作为 Mandatory Roadmap 保留在本 SSOT，不因删除旧设计而缩水。
6. **安全监测模块。** `docs/process/plans/2026-07-15-security-monitoring-module-claude.md` 是同日活跃
   规划，保留为 Owner-gated 输入，不宣称已完成。

## 6. 当前保留的领域文档

- `docs/specs/privacy-no-user-data-logs.md`：规范目标；实现差距已在 DRIFT 登记。
- `docs/process/plans/2026-07-15-security-monitoring-module-claude.md`：活跃/Owner-gated 规划。
- 领域以外的 audit ledger、trust-chain 证据和原始研究：受保护，本波不动。
- 被删除的实现计划、错误 feature tree 与重复运行日志说明的逐项依据见
  `docs/architecture/DOC-CONSOLIDATION-DELETION-LOG.md`。
