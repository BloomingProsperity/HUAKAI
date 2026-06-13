# Hermes 运维助手全面对齐 — 架构方案 (Claude draft)

> 2026-06-13 · Owner 拍板「全面对齐」(Option 1) · landing `fix/h-fixes@aa4a3b7d`
> 证据来源:8 路并行测绘(主 workflow w6h6rj3p5 6 图 + 日志图 + 邮件/调度图 + sub2api/new-api 设置 clean-room 研究),全部 file:line。
> Codex 并行起草按 2026-06-11 Owner 裁决豁免(codex 未重登);架构走 Claude 3 镜头对抗评审后再落地。

## 0. 目标(Owner 原话归纳)

Hermes = **admin/operator 运维助手**,一切为了**更好的运维、找根因、修复**。六条要求:
1. admin/operator 鉴权门(现状任意用户可访问)。
2. skill/接口**对接每一个模块**(快速定位+检查)。
3. 内置各模块**逻辑与运行方式**知识。
4. 对接**日志系统**(日志分析)。
5. **每日定时巡检 → 报告总结 → 邮件发管理员**。
6. 学 sub2api **设置模块**,东西尽量收进统一「设置」。

参考对照(#15):sub2api/new-api/CLIProxyAPI **均无运维 AI 助手等价物**(已核实)→ Hermes ops 部分为 HUAKAI 原创,架构自研;仅「设置模块」有 ref 先例(下 §S1)。

## 1. 现状(3 层漂移,均 file:line)

- **鉴权**:`routes.go:318-326` Hermes 用 `APIKeyMiddleware(inboundAuth)`,与 `/v1/chat/completions` 同,任意用户 API key 即可;Identity 无 role 字段。
- **能力**:runner 仅透传 LLM 聊天(`hermes-runner/main.py:57-61`、`hermeschat/bridge.go:116-159`);**无 tool 执行层、无 hermes_tool_calls 表**(`bridge_sse.go:39` 默认丢弃未知事件);8 ops 工具全 deferred(plan:171)。
- **文档/隐私**:plan:7 "normal AI chat surface";F-PRIV-001 当用户聊天历史做隐私例外 → 改 admin 后隐私模型须重写。
- **附带订正**:保留期默认 `message_retention.go:20 = 0`(永久,opt-in);F-PRIV-001 写"90 天"是错的。Hermes 在 feature-tree.json 有 F-OBS-004 但 live-status.json 无 module(台账缺口)。

## 2. 已具备的地基(可复用,不重造)

| 能力 | 现成 | 证据 |
|---|---|---|
| admin 鉴权 | AdminResolver(`hk_admin_` + admin_tokens)、RolePlatformAdmin/TenantOperator、`adminGate()`、`CanIssueForTenant()` | `internal/admin/operator_auth.go:38-131`、`cmd/gateway/middleware.go:104-141` |
| admin 审计 | `admin_audit_events`(action/target_type CHECK 白名单,DROP+ADD 迁移范式 0010/0077/0139) | `sql/queries/admin_audit.sql`、`sql/migrations/0139_*` |
| 只读诊断面 | DryRunProviderAccountCredential、Router.Plan、Selector.Select、RoutingReasonBuilder、provider account health、channel health、rate.HandleUpstreamError、DLQ List、observability 审计查询 | `credentialworker/provider_account_dry_run.go:28`、`router/router.go:26`、`adminhttp/provider_account_health_handler.go:82`、`gatewayhttp/admin_dlq_handler.go:32-64`、`admin_observability_handler.go:66-76` |
| 变更操作(需 RBAC+审计) | UpdateProviderAccountEnabled(pause/resume)、AdminCredentialStore.Rotate(renew)、DLQ.Replay、channelhealth ManualPause/Resume | `admin_pool_accounts_handler.go:550-590`、`admin_credentials_handler.go:71-77`、`dlq/service.go:81-100` |
| 日志安全读取面 | 3-channel logger(System/UserAction/Security)+ Redactor 117-字段 allowlist + audit_ledger + `/admin/observability/*`;raw body/prompt/completion 从不入日志(中间件即弃) | `internal/privacy/logger.go`、`default_redactor.go:237-256`、`privacy/middleware.go:32-63` |
| 邮件 | SMTP transport 已建(`SendTenantMessage` + DLQ 重试)、notify 多通道(email/webhook/bark/gotify) | `internal/email/sender_factory.go:137-145`、`notify/notifier.go:159-202` |
| 定时 worker | 成熟范式 ExpiryWorker(ticker+graceful stop+metrics+wiring 注册) | `subscription/worker.go:1-140`、`wiring.go:1198-1215`、`lifecycle.go:146-216` |
| 设置 | platformsettings(scoped KV + SettingKey 枚举 + 默认值 + 30s cache + lastKnown fallback,~50 键) | `internal/platformsettings/types.go`、`service.go` |
| 模块台账 | feature-tree.json(65 module/16 段/pkgs)、live-status.json、docs/specs 28 份、6 个 registry.go 范式 | `docs/process/feature-tree/*`、`internal/provider/registry.go` |

## 3. 待建(明确 gap)

- 鉴权:hermes admin 中间件(替换 inboundAuth)。
- tool 执行:`hermes_tool_calls` 表 + gateway 中介执行端点 + RBAC 映射 + 确认/dry-run + 原子审计。
- 模块知识:`ModuleDescriptor` 接口 + 活注册表 + 静态 catalog 生成器 + `/admin/v1/modules`。
- 日志技能:`log_analyze` 工具(查 observability/audit_ledger,聚合 error_class 趋势)。
- 巡检:`hermesadmin` 包 inspection worker + 跨模块报告聚合器 + admin 收件人配置(`admin_notification_email` 现无)。
- 设置:platformsettings 分层子包 + 类型化访问器 + 类别级缓存 + 统一设置面 + 缺失类别补齐。

## 4. 波次方案(依赖序 + 风险闸)

### H0 — 真相订正 + 地基(低风险,先做)
- 修 F-PRIV-001 保留期默认(90→0/opt-in);重写 Hermes 定位文档(admin 运维,非 end-user 聊天)+ 隐私模型(admin 诊断会话)。
- Hermes 进 live-status.json(归 `admin-ops`/`ops-suite` 域)。
- 加 `admin_notification_email` platform_setting。
- **门**:文档+台账,可逆,直接做。

### H1 — admin 鉴权门(安全高风险,Owner 闸)
- 新 `hermeshttp` admin 中间件复用 AdminResolver,替换 `routes.go:319` 的 inboundAuth;每 handler `CanIssueForTenant` 域隔离。
- **feature-flag `HUAKAI_HERMES_ADMIN_ONLY`** 安全切换(默认开=admin-only;可回退)。
- hermes 审计 actor 适配 admin 身份(考虑加 `admin_actor_id`)。
- **决策点**:硬切换 vs feature-flag 渐进(建议 flag);现有 end-user Hermes 集成会断(确认无生产用户依赖)。

### H2 — 模块知识脊柱(中风险)
- `ModuleDescriptor` 接口(id/category/capabilities/health_probe)+ wiring 自注册活注册表(Option B)。
- 静态 catalog 生成器:feature-tree.json + specs → `module-catalog.json`(Option A overlay)+ CI 防漂移门。
- `/admin/v1/modules?category=` 端点;catalog+活状态喂进 Hermes system context。
- 起步先 billing/routing/credentials 三高价值域,渐进铺满。

### H3 — 只读 ops 工具(中风险,"快速定位+检查"核心)
- tool 执行脊柱:`hermes_tool_calls` 表(迁移)+ gateway `/v1/hermes/tool-execute` 中介端点 + RBAC 查表 + 原子审计。
- 先落**只读**工具(最便宜最安全,全包真函数):`request_diagnose`、`credential_diagnose`、`account_health_diagnose`、`dlq_inspect`、`audit_lookup`、`log_analyze`(日志集成)。

### H4 — 变更 ops 工具(HIGH 风险,逐个 Owner 闸)
- `dlq_replay`、`account_pause`/`resume`、`renew_trigger`。
- 5 层模型:gateway RBAC 前置 → **dry-run-first + 二次确认(correlation_id 短 TTL)** → 原子事务(tool_calls + admin_audit_events 同 TX)→ advisory lock 互斥 → admin_audit_events action 白名单扩展(`hermes.tool.*` 前缀避免命名冲突,DROP+ADD 范式)。
- 幂等(DLQ IdempotencyKey)、跨租户隔离(JWT sub claim 校验)、轮换原子失效旧凭据。

### H5 — 每日巡检 → 报告 → 邮件(中风险)
- `internal/hermesadmin` inspection worker(ExpiryWorker 范式,interval=24h)。
- 跨模块报告聚合器:调 H3 只读探针 + 健康快照 + error_class 趋势。
- 经 `SendTenantMessage` 发 `admin_notification_email`;报告守同一隐私边界(只系统诊断)。

### S1 — 设置模块统一(独立轨,中风险,可与 H 轨并行)
- 扩 platformsettings:分层子包(new-api 范式:auth_sources/oauth_providers/payment/notification/integration/gateway/ops)+ 类型化访问器 + 类别级缓存(sub2api singleflight 范式)+ 写入校验钩子。
- 缺失高优类别:per-auth-source 用户默认、OAuth provider 配置、SMTP 设置面+测试、通知/告警、payment 流水线状态。
- 统一 admin 设置面;现 ~50 键迁入子包(无 schema 变更,KV 表不动)。
- clean-room:sub2api(LGPL,paraphrase)+ new-api(AGPL,paraphrase),引用只进 commit/docs。

## 5. Blast radius / 风险

- **安全**:H1 改鉴权门 = auth-core 高危;H4 让 LLM 触发 pause/replay/rotate = 最高危,必须 dry-run+确认+RBAC+审计+互斥。
- **隐私**:日志/报告严守 Redactor 边界;admin 诊断会话仍走 0091 静态加密;F-PRIV-001 隐私例外语义要随定位重写。
- **schema**:hermes_tool_calls + admin_audit action 扩展 + 可能 admin_actor_id,均加性+down 可回滚,过真双门 + 判别 integration_pg 守卫。
- **codebudget**:新功能进新子包(hermesadmin/hermesmcp/平 settings 子包),不堆 god-package。
- **运维并发**:Hermes 多副本下 pause/replay 用 advisory lock + 幂等防双发。

## 6. 决策点(待 Owner)

1. **起步序**:H0→H1 先(定位+安全先正)还是 S1 设置并行先起?
2. **H1 切换**:feature-flag 渐进(建议)还是硬切?是否确认无生产 end-user Hermes 依赖?
3. **H4 变更工具**:是否逐个工具单独 sign-off(建议),还是批准整组按 5 层模型落?
4. **S1 范围**:本轮只补 SMTP/通知/admin 邮箱(支撑 H5)还是全量设置统一(per-auth-source/OAuth/payment 都上)?

## 7. 验证纪律(每波)

commit-first → 判别测试(变异验证)→ 真双门(fresh DB→migrate→unit -race→integration_pg -p1)→ quality-delta=0 → 独立 3 镜头对抗评审(S0/S1 阻断)→ FF main + 推送 + 刷功能树。H1/H4 安全波加专项越权/泄密/跨租户对抗测试。
