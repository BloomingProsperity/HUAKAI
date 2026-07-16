# 2026-07-16 HUAKAI 后端全局接线真实性审计 Batch 1

## 元数据

| 项目 | 值 |
| --- | --- |
| 审计分支 | `audit/backend-global-wiring-20260716-codex` |
| 基线提交 | `438536e6` |
| 审计范围 | `backend/cmd/gateway` composition root、生产路由、后台 worker、内部包生产可达性、渠道健康/探测、provider 注册表 |
| 证据原则 | `rg` 只用于定位；结论来自实际打开的生产源码、调用链和可判别测试 |
| 参考项目车道 | 独立 specifier 会话，只读 CLIProxyAPI、Sub2API、New API 快照；本会话未读取参考源码 |
| Observed findings | 4 |
| Inferences | 2 |
| Open questions | 5 |
| PR 规则 | 所有修改通过独立 Draft PR；未经 Owner 明确同意不合并 |

## 本批结论

Batch 1 没有发现 `cmd/gateway/routes_*.go` 中定义了却未被生产 router 调用的 mount helper。13 个 helper 均有一个生产调用点；模块注册路由额外有一个测试调用点。管理员 provider-account 能力同时挂载在 `/admin/v1/provider-accounts` 和 `/v1/admin/provider-accounts`，账号测试、健康、近期请求、批量操作、凭据和渠道健康控制器均进入两个 alias。

生产依赖图共有 267 个 `internal/*` 包，`cmd/gateway` 可达 261 个。六个不可达包中，`adminsessionauthtest`、`codebudget`、`openapicheck` 是测试或检查工具，`obs` 根包不代表其已被使用的子包，真正需要继续处理的是：

1. `internal/channelprobe`：完整主动探测 scheduler 已建成，但生产网关不构造、不启动、不持有。
2. `internal/provider/grok`：网页 session adapter 已建成但明确不注册；xAI 官方 API key 路径由通用 OpenAI-compatible adapter 正常提供，二者不是同一个能力。

本批确认并修复了一个跨 worker 的生命周期错误：原先所有 worker 共用进程信号 context，SIGTERM 到达时 worker 会先于 `http.Server.Shutdown` 排空请求而退出。现在 worker 使用独立 context，HTTP 排空完成后才统一取消；仅依赖 context 的四类 worker 还提供可等待的退出合同，网关确认它们全部退出后才允许关闭数据库。构建失败仍会立即取消并等待清理。

## 参考项目行为基线

以下行为来自独立 clean-room specifier 报告：

- Sub2API 的进程先完成 HTTP 优雅关闭，再由统一清理闭包停止后台对象；应用后台对象先停，Redis/数据库等基础设施后关。证据：`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:155`、`backend/cmd/server/main.go:166`、`backend/cmd/server/wire.go:109`、`backend/cmd/server/wire.go:298`。
- CLIProxyAPI 把后台生命周期所有权保留在核心服务，并显式停止观察器、HTTP、诊断和扩展宿主。证据：`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:1783`、`sdk/cliproxy/service.go:1843`。
- New API 的周期任务在适配能力注入后才启动，并声明使用数据库租约避免多实例重复执行。证据：`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:136`、`main.go:147`。

参考报告快照和限制见 `docs/process/research/2026-07-16-reference-runtime-wiring-batch1-specifier.md`。该报告无法在线重新 fetch，但三个本地 `origin/main` 快照均为 2026-07-14 至 2026-07-16 的提交。

## 生产路由矩阵

| 路由组合能力 | Defined | Mounted | Alias/入口 | 本批结论 |
| --- | --- | --- | --- | --- |
| Alerting admin | 是 | `routes.go:1225` | admin 主入口 | 已接 |
| Backup manifest | 是 | `routes.go:941` | admin 主入口 | 已接 |
| Invite validate | 是 | `routes.go:277` | 公开邀请入口 | 已接 |
| Moderation admin | 是 | `routes.go:1227` | admin 主入口 | 已接 |
| Module registry | 是 | `routes.go:942` | admin + 测试 | 已接 |
| Notifications | 是 | `routes.go:1208` | admin 主入口 | 已接 |
| Platform settings | 是 | `routes.go:929` | admin 主入口 | 已接 |
| Pricing catalog | 是 | `routes.go:1139` | admin 主入口 | 已接 |
| Risk admin | 是 | `routes.go:1226` | admin 主入口 | 已接 |
| Site config | 是 | `routes.go` 生产调用 | 公开配置入口 | 已接 |
| System health | 是 | `routes.go:931` | admin 主入口 | 已接 |
| Usage admin | 是 | `routes.go:930` | admin 主入口 | 已接 |
| User key controls | 是 | `routes.go:363` | 用户入口 | 已接 |
| Provider accounts | 是 | `routes.go:1049-1095` | 两套 admin alias | 已接 |

备注：若某个内部包还提供一个未调用的总 `MountRoutes` wrapper，但生产 router 已逐项挂载同一包的细粒度 handler，本批不把 wrapper 未调用判为功能缺失。判断依据是最终 HTTP 入口，不是 wrapper 名称。

## Worker 接线矩阵

| Worker 类别 | 构造/启动 | 停止所有权 | 本批状态 |
| --- | --- | --- | --- |
| selector aging、replay janitor、billing lease、settlement intent、pending reconcile、quota reconcile | 生产构造并启动 | stop function 保留在 `gatewayRuntime` | 已接；本批修复取消时序 |
| Hermes/usage retention、media task、API key expiry、payment expiry、subscription workers | 按配置构造并启动 | worker 或 stop function 保留 | 已接；本批修复取消时序 |
| credential scheduler、DLQ、outbox、model sync | 生产构造并启动 | `shutdownGateway` 显式 drain/stop | 已接；本批修复取消时序 |
| proxy health、TLS profile health、window cost、quota probe | 生产构造并启动 | 独立 context 取消 + `Wait` 退出确认 | 已接；HTTP 排空后取消，关库前等待退出 |
| channel active probe | 包、lister、scheduler、测试均存在 | 无生产所有者 | **未接线** |
| runtime log sink | 独立 context | `runtime.close` 最后 drain、DB 前停止 | 已有正确独立生命周期 |

## 内部包生产可达性矩阵

| 包 | gateway 可达 | 判定 |
| --- | --- | --- |
| `internal/channelprobe` | 否 | Confirmed built-but-unused |
| `internal/provider/grok` | 否 | Confirmed intentionally parked session path |
| `internal/adminsessionauthtest` | 否 | 测试支持包，不是生产缺口 |
| `internal/codebudget` | 否 | 代码预算检查工具，不是生产缺口 |
| `internal/openapicheck` | 否 | OpenAPI 检查工具，不是生产缺口 |
| `internal/obs` | 根包否 | 不能据此判缺失；`obs/dlq` 等子包已进入生产 |

## 设置与观测矩阵

| 能力 | 配置/字段存在 | 真实消费者 | 结论 |
| --- | --- | --- | --- |
| completion event bus | runtime config | completion producer + 四类 handler | 已接；关闭时相关异步观测均不运行 |
| provider `last_probe_at` | DB 字段 + admin JSON | completion event handler | 有消费者，但语义不是主动探测 |
| provider `last_probe_latency_ms` | DB 字段 + admin JSON | 未发现生产写入 | 观测字段半接线 |
| provider `probe_model` | admin 配置 | 人工凭据 dry-run | 已消费，但不能直接作为通用主动探测器 |
| channel active probe interval/config | scheduler 结构体 | 无 production composition root | 未激活 |

## 确认问题

### GW-WIRE-001：worker 在 HTTP 排空前收到进程取消

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1` |
| 分类 | W-02 半接线、W-11 顺序错误 |
| 状态 | **Fixed in this branch** |
| 用户影响 | SIGTERM、滚动发布或监听失败时，后台消费者可能先退出，而仍在执行的 HTTP 请求继续产生结算、outbox、健康或恢复动作。不同 worker 的持久化和 Stop 语义不同，结果可能是处理延迟到重启、停机窗口告警缺失或运行状态不一致。当前没有证据证明会永久丢钱，因此不报 S0。 |

**源码证据**

1. 进程信号 context 在 `backend/cmd/gateway/main.go:95-106` 创建，并传给 `buildGatewayRuntime` 和 `serveGateway`。
2. 修复前多个 worker 直接 `Start(ctx)`；信号到达时 `ctx.Done()` 先于 `shutdownGateway` 执行。
3. `shutdownGateway` 的声明顺序是先 `srv.Shutdown` 排空请求，再停止 worker，见 `backend/cmd/gateway/lifecycle.go:178-251`。原 context 所有权使真实行为与声明不一致。
4. runtime log sink 已单独绕开信号 context，并在注释中准确指出信号会早于 HTTP drain 取消，见 `backend/cmd/gateway/wiring.go:841-853`。这证明该时序不是假设。
5. 第一轮独立 Codex review 进一步确认：只调用 cancel 仍不足够。`proxyhealth`、`tlsfphealth`、`windowcost`、`quotaprobe` 原先只有内部 goroutine，没有 `Stop/Wait`，`runtime.close` 可能在 goroutine 尚未退出时关闭 `pgPool`。

**修复**

- `backend/cmd/gateway/wiring.go:864-866` 建立独立 `workerCtx`。
- 所有生产 worker 和 selector aging worker 使用 `workerCtx` 启动。
- `backend/cmd/gateway/lifecycle.go:186-189` 在 HTTP 排空结束后统一取消 worker。
- 四类只有 context 生命周期的 worker 新增并发安全的 `Wait(context.Context)`，生产构造后把 waiter 保留到 `gatewayRuntime`，见 `backend/cmd/gateway/wiring.go:1286-1339`。
- `backend/cmd/gateway/lifecycle.go:251-294` 并行等待全部 context worker，单个总预算 10 秒；等待错误并入 shutdown 返回值。
- `gatewayRuntime.close` 在构建失败或 defer 清理时也会取消并等待，见 `backend/cmd/gateway/lifecycle.go:71-135`，避免启动中途泄漏或关库竞态。

**验证**

- `TestShutdownGatewayKeepsWorkersAliveUntilHTTPDrainCompletes` 启动真实 TCP server，阻塞一个 in-flight handler，断言 handler 释放前 worker 未取消；HTTP 排空后 worker 收到取消；worker 尚未真正退出时 shutdown 仍不能返回。
- 四个 worker 各有判别测试，证明 `Wait` 在启动 context 取消前阻塞、取消后成功返回；相关包通过 `-race`。
- 正向测试通过。
- 变异测试把 `cancelWorkers()` 临时挪到 `srv.Shutdown` 前，测试立即以“HTTP handler 尚未排空时 worker 已被取消”失败，随后已恢复正确实现。

### GW-WIRE-002：主动渠道探测完整存在，但未进入生产

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（功能完整度/运维可靠性） |
| 分类 | W-01 建而未用、W-03 假激活风险 |
| 状态 | **Owner Decision Required** |
| 用户影响 | 当前渠道健康主要依赖真实用户请求产生被动信号。无流量账号不会被主动验证；运维不能把 health 页面上的时间戳当作主动探测证据。 |

**源码证据**

1. `backend/internal/channelprobe/scheduler.go:48-84` 定义 `ActiveProbe`、lister、health service 和 interval。
2. `backend/internal/channelprobe/scheduler.go:87-155` 每次 tick 列举账号、执行 probe、分类结果并写入 `channelhealth.Service.ApplySignal`。
3. `backend/internal/channelprobe/postgres_lister.go:33-80` 已有真实 PostgreSQL 账号/凭据枚举。
4. `go list -deps ./cmd/gateway` 不包含 `internal/channelprobe`；`cmd/gateway` 没有构造、启动或持有 scheduler。
5. `Run` 在 `ActiveProbe == nil` 时直接成功返回，见 `scheduler.go:87-90`，因此仅构造 scheduler 但漏 probe 仍会静默空转。

**为什么没有直接接入**

管理员账号测试不是可直接复用的主动探测器：

- `backend/internal/credentialworker/provider_account_dry_run.go:61-78` 调用的是 credential refresh adapter，不返回 HTTP status、latency、rate-limit reset 或 request ID。
- 多种 OAuth/session 模式因为验证会持久化刷新结果而被明确拒绝 dry-run，见 `provider_account_dry_run.go:81-106`。
- 静态 API key adapter 可能返回“不需要 refresh”，无法证明实际模型请求可用。
- 定时执行真实模型请求可能产生费用、限流和风控信号；多副本同时执行还会重复探测。

**推荐方案**

选择 `Safe Equivalent`，不要把人工 credential dry-run 包装成定时 probe：

1. 定义 provider-neutral 的最小探测合同：账号、规范模型、只读最小请求、timeout、最大响应、状态码、安全错误类、延迟和 reset time。
2. 默认关闭，按 tenant/account 显式启用；限制频率、每日成本和并发。
3. scheduler 使用 PostgreSQL advisory lock 或租约防多副本重复；保留运行历史和最后错误类。
4. probe 成功/失败写 `channelhealth`，但把凭据刷新健康、provider-account health 和 channel-health 三套状态的优先级写成明确合同。
5. 启动期和 system health 暴露 `disabled / leader / running / degraded / last_success`，不能只报 service non-nil。
6. 用真实 composition-root 测试证明 producer、scheduler、lease、probe、health write 和 shutdown 全链闭环。

此方案会产生真实上游请求并改变健康状态，必须由 Owner 确认默认策略、费用预算和多副本协调后再实现。

### GW-WIRE-003：普通请求完成时间被写成 `last_probe_at`

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2` |
| 分类 | W-03 假激活、W-08 观测失真、W-12 重复体系 |
| 状态 | **Owner Decision Required** |
| 用户影响 | 管理端看到 `last_probe_at` 非空，会自然理解为主动健康探测已经执行；真实含义却是“某次普通请求 completion event 到达”。低流量账号为空，高流量账号看似持续被探测，两个含义完全不同。 |

**源码证据**

1. completion event bus 注册 `AccountHealthProbeHandler`，见 `backend/cmd/gateway/middleware.go:408-445`。
2. handler 输入是 `RequestCompletionEvent`，并用 `time.Now()` 构造 signal，见 `backend/internal/observability/account_health_probe_handler.go:53-64`。
3. PostgreSQL adapter 只把该时间写到 `provider_accounts.last_probe_at`，见 `backend/internal/observability/accounthealthprobe/postgres_probe.go:43-52` 和 `backend/internal/db/admin/admin_provider_account_health.sql.go:115-135`。
4. admin API 原样返回 `last_probe_at` 与 `last_probe_latency_ms`，见 `backend/internal/adminhttp/provider_account_health_handler.go:37-58`、`149-180`。
5. `last_probe_latency_ms` 在该链没有写入，生成 SQL 注释也明确留作 follow-up。

**推荐方案**

- 不再把普通请求 completion 命名为 probe。
- 最干净的合同是新增/迁移为 `last_request_observed_at`，真正主动探测独占 `last_probe_at` 和 `last_probe_latency_ms`。
- 在 schema/API 调整前，管理面必须明确标记 source，不能把该字段作为主动探测 activation 证据。

该方案涉及 schema 和管理 API 合同，按项目规则等待 Owner 确认。

### GW-WIRE-004：Grok 网页 session adapter 未注册，且存在 clean-room 高风险

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（clean-room/license） |
| 分类 | W-01 建而未用；功能处置为 Mandatory Roadmap/Owner Decision |
| 状态 | **Owner Decision Required；禁止直接注册** |
| 用户影响 | xAI 官方 API key 能力正常；网页 session 能力当前不可用。直接注册会把浏览器模拟、Cookie、WAF/Cloudflare 处理带入生产出口。 |

**源码证据**

1. `backend/internal/provider/grok/session.go:1-13` 明确说明 adapter 已就绪但 serving/cookie 接线未完成，因此不注册。
2. 该文件包含静态浏览器指纹、Cookie 和绕 WAF 相关实现，见 `session.go:27-38`、`69-129`。
3. `backend/internal/provider/registrydefault/default.go:249-253` 的生产 Grok 路径是 xAI 官方 API endpoint，使用通用 OpenAI-compatible adapter。
4. `go list -deps ./cmd/gateway` 不包含 `internal/provider/grok`。

**风险**

- 代码注释直接点名外部实现来源，违反 HUAKAI “代码注释禁止提及借鉴项目”硬规则。
- 静态指纹值、独特请求形态和绕 WAF 意图需要单独 clean-room/合规复核；删除注释不能自动消除实现来源风险。
- 该能力和官方 API key 路径不能简单合并注册，否则会混淆凭据类型、合规边界和出口策略。

**处置建议**

1. 保持 fail-closed，不注册、不删除功能。
2. 单独开 clean-room 审计 PR，先判断现有实现能否保留；不能保留时做行为级 Safe Equivalent 或 Plugin。
3. 由 Owner 决定该能力是否允许进入生产、是否必须插件化、默认是否关闭，以及 WAF/反检测边界。

## 非问题与反证

1. `cmd/gateway/routes_*.go` 的 mount helper 本批全部存在生产调用，不支持“很多页面后端路由没挂”的笼统结论。
2. `provider/grok` 不可达不等于 Grok 全部不可用；官方 xAI API key 路径已注册。
3. 某些包级 `MountRoutes` wrapper 没被调用，不代表 handler 没挂；生产 router 可能使用细粒度 mount。
4. `deps.mediaTaskWorker` 等字段若未被 handler 读取，但实例被 `gatewayRuntime` 保留用于 lifecycle，不属于“构造后丢弃”。
5. worker 同时由 `shutdownGateway` 和 `runtime.close` 调 Stop，在已核实现中 Stop/cancel 具有幂等语义；本批不把重复调用本身报为 bug。

## 本批修改

| 文件 | 修改 |
| --- | --- |
| `backend/cmd/gateway/lifecycle.go` | runtime 保留统一 worker cancel 和 waiter；HTTP 排空后取消，关库前等待 |
| `backend/cmd/gateway/wiring.go` | 所有生产 worker 改用独立 `workerCtx`；登记四类 context worker 的 waiter |
| `backend/cmd/gateway/lifecycle_test.go` | 新增真实连接的停机顺序与 worker 退出等待判别测试 |
| `backend/cmd/gateway/quota_probe_wiring_test.go` | 接线断言升级为必须使用 `workerCtx`、登记四个 waiter，并拒绝退回进程信号 `ctx` |
| `backend/internal/{proxyhealth,tlsfphealth,windowcost,quotaprobe}/worker.go` | 增加可等待的 goroutine 退出合同 |
| 对应四个 `worker_test.go` | 验证取消前阻塞、取消后退出 |
| 本报告 | 记录 route、worker、registry、setting 矩阵和四个确认问题 |
| 参考报告 | 保存隔离 specifier 的三项目运行接线证据 |

## 测试记录

已通过：

```text
go test -race ./internal/proxyhealth ./internal/tlsfphealth ./internal/windowcost ./internal/quotaprobe -count=1
go test -race ./cmd/gateway -run 'TestShutdownGatewayKeepsWorkersAliveUntilHTTPDrainCompletes|TestGatewayWiringInjectsAndStartsQuotaProbe' -count=1
go test ./cmd/gateway -run 'TestShutdownGatewayKeepsWorkersAliveUntilHTTPDrainCompletes|TestServeGatewayReturnsListenAndServeError|TestNewGatewayServerHasReadAndIdleTimeouts' -count=1
go test ./cmd/gateway -count=1
go test ./internal/channelprobe ./internal/channelhealth ./internal/observability/... -count=1
go test ./internal/codebudget -count=1
go test ./... -count=1
```

所有最终验证均显式设置：

```text
TMPDIR=/home/ubuntu/.codex-tmp/global-wiring/tmp
GOTMPDIR=/home/ubuntu/.codex-tmp/global-wiring/go-tmp
```

初次网关链接使用系统 `/tmp` 时出现 `disk quota exceeded`；`/tmp` 是仅余约 1.6G 的 tmpfs，代码未进入测试执行。改用根磁盘 `/dev/root` 上述目录后同一测试通过。未删除共享缓存，也未修改其他工作树。

变异验证：

```text
把 cancelWorkers 临时移到 srv.Shutdown 前
=> TestShutdownGatewayKeepsWorkersAliveUntilHTTPDrainCompletes 失败
=> 失败原因：HTTP handler 尚未排空时 worker 已被取消
```

第一轮独立 review 已完成。当前 Codex CLI 不接受旧命令中 `review` 子命令后的 `--sandbox`，因此使用等价只读形式 `codex exec --sandbox read-only --ephemeral review --uncommitted`。第一轮报告一个 S1：context worker 取消后没有等待，可能与关库竞态；本分支已按上述方式修复。第二轮使用相同只读形式复核，未发现当前改动引入的明确功能缺陷，未留下 S0/S1。

## Owner 决策点

1. 是否批准建设真正主动探测 Safe Equivalent：默认关闭、成本预算、账号级开关、数据库租约、多副本去重、真实 health write。
2. 是否批准把普通请求观测时间从 `last_probe_at` 迁到独立 `last_request_observed_at` 合同；该项涉及 schema/API。
3. Grok 网页 session 能力是保留为插件、重做 clean-room Safe Equivalent，还是继续 Mandatory Roadmap；当前禁止直接注册。

## 下一批

Batch 2 将横向追踪 chat、completions、embeddings、rerank、images、audio、Responses、Gemini 和 media task，逐协议核对：

`身份 → 规范模型 → 选号 → gate → 凭据 → 出站 → retry/fallback → health → claim/billing → audit → DLQ/recovery`

重点查 chat 已接而其他协议绕过统一健康、结算、错误分类或恢复的 W-05/W-10/W-11 问题。

## 真实性摘要

本报告的四个问题均来自实际生产源码链。两项推断是：主动探测若直接启用会产生真实上游费用/多副本重复，以及停机窗口影响因各 worker 持久化语义不同而表现不同；两项均已明确标注，没有断言已发生永久资金损失。当前五个 open question 集中在主动探测成本合同、多副本租约、三套健康状态优先级、观测字段迁移兼容和 Grok session clean-room 处置。
