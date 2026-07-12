# B7 运行日志入库 + 运营台日志查询(实时轮询)— Claude 计划 2026-07-12

## 三镜对照(§16,全部真码核查)

- **sub2api**(默认跟法):`ops_system_logs` DB 表(level/component/message/request_id/extra JSONB,
  request_id 索引 + message GIN);**异步 sink**:内存队列 5000、批量 200、超载丢弃计数、
  **只采集 warn/error/fatal/panic**;分页 REST 查询 + request_id/client_request_id 过滤 +
  cleanup 端点 + sink health 端点。WebSocket 只推 QPS 指标,**不推日志行**。
- **new-api**:DB `logs` 表分页查询,request_id/upstream_request_id 过滤有索引;运行日志落文件,
  管理端只能列举/清理文件。无 SSE/WS/轮询推送。
- **CLIProxyAPI**:运行日志文件游标 tail(客户端轮询)、请求日志每请求一 JSON 文件按 ID 下载。
- **结论:服务端推送式日志流三镜均无等价物**;「实时」体验 = 前端轮询增量拉取。

## HUAKAI 现状(摸排坐实)

运行日志(zap+slog 双栈,logfacade 统一脱敏+级别真相源)只落 stderr,无读取面;
`usage_records`+`billing_ledger_claims.logical_request_id` 是现成请求日志,用户侧
`GET /v1/generation?id=` 可单查,admin 侧无 request_id 过滤;前端「日志与诊断」页只有
loglevel 旋钮。

## 设计(sub2api 跟法 + HUAKAI delta)

1. **schema 0180**:`ops_runtime_logs`(id/created_at/level CHECK warn|error/component/
   message/request_id nullable/attrs JSONB);索引 created_at desc、request_id、level。
   保留策略 = cleanup 端点(按 before 时间戳删除)。
2. **internal/logsink**:异步 sink——有界队列(4096)+ 批量入库(100 条或 1s flush)+
   超载丢弃计数 + panic 隔离;**只收 warn+**。挂两处:logfacade.Handle 脱敏后 hook
   (slog 全量)+ zapcore Core 包装(zap 侧);sink 内对 message 再过 privacy 扫描
   (zap 侧无门面脱敏,纵深防御)。DB 不可用时丢弃不阻塞(fail-open,日志绝不拖垮主链)。
3. **admin 端点**(gatewayhttp,admin Bearer):
   - GET /v1/admin/ops/runtime-logs?level=&component=&request_id=&before_id=&limit=
     (键集分页,新→旧)
   - POST /v1/admin/ops/runtime-logs/cleanup {before}
   - GET /v1/admin/ops/runtime-logs/health(队列深度/丢弃计数/最后入库时刻)
   - GET /v1/admin/usage/by-request-id?request_id=(admin 版单查,tenant 收敛,补 admin 缺口)
4. **前端**:「日志与诊断」页聚合升级(Owner 聚合规则):运行日志表(级别/组件/request_id
   过滤)+ **自动刷新开关(3s 轮询增量,before_id 键集)**=「实时」体验 + request_id 检索卡
   (同时查运行日志与请求记录)。
5. **sqlc 漂移坑**:新查询手写生成码进独立包 internal/db/opslogs(B9 同法),不跑全量 regen。

## HUAKAI delta(三维)

- 架构:请求日志(usage_records)与运行日志(ops_runtime_logs)经 logical_request_id 单键互查
  (sub2api 双 ID,new-api 请求侧有运行侧无关联)。
- 生态:sink health 直接进日志页头部;脱敏两层(门面+sink)三镜皆无。

## 风控

- 新 goroutine worker:panic recover + 停机 drain;入库失败只计数丢弃,绝不反压请求链。
- 迁移 roundtrip 验证;真 PG 集成测过滤/分页/清理;变异证(级别过滤砍除/丢弃计数/脱敏跳过)。
- OpenAPI 同步 + cmd/gateway 一致性;routes.go+cmd/gateway 注入判别测试(ChatHandlerDeps 教训)。

## 成功标准

门禁全绿;warn+ 日志真实入库可查可清;request_id 双向检索;前端轮询实时视图可开关;
sink 超载丢弃有观测;上线说明补 B7 节。
