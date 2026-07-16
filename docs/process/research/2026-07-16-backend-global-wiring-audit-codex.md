# 2026-07-16 HUAKAI 后端全局接线真实性审计 Batch 1-2C

## 元数据

| 项目 | 值 |
| --- | --- |
| 审计分支 | `audit/backend-global-wiring-20260716-codex` |
| 基线提交 | `438536e6` |
| 审计范围 | `backend/cmd/gateway` composition root、生产路由、后台 worker、内部包生产可达性、渠道健康/探测、provider 注册表、Claude/Gemini/Antigravity/Kimi 账号链、账号导入/同步/迁移、三身份与单层租户边界，以及以 Sub2 为主轴的完整账号系统对照 |
| 证据原则 | `rg` 只用于定位；结论来自实际打开的生产源码、调用链和可判别测试 |
| 参考项目车道 | 独立 specifier 会话，只读 CLIProxyAPI、Sub2API、New API 快照；本会话未读取参考源码 |
| Observed findings | 20 |
| Inferences | 8 |
| Open questions | 17 |
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

Sub2 整套账号系统深读见 `docs/process/research/2026-07-16-sub2-account-system-full-logic-specifier.md`。CLIProxyAPI 与 New API 的运行时补充对照已重新锁定远端默认分支可达 SHA，见 `docs/process/research/2026-07-16-reference-default-branch-account-runtime-supplement.md`。

账号导入四项能力的独立 clean-room 深读见 `docs/process/research/2026-07-16-sub2-account-import-four-gaps-specifier.md`。该报告把 `Agent Identity` 核实为包含 Ed25519 私钥和任务绑定的实际认证模式，并把 CRS 核实为 `claude-relay-service` 专用同步协议；二者都不能按名称猜成普通账号元数据或通用同步标准。

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

## Batch 2B：Sub2 整套账号系统对照 HUAKAI

### 大白话结论

Sub2 的账号系统不是“建账号、存 token、发请求”三个零件，而是一整条持续流转的生产链：

`创建/导入/授权 → 秘密保护 → 自动续期与防旧 token 回滚 → 账号运行状态 → 候选硬门 → 排序与粘性 → 并发/RPM/session 占用 → 凭据/代理/协议准备 → 单账号 retry → 跨账号 fallback → 错误反写 → 最终账号计量计费 → 管理恢复 → 多副本后台收敛`

隔离 specifier 实际观察到，Sub2 会让账号的到期、限流、过载、临时不可调度、逐模型限流、额度和人工调度状态直接影响候选；选中后仍需占槽、准备 token/代理/协议；失败后按 401/403/429/529 等分类修改运行态；最终按真正成功的账号和上游结果归因；管理端测试、刷新、重新授权、清状态和后台快照又回到同一条链。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:368` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:221` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:249`

HUAKAI 不是“后端功能少”。相反，HUAKAI 的 claim、slot、结算恢复、审计、多租户数据库边界、加密凭据仓库和协议规范化骨架很强。真正的问题是：**核心数据面已经比较完整，但账号运营闭环较弱；同一账号同时受多套状态体系管理；部分高级能力默认关闭或只在局部 provider 生效；管理端看到的状态和 selector 真正使用的状态不总是同一份。**

### 真实调用顺序

HUAKAI 当前主 chat 链可以还原为：

`管理端先建 provider account → 再向既有 account 写入/采集 credential → selector 从 PostgreSQL 拉候选 → SQL 先按 enabled/provider/channel/health_state/credential_state/模型/协议/能力过滤 → channelhealth/auth/window/session/rate 等 gate 再过滤 → DB slot + claim → 凭据物化与出站 → retry/fallback → channelhealth/逐模型冷却/凭据热刷新反馈 → 最终结算、审计和恢复`

这条主链能够工作，但有三个明显分叉：

1. `provider_accounts.health_state` 主要由凭据刷新结果维护，`channel_health_state` 主要由真实请求反馈维护，auth 还有独立降级车道。
2. `provider_accounts.rate_limit_reset_at/overload_until/temp_unschedulable_until` 能被管理列表识别和清除，但生产候选 SQL不读取；请求错误通常改写 `channelhealth` 或逐模型 JSON。
3. 管理“测试”走 credential refresh dry-run，不走真实 selector、协议映射、上游请求、错误回写和计费链。

### 功能总账

状态含义：

- `完整接入`：生产热路径、失败反馈、管理或恢复已形成闭环。
- `实现更强`：HUAKAI 在多租户、资金或恢复保证上更严格。
- `部分接入`：请求能跑，但运营、恢复或跨 provider 统一合同不完整。
- `重复体系`：同一职责存在两套以上状态或入口，尚无统一真相。
- `建而未用`：代码已存在，但默认或生产 composition root 未激活。
- `缺失`：本轮源码范围没有找到对应生产能力。
- `待真实验证`：存在实现和单测，但没有 live 全旅程证据。

| 功能域 | Sub2 源码观察 | HUAKAI 真码 | 判定 |
| --- | --- | --- | --- |
| 账号创建与类型 | 创建时同时带平台、凭据形态、分组、代理、并发、优先级和到期策略。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:108` | account CRUD 支持并发、优先级、权重、模型、能力、标签、代理和错误规则；凭据通常在账号创建后另走 acquisition/store。 | `部分接入`，对象能力丰富但入口分两段 |
| 账号级批量导入 | 混合输入逐项解析、去重，并决定创建、更新、跳过或失败。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:144` | helper 强制要求一个既有 `ProviderAccountID`，所有 candidate 都写入同一账号，见 `admin_credential_acquisition_handler.go:70-80,248-267,352-360`。 | `缺失`：现有是批量凭据导入，不是批量账号导入 |
| 敏感值边界 | 管理读取遮蔽 token/key/cookie；未回传敏感子项时保留旧值。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_redact.go:3` | 独立加密 credential store、明文零化、审计脱敏、rotate/acquisition 分离。 | `实现更强`，但需继续验证所有管理响应 |
| 刷新锁与竞态 | 后台刷新、请求准备和管理员重授权防止旧 token 覆盖新 token。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_cache_invalidator.go:71` | 刷新事务、advisory lock、版本/CAS、storm slot 和 stale slot reaper 已接。 | `完整接入` |
| 自动续期 | 分页扫描，按平台做并发/QPS/超时/重试/熔断。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:20` | credential scheduler 有扫描、预算、重试、审计、告警和多种 mode adapter。 | `完整接入` |
| 手动立即刷新 | 管理端可以刷新单账号，结果回到同一账号状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/grok_oauth_handler.go:128` | runtime 有 401 `RefreshHotPath`；provider-account 管理路由有 rotate/acquisition，但没有直接的 refresh-now 路由，见 `routes.go:1049-1095`。 | `缺失` |
| 重新授权 | 无法续期或撤销后进入重新授权。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:209` | 可重新走 credential acquisition/rotate。 | `完整接入`，但没有统一“需要重授权”运营状态 |
| 正交状态轴 | 活动、人工调度、到期、限流、过载、临时冷却、逐模型限制和额度分别表达。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` | `provider_accounts`、`channel_health_state`、credential state、auth cooldown、model rate limits 并存。 | `重复体系` |
| 到期硬门 | 账号到期直接排除候选。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` | account `expires_at` 用于刷新扫描，但生产 pool-group 候选 SQL不读取该字段，见 `pool_accounts.sql:125-177`。 | `部分接入/合同不清` |
| 账号级限流/过载/临时冷却 | 对应时间状态直接影响后续候选。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:221` | 管理列表识别三类字段，但 selector SQL不读；请求热路径主要写 channelhealth，见 `admin_provider_accounts.go:174-185`、`chat_completions_error.go:249-302`。 | `重复体系` |
| 逐模型冷却 | 429/模型错误可以只下掉账号×模型，不必下掉整号。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:271` | `model_rate_limits` 真实写库、加载到 `AccountSnapshot` 并进入 gate。 | `完整接入` |
| 候选硬门 | 状态、分组、模型、能力、账号类型和传输先过滤，再排序。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:231` | SQL已过滤 tenant/channel/provider/enabled/health/credential/model/protocol/capability，随后再过真实 gate。 | `完整接入` |
| 优先级与权重 | 硬门后可综合优先级、负载、排队、错误率、TTFT、额度和成本。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:924` | 默认 selector 支持 strict priority/priority weighted；高级 PASR 支持 segment 学习，但全局 mode 默认 `default`。 | `部分接入`，高级能力默认关闭 |
| 粘性与逃逸 | 前一响应和 session 粘性不得绕过健康、模型、传输和容量。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:379` | DB sticky store 与 gate/slot/claim 同链，失败可重新选号。 | `完整接入` |
| 并发槽 | 请求前原子占槽，异常和 fallback 释放。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:511` | PostgreSQL slot manager、租约和回收链已接。 | `实现更强` |
| RPM/TPM/session/window | 账号运行数据既参与调度也可在管理端查看。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:218` | session/window gate 已接但依赖缺失时 fail-open；主动 RPM/TPM 预检默认关闭。 | `部分接入` |
| 代理与传输 | 账号代理、身份、传输和协议在最终发网前准备。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:383` | 账号代理绑定、代理组、TLS profile、协议 adapter 和 transport selection 已接。 | `完整接入` |
| 单账号 retry | 短暂失败可在同号内按预算重试。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:310` | 有分类、预算和 delivery 边界。 | `完整接入` |
| 跨账号 fallback | 下一账号从原始请求重新应用自己的模型和身份。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_failover_cached_body_test.go:26` | attempt loop 重新 acquire、重新物化账号和 credential；claim/slot 边界有测试。 | `完整接入` |
| 错误反写 | 401/403/429/529 改变后续账号运行态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:221` | 逐模型冷却、channelhealth、auth cooldown、credential hot refresh 都存在，但写入不同状态源。 | `重复体系` |
| 最终账号计量计费 | 以最终成功账号、最终上游模型和 token 桶结算。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:249` | claim、settlement、delivery intent、DLQ/recovery 和 audit 共享最终 acquire 身份。 | `实现更强` |
| 账号级 quota/余额/模型 | 主动探测、被动头和本地统计形成账号视图。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:87` | 额度采集只完整覆盖 Claude OAuth；模型同步主要是全局源；account upstream-models 只支持 passthrough。 | `部分接入` |
| 真实账号测试 | 测试复用真实账号凭据和模型路径，并可写额度/恢复状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service_openai_test.go:104` | `/{id}/test` 是 refresh adapter dry-run；多数 OAuth/session mode 直接返回 unsupported，见 `provider_account_test_handler.go:51-90`、`provider_account_dry_run.go:58-105`。 | `缺失` |
| 管理恢复动作 | 测试、刷新、重新授权、清状态、重置 quota、批量修改和调度启停相互收敛。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:181` | 有 clear-rate-limit、rotate、acquisition、channelhealth pause/resume/force-active；缺 refresh-now、复制和统一 quota reset。 | `部分接入` |
| 批量修改一致性 | 批量操作逐项返回结果；本次未观察到全链补偿保证。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:147` | bulk-by-tag 只改 enabled/priority/weight，逐行 update 后逐行 audit，中途失败会留下部分成功。 | `部分接入` |
| 多副本路由收敛 | DB 账号通过 outbox、水位、fencing、租约和全量重建发布调度快照。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:299` | selector 每次直接查 PostgreSQL，避免快照陈旧，但增加数据库热路径压力；channelhealth 也持久化在 PostgreSQL。 | `产品差异`，不是功能缺失 |
| 真实全旅程证据 | 局部竞态测试较深，但未观察到所有 provider 的真实外部 E2E。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service_test.go:179` | Released 账号链同样缺 provider 级授权/导入到恢复的 live E2E。 | `待真实验证` |

### 对比后的架构判断

1. **HUAKAI 强在数据面正确性。** selector、DB slot、claim、结算、审计和恢复是一条共享骨架，没有让每个 provider 各写一套资金逻辑。
2. **Sub2 强在账号运行态闭环。** 管理动作、后台刷新、请求失败和 selector 围绕同一账号状态持续流转，运维看到的状态更接近实际调度状态。
3. **HUAKAI 当前最需要的不是继续堆 provider adapter。** 应先把账号状态、账号测试、账号级 quota/model 观测和管理恢复动作收敛成 provider-neutral 合同。
4. **PASR 不是当前第一矛盾。** 高级 selector 已存在，但默认关闭；即使立刻打开，也不能解决账号测试是假测试、运营状态分裂、批量导入不是账号导入等问题。
5. **不能照搬 Sub2 的 Redis 快照。** HUAKAI 当前 PostgreSQL 直选有更简单的权威性；是否引入快照应由压测和数据库负载决定，不应为了形式对标而增加一致性系统。

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

### GW-WIRE-009：账号运行状态存在多套真相，管理状态与实际选号不完全一致

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（运营判断/路由一致性） |
| 分类 | W-02 半接线、W-08 观测失真、W-10 信息断链、W-12 重复体系 |
| 状态 | **Owner Decision Required** |
| 用户影响 | 运维可能在 provider-account 列表中看到账号处于 active，也可能看到 `rate_limit_reset_at/overload_until/temp_unschedulable_until`，但生产 selector 不直接读取这些时间字段；真实请求产生的冷却又主要写到 `channel_health_state`。因此“管理页面认为账号什么状态”和“下一次请求是否选到账号”需要跨两个接口人工拼接。 |

**源码证据**

1. 生产候选 SQL只返回并过滤 `health_state/health_state_until`、`credential_state`、逐模型限制、模型、协议和能力，没有读取账号级 `expires_at/rate_limit_reset_at/overload_until/temp_unschedulable_until`，见 `backend/sql/queries/pool_accounts.sql:125-177`。
2. `DBAccountSource` 也只把查询返回的健康、逐模型冷却、窗口/session/RPM 等有限字段放进 `AccountSnapshot`，见 `backend/internal/pool/dispatcher/account_source.go:46-91`。
3. 管理列表却明确用三类账号级时间字段计算 `active/rate_limited/overloaded/temp_unschedulable`，见 `backend/internal/db/admin/admin_provider_accounts.go:162-185`。
4. `rate.Service.HandleUpstreamError` 只计算 `Decision`；它的 PostgreSQL store 只用于 clear cascade，不负责写入新冷却，见 `backend/internal/rate/upstream_service.go:192-213,215-291`。
5. chat 热路径拿到该决策后，逐模型 429 写 `model_rate_limits`，整号冷却则调用 `channelhealth.ForceCooldown` 或继续 `ApplySignal`，见 `backend/internal/gatewayhttp/chat_completions_error.go:249-302` 和 `backend/internal/gatewayhttp/chat_completions_dispatch.go:760-769`。
6. 生产 gate 最终按 provider account 查询最新 `channel_health_state` 并决定放行，见 `backend/internal/channelhealth/failover.go:47-92`。
7. 凭据刷新失败又单独写 `provider_accounts.health_state`，见 `backend/internal/credentialworker/health_state.go:48-81,124-159`。

**判断**

这不是“限流完全不生效”：逐模型冷却和 channelhealth 会真实挡住请求。问题是同一个账号至少有 provider health、channel health、auth cooldown、credential state、逐模型限制五套状态，写入者、恢复者和管理视图不统一。Sub2 的优势不是状态少，而是这些状态最终由同一账号候选门消费。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`

**建议**

先定“账号调度真相合同”，再改代码：

1. 明确哪些是正交状态、哪些是历史兼容字段，禁止两个字段表达同一冷却。
2. provider-account 详情聚合返回 selector 当前实际使用的 channel health、auth cooldown、credential state 和逐模型状态，并标注来源。
3. 选择一种迁移方向：要么账号级时间状态进入 selector；要么停止把它们当实时调度状态并迁移到 channelhealth。
4. `clear-rate-limit` 必须清理 selector 真正读取的状态，不能只清 provider account 列和 model JSON。
5. 该项可能改变 selector、管理 API 和历史数据解释，按高风险合同变更等待 Owner 确认。

### GW-WIRE-010：现有批量导入只能向一个既有账号导入多份凭据

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2`（运营效率/feature parity） |
| 分类 | W-02 半接线 |
| 状态 | **Confirmed；建议独立中风险 PR** |
| 用户影响 | 管理员批量导入一组账号材料前，必须先手工建立 provider account，并把所有候选绑定到同一个 account ID。系统没有账号身份去重、创建/更新/跳过/失败汇总，也不能把一批独立账号自动落成独立调度单元。 |

**源码证据**

1. helper 请求强制携带一个 `ProviderAccountID`，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:70-80`。
2. CSV/JSON/CLI 可以解析出多个 candidate，但循环为每个 candidate 创建的 flow 都复用同一个 `req.ProviderAccountID`，最终再次覆盖 candidate 的 account ID，见 `admin_credential_acquisition_handler.go:232-267`。
3. 创建 flow 时 `tenant_id` 和 `provider_account_id` 都是必填，见 `admin_credential_acquisition_handler.go:352-360`。
4. Sub2 的账号级导入会逐项识别账号身份并决定创建、更新、跳过或失败；access-token-only 更新还保护已有 refresh material。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:144` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:265`

**建议**

新增独立“账号批量导入”服务，不改现有 credential helper 语义。先 dry-run 解析和去重，再由 operator 确认创建/更新计划；每项事务隔离、返回明确结果，凭据仍通过现有 encrypted store/finalizer 落库。涉及账号身份键和更新策略时先提交 Owner 决策，不应凭邮箱、token 文本或显示名直接猜唯一性。

### GW-WIRE-011：账号“测试”不是实际请求链测试，OAuth/session 账号大多无法测试

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（发布证据/运维误判） |
| 分类 | W-03 假激活、W-09 测试假覆盖 |
| 状态 | **Owner Decision Required** |
| 用户影响 | 运维点击 test 可能以为已经验证“该账号能按当前模型和协议真实出站”，实际只是调用 refresh adapter 的非持久化 dry-run；Claude/OpenAI/Gemini/Antigravity 多种 OAuth/session mode 被明确返回 unsupported。 |

**源码证据**

1. `POST /{id}/test` 直接委托 `DryRunProviderAccountCredentialWithProbeModel`，见 `backend/internal/adminhttp/provider_account_test_handler.go:47-56,75-108`。
2. dry-run 不经过 selector、claim、协议 adapter、真实 relay、错误回写和计费；它只调用 mode refresh adapter，见 `backend/internal/credentialworker/provider_account_dry_run.go:44-78`。
3. Claude OAuth/Code、OpenAI OAuth/Codex、Gemini Code Assist/Google One/Antigravity/OAuth、Copilot 和 canonical Antigravity 都被列为需要持久化刷新，因此直接返回 unsupported，见 `provider_account_dry_run.go:81-105`。
4. 账号级 upstream-models 也只支持 `upstream_passthrough` credential，不覆盖官方 API key 或 OAuth/session 账号，见 `backend/internal/adminhttp/provider_account_upstream_models_handler.go:96-125`。
5. Sub2 的账号测试实际使用账号凭据和模型路径，并能解析用量反馈；但其全外部旅程也不是所有 provider 都完整覆盖。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service_openai_test.go:104`

**建议**

建设默认不计费或严格成本封顶的真实账号测试合同：复用生产协议映射、凭据物化、代理和 transport，但使用独立 test claim/usage 分类，不进入客户账；结果要明确区分 credential validation、relay validation、model validation 和 quota probe。该动作会发真实上游请求并影响风控/限流，默认策略和成本预算需 Owner 批准。

### GW-WIRE-012：账号恢复动作没有形成一个完整管理闭环

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2` |
| 分类 | W-02 半接线、W-06 恢复断路 |
| 状态 | **Confirmed；部分动作需要 Owner 决策** |
| 用户影响 | 当前可以 rotate/re-acquire credential、清账号限流字段、暂停/恢复 channelhealth，但没有直接 refresh-now、复制账号或统一 reset provider quota。运维处理事故时需要知道每套状态该去哪个入口恢复。 |

**源码证据**

1. provider-account 路由总账只挂 CRUD、test、health、recent requests、bulk、upstream models、credential、acquisition 和 channelhealth，见 `backend/cmd/gateway/routes.go:1049-1095`。
2. account CRUD 的恢复动作只有 `clear-rate-limit`，见 `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:131-138`。
3. channelhealth 的手工动作是 pause/resume/force-active，且请求必须提供 credential ID/version，见 `backend/internal/gatewayhttp/channel_health_admin_handler.go:40-57`。
4. 401 热路径能调用 `RefreshHotPath`，但该能力未暴露为管理端单账号立即刷新入口。
5. Sub2 把测试、刷新、重新授权、清状态、重置 quota、批量修改和调度开关放在同一账号运营面。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:181`

**建议**

先增加只读“可执行恢复动作”诊断，让系统根据当前 credential/health 状态返回允许的 action，不立即改状态。随后分批补 refresh-now 和统一 clear/recover orchestration；复制账号和 quota reset 需要先定义哪些配置可复制、哪些运行态必须清空，不能盲目复制秘密或瞬态限制。

### GW-WIRE-013：bulk-by-tag 会留下部分更新或“已更新但无审计”的状态

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（管理操作一致性/审计完整性） |
| 分类 | W-02 半接线、W-11 顺序错误 |
| 状态 | **Confirmed；建议下一修复批** |
| 用户影响 | 一批账号逐行更新，任意中途错误都会保留前面已成功的行；更严重的是每行先 update 再写 audit，若 audit 插入失败，该账号已经修改但没有对应审计记录。响应只报告“前 N 个成功”，没有每项结果或补偿信息。 |

**源码证据**

1. bulk 只支持按 tag 修改 enabled、priority、static weight，见 `backend/internal/adminhttp/provider_account_bulk_handler.go:58-78`。
2. handler 在普通循环中逐行 `UpdateAdminProviderAccount`，失败时立即返回并明确显示已有成功数量，见 `provider_account_bulk_handler.go:84-100`。
3. 每行 update 完成后才构造并插入 audit；audit 失败同样立即返回，不回滚前面的 update，见 `provider_account_bulk_handler.go:102-142`。
4. Sub2 本轮也没有证明所有批量动作具备全链补偿，因此这里不宣称其事务性更强；只确认 HUAKAI 当前存在可复现的部分成功合同。

**建议**

把单账号 update + audit 收敛到同一数据库事务；批次层面可选择“全有或全无”或“逐项隔离并完整返回结果”，但必须显式定义。推荐先做逐项隔离：每个账号事务原子，响应返回 succeeded/failed/skipped 列表，支持幂等请求键和安全重试；是否做整批原子由规模与锁影响决定。

### GW-WIRE-014：Claude Cookie 自动登录整条链缺失

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2`（账号接入效率/feature parity；实现本身属于高风险凭据处理） |
| 分类 | W-01 能力缺失、W-10 信息断链 |
| 状态 | **Confirmed；实现前需要 Owner 确认安全策略** |
| 用户影响 | 管理员目前只能走浏览器 PKCE 或向既有账号导入凭据，不能粘贴 `sessionKey` 后由服务端完成账号识别、授权码获取和正式 OAuth/Setup Token 转换。批量接入 Claude 账号时仍依赖逐个浏览器流程或外部手工处理。 |

**HUAKAI 源码证据**

1. Anthropic 正式计划只有 API key、交互式 OAuth 和 Claude Code CLI/JSON 导入；没有 Cookie/sessionKey flow，见 `backend/internal/credentialacq/types.go:214-225`。
2. 管理 helper 只挂 paste、CLI、CSV、JSON 和 OAuth init/callback，没有 Cookie 转换入口，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:83-98`。
3. 当前 Claude OAuth exchanger 从浏览器回调取得授权码，再用已保存 PKCE verifier 换 token，见 `backend/internal/credentialacq/anthropic_oauth.go:62-124`；没有用 Cookie 发现组织和服务端取得授权码的路径。

**参考行为**

Sub2 允许粘贴一个或多行 `sessionKey`，后端完成组织发现、PKCE、授权码取得和令牌交换；原 Cookie 只作为转换输入，最终保存转换后的 OAuth/Setup Token 凭据。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:175-282`

**建议**

作为 `Safe Equivalent` 单独建设，不与普通 paste 混用：

1. 默认关闭；目标模型中的部署治理主体只负责部署级开关、授权和撤权，不能替租户执行；被授权租户管理员只能在自身 tenant scope 内使用，操作要求 step-up。
2. Cookie 只存在于单次请求内，不入库、不进审计正文、不进错误文本；完成后立即清零内存副本。
3. 使用固定批准的上游域名、client profile、scope 和受控 HTTP client，不接受请求体覆盖 endpoint。
4. 先返回逐项 dry-run/转换结果，再创建账号；稳定区分 Cookie 无效、组织发现失败、授权拒绝、上游变更和网络失败。
5. 批量任务允许只重试失败项，成功项不重复换取。

### GW-WIRE-015：Setup Token 只有枚举、闸门和刷新影子，没有正式生产入口

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（鉴权合同半接线/账号长期可用性） |
| 分类 | W-02 半接线、W-03 假激活风险、W-10 信息断链 |
| 状态 | **Confirmed；涉及鉴权合同，需 Owner 确认默认策略** |
| 用户影响 | 代码看起来支持 `setup_token`，但生产模式目录没有任何账号类型允许该 flow，管理路由没有专用入口，production deps 也没有开启 long-lived gate；即使通过旁路写入，生产 refresher 默认仍会拒绝使用 setup token。 |

**源码证据**

1. `FlowKindSetupToken` 和 `LongLivedToggle` 已存在，见 `backend/internal/credentialacq/types.go:11-23,106-120`。
2. `DefaultModePlans` 的 Anthropic OAuth 与 Claude Code 计划都没有把 `FlowKindSetupToken` 放进 `AllowedHelpers`，见 `types.go:214-225`。
3. 管理依赖有 `AllowLongLivedSetupToken`，但 production `credentialAcquisitionRouteDeps` 没有赋值，零值为 false，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:22-33`、`backend/cmd/gateway/routes.go:95-103`。
4. start 请求 long-lived 时会被该闸直接拒绝，见 `admin_credential_acquisition_handler.go:352-355`。
5. Anthropic refresh adapter 能识别 setup token，但默认实例的 allow 仍为 false；生产注册使用零值 adapter，见 `backend/internal/credentialworker/adapters/anthropic.go:24-45`、`backend/internal/credentialworker/mode_refresh.go:96-104`。

**参考行为**

Sub2 把 Setup Token 作为独立账号类型，使用更窄的推理 scope，并由统一后台 refresher 维持长期可用；“长期”指可刷新账号，不是访问令牌永久有效。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:365-371` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/token_refresher.go:40-71`

**建议**

把现有半截能力收敛成一等账号形态，而不是再造另一套刷新器：

1. 增加明确的 Setup Token acquisition plan、专用 start/helper 合同和权限范围展示。
2. acquisition gate 与 refresher gate 使用同一配置源，启动时校验两者一致，禁止“能导入但不能刷新”。
3. 管理合同同时展示当前 access token 到期、refresh material 状态、最近刷新和下一重试。
4. 默认建议保持关闭；目标模型中的部署治理主体只授权或撤权，不替租户执行；被授权租户管理员仅可在自身 tenant scope 内使用，未授权者固定拒绝。

### GW-WIRE-016：HUAKAI 有账号身份元数据，但没有 Codex Agent Identity 认证链

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（新认证模式/私钥生命周期） |
| 分类 | W-01 能力缺失、W-02 半接线、W-05 协议差异 |
| 状态 | **Confirmed；禁止把现有 account identity 误当成已实现** |
| 用户影响 | HUAKAI 可以从 ChatGPT/Codex token 提取上游账号 ID 和邮箱用于管理展示，但这只是非授权元数据。系统没有导入、验证、加密保存、请求签名、任务绑定注册/恢复和撤销 Agent Identity 的完整认证链。 |

**源码证据**

1. HUAKAI 的 `accountident.Identity` 明确只用于管理元数据，不得进入访问控制、计费或配额，见 `backend/internal/credentialacq/accountident/identity.go:1-10,34-41`。
2. ChatGPT/Codex identity 只从 token claims/body 提取 account ID 和 email，见 `identity.go:104-129`。
3. `AttachIdentity` 只把非机密 ID/email/source 放进 credential candidate 和 redacted context，见 `backend/internal/credentialacq/accountident_wire.go:18-49`。
4. 现有通用 CLI parser 能解析多行、JSON 和 raw token，但只生成 token credential candidate，不包含运行时私钥、签名或任务绑定语义，见 `backend/internal/credentialacq/cli_import.go:11-50,91-155`。

**参考行为**

Sub2 的 Agent Identity 是实际认证模式：包含运行时标识、Ed25519 私钥、上游账号/用户标识和可选任务绑定；每次请求生成签名声明，任务绑定失效后可以注册或恢复。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:64-132` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:175-313`

**建议**

1. 继续保留现有 `accountident` 为纯管理元数据，禁止扩权复用。
2. 若 Owner 批准，另建“签名身份凭据”模式，私钥必须进入现有 AES-GCM 信封仓库并绑定 tenant/account/vendor/version AAD。
3. 专用导入先 dry-run 校验 Ed25519 材料和身份冲突；不得把私钥、任务绑定秘密或原始载荷写入审计。
4. 请求签名、任务注册、持久化恢复和连接失效必须形成一条可判别测试链。
5. 在真实协议和撤销边界未验证前保持 Experimental/Feature Flag，不宣称 Released。

### GW-WIRE-017：CRS 同步和安全账号迁移包均未进入账号域

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S2`（迁移效率/灾备完整度；秘密导出与远程同步实现属于高风险） |
| 分类 | W-01 能力缺失、W-06 恢复断路、W-10 信息断链 |
| 状态 | **Confirmed in account domain；实现前需要 Owner 确认秘密和网络边界** |
| 用户影响 | 管理员不能从 `claude-relay-service` 预览并同步账号，也不能把账号、调度配置、分组、代理引用和凭据打成可验证迁移包。现有 credential helper 只向既有单账号写凭据，无法承担跨实例迁移和恢复。 |

**HUAKAI 源码证据**

1. 生产账号 helper 路由只有 paste、CLI、CSV、JSON 和 OAuth，没有远程账号源同步或账号包导入导出入口，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:83-98`。
2. 通用导入要求既有 `ProviderAccountID`，所有 candidate 复用同一账号，见 `admin_credential_acquisition_handler.go:229-275`。
3. HUAKAI 的 credential store 已提供应用级加密、tenant/account 绑定和 mutation+audit 同事务，可作为安全迁移落库基础，见 `backend/internal/credentialstore/postgres_store.go:280-353`。

**参考行为**

Sub2 的 CRS 连接器后端登录 `claude-relay-service`，预览已有/新增账号并逐项创建、更新、跳过或失败，同时可选同步代理。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:222-380`

其账号迁移包可以包含原始账号凭据和代理秘密，但不会完整保存影子关系、分组和多类运行状态；文件本身未观察到加密、签名或批次撤销，因此只能视为有损管理员迁移包。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:27-73` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:245-484`

**建议**

1. 把 CRS 建成“兼容账号源连接器”插件，不让专用协议侵入账号核心域。
2. 地址默认 allowlist，解析时和 dial 时双重 SSRF 校验；密码只作为一次输入或外部 secret ref，不持久化明文。
3. 同步和文件导入共用统一 `AccountIntakePlan`：先预览字段级差异，再选择保持本地、采用远端或仅补空值。
4. 迁移包分为默认无秘密的安全包，以及显式启用的加密恢复包；恢复包必须有版本、来源、生成时间、校验摘要、签名和 step-up。
5. 每个批次记录新建/更新对象清单、逐项结果、失败重试和撤销能力；不做无法解释的静默字段丢失。

### GW-WIRE-018：三种身份与单层租户权限尚未落地

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（跨租户授权模型/后续高风险能力接线） |
| 分类 | W-02 半接线、W-05 契约漂移、W-12 身份边界缺失 |
| 状态 | **Confirmed；产品定性已完成，实现涉及 auth/schema** |
| 用户影响 | 当前任意 `users.role=admin` 的有效 session 会成为全平台管理员，可跨租户处理业务，不符合已确定的三身份边界。目标只有系统部署者、系统用户和下级租户：部署者负责平台治理及能力、账号、经营额度分配；租户负责自己的客户用户和已分配资源；用户只负责自己的资源。租户不能继续创建租户，不存在多级代理、租户子树或跨租户委托管理。 |

**源码证据**

1. 主线 session admin 直接映射为无 scope 的 `platform_admin`，见 `backend/internal/adminsessionauth/resolver.go:67-91`。
2. 主线 `platform_admin` 可操作任意租户，`tenant_operator` 只限一个 scope，见 `backend/internal/admin/operator_auth.go:140-157`。
3. `users` 直接绑定单个 tenant，`users.role` 只有 `admin/user`；主线没有独立部署者身份或租户能力 grant，见 `backend/sql/migrations/0007_l0_inbound_auth.up.sql:17-39`、`0076_user_role.up.sql:1-14`。
4. `platform_settings` 只支持全局 scope，不能表达部署者给指定租户开通指定能力，见 `backend/sql/migrations/0077_platform_settings.up.sql:3-28`、`backend/internal/platformsettings/service.go:73-166`。
5. 历史 `feat/reseller-phase1` 建过休眠租户父子 schema；历史 resolver 还会递归授权后代租户，见 `feat/reseller-phase1:backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql:1-75`、`feat/reseller-phase1:backend/internal/admin/operator_auth.go:314-331`。该设计与当前单层租户定性冲突，禁止并入主线。
6. 当前邀请返利是同租户最终用户推广奖励，规格明确排除多级佣金树；订阅也是同租户用户权益，二者都不应扩展成租户层级，见 `docs/specs/community-invitation-referral.md:14-19`、`backend/sql/migrations/0073_subscription.up.sql:15-96`。

权威产品模型见 `docs/process/plans/2026-07-16-three-role-single-level-tenant-model-codex.md`。

**建议**

1. 三种身份固定为系统部署者、系统用户、下级租户；租户创建的客户仍然是用户，不增加第四种身份。
2. 部署者负责平台、租户生命周期以及能力、账号和经营额度分配；租户业务 handler 必须拒绝部署者代操作客户业务。
3. 租户只能管理自身 tenant 的客户、已分配账号和可分发额度，不能创建租户。
4. Cookie、Setup Token、Agent Identity、CRS 等能力默认未授权，由部署者按租户开通，租户侧管理账号代表该租户在自身 tenant 内执行；该账号不构成第四种身份。
5. 禁止合并历史 0185 及递归租户 scope；后续 schema 不增加 `parent_tenant_id`。

### GW-WIRE-019：无 claim 的 countTokens 请求在默认 selector 占槽后无人释放

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（账号并发容量泄漏/协议横向半接线） |
| 分类 | W-05 协议漂移、W-10 信息断链、W-11 顺序错误 |
| 状态 | **Fixed in this branch** |
| 用户影响 | `/v1/messages/count_tokens` 和 Gemini `:countTokens` 是不走 billing claim 的辅助请求。修复前它们仍通过默认 selector 真实递增账号 `in_flight_count` 并写 slot acquisition，但响应合同不携带 release 闭包，handler 也没有 settle/abort。每次只数 token 都会占用一个账号并发槽，直到 90 秒租约回收；频繁调用时可把正常生成请求错误挤成无容量。 |

**源码链**

1. Anthropic 形 count-tokens 明确不 reserve/settle，只直接进入 route、selector、credential 和 dispatch，见 `backend/internal/completionshttp/count_tokens.go:40-80`；共用 `selectAccount` 因 `reserveRes=nil` 把 `ClaimID=0` 传给 selector，见 `backend/internal/completionshttp/attempt.go:25-44`。
2. Gemini count-tokens 同样直接选号、取凭据和出站，`SelectionRequest` 未设置 claim，见 `backend/internal/geminihttp/generate_content.go:171-193,262-277`。
3. 修复前默认 selector 在 gate 通过后无条件调用 `SlotManager.Acquire`；真实 PostgreSQL slot manager 会递增 `provider_accounts.in_flight_count`、插入 acquisition，并把租约设为 90 秒，见 `backend/internal/pool/dispatcher/slot_manager.go:68-115`。
4. `SelectionResult` 只有 account、token、wait plan 和路由原因，没有 release 函数，见 `backend/internal/pool/router/types.go:123-131`；count-tokens 两个 handler 也没有任何释放调用。
5. 同仓 PASR 已明确识别这一不变量：`ClaimID=0` 时必须在 acquire 前短路，否则 caller 无法释放，见 `backend/internal/pool/router/pasr.go:440-445,525-548`。这提供了可验证的 HUAKAI 内部 Safe Equivalent，不需要另造资源生命周期。

**修复**

- 默认 selector 在完成候选、协议和 gate 判定后，若 `ClaimID=0`，返回临时 acquisition token，但不调用真实 `SlotManager.Acquire`，见 `backend/internal/pool/router/default_selector.go:206-227`。
- money path 的非零 claim 行为保持不变：仍然先占槽，再把 acquisition 写回 claim，失败时脱钩释放。
- 判别测试给默认 selector 注入计数 slot manager，确认仍选择正确 protocol family 账号，同时 `Acquire calls=0`，见 `backend/internal/pool/router/default_selector_protocol_family_test.go:10-52`。删除短路后该测试精确变红。

**辐射检查**

- 当前直接受益入口：Anthropic 形 count-tokens、Gemini count-tokens，以及所有明确以 `ClaimID=0` 调默认 selector 的只读/辅助路径。
- PASR 原有 ClaimID=0 语义不变。
- 正常 chat、completions、embeddings、rerank、images、audio money path 均在 reserve 后传非零 claim，不受本修复影响。
- 后续仍需单独审计 count-tokens 的 401/429 健康反写、运行时凭据兼容门和跨账号 fallback；本修复只闭环资源泄漏，不把整个辅助协议链宣称为完整。

### GW-WIRE-020：音频 speech 已完整交付后的结算失败只有日志，没有持久恢复

| 项目 | 内容 |
| --- | --- |
| 严重度 | `S1`（已交付未入账/协议横向恢复链缺口） |
| 分类 | W-05 协议漂移、W-10 信息断链、W-12 恢复缺失 |
| 状态 | **Fixed in this branch** |
| 用户影响 | `/v1/audio/speech` 会先把二进制音频完整写给客户端，再执行结算。修复前若此时数据库、锁或结算服务瞬时失败，客户端已经得到 200，claim 仍未完成，系统只写一条错误日志；日志丢失、未被及时处理或进程退出后，没有机器可重放的完整 settle intent，形成已交付但可能长期未入账的缺口。 |

**源码链**

1. speech 分支先写响应头和音频体，完整交付后才调用 settle；客户端写失败走 Abort，不计费，见 `backend/internal/audiohttp/attempt.go:130-164`。这个顺序本身正确，不能为了结算方便改回交付前扣费。
2. 修复前 settle 失败只调用日志方法；该日志只保留 tenant、claim、endpoint 和脱敏错误类，不能重构最终账号、金额、模型、路由快照和 acquisition token，见修复前 `backend/internal/audiohttp/billing.go` 的 `logSettleAfterDeliveryFailure` 路径。
3. 同仓图片和 completions 已经证明可用的 Safe Equivalent：响应交付后把完整 `billing.SettleRequest` 转为恢复载荷并进入统一 DLQ，见 `backend/internal/imageshttp/billing.go:171-187`、`backend/internal/completionshttp/billing.go:151-176`。
4. 统一恢复队列使用 tenant、claim、request 组成幂等键；worker 只重调公开 `Settler.Settle`，若 claim 已提交则通过 claim、usage、billing event 三证确认幂等成功，见 `backend/internal/settlementrecovery/enqueue.go:40-91`、`backend/internal/settlementrecovery/handler.go:41-90`。

**修复**

- `audiohttp.Deps` 增加统一 settlement recovery enqueuer，composition root 注入现有 `dlqService`，见 `backend/internal/audiohttp/handler.go:48-64`、`backend/cmd/gateway/routes.go:882-900`。
- speech 完整交付后构造一次最终 `SettleRequest`；直接结算失败时，以 `audio_delivered` 来源保存完整恢复载荷，并继续写脱敏运营事件，见 `backend/internal/audiohttp/attempt.go:157-164`、`backend/internal/audiohttp/billing.go:159-200`。
- recovery payload 严格允许新增音频来源，其他未知来源仍 fail-closed，见 `backend/internal/settlementrecovery/payload.go:27-43,159-172`。
- 判别测试验证：客户端仍得到完整 200、不会错误 Abort、只尝试一次直接 settle、只入队一次恢复事件，并且重放载荷中的 claim、最终账号、模型和金额与原请求完全一致；若 settle 与恢复入队双故障，还必须发出不含原始秘密的 P0 事件，见 `backend/internal/audiohttp/handler_test.go:308-378`。composition root 和 payload source 另有独立接线测试，见 `backend/cmd/gateway/wiring_test.go:337-354`、`backend/internal/settlementrecovery/payload_test.go:243-255`。

**辐射检查**

- transcription/translation 当前在写业务响应前完成 settle；settle 失败还能安全返回 500，没有“已交付后不可反悔”的同类缺口，本批不强行改成异步恢复。
- 图片、chat、completions 的原恢复来源和重放合同不变；音频只是新增合法来源，复用同一幂等键、HIGH lane、重试和三证 proof。
- 本批没有改变费率、预测金额、实际金额、余额、quota、claim 状态机或数据库 schema。
- 后续仍需横向审计 audio 的 401/429 健康反写、等待队列和跨账号 retry；不能因本次补齐 recovery 就宣称整个音频链与 chat 等价。

## 成熟项目颗粒度基线

隔离 clean-room specifier 对 Sub2、New API、CLIProxyAPI 当前默认分支源码拆出 46 个微功能节点。本轮以后不再用“大功能存在”作完成结论，重点核以下六类协作：

| 颗粒度 | 已观察的成熟链行为 | HUAKAI 审计动作 |
| --- | --- | --- |
| 导入 | 批量输入会展开多种格式、逐项返回、区分批内重复和存量冲突，并避免简化凭据覆盖续期材料。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:159`、`:237`、`:259` | 每种账号入口分别核解析、严格校验、身份、冲突、合并、逐项错误和缓存生效 |
| 状态 | 主状态、可调度、全账号限流、逐模型限流、过载、过期和错误说明是不同轴。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/service/account_service.go:80`、`:95` | 不再用一个 `active` 或 health 分数代替所有运行状态 |
| 选择 | 用户槽、候选硬门、账号槽、等待上限、粘性写入和失败逃逸有明确顺序。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/openai_gateway_handler.go:318`、`backend/internal/handler/gateway_handler.go:377`、`:414` | 逐协议检查是否绕过 queue、slot、粘性复核或 claim 生命周期 |
| 重试 | 每次尝试从原始请求体重建；区分同号 retry、跨号 fallback；首块交付后禁止透明换号。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/openai_gateway_handler.go:217`、`:480`、`:494` | 横向核对 chat、completions、embeddings、rerank、images、audio、Gemini 和 media task |
| 状态回写 | 认证、限流、过载等错误会写到不同账号状态，并影响下一次选号；渠道停用也是受分类和开关控制的后果。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/service/account_service.go:95`、`new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/relay.go:357` | 检查“错误枚举存在”后是否真有写入、缓存传播、恢复和运维动作 |
| 最终归因 | retry 后使用最终账号、最终模型、最终渠道和真实金额落账；重复提交必须可判别且幂等。`sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/gateway_handler.go:527`、`backend/internal/service/account_usage_service.go:28` | 每个交付路径核 claim、slot、usage、billing、audit、DLQ 是否指向同一最终尝试 |

## 非问题与反证

1. `cmd/gateway/routes_*.go` 的 mount helper 本批全部存在生产调用，不支持“很多页面后端路由没挂”的笼统结论。
2. `provider/grok` 不可达不等于 Grok 全部不可用；官方 xAI API key 与 xAI OAuth 路径均已注册。
3. 某些包级 `MountRoutes` wrapper 没被调用，不代表 handler 没挂；生产 router 可能使用细粒度 mount。
4. `deps.mediaTaskWorker` 等字段若未被 handler 读取，但实例被 `gatewayRuntime` 保留用于 lifecycle，不属于“构造后丢弃”。
5. worker 同时由 `shutdownGateway` 和 `runtime.close` 调 Stop，在已核实现中 Stop/cancel 具有幂等语义；本批不把重复调用本身报为 bug。
6. HUAKAI 不使用 Sub2 的 Redis 调度快照不自动构成功能缺失；当前 PostgreSQL 直选提供更直接的权威读取，是否需要快照必须由热路径压测决定。
7. PASR 默认关闭是显式发布策略，不是代码不存在；当前问题是高级能力尚未激活，不能把它当作线上已具备的默认行为。

## 本批修改

| 文件 | 修改 |
| --- | --- |
| `backend/cmd/gateway/lifecycle.go` | runtime 保留统一 worker cancel 和 waiter；HTTP 排空后取消，关库前等待 |
| `backend/cmd/gateway/wiring.go` | 所有生产 worker 改用独立 `workerCtx`；登记四类 context worker 的 waiter |
| `backend/cmd/gateway/lifecycle_test.go` | 新增真实连接的停机顺序与 worker 退出等待判别测试 |
| `backend/cmd/gateway/quota_probe_wiring_test.go` | 接线断言升级为必须使用 `workerCtx`、登记四个 waiter，并拒绝退回进程信号 `ctx` |
| `backend/internal/{proxyhealth,tlsfphealth,windowcost,quotaprobe}/worker.go` | 增加可等待的 goroutine 退出合同 |
| 对应四个 `worker_test.go` | 验证取消前阻塞、取消后退出 |
| `backend/internal/pool/router/default_selector.go` | 无 claim 的辅助请求完成 gate 后不再占用无法释放的真实并发槽 |
| `backend/internal/pool/router/default_selector_protocol_family_test.go` | 判别 ClaimID=0 时协议门仍生效且 slot acquire 精确为零 |
| `backend/internal/audiohttp/{handler.go,attempt.go,billing.go}` | speech 交付后结算失败进入统一持久恢复链 |
| `backend/internal/audiohttp/handler_test.go` | 判别完整交付、禁止 abort、单次入队和重放字段一致性 |
| `backend/internal/settlementrecovery/{payload.go,payload_test.go}` | 新增并严格校验 `audio_delivered` 恢复来源 |
| `backend/cmd/gateway/{routes.go,wiring_test.go}` | 生产注入并判别音频复用统一 settlement recovery queue |
| 本报告 | 记录 route、worker、registry、setting、账号链矩阵、成熟项目微功能颗粒度和二十个确认发现 |
| 三身份与单层租户规划 | 固定部署者、用户、租户三种身份，禁止多级租户，并记录能力、账号和经营额度的分配边界 |
| 参考报告 | 保存隔离 specifier 的运行接线、账号链三镜证据、Sub2 账号系统完整生产逻辑、默认分支补充证据和账号导入四项能力证据 |

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
go test ./internal/adminhttp ./internal/gatewayhttp ./internal/credentialworker ./internal/channelhealth ./internal/rate ./internal/pool/... -count=1
go test ./cmd/gateway -run 'Test.*(Selector|ProviderAccount|Credential|QuotaProbe|Wiring)' -count=1
go test ./internal/pool/router -count=1
go test ./internal/completionshttp ./internal/geminihttp -count=1
go test ./internal/audiohttp ./internal/settlementrecovery ./cmd/gateway -run 'TestAudioSpeech_Settle(ErrorAfterDeliveryKeeps200AndEnqueuesRecovery|AndRecoveryDoubleFailureEmitsP0WithoutSecret)|TestValidate_AcceptsAudioDeliveredSource|TestWiring_PricingRatioResolverSharedByChatEmbeddingsRerankImagesAndAudioDeps' -count=1
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

worker 修复批第一轮独立 review 已完成。当前 Codex CLI 不接受旧命令中 `review` 子命令后的 `--sandbox`，因此使用等价只读形式 `codex exec --sandbox read-only --ephemeral review --uncommitted`。第一轮报告一个 S1：context worker 取消后没有等待，可能与关库竞态；本分支已按上述方式修复。第二轮使用相同只读形式复核，未发现该批改动引入的明确功能缺陷，未留下 S0/S1。Batch 2B 文档第一轮 review 发现 CLIProxyAPI、New API 的补充 SHA 不在本地 `origin/main` 祖先链，归一化为 S1；随后通过 GitHub 远端 `main` 核实可达 SHA，新增独立默认分支 specifier artifact 并替换全部依赖。第二轮 review 确认新增结论有源码证据、元数据与正文一致，未留下 S0/S1。

GW-WIRE-019/020 提交前 review 也已完成。当前 CLI 的 `codex exec review` 已不再接受旧 `--sandbox/--full-auto` 参数，第一轮改用 `codex exec review --uncommitted --ephemeral`，静态审查无 finding；其自带测试因默认临时目录磁盘配额耗尽未完成，但本会话使用根磁盘目录的同范围测试已经全绿。money-path 第二轮使用物理只读 `codex exec -s read-only --ephemeral`，专项核对 ClaimID=0 资源生命周期、音频完整 settle payload、tenant/claim 幂等键、双故障 P0 脱敏和生产注入，最终结论为 `APPROVE`，无 S0/S1/S2/S3；只读沙箱不能创建 `GOTMPDIR`，故该轮不重复执行测试。

## Owner 决策点

1. 是否批准建设真正主动探测 Safe Equivalent：默认关闭、成本预算、账号级开关、数据库租约、多副本去重、真实 health write。
2. 是否批准把普通请求观测时间从 `last_probe_at` 迁到独立 `last_request_observed_at` 合同；该项涉及 schema/API。
3. Grok 网页 session 能力是保留为插件、重做 clean-room Safe Equivalent，还是继续 Mandatory Roadmap；当前禁止直接注册。
4. 是否批准把运行时 `ValidateAccountCompatibility` 泛化到全部有 serving contract 的 family；该项会改变异常/遗留账号的凭据放行规则。
5. 是否批准以 `antigravity/oauth` 为唯一 canonical 身份，并另开数据迁移决策包处置 `gemini/antigravity` legacy 行。
6. 是否批准建设 Gemini、Antigravity、Kimi 的账号级只读观测合同；第一阶段只展示，不进入强配额、资金或 selector。
7. 是否批准把 provider health、channel health、auth cooldown、credential state 和 model cooldown 收敛为一份“账号调度真相合同”；第一阶段先做聚合只读视图，不改 selector。
8. 是否批准建设真实账号测试 Safe Equivalent：默认关闭、单账号显式触发、成本上限、不计客户账、真实 protocol/credential/proxy 出站。
9. 账号批量导入的唯一身份应由哪些字段组成；该决策将影响去重、更新、审计和未来数据迁移。
10. bulk-by-tag 修复采用逐项事务并返回完整结果，还是整批单事务；推荐逐项原子，避免大批量锁住账号表。
11. Claude Cookie 自动登录已确定采用“部署级默认关闭 + 部署治理主体只授权/撤权 + 已授权租户管理员仅操作自身 tenant + 管理员 step-up + Cookie 仅单次使用”；尚需决定授权绑定用户身份还是管理令牌。
12. Setup Token 已确定沿用同一部署管理员授权模型；未授权租户管理员固定拒绝，不能把半截能力直接变成默认生产鉴权入口。
13. 是否建设 Codex Agent Identity Experimental 模式；该项包含私钥、签名、任务注册/恢复和真实上游协议验证。
14. CRS 是否只作为可插拔兼容连接器，并强制地址 allowlist、双时刻 SSRF 检查和一次性管理密码。
15. 账号迁移包的秘密策略：推荐默认只导出结构；可恢复秘密仅进入 step-up 后生成的加密、签名、短时恢复包。
16. 是否批准把部署者从当前 `platform_admin=可代操作任意租户` 语义中拆出，成为只做平台治理和租户能力授权的独立主体。
17. 租户经营额度采用预充值额度还是平台授信；该选择决定回收、退款、超额、坏账和并发结算边界，不能沿用邀请返利代替。

## 下一批

Batch 2C 已完成 Sub2 整套账号系统、HUAKAI 账号链和四项账号导入/同步能力的第一轮功能总账。`GW-WIRE-013` 的账号接入身份、冲突和稳定凭据指纹已在独立 Draft PR #258 闭环；未经 Owner 同意未合并。

`GW-WIRE-014` 至 `GW-WIRE-017` 的生产写入和真实网络能力等待 Owner 对第 11-15 项作出选择后，分别作为独立 PR 实现，避免把多个高敏感凭据入口一次性混入同一提交。

随后继续横向追踪 chat、completions、embeddings、rerank、images、audio、Responses、Gemini 和 media task，逐协议核对：

`身份 → 规范模型 → 选号 → gate → 凭据 → 出站 → retry/fallback → health → claim/billing → audit → DLQ/recovery`

重点查 chat 已接而其他协议绕过统一健康、结算、错误分类或恢复的 W-05/W-10/W-11 问题。

## 真实性摘要

本报告的二十个问题均来自实际生产源码链或实际打开的历史分支源码。八项推断是：主动探测若直接启用会产生真实上游费用/多副本重复；停机窗口影响因各 worker 持久化语义不同而表现不同；非 Claude 凭据错配需要遗留/旁路条件才会触发；Kimi OAuth 缺设备身份是否影响真实上游仍需 live 验证；多套账号状态会增加运维误判概率；PostgreSQL 直选是否需要演进为路由快照必须由压测而不是对标形式决定；账号恢复包若包含秘密而无文件级加密会扩大浏览器下载和离线存储泄露面；继续沿用两档 admin 会让新增租户授权再次混入跨租户管理员语义。没有断言已发生永久资金损失、凭据泄露，也没有把参考项目 Open Question 写成既定行为。当前十七个 open question 集中在账号链、高敏感账号接入、三种身份落地、租户经营额度边界，以及非 chat 协议的健康、retry 与 recovery 对齐。
