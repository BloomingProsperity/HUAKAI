# 2026-07-16 HUAKAI 后端全局接线真实性审计 Batch 1-2

## 元数据

| 项目 | 值 |
| --- | --- |
| 审计分支 | `audit/backend-global-wiring-20260716-codex` |
| 基线提交 | `438536e6` |
| 审计范围 | `backend/cmd/gateway` composition root、生产路由、后台 worker、内部包生产可达性、渠道健康/探测、provider 注册表，以及 Claude、Gemini、Antigravity、Kimi 账号链 |
| 证据原则 | `rg` 只用于定位；结论来自实际打开的生产源码、调用链和可判别测试 |
| 参考项目车道 | 独立 specifier 会话，只读 CLIProxyAPI、Sub2API、New API 快照；本会话未读取参考源码 |
| Observed findings | 8 |
| Inferences | 4 |
| Open questions | 7 |
| PR 规则 | 所有修改通过独立 Draft PR；未经 Owner 明确同意不合并 |

## 本批结论

Batch 1 没有发现 `cmd/gateway/routes_*.go` 中定义了却未被生产 router 调用的 mount helper。13 个 helper 均有一个生产调用点；模块注册路由额外有一个测试调用点。管理员 provider-account 能力同时挂载在 `/admin/v1/provider-accounts` 和 `/v1/admin/provider-accounts`，账号测试、健康、近期请求、批量操作、凭据和渠道健康控制器均进入两个 alias。

生产依赖图共有 267 个 `internal/*` 包，`cmd/gateway` 可达 261 个。六个不可达包中，`adminsessionauthtest`、`codebudget`、`openapicheck` 是测试或检查工具，`obs` 根包不代表其已被使用的子包，真正需要继续处理的是：

1. `internal/channelprobe`：完整主动探测 scheduler 已建成，但生产网关不构造、不启动、不持有。
2. `internal/provider/grok`：网页 session adapter 已建成但明确不注册；xAI 官方 API key 与 xAI OAuth 均通过正式 `grok_chat` 路径提供，二者与网页 session 不是同一个能力。

本批确认并修复了一个跨 worker 的生命周期错误：原先所有 worker 共用进程信号 context，SIGTERM 到达时 worker 会先于 `http.Server.Shutdown` 排空请求而退出。现在 worker 使用独立 context，HTTP 排空完成后才统一取消；仅依赖 context 的四类 worker 还提供可等待的退出合同，网关确认它们全部退出后才允许关闭数据库。构建失败仍会立即取消并等待清理。

## 参考项目行为基线

以下行为来自独立 clean-room specifier 报告：

- Sub2API 的进程先完成 HTTP 优雅关闭，再由统一清理闭包停止后台对象；应用后台对象先停，Redis/数据库等基础设施后关。证据：`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:155`、`backend/cmd/server/main.go:166`、`backend/cmd/server/wire.go:109`、`backend/cmd/server/wire.go:298`。
- CLIProxyAPI 把后台生命周期所有权保留在核心服务，并显式停止观察器、HTTP、诊断和扩展宿主。证据：`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:1783`、`sdk/cliproxy/service.go:1843`。
- New API 的周期任务在适配能力注入后才启动，并声明使用数据库租约避免多实例重复执行。证据：`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:136`、`main.go:147`。

参考报告快照和限制见 `docs/process/research/2026-07-16-reference-runtime-wiring-batch1-specifier.md`。该报告无法在线重新 fetch，但三个本地 `origin/main` 快照均为 2026-07-14 至 2026-07-16 的提交。

账号链对照见 `docs/process/research/2026-07-16-reference-account-chains-sub2-specifier.md`。隔离 specifier 实际核实的本地 SHA 中，Sub2API 的 Claude、Gemini、Antigravity 已形成专属账号运营闭环；Kimi 主要是通用 API-key/协议/计量能力。CLIProxyAPI 补充了 Kimi 设备授权、续期和设备身份保存；New API 补充了 Kimi/Moonshot API-key 渠道、模型同步、余额和跨渠道 retry。

## Batch 2 账号链总览

### 代码能力与主库现实

本节的“已实现”指生产源码具备合法接线路径，不等于当前主库已有可用账号。对本机 `huakai` 数据库执行只读事务核对后，当前只发现启用的 `anthropic_messages` provider，共 9 个 provider account；相关凭据为 1 个 `needs_rotation`、4 个 revoked API key 和 2 个 revoked Claude Code，未发现 active 凭据。Gemini、Antigravity、Kimi、Grok 在该主库没有账号/凭据记录。该查询没有读取加密 payload，也没有写数据库。

| 账号链 | 获取/导入 | 自动续期 | 正式出站 | 账号运维闭环 | 与 Sub2/三镜对照 | 当前判定 |
| --- | --- | --- | --- | --- | --- | --- |
| Claude API key | 粘贴 | 静态，不刷新 | `anthropic_messages` Released | 全局模型同步；无账号 live E2E | Sub2 同时提供连接测试、模型查询、额度与恢复动作 | **请求链已接，运营链不完整** |
| Claude OAuth / Claude Code | PKCE、CLI/JSON 导入 | 专用 Anthropic refresher，失败回落通用 refresher | `anthropic_claude_session` Released，默认注册 | 当前唯一有主动额度窗口采集的 OAuth 账号 | Sub2 同样成熟，但其账号管理恢复面更集中 | **四类中最完整** |
| Gemini AI Studio API key | 粘贴 | 静态 | `gemini_messages` Released | 全局模型同步；没有 Gemini 账号额度采集 | Sub2 的 Gemini 是账号级 OAuth/套餐/额度体系 | **基础 API 链完整，账号运营偏薄** |
| Vertex Gemini | JSON/cloud bootstrap | metadata/service-account token 刷新 | `vertex_gemini` Released | 无账号级套餐/额度闭环 | 不与 Sub2 个人账号链完全同类 | **云账号请求链已接** |
| Gemini Code Assist | PKCE | Google public-client 刷新 | adapter 已实现但默认关闭，Experimental | 管理端默认拒绝 enabled；无 live E2E | Sub2 已形成授权、套餐、额度和请求闭环 | **功能很多，但尚未发布接线** |
| Gemini Advanced / Google One | PKCE | 可刷新 | session adapter 仍是未验证占位合同 | 不允许正式流量 | Sub2 的个人订阅模式已有专属账号链 | **Mandatory Roadmap** |
| Antigravity | 两套身份：`gemini/antigravity` 与 `antigravity/oauth` | 两套 refresh 路径并存 | 正式合同只接受后者，但合同仍红灯；前者无法合法进入该合同 | 无额度、逐模型健康、人工验证恢复闭环 | Sub2 已具备项目发现、逐模型额度、错误分级和管理恢复 | **确认存在身份与接线割裂** |
| Kimi API key | 粘贴 | 静态 | `kimi_chat` Released，coding endpoint | 无 Kimi 模型同步、额度/余额、专属健康 | Sub2 也主要停留在通用 Kimi 渠道；New API 有模型/余额 | **基础链与 Sub2 相当** |
| Kimi OAuth | 设备授权、JSON 导入 | 固定 Kimi token endpoint 自动刷新 | `kimi_chat` Released，Bearer passthrough | 没有设备身份保存、额度/封禁/重授权专属状态和 live E2E | 该能力超过 Sub2；CLIProxyAPI 的设备身份和续期链更完整 | **请求链领先 Sub2，运营链落后 CLIProxyAPI** |

### 共用请求链

上述 Released chat family 最终共用 HUAKAI 的 selector、账号健康/冷却、并发槽、claim、retry/fallback、channel health、billing、audit 和恢复骨架。Kimi 不是另起一套 handler，而是规范化为 OpenAI chat 形态后进入统一 HCSF/dispatcher；Claude session 和 Gemini 原生族则使用各自协议 adapter。

这部分的优点是资金、claim 和审计没有按 vendor 复制。缺点是账号运营能力没有同样统一：额度探测只认 Claude OAuth，模型同步只认 OpenAI/Anthropic/Gemini 三个全局 API-key 源，主动健康探测又尚未进入生产。

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
| 用户影响 | xAI 官方 API key 与 xAI OAuth 能力正常；网页 session 能力当前不可用。直接注册会把浏览器模拟、Cookie、WAF/Cloudflare 处理带入生产出口。 |

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

### GW-WIRE-005：运行时凭据兼容门只保护 Claude session

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（凭据错投/协议正确性） |
| 分类 | W-02 半接线、W-05 协议漂移、W-10 信息断链 |
| 状态 | **Owner Decision Required** |
| 用户影响 | 正常管理写入会校验 family/vendor/auth/runtime，但该写入校验在账号或 provider 查询失败时明确 fail-open，并声称由运行时兜底。真实热路径却只对 Claude session 复核。旧数据、直接数据库写入、并发变更或未来旁路若把 Gemini/Kimi/Antigravity/Grok 凭据绑错 family，非 Claude 请求可能把错误 secret 发给错误上游，并让 health、计价归因和 auth cooldown 一起串线。 |

**源码证据**

1. 通用兼容合同已覆盖 family、vendor、auth mode 和 runtime kind，见 `backend/internal/servingcapability/contracts.go:115-133`。
2. 账号创建会调用该通用合同，见 `backend/internal/gatewayhttp/accountcreate/atomic.go:38-65`。
3. 凭据创建前的账号/provider 查询失败会直接返回 nil，并在注释中依赖运行时兜底，见 `atomic.go:75-82`。
4. 发网前复核却被 `if family == anthropic_claude_session` 限定，见 `backend/internal/gatewayhttp/chat_completions_dispatch.go:565-582`。
5. `TestAllContractAuthModesMaterializeCompatibly` 已证明所有已声明合同的 handler runtime kind 可被自身合同接受，可作为泛化前置安全网。

**建议**

把发网前复核泛化到所有存在 serving contract 的 family；不兼容账号必须先释放/中止 claim，再切换账号，且绝不能构造上游请求。该改动会改变凭据鉴权契约和异常数据的线上行为，按 Owner 规则先确认再改。

### GW-WIRE-006：Antigravity 两套凭据身份没有收敛成一条合法生产链

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1` |
| 分类 | W-01 建而未用、W-02 半接线、W-12 重复体系 |
| 状态 | **Owner Decision Required** |
| 用户影响 | `gemini/antigravity` 能导入、保存、刷新，专用 refresher 也兼容它；但 `antigravity_session` serving 合同只接受 `antigravity/oauth`。正常管理写入因此不会让旧身份合法进入正式流量。另一方面，canonical `antigravity/oauth` 仍是红灯实验合同，且生产刷新走 Gemini operator OAuth 配置，而不是同包已经实现的固定公开客户端 refresher。结果是两条各自有一半能力的链并存。 |

**源码证据**

1. 获取目录同时暴露 `gemini/antigravity` 与 `antigravity/oauth`，见 `backend/internal/credentialacq/types.go:235-244`。
2. credential handler 同时物化两种身份，见 `backend/internal/credentialstore/types.go:292-302`。
3. 通用刷新注册表为 legacy 身份使用内置 Antigravity profile；canonical 身份却读取 Gemini operator OAuth 配置，见 `backend/internal/credentialworker/mode_refresh.go:119-156`。
4. Antigravity 专用 refresher 接受两种身份，见 `backend/internal/provider/antigravity/refresher.go:487-499`，但生产 scheduler 只专门注册 Anthropic 和 Copilot，Antigravity 走通用 refresher，见 `backend/cmd/gateway/wiring.go:1610-1627`。
5. 项目 enrichment 只认 vendor=`antigravity`，legacy `gemini/antigravity` 在 finalize 时不会进入该步骤，见 `backend/internal/credentialacq/projectenrich/finalize.go:23-38`。
6. serving 合同只接受 `antigravity/oauth`，且明确 `Experimental + wire unverified`，见 `backend/internal/servingcapability/contracts.go:194-197`；管理目录测试固定为不可 enable、不可流量，见 `backend/internal/adminhttp/serving_capability_wiring_test.go:27-60`。

**建议**

先做 Owner 级身份决策，再改代码：

1. 推荐以 `antigravity/oauth` 为 canonical，legacy 只作为迁移输入，不再作为长期运行身份。
2. 明确现有 legacy 行如何迁移、回滚和审计；这一步可能触及数据迁移，必须单独批准。
3. canonical acquisition、project enrichment、refresh、runtime material、serving contract 和 admin recovery 必须使用同一身份和同一 OAuth profile。
4. 专用 refresher 要么进入生产并成为唯一实现，要么删除重复职责前先证明通用 refresher具备同等错误分类、锁、审计和恢复语义。

### GW-WIRE-007：账号级额度、模型、健康和恢复没有形成跨 provider 统一闭环

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（运营可靠性/feature parity） |
| 分类 | W-02 半接线、W-05 协议漂移、W-12 重复体系 |
| 状态 | **Owner Decision Required** |
| 用户影响 | Claude OAuth 能看到 5h/7d 配额窗口；Gemini、Antigravity、Kimi 账号即使请求能跑，也没有同等级的账号额度、套餐、逐模型范围、人工验证或余额状态。模型同步是独立的全局 API-key 目录，不能代表某个 OAuth/订阅账号实际可用模型。主动 health 又尚未接线，因此运维容易把“请求 adapter 存在”误当成“账号可运营”。 |

**源码证据**

1. quota worker 只接受 `anthropic/claude_ai_oauth` 和 OAuth access token，见 `backend/internal/quotaprobe/worker.go:190-205`。
2. quota lister SQL 也只枚举该模式，见 `backend/internal/quotaprobe/postgres.go:24-39`。
3. model-sync 配置只有 OpenAI、Anthropic、Gemini 三个全局 API-key 源，见 `backend/internal/config/model_sync.go:16-25`、`45-63`。
4. production fetcher 同样只构造这三类，见 `backend/cmd/gateway/wiring.go:1757-1798`。
5. Batch 1 已确认主动 channel probe 包完整存在但生产未启动。
6. 隔离参考报告观察到 Sub2 对 Claude、Gemini、Antigravity 分别提供不同的额度/套餐/逐模型健康与管理恢复，而不是用一个全局 models endpoint 代替账号状态。

**建议**

建立 provider-neutral 的账号观测合同，但允许每家返回不同维度：数据来源、抓取时间、旧快照、套餐、窗口、余额、模型能力、验证/封禁/重授权状态。额度默认只读，不直接改变强配额或资金；是否让观测结果参与 selector 必须另行 Owner 决策。

### GW-WIRE-008：Released 账号链缺少真实上游全旅程验收

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2` |
| 分类 | W-09 测试假覆盖 |
| 状态 | **Confirmed；建议下一修复批处理** |
| 用户影响 | Claude、Gemini、Antigravity、Kimi 都有大量 adapter、刷新、协议和接线单测，但当前 live/E2E 文件只明确覆盖 OpenAI/Codex、Grok、图片和通用 upstream。无法用现有测试证明每个 Released/拟发布账号真的完成“授权或导入 → 首次请求 → 临期刷新 → 401/429 → fallback → 管理恢复 → 脱敏”的全旅程。 |

**证据与反证**

- 本轮枚举 `backend/**/*live*test.go` 与 `*e2e*test.go`，没有发现 Claude、Gemini、Kimi、Antigravity 专属 live E2E。
- 组件测试并不空白：本轮目标测试覆盖 serving contract、admin readiness、credential acquisition/store/refresh、provider adapter 和 account compatibility，全部通过。
- 因为缺真实凭据，本轮没有声称这些链当前请求失败；问题是发布证据不足，而不是伪报已发生故障。

**建议**

按账号类型建立 opt-in live harness，凭据只从环境/密钥服务注入，默认跳过；每条旅程记录上游 request ID、最终账号、刷新结果、claim/结算状态和脱敏断言。Antigravity 在合同转绿前只做实验环境验收，不进入 Released gate。

## 非问题与反证

1. `cmd/gateway/routes_*.go` 的 mount helper 本批全部存在生产调用，不支持“很多页面后端路由没挂”的笼统结论。
2. `provider/grok` 不可达不等于 Grok 全部不可用；官方 xAI API key 与 xAI OAuth 路径均已注册。
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
| 本报告 | 记录 route、worker、registry、setting、账号链矩阵和八个确认问题 |
| 参考报告 | 保存隔离 specifier 的运行接线与账号链三镜证据 |

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
go test ./internal/servingcapability ./internal/adminhttp ./internal/credentialacq ./internal/credentialstore ./internal/credentialworker ./internal/provider/anthropic ./internal/provider/gemini ./internal/provider/antigravity ./internal/provider/registrydefault ./internal/gatewayhttp/accountcreate -count=1
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
4. 是否批准把运行时 `ValidateAccountCompatibility` 泛化到全部有 serving contract 的 family；该项会改变异常/遗留账号的凭据放行规则。
5. 是否批准以 `antigravity/oauth` 为唯一 canonical 身份，并另开数据迁移决策包处置 `gemini/antigravity` legacy 行。
6. 是否批准建设 Gemini、Antigravity、Kimi 的账号级只读观测合同；第一阶段只展示，不进入强配额、资金或 selector。

## 下一批

Batch 2 已完成 Claude、Gemini、Antigravity、Kimi 账号链切片。下一切片继续横向追踪 chat、completions、embeddings、rerank、images、audio、Responses、Gemini 和 media task，逐协议核对：

`身份 → 规范模型 → 选号 → gate → 凭据 → 出站 → retry/fallback → health → claim/billing → audit → DLQ/recovery`

重点查 chat 已接而其他协议绕过统一健康、结算、错误分类或恢复的 W-05/W-10/W-11 问题。

## 真实性摘要

本报告的八个问题均来自实际生产源码链。四项推断是：主动探测若直接启用会产生真实上游费用/多副本重复；停机窗口影响因各 worker 持久化语义不同而表现不同；非 Claude 凭据错配需要遗留/旁路条件才会触发；Kimi OAuth 缺设备身份是否影响真实上游仍需 live 验证。没有断言已发生永久资金损失或凭据泄露。当前七个 open question 集中在主动探测成本与租约、健康状态优先级、观测字段迁移、Grok clean-room、Antigravity canonical/迁移、账号观测是否参与 selector，以及各 provider live 凭据验收。
