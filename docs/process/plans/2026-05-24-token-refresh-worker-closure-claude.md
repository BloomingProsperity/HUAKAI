# Token Refresh Worker 闭环 + 账号采集 Handler 实落 — Claude Lane Plan

- Lane: Claude PM-orchestrator (plan-only)
- UTC: 2026-05-24T07:00Z
- 互补 lane: docs/process/plans/2026-05-24-token-refresh-worker-closure-codex.md (codex,后台跑中)
- CLAUDE.md 条款: #10 (parallel) / #11 (clean-room) / #12 (源码必读) / #13 (包结构) / #14 (测试质量) / #15 (Owner 决策对照)
- 范围声明:transport 技术(uTLS/rquest/curl_cffi/BoringSSL)选型不在本 plan 决策,引用 [[project_l1_tls_boringssl]] 现状,本 plan 只决定如何接入,不决定选哪个

> 【2026-06-02 已更新】本文中 G-3 “生产数据面没接 / L2 H2 0 生产能力 /
> Rust core_gateway 未接通”是 2026-05-24 历史。当前 transport mimicry 已按 provider
> mode 在 `gatewayhttp` 选择，`transport.Factory` 支持 sidecar branch，`HUAKAI_TRANSPORT_SIDECAR_SOCKET`
> 可把 Go 出站接到 Rust/BoringSSL sidecar；`tls-sidecar` 已有 H2 SETTINGS 逐字段控制。
> 但应用层 `ApplyMimicryPlan` 本轮未观察到非测试 caller，body-level cloaking 不标为已闭；
> 以下为历史计划。

## §1 目标与范围

Owner 在战略快照里点了 4 个生产级缺口,本 plan 围绕这 4 块闭环:

| 缺口 | 当前位置 | Claude 闭环切片 |
|---|---|---|
| **G-1 真实登录 bootstrap** | `backend/internal/credentialacq/oauth.go` 有 `StartOAuthFlow`/`CompleteOAuthCallback` 框架,但没有 vendor-specific handler | L-A: vendor bootstrap handler |
| **G-2 动态 endpoint 捕获** | cursor/copilot session adapter 用 hardcode `defaultXxxEndpoint`,vendor 端 endpoint 变更不感知 | L-B: endpoint catalog + 周期采集 |
| **G-3 反检测 mimicry** | `backend/internal/gateway/mimicry*` 有 transport mimicry 骨架,但生产数据面没接;Rust core_gateway 未接通 [[project_two_data_planes]] | L-C: mimicry 接入(出站 transport gating) |
| **G-4 长周期账号健康** | `credentialworker.Scheduler` 只 refresh,不管 revoke / quota / 风控封 | L-D: 健康探测 + soft-disable |

**ceremony**:高难度 (信任链 + 长效 token + 风控对抗 + 多事务边界 + 涉及钱) — 全套 plan parallel + Owner D 决策 + 双 verify。

**不在范围 (refer-out)**:
- transport 技术选型 (BoringSSL vs uTLS vs rquest) → [[project_l1_tls_boringssl]]
- Rust core_gateway 接通生产 → [[project_two_data_planes]]
- 具体 vendor placeholder 实施细节 → docs/process/plans/2026-05-24-placeholder-session-adapters-{claude,codex,synthesis}.md
- Anthropic OAuth 反转细节 → docs/process/plans/2026-05-24-anthropic-oauth-inversion-{claude,codex,synthesis}.md

## §2 现状与 4 个缺口的具体锚点

### G-1 真实登录 bootstrap 现状

[`backend/internal/credentialacq/oauth.go`](backend/internal/credentialacq/oauth.go) (上轮已 read):
- line 44 `StartOAuthFlow(ctx, tenantID, vendor, callbackURL, opts)` — 通用 PKCE 起手,生成 state + verifier,落 `oauth_acquisition_session` 表
- line 90 `CompleteOAuthCallback(ctx, state, code, exchange)` — 通用回调,把 code 交给 `exchange` 回调拿 token

**缺口**:`exchange` 回调对每 vendor 不同,但仓库里 **没有任何 vendor-specific exchanger 实现**。
- Anthropic OAuth (本日另一 plan 在处理) → 那 plan 加 `AnthropicExchanger`
- Google OAuth (gemini/antigravity) → 缺
- GitHub device-code (copilot) → device-code 跟 PKCE 不是一回事,`StartOAuthFlow` 框架本身不适用
- AWS SSO (kiro) → 完全异质,SSO 走 OIDC + IAM identity center,需独立 flow

**结论**:G-1 不只是补 exchanger,**device-code / SSO 不走 OAuth PKCE,需要 credentialacq 加新 entry point**。

### G-2 动态 endpoint 捕获现状

各 vendor session adapter (`backend/internal/provider/*/`):
- `defaultCursorEndpoint = "https://api2.cursor.sh/aiserver.v1.AiService/StreamChat"` — hardcode
- `defaultCopilotEndpoint = "https://api.githubcopilot.com/chat/completions"` — hardcode,但 copilot device-code OAuth 返回 `endpoint.api` 字段会指向真 endpoint(可能 region-specific)
- `defaultGeminiAdvancedEndpoint` — 占位,真 endpoint 含动态 `bl=` 参数

**缺口**:
- 没有"endpoint catalog"集中表 — 每 vendor adapter 自己 hardcode,变更 = 改代码 + 重新发布
- copilot `endpoint.api` 字段没读 — token 返的真 endpoint 没用
- gemini `bl=` 周期采集没做

**结论**:G-2 = 加一个 endpoint catalog(每 vendor 一行,定时 refresh)+ adapter 从 catalog 读 endpoint。

### G-3 反检测 mimicry 现状

[`backend/internal/gateway/`](backend/internal/gateway/) (god-package,冻结) — 有 `mimicry*.go` 文件骨架,但 Owner [[project_rust_gateway_review_2026_05_20]] 给的判断:**L2 HTTP/2 fingerprint 0 生产能力**;**[[project_huakai_codex_mimicry_verified]]** 只 sandbox 抓包验证了 codex CLI 一条线。

**缺口**:
- 出站 mimicry 没按 vendor 切片配置 (cursor mimicry 模板 / copilot mimicry 模板 / gemini mimicry 模板独立)
- 生产数据面 (Go gatewayhttp) 没接 mimicry,只接了 standard `net/http.Transport`
- Rust core_gateway 有 mimicry 但未接通生产 [[project_two_data_planes]]

**结论**:G-3 = (a) per-vendor mimicry profile 表;(b) 出站 transport 按 vendor 路由到对应 mimicry profile;(c) Rust 接通是 [[project_two_data_planes]] 战略岔路,不在本 plan,只留接入点。

### G-4 长周期账号健康现状

[`backend/internal/credentialworker/scheduler.go`](backend/internal/credentialworker/scheduler.go) (上轮 read):
- 有 `ListAccountsForRefresh` 查 expire 临近账号
- 有 refresh `attempt` + `maxAttempts` backoff
- 有 audit 同事务记录 outcome

**缺口**:
- 没有"账号被 vendor 远端 revoke / quota 超 / 风控封"的检测
  - revoke:refresh 收到 `invalid_grant` 或 `revoked_token`
  - quota:vendor 返 `rate_limit_exceeded` 或 `quota_exhausted`
  - 风控:vendor 返 `account_disabled` / `risk_control_triggered` / IP/JA3 异常 403
- 没有 soft-disable 机制 — 账号坏了仍在被 dispatcher 选中,继续打错请求
- 没有 cooldown 自愈 — soft-disable 后没有逻辑自动 retry

**结论**:G-4 = (a) refresh outcome 扩展 (新增 outcome enum:revoked / quota / risk / disabled);(b) `provider_account` 表加 `health_state` 列;(c) cooldown scheduler tick (e.g. 1h 后自动 healing retry)。

## §3 参考项目对照

### G-1 bootstrap

| 子缺口 | 主 ref (MIT/Apache) | 次 ref |
|---|---|---|
| 通用 PKCE OAuth | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go`(MIT,完整 PKCE flow) | `~/refs/sub2api/backend/internal/service/oauth_service.go`(LGPL paraphrase) |
| device-code (GitHub) | `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py`(Apache-2.0) | RFC 8628 (公开协议,非 ref-project) |
| AWS SSO (kiro) | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/vertex/vertex_credentials.go`(GCP service account 模式,SSO 类比) | AWS SDK Go v2 (Apache-2.0,可 vendor 但慎,体积大) |
| Google OAuth (antigravity/gemini) | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/{antigravity,gemini}/`(MIT) | (无次 ref) |

### G-2 endpoint catalog

| 子缺口 | 主 ref | 次 ref |
|---|---|---|
| endpoint 元数据存储 | `~/refs/portkey-gateway`(MIT,multi-vendor gateway 有 endpoint table) | `~/refs/litellm/litellm/utils.py`(Apache-2.0,model mapping → endpoint) |
| copilot `endpoint.api` 字段动态采用 | `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py`(Apache-2.0,token 返字段直接用) | (无次 ref) |
| gemini `bl=` 周期采集 | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go`(MIT,可能含 bl 解析) | (无次 ref,可能要 Owner 抓 HTML) |

### G-3 mimicry 接入

| 子缺口 | 主 ref | 次 ref |
|---|---|---|
| per-vendor TLS profile | `tonghuilong/wreq`(BSD,Rust 项目,profile 模板可借鉴形式) [[project_relay_core_path]] | `[refraction-networking/utls]`(BSD,Go TLS fork) |
| HTTP/2 fingerprint | `~/refs/h2/`(BSD,h2 Rust crate) | `[lucas-clemente/quic-go]`(MIT,HTTP/3 同思路) |
| outbound transport gating | (HUAKAI 自研) | `~/refs/envoy-ai-gateway`(Apache-2.0,outbound config) |

**Claude 判断**:G-3 主要靠 [[project_l1_tls_boringssl]] 决策的 backend (BoringSSL fork),HUAKAI 这层只做 vendor profile 配置 + 出站 router gating。不在本 plan 重新决策选哪个 transport,只决定 **配置注入点**。

### G-4 健康维护

| 子缺口 | 主 ref | 次 ref |
|---|---|---|
| account revoke 检测 | `BerriAI/litellm@HEAD:litellm/proxy/_experimental/health_check/`(Apache-2.0)— health check 框架 | `~/refs/sub2api/backend/internal/service/channel_monitor_service.go`(LGPL paraphrase only,channel monitor) |
| quota / rate-limit handling | `router-for-me/CLIProxyAPI@50d19e204fed:internal/cli/`(MIT,CLI 工具有 quota header 解析) | `~/refs/portkey-gateway/src/handlers/`(MIT) |
| soft-disable + cooldown | `~/refs/sub2api/backend/internal/service/channel_monitor_service.go`(LGPL paraphrase only)— sub2api channel_monitor 有 disable/restore 流程 | `~/refs/litellm/litellm/proxy/_experimental/`(Apache-2.0) |

**Recency check**:CLIProxyAPI 50d19e204fed pushed_at 2026-05-23(上轮 verify);sub2api / new-api 是 LGPL → 只能 paraphrase,不能 vendor;litellm Apache-2.0 可 vendor 但本 plan 用 paraphrase 也够。

## §4 文件级范围

**新文件**:

```
backend/internal/credentialacq/
  oauth_devicecode.go              (新,device-code OAuth flow for copilot)
  oauth_sso.go                     (新,AWS SSO + Microsoft Entra ID flow)
  vendor_exchangers.go             (新,vendor-specific PKCE exchanger 注册)

backend/internal/endpointcatalog/   (新包,non-frozen)
  catalog.go                       (Endpoint 表:vendor / region / url / last_refreshed)
  refresher.go                     (周期更新 endpoint;copilot 用 token 字段;gemini 抓 HTML)
  catalog_test.go

backend/internal/credentialworker/
  health_check.go                  (新,health probe tick — 与 refresh tick 独立)
  health_state.go                  (新,HealthState enum + transition rules)
  health_check_test.go
  health_state_test.go
  options.go                       (改:加 WithHealthCheckInterval / WithHealthQueries)

backend/internal/credentialworker/mimicry/   (新子包,non-frozen)
  profile.go                       (per-vendor TLS profile metadata)
  router.go                        (按 vendor 路由 transport)
  profile_test.go

backend/sql/migrations/0008_provider_account_health.up.sql      (新,加 health_state 列)
backend/sql/migrations/0008_provider_account_health.down.sql    (新)
backend/sql/migrations/0009_endpoint_catalog.up.sql             (新)
backend/sql/migrations/0009_endpoint_catalog.down.sql           (新)
backend/sql/migrations/0010_oauth_acquisition_session_devicecode.up.sql (新,加 device-code 列)
backend/sql/migrations/0010_oauth_acquisition_session_devicecode.down.sql (新)
```

**修改文件**:
- [`backend/internal/credentialworker/scheduler.go`](backend/internal/credentialworker/scheduler.go):refresh 失败 outcome 扩展,health_state 写入
- [`backend/internal/credentialworker/audit.go`](backend/internal/credentialworker/audit.go):outcome enum 加 `revoked` / `quota` / `risk` / `disabled` (sqlc 重生成)
- [`backend/cmd/gateway/wiring.go`](backend/cmd/gateway/wiring.go):wire endpointcatalog + health_check + mimicry router
- [`backend/internal/credentialacq/oauth.go`](backend/internal/credentialacq/oauth.go):exchanger 注册改成可拔插 (从 `vendor_exchangers.go` 取)

**禁止新增**:
- ❌ `backend/internal/proto/` (冻结)
- ❌ `backend/internal/gateway/` (冻结) — mimicry router 入新子包 `credentialworker/mimicry/`,**不进 gateway**
- ❌ `backend/internal/gatewayhttp/` (冻结)

**rationale (CLAUDE.md #13)**:
- `endpointcatalog` 独立包:G-2 是跨 vendor 共享能力,放 vendor 包内会重复
- `credentialworker/mimicry/` 子包:G-3 接入点紧贴 credentialworker(出站 transport 是 worker 选择的),不进 gateway 避免冻结包扩散

## §5 切片建议

### 切片 L-A: vendor bootstrap handler (G-1)

**Spec 要点**:
1. `credentialacq/vendor_exchangers.go`:定义 `Exchanger` interface,注册 6 个 vendor (anthropic / gemini / antigravity / cursor / copilot-via-devicecode / kiro-via-sso)
2. `credentialacq/oauth_devicecode.go`:GitHub device-code flow — `POST /login/device/code` → user_code/verification_url → 客户端轮询 `POST /login/oauth/access_token` 拿 token
3. `credentialacq/oauth_sso.go`:AWS SSO flow — 启动 `start_authorization` → 用户浏览器登录 → 客户端轮询 `create_token`
4. `oauth_acquisition_session` 表迁移 0010 加列:`auth_type` enum (`pkce` / `device_code` / `sso`),`device_code_payload` JSONB
5. `credentialacq/oauth.go` `StartOAuthFlow` 改成 dispatch to vendor handler

**风险测试 (CLAUDE.md #14 判别 fixture + mutation)**:
- **R-LA1: PKCE state CSRF**:fixture mock 攻击者重放别人的 state → backend 必须返 400 `state_mismatch`。Mutation:跳过 state 校验 → test 必须红 (验证逻辑被去掉)。
- **R-LA2: device-code 降频**:mock 返 `slow_down`,轮询客户端必须延迟更长。Mutation:固定 5s 轮询 → test 必须红 (违反 RFC 8628)。
- **R-LA3: AWS SSO expires_in 边界**:mock 返 `expires_in=300`,client 轮询 → 5min 内必须 give-up。Mutation:无 expires_in 检查 → test 必须红。
- **R-LA4: exchanger 注册错路由**:fixture 把 vendor="anthropic" 路由到 cursor exchanger → test 必须红 (跨 vendor token shape 不同)。

**文件**:`oauth_devicecode.go` / `oauth_sso.go` / `vendor_exchangers.go` + migration 0010 + tests。

### 切片 L-B: endpoint catalog + 周期采集 (G-2)

**Spec 要点**:
1. 新包 `backend/internal/endpointcatalog/`
2. `catalog.go` 定义 `Endpoint{Vendor, Region, URL, LastRefreshed, Source}`,`Lookup(vendor, region) (URL, error)`
3. `refresher.go` 周期 tick:
   - copilot:从 token `endpoint.api` 字段读 (token refresh 时同步)
   - gemini_advanced:HTTP GET `https://gemini.google.com` 抓 `boq_assistant-bard-web-server_*` 缓存 7 天
   - cursor:从配置文件读 (cursor endpoint 较稳定,不需采集)
4. migration 0009:`endpoint_catalog` 表
5. session adapter 改 `defaultXxxEndpoint` → `endpointcatalog.Lookup(...)` 调用

**风险测试**:
- **R-LB1: endpoint refresh 失败盲用 stale**:mock catalog refresh 401 → adapter 必须用 last-known + 写 audit `endpoint_stale`。Mutation:盲用 hardcode default → test 必须红 (Owner 改了 endpoint 仍打老 URL)。
- **R-LB2: bl= 采集失败 fail-closed**:mock HTML 200 但内容不含 `boq_assistant-bard-web-server_` → catalog `gemini_advanced` 行 `URL` 必须 nil + audit `bl_capture_failed`。Mutation:返 hardcode `boq_assistant-bard-web-server_unknown` → test 必须红。
- **R-LB3: 跨 region**:fixture `region=us-east-1` 查 copilot endpoint,catalog 必须返对应 region URL,不是默认。Mutation:忽略 region 参数 → test 必须红。

**文件**:`endpointcatalog/catalog.go` + `refresher.go` + tests + migration 0009 + 改 6 个 session adapter。

### 切片 L-C: mimicry profile 注入 + 出站 router (G-3)

**Spec 要点**:
1. 新包 `backend/internal/credentialworker/mimicry/`
2. `profile.go`:`Profile{Vendor, ClientName, JA3, JA4, H2Settings}` (元数据);profile 内容是文本配置,不是代码 (避免代码 mimicry 散乱)
3. `router.go`:`ResolveTransport(vendor string) http.RoundTripper` — 按 vendor 返不同 RoundTripper。**这里不实现 RoundTripper 本体**,只定 interface;实现由 [[project_l1_tls_boringssl]] 战略决策的 backend 提供 (uTLS / wreq / BoringSSL)
4. wiring.go 拼:`mimicry.NewRouter(profileFiles...)` 注入到 scheduler

**风险测试**:
- **R-LC1: profile 不匹配 vendor 时 fallback 行为**:fixture vendor="unknown" → router 必须返 stdlib `http.DefaultTransport` + audit `mimicry_profile_missing`。Mutation:panic 或返 nil → test 必须红。
- **R-LC2: profile 表 hot reload**:运行时改 profile 文件 → router 5min 内自动 reload。Mutation:cache 永久 → test 必须红 (改了不生效)。
- **R-LC3: per-vendor JA3 隔离**:fixture cursor / copilot 两 profile JA3 不同 → 抓包必须看到对应 vendor 用对应 JA3。Mutation:共享一份 transport → test 必须红 (JA3 都一样)。

**文件**:`mimicry/profile.go` + `router.go` + tests + Owner 抓包 profile.toml(per vendor)。

### 切片 L-D: 健康探测 + soft-disable + cooldown (G-4)

**Spec 要点**:
1. `credentialworker/health_state.go` 定义状态机:`healthy → throttled → revoked → cooldown → healthy` (transition rules)
2. `health_check.go` 独立 tick (interval=5min):
   - refresh outcome 是 `invalid_grant` / `revoked_token` → state=revoked
   - vendor backend 返 429 / `rate_limit_exceeded` → state=throttled (cooldown 30min)
   - vendor backend 返 `account_disabled` / `risk_control_triggered` → state=revoked + 报警
3. migration 0008:`provider_account` 加列 `health_state` enum + `health_changed_at` timestamp + `cooldown_until` timestamp
4. dispatcher (`backend/internal/.../dispatcher.go`,本 plan 不动 dispatcher 实现) 查 `health_state` 过滤:`healthy` 的才参与选号

**风险测试**:
- **R-LD1: revoke 检测 false negative**:fixture mock vendor 返 401 `invalid_grant` → 必须立即 state=revoked。Mutation:忽略 401 → test 必须红 (账号继续被选)。
- **R-LD2: throttled cooldown 没到期不复活**:fixture cooldown_until=now+30min,health_check tick 现在跑 → state 仍 throttled。Mutation:跳过 cooldown_until 检查 → test 必须红 (提前复活,vendor 再次 429)。
- **R-LD3: 多事务 race**:并发 1 refresh 写 state=revoked + 1 dispatch 读 state=healthy → 必须串行 (SELECT...FOR UPDATE 或 state 字段加版本号)。Mutation:不锁 → test 必须红 (concurrent test 频闪)。
- **R-LD4: audit ledger 同事务**:state 切换必须同事务写 audit_ledger (复用 P0-4 的 audit 同事务) — Mutation:state 改成不写 audit → test 必须红 (信任链断)。

**文件**:`health_state.go` + `health_check.go` + tests + migration 0008 + 改 `scheduler.go` outcome 路由 + 改 audit outcome enum。

### 切片 L-Z: dispatcher 接入 (压轴)

**Spec**:dispatcher 查 `health_state` 字段;放 default true 让 unhealthy 账号被自动 skip。**这条等 L-D verify 完才动**,dispatcher 在 gateway 包(冻结)→ 这切片是改老文件不是新加文件,不违 CLAUDE.md #13。

## §6 风险测试矩阵汇总

| ID | 风险 | 真实损失 | mutation 自检 | 判别 fixture |
|---|---|---|---|---|
| R-LA1 | PKCE state CSRF | 攻击者拿到别人 token | 删 state 校验 | 重放别人 state |
| R-LA2 | device-code 违 RFC 8628 | GitHub 拉黑应用 | 固定轮询 5s | mock 返 slow_down |
| R-LA3 | AWS SSO 不超时 | 内存泄露 + token 永远 pending | 删 expires_in 检查 | expires_in=300 mock |
| R-LA4 | exchanger 跨 vendor 误路由 | token shape 错解析 | vendor 路由表错 | anthropic→cursor exchanger |
| R-LB1 | endpoint 盲用 stale | 打错 URL | 盲用 default | mock catalog 401 |
| R-LB2 | bl= 采集失败盲用 | gemini 拒签 | 返 hardcode bl | HTML 200 缺关键字 |
| R-LB3 | 跨 region 忽略 | 路由错 region | 忽略 region 参数 | us-east-1 vs eu-west-1 |
| R-LC1 | mimicry profile 缺失盲发 | 风控触发 | nil transport | unknown vendor |
| R-LC2 | profile hot reload | 改了不生效 | cache 永久 | 改 profile.toml |
| R-LC3 | per-vendor JA3 串号 | vendor backend 串号检测 | 共享 transport | cursor vs copilot 抓包 |
| R-LD1 | revoke false negative | 钱继续算账 | 忽略 401 | 401 invalid_grant |
| R-LD2 | cooldown 提前复活 | vendor 再 429 | 跳过 cooldown_until | now+30min 假期 |
| R-LD3 | state 切换 race | 多事务读旧 state | 不锁 | concurrent refresh+dispatch |
| R-LD4 | state 切换不同事务 audit | 信任链断裂 | 删 audit 写 | 复用 P0-4 audit tx |

## §7 D 决策点 (Owner pick)

### D-1: device-code OAuth (G-1) 接入哪个 credentialacq entry

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) `oauth.go` 内 `if auth_type==device_code branch` | 通用 entry 内分支 | `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py`(Apache-2.0) — 单文件含 device-code |
| (B) 独立 `oauth_devicecode.go` + 独立 entry point | 物理隔离 | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/oauth_server.go`(MIT)— 每 vendor 独立 oauth_server |
| (C) `vendor_exchangers.go` 注册表,handler 形态自由 | 工厂模式 | (HUAKAI 自研,但 portkey-gateway 有 vendor registry 类比) |

**Claude 推荐**:(C) 注册表。**Why**:device-code / SSO / PKCE 形态差异大,(A) 分支会膨胀,(B) 物理隔离没法共享 audit/error;(C) 注册表让每 vendor 独立写 + Scheduler 看到一个 unified Exchanger interface。

### D-2: endpoint catalog 存储位置

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) PG 表 `endpoint_catalog`(本 plan 默认设计) | 持久化,可 ops 改 | `~/refs/portkey-gateway`(MIT)— 配置表模式 |
| (B) etcd / consul | 分布式 config | (无 HUAKAI 既有依赖) |
| (C) 文件 + hot reload | 简单 | `~/refs/envoy-ai-gateway`(Apache-2.0)— YAML 配置 |
| (D) Redis | 快,但 ops 复杂 | `~/refs/sub2api`(LGPL,sub2api 用 Redis) |

**Claude 推荐**:(A) PG。**Why**:HUAKAI 已经强依赖 PG,加 etcd/redis 是新依赖 [[feedback_relax_self_constraints_for_project_benefit]];endpoint 变更频率低(几天一次),不需要 sub-second 一致性。

### D-3: mimicry profile 配置文件还是代码

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) **TOML/YAML 配置文件**:JA3 / JA4 / H2 settings 都在文件 | ops 改不动代码 | `~/refs/envoy-ai-gateway`(Apache-2.0)— YAML 配置 |
| (B) **Go 代码 const**:每 vendor 一个 .go 文件,固定 profile | 编译期校验 | `[refraction-networking/utls]`(BSD)— hardcode profile |
| (C) **DB 表**:profile 存 PG,运行时改 | ops dashboard 可改 | (HUAKAI 自研) |

**Claude 推荐**:(A) TOML/YAML 文件。**Why**:profile 修改 = ops 抓真包对照,改文件比改代码方便;(B) 改要重新编译 + 部署;(C) 过度,profile 不需要事务一致性。

### D-4: health_state cooldown 时长 / 转移规则

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) **3 连封 disable + 30min cooldown** (本 plan 默认建议) | 兼容 [[project_trust_ledger_failclosed_policy]] | `~/refs/sub2api/backend/internal/service/channel_monitor_service.go`(LGPL paraphrase only) — sub2api 3 连封 model |
| (B) **1 次封即 disable + 1h cooldown** | 更保守 | (无 ref,推断 conservative) |
| (C) **指数退避**:第 N 次封 cooldown=2^N min | 自适应 | `~/refs/portkey-gateway` 有 backoff |
| (D) **配置可调**:env var 控 | 灵活,默认 conservative | (HUAKAI 自研) |

**Claude 推荐**:(D) 配置可调,默认 3 连封 + 30min。**Why**:不同 vendor 风控阈值不同 (cursor 严 / copilot 松),需要 per-vendor 调,但默认值给 conservative 防误关。

### D-5: G-3 mimicry 接入和 transport 实现的边界

| 选项 | 大白话 |
|---|---|
| (A) 本 plan 只接 interface,RoundTripper 实现等 [[project_l1_tls_boringssl]] 落 | 解耦 |
| (B) 本 plan 也实现一个 stdlib `net/http.Transport` 包装版 | 有 fallback |

**Claude 推荐**:(A) 只接 interface。**Why**:[[project_l1_tls_boringssl]] 没决,stdlib 包装是假 mimicry(JA3 仍是 Go 默认),交付假 mimicry 比不交付更危险 (用户以为伪装了)。

### D-6: dispatcher 接入 health_state 时机

| 选项 | 大白话 |
|---|---|
| (A) L-D 切片同期就改 dispatcher | 一次到位 |
| (B) L-Z 压轴切片独立做 dispatcher 接入 | 解耦 |

**Claude 推荐**:(B) 独立。**Why**:dispatcher 在 gateway 冻结包,改它要小心,跟 G-4 写入侧解耦让 L-D verify 不依赖 dispatcher 行为。

## §8 验证

- 单元:`go test -C backend ./internal/credentialacq/... ./internal/credentialworker/... ./internal/endpointcatalog/...`
- 集成 PG (audit 同事务 + migration round-trip):`go test -C backend -tags integration_pg ./internal/credentialworker/...`
- migration 双向:`migrate -path backend/sql/migrations -database $DATABASE_URL up` + `down 1` + `up` 三次(0008/0009/0010 各一)
- mutation 自检:每切片对应 `*_mutation_test.go` 显式注入错误 + 断言变红
- 全量:`go test -C backend ./...`
- Owner 本机 e2e:device-code OAuth (Owner 用 GitHub 真账号) + SSO (Owner 用 AWS 账号) — 不能 sandbox 跑,见 [[feedback_owner_local_verification]]

## §9 Source files read

**HUAKAI**:
- `backend/internal/credentialacq/oauth.go:1-100` (上轮 read)
- `backend/internal/credentialworker/scheduler.go` (上轮 read,P0-4 改造)
- `backend/internal/credentialworker/audit.go:1-120` (本轮 read)
- `backend/internal/credentialworker/options.go:1-115` (本轮 read)
- `backend/internal/provider/registrydefault/default.go:106,130-137` (上轮 read)
- `backend/cmd/gateway/wiring.go` (上轮 read,credentialworker wiring)
- `backend/internal/provider/{cursor,copilot,gemini,antigravity,kiro,windsurf}/*_session.go` (本轮 grep)

**Refs (lane=specifier,paraphrase only or vendoring per #12)**:
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go`(MIT)— PKCE flow template
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/oauth_server.go`(MIT)— vendor 独立 oauth_server 模板
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/vertex/vertex_credentials.go`(MIT)— SSO/SA 类比
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go`(MIT)— Google OAuth
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go`(MIT)— Google OAuth 同上
- `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py`(Apache-2.0)— device-code template
- `~/refs/sub2api/backend/internal/service/channel_monitor_service.go`(LGPL paraphrase only)— health/disable/cooldown 行为
- `~/refs/sub2api/backend/internal/service/oauth_service.go`(LGPL paraphrase only)— OAuth flow 思路
- `~/refs/envoy-ai-gateway`(Apache-2.0)— outbound config 模式
- `~/refs/portkey-gateway`(MIT)— vendor registry / endpoint table 模式
- 公开规范:RFC 6749 (OAuth 2.0), RFC 7636 (PKCE), RFC 8628 (device-code), AWS SigV4 docs

**Recency check (CLAUDE.md #12)**:
- CLIProxyAPI: 上轮 verify 2026-05-23 ✓
- litellm: BerriAI/litellm 已 fresh fetch 2026-05-23T23:57 → 414866767176 (Apache-2.0)
- envoy-ai-gateway / portkey-gateway / sub2api / new-api / helicone / llmgateway:全 fresh fetch 完成 (sub2api owner 正确是 **Wei-Shaw/sub2api**,new-api 正确是 **QuantumNous/new-api**)
- **最新 anchor 表**:[docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md) — 本日 07:25Z 拉 latest;ref path 用 ~/refs-latest/<name>-extracted/<repo>-main/

## §10 Lane + agent attribution

- Agent: claude-opus-4-7
- Session: HUAKAI 2026-05-24 (本 plan 是本 session 第 3 个 plan,前两个:anthropic-oauth-inversion-claude.md + placeholder-session-adapters-claude.md)
- Lane: PM-orchestrator + specifier (本 plan plan-only;实施 lane 转 codex executor)
- UTC: 2026-05-24T07:00Z
- Cross-discuss target: `docs/process/plans/2026-05-24-token-refresh-worker-closure-codex.md` (codex,后台跑)
- Synthesis 文件: `docs/process/plans/2026-05-24-token-refresh-worker-closure-synthesis.md` (codex 完工后写)

**Plan 间依赖**:
- 本 plan G-1 (vendor exchangers) 跟 Anthropic OAuth plan 的 `AnthropicExchanger` 共用框架 — synthesis 时合并
- 本 plan G-3 (mimicry) 跟 placeholder plan P-D (cursor 抓包) 共享 mimicry profile 输入 — synthesis 时确认 timeline
