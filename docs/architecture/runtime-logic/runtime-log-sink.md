# 运行日志入库链(logsink)

## 模块协作图

```
zap 调用点 ──► sinkCore(Core 包装,warn+ 旁路,privacy 扫描)──┐
                                                              ├─► logsink.Sink(有界队列 4096)
slog 调用点 ─► logfacade(脱敏)─► Tap(warn+ 旁路)────────────┘         │
                                                     批量 100 条/1s flush │ 超载丢弃计数
                                                                          ▼
                                            ops_runtime_logs(迁移 0180,平台级无租户列)
                                                                          ▼
                       GET/POST /v1/admin/ops/runtime-logs[/cleanup|/health](platform_admin)
                                                                          ▼
                                  前端「日志与诊断」页 RuntimeLogsPanel(3s 轮询增量 = 实时)
```

## 关键配合点

- **装配顺序**:sink 在 main 里先于 setupSlogFacade 创建(worker 构造期捕获 slog.Default);
  DB 就绪(buildGatewayRuntime 开 pgPool 后)才 Start 落库,此前 warn+ 积压在有界队列。
- **fail-open 铁律**:队列满丢弃、DB 失败整批丢弃、panic 隔离——日志采集绝不反压/打崩业务链;
  丢弃有计数,经 /health 端点可观测。
- **脱敏两层**:slog 路径复用 logfacade 脱敏(Tap 在 scrub 之后);zap 路径无门面,
  sinkCore 对 message+字段值自扫 privacy 禁写。
- **request_id 语义**:采集 attr 里的 request_id/logical_request_id(计费链 ID);
  chi access-log 的 X-Request-Id 不入库、与之无关联(已知数据模型缺口,见三镜对照)。
- **停机**:signal ctx 取消 → worker drain 队列存量再退出。

## 三镜对照

sub2api ops_system_logs(异步 sink/warn+/丢弃计数/分页+request_id 过滤)= 跟法来源;
new-api 仅 DB 分页无运行日志入库;CLIProxyAPI 文件游标 tail。三镜均无服务端推送流,
「实时」业界形态即轮询,HUAKAI 前端 3s 轮询增量合并。

## 已知边界

- 表为进程共享(多实例都写同一表,天然多实例安全,优于内存 ring 方案)。
- admin 用量列表尚无 request_id 过滤(触 billing sqlc 漂移面,defer;
  运行日志表自身可按 request_id 查,用户侧 /v1/generation?id= 已可单查)。
- 保留策略靠 cleanup 端点手动/外部调度,无自动清理 worker。
