# Anthropic Pro/Max OAuth 反转 — Claude lane 独立 plan

## Lane Header

=== CLEAN-ROOM LANE GUARD ===

LANE: SPECIFIER (Claude lane)

REFERENCE PROJECTS IN SCOPE:
- `router-for-me/CLIProxyAPI@50d19e204fed`(2026-05-23 pushed,MIT,34.4k stars,active)— Anthropic Pro/Max OAuth 流程头号参照;独立 specifier 读源 + 提取 behavior。
- `Wei-Shaw/sub2api`(LGPL)— 仅做横向对照(订阅账号转 API 的另一思路),禁 vendoring,只 paraphrase。

HARD PROHIBITIONS:
- 不复制 CLIProxyAPI 的函数名 / 字段名 / 注释 / raw code 进 HUAKAI 文件 — 只提取 behavior 描述 + 算法步骤 + 协议契约。
- 不逐字翻译;描述用不同顺序与措辞,锚点用 file:line 引用但禁止 verbatim 转写。
- 不引入 LGPL/AGPL 借鉴项目的 vendoring(sub2api 是 LGPL,只能 paraphrase 行为)。MIT 借鉴项目(CLIProxyAPI)的 vendoring 是 D 决策可选项,由 Owner 拍板。

CITATION POLICY:
- HUAKAI 内部:`backend/internal/<path>:<line>` 形式
- 参考项目:`router-for-me/CLIProxyAPI@50d19e204fed:<file>:<line>` 形式
- 每段"上游做 X"的描述必带至少一条 cite。

=== END CLEAN-ROOM LANE GUARD ===

## §1 目标 + 范围

### 1.1 目标

把 HUAKAI 当前 `backend/internal/provider/registrydefault/default.go:106` 注册的纯 API key 直通 `anthropic.PassthroughAdapter`,扩展出**第二条凭据形态分支**:OAuth 反转 — 用户接入自己的 Anthropic Pro / Max 订阅账号(浏览器登录拿 OAuth tokens),HUAKAI 用这套 tokens 出站调 Anthropic backend,对外仍按 Anthropic Messages API 兼容形态输出。

核心信任链承诺([[project_core_trust_chain_differentiator]]):
- 用户能看到自己的 OAuth 凭据被什么时刻被刷新、被哪个 worker 用过(audit ledger 留痕)
- 商家不能拿用户的 OAuth tokens 调任何"非用户原意"的接口(凭据使用边界)
- token refresh 失败 → fail-closed 不掩盖

### 1.2 范围 in-scope

- **OAuth flow 落地**:PKCE 生成 + authorize URL 构造 + 本地回调接收 + code-to-token 交换 + token 持久化加密。
- **Refresh 闭环**:接入既有 `credentialworker.Scheduler`(P0-4 已修同事务审计);access_token 过期前自动用 refresh_token 换新。
- **出站 adapter**:新 `anthropic.OAuthSessionAdapter`(或类似名);凭据形态为 OAuth bearer access_token,不接 `x-api-key`;走真 Anthropic API endpoint。
- **凭据采集 handler**:复用 `credentialacq` 既有框架(`oauth.go:44 StartOAuthFlow` / `oauth.go:90 CompleteOAuthCallback`),加 anthropic-specific 的 client_id / scope / URL 配置。
- **CLI import 替代**:supports 用户在自己机器跑过 `claude` CLI 拿到 ~/.claude/* token,导入 HUAKAI(复用既有 `credentialacq/cli_import.go` 框架)。
- **Schema 评估**:`account_credentials` 当前字段是否够存 access_token + refresh_token + email + expire?需要加列 → Owner schema gate。
- **Provider registry**:新增 `ProtocolAnthropicOAuthSession` 常量;**不替换**现有 `ProtocolAnthropicMessages`(后者继续服务纯 API key)。出站时由 credential 形态(api_key vs oauth)决定走哪个 adapter。

### 1.3 范围 out-of-scope(本 wave 不做)

- Anthropic Console 工作账号(Enterprise)— 走 API key 已够,不需要 OAuth 反转
- Claude Sonnet 4.x 多模态全 codec — Messages API contract 不动
- 重写 `credentialworker.Scheduler` 调度逻辑 — 只接入新 refresh adapter
- Transport mimicry 的 uTLS / rquest 实施(D 决策点 — 由 Owner 拍是否本 wave 做)
- 浏览器自动化(headless chrome 拉 cookie)— 不做,要么 OAuth 要么 CLI import
- Bedrock 间接 Anthropic 路径 — 已有 `bedrock.PassthroughAdapter` 独立链路

## §2 HUAKAI 现状与缺口

### 2.1 已有(可直接复用)

| 现有模块 | 锚点 | 复用方式 |
|---|---|---|
| credentialacq OAuth 框架 | `backend/internal/credentialacq/oauth.go:44`(StartOAuthFlow)+ `:90`(CompleteOAuthCallback) | 已生成 PKCE state + verifier + challenge + session 持久化 + 转 CredentialCandidate;只需补 Anthropic 的 `OAuthClientConfig`(client_id / auth_url / token_url / redirect / scopes) |
| credentialacq CLI import | `backend/internal/credentialacq/cli_import.go` | 已有手 paste / file import 框架;只需补 anthropic 的 JSON shape parser |
| credentialacq cloud bootstrap | `backend/internal/credentialacq/cloud_bootstrap.go` | 已有云端 bootstrap 路径 |
| credentialstore 加密存储 | `backend/internal/credentialstore/postgres_store.go` | 已支持 lifecycle:Create/Rotate/SetState/Delete;只需补 anthropic_oauth credential kind |
| credentialworker refresh scheduler | `backend/internal/credentialworker/scheduler.go`(P0-4 已落同事务审计) | 已有 refresh 调度 + audit + ledger 同事务;只需注册新 Refresher adapter |
| provider StaticRegistry | `backend/internal/provider/registrydefault/default.go:33-145` | 已有 ProtocolFamily 字符串约定 + opt-in placeholder gate;直接加 `ProtocolAnthropicOAuthSession` 常量 + register |
| audit ledger 信任链 | `backend/internal/auditledger/` | 已有 PreparedEntry + AppendInTransaction;refresh worker 已接入 |

### 2.2 缺口(必须新写)

| 缺口 | 范围 |
|---|---|
| Anthropic OAuth Client 常量与 endpoint | client_id / authorize_url / token_url / scopes — 需 Owner 决策来源(从 Anthropic 公开 CLI 抠 vs 自己注册) |
| `provider/anthropic/oauth_session.go`(假设文件名)| 新 Adapter,凭据形态为 OAuth bearer,出站到 Anthropic Messages API,挂 `authorization: Bearer <access_token>` |
| `credentialworker/adapters/anthropic_oauth.go`(假设文件名)| 新 Refresher 实现 refresh_token → 新 access_token,符合 `credentialworker.Refresher` 接口 |
| Schema 评估(D 决策点)| `account_credentials` 加 `oauth_id_token` / `oauth_refresh_token` / `oauth_expire_at` 等列?或者 JSON payload?Owner schema gate |
| Provider Family 常量 | `ProtocolAnthropicOAuthSession = "anthropic_oauth_session"` 加到 registrydefault/default.go |
| Token expiry 监控 + 主动 refresh 触发 | refresh scheduler 已有 list-by-expiry,只需新 query "anthropic_oauth 即将过期" |
| Outbound transport mimicry | D 决策:本 wave 做 vs 推后 |
| 测试 fixture:OAuth callback mock server + token refresh integration_pg | 新增,符合 [[feedback_test_quality_discipline]] 判别 fixture |

### 2.3 信任链与现有规则的对齐

- W4/W5 audit 链:refresh 已经在 `credentialworker.Scheduler.recordAudit`(P0-4 同事务)记录 outcome — anthropic_oauth refresh 自动继承
- DR-002 商业 edition:OAuth 反转跨 Personal / SaaS Edition,SaaS Edition 需要多用户隔离(不在本 wave,但 schema 不能阻塞 SaaS)
- [[project_real_vendor_account_scope]]:Owner 2026-05-09 已批 anthropic 真上游测试 OK — 可以跑真账号 e2e

## §3 参考项目方案(specifier 提取 behavior)

### 3.1 router-for-me/CLIProxyAPI@50d19e204fed Anthropic 实现

**3.1.1 PKCE 生成**([cite](router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/pkce.go:21-56))
- 96 随机字节 → base64url 无 padding 编码,约 128 字符;符合 RFC 7636 verifier 边界
- challenge = SHA256(verifier),同样 base64url 无 padding
- `S256` 方法,**不是** `plain`

**3.1.2 Token 数据结构**([cite](router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic.go:1-32) + [cite](router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:18-43))
- 持久化字段:id_token(JWT) / access_token / refresh_token / last_refresh / email / expire(timestamp ISO)
- 上游同时返一个 anthropic api_key — 上游做了 dual-credential 设计(api_key 兜底 + oauth 主用)
- Type 字段标 "claude" 用于路由

**3.1.3 OAuth flow 大致流程**(`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go` 502 行,未完整读;以下是 specifier 抽 high-level behavior)
- 启动本地 HTTP server 监听回调(`oauth_server.go:1-320`)
- 构造 authorize URL:Anthropic 的 OAuth endpoint + client_id + redirect_uri(localhost:port)+ code_challenge + state
- 浏览器开 URL → 用户登录 → 回调到 localhost server → 解析 code + state
- POST 到 Anthropic token endpoint:code + verifier + client_id → 拿 (access_token, refresh_token, expire)
- 上游通过 utls_transport 把 TLS 握手伪装成 Claude Code CLI 客户端的指纹

**3.1.4 Transport 特殊化**(`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go`)
- 用 utls 自定义 ClientHello 把 JA3 / JA4 指纹做成跟官方 Claude CLI 同样的样子,避免 Anthropic backend WAF / 风控判断异常 client。这与 HUAKAI 既有 `internal/transport/mimicry` 设计同向。

### 3.2 sub2api 对照(LGPL,只 paraphrase 不引用代码)

sub2api 是订阅账号转 API 的另一种思路,但其设计偏向 cookie session 而非 OAuth — 不直接适用 Anthropic Pro/Max。HUAKAI Anthropic 路径走 OAuth(更稳定 + 标准化)优于 cookie。

### 3.3 Fusion-upgrade 三维 delta(HUAKAI 相对参照项目)

| 维度 | CLIProxyAPI 做法 | HUAKAI delta |
|---|---|---|
| 架构升级 | 单机 token 文件 JSON 持久化(`token.go:60 SaveTokenToFile`) | HUAKAI 用 `credentialstore.Store` 加密存 PG + lifecycle audit(Create/Rotate/SetState/Delete 已落地)+ DR-002 多租户隔离 |
| 算法升级 | refresh 是用户手动触发或简单定时 | HUAKAI 用 `credentialworker.Scheduler` 已有 warning_window + storm controller + max_attempts backoff + 同事务审计(P0-4) |
| 生态升级 | 单机 CLI 流程 | HUAKAI 是中转 SaaS:audit 链 + observability dashboard + DLQ replay + 多账号 pool routing + per-tenant quota + refund worker;OAuth 凭据只是其中一个 vault 类型,自动接入 W4/W5 信任链 |

## §4 文件级范围

| 文件 | 新/改 | 冻结? | 责任 |
|---|---|---|---|
| `backend/internal/provider/anthropic/oauth_session.go` | 新 | 否 | OAuthSessionAdapter — 实现 provider.Adapter,出站带 Bearer access_token |
| `backend/internal/provider/anthropic/oauth_session_test.go` | 新 | 否 | 判别 fixture:OAuth 凭据时挂 Bearer 不挂 x-api-key;反向 mutation 自检 |
| `backend/internal/provider/anthropic/passthrough.go` | 不动 | 否 | API key 路径保留 |
| `backend/internal/credentialacq/anthropic_oauth.go` | 新 | 否 | Anthropic-specific OAuthClientConfig(常量 / endpoints / scopes)+ OAuthExchanger 实现(code → token) |
| `backend/internal/credentialacq/anthropic_oauth_test.go` | 新 | 否 | mock HTTP token endpoint;判别 fixture 验 code → token 解码 |
| `backend/internal/credentialacq/anthropic_oauth_pg_test.go` | 新 | 否 | real PG 验 StartOAuthFlow + CompleteOAuthCallback 端到端 anthropic |
| `backend/internal/credentialworker/adapters/anthropic_oauth.go` | 新 | 否 | Refresher 实现:refresh_token → 新 access_token,符合既有 Refresher 接口 |
| `backend/internal/credentialworker/adapters/anthropic_oauth_test.go` | 新 | 否 | mock refresh endpoint;mutation 自检 |
| `backend/internal/credentialstore/types.go` | 改 | 否 | 加 CredentialKind 常量 `KindAnthropicOAuth`(或类似)— 用于 lifecycle 路由 |
| `backend/internal/provider/registrydefault/default.go` | 改 | 否 | 加 `ProtocolAnthropicOAuthSession` 常量 + `r.MustRegister` 调用;**不替换** ProtocolAnthropicMessages |
| `backend/sql/migrations/0054_anthropic_oauth_credentials.up.sql` + `.down.sql` | 新 | — | (D 决策点)如果选 schema 加列方案,本 migration 加 oauth-specific 列 |
| `backend/cmd/gateway/routes.go` | 改 | 否 | 加 admin endpoint 触发 anthropic OAuth flow(start / callback / list) — 或者复用既有 credentialacq HTTP handler |
| `backend/cmd/gateway/wiring.go` | 改 | 否 | wire anthropic OAuth adapter + refresher |
| `docs/openapi/openapi.yaml` | 改 | 否 | 加 admin 端 OAuth start / callback endpoint 声明 |
| `docs/runbooks/anthropic-oauth-inversion-runbook.md` | 新 | — | 部署 / 验证 / rollback runbook |

冻结包合规:gatewayhttp / gateway / proto 全程不动新文件;routes.go 在 cmd/gateway(非冻结)。

## §5 切片建议(C1..C6)

### C1 — credentialstore + provider 常量 + registry 注册(scaffold)

**spec**:
- 加 CredentialKind = "anthropic_oauth"
- ProtocolAnthropicOAuthSession 常量 + default.go MustRegister stub adapter(暂时是空 Passthrough 等 C2 真实)
- 不动 schema(本 commit 不改 0054)

**risk test**:`registrydefault/default_test.go` 加用例 — Build() 返回的 registry 必含 ProtocolAnthropicOAuthSession,断言适配器 nil 不可 — mutation: 删 r.MustRegister 后该测试 red

**commit**: `provider registrydefault 注册 anthropic_oauth_session 占位`

### C2 — credentialacq Anthropic OAuth client config + exchange(凭据采集)

**spec**:
- 写 `credentialacq/anthropic_oauth.go` — 暴露 AnthropicOAuthConfig()(返 OAuthClientConfig)+ AnthropicOAuthExchange(OAuthExchanger 实现)
- AnthropicOAuthExchange 拿 session + code → POST token endpoint(用 token_url + verifier + redirect)→ 解析 JSON 返 CredentialCandidate
- mock HTTP server 测试 code → token

**风险测试**(判别 fixture,符合 CLAUDE.md #14):
- T_C2A:mock token endpoint 返合法 (access_token, refresh_token, expire) → CredentialCandidate.AccessToken / RefreshToken / ExpiresAt 字段都正确填充。mutation 自检:删 `cand.AccessToken = body.AccessToken` 赋值 → cand 字段空 → red
- T_C2B:mock endpoint 返 4xx → AnthropicOAuthExchange 返 typed error(不掩盖)。mutation:把 status != 200 时 return nil 而不是 err → red
- T_C2C(integration_pg):StartOAuthFlow + CompleteOAuthCallback 链跑通,真 PG 验 session 行被 mark consumed

**commit**: `credentialacq anthropic oauth client + exchange`

### C3 — credentialworker refresh adapter

**spec**:
- 写 `credentialworker/adapters/anthropic_oauth.go` — Refresher 实现:用 refresh_token 调 token endpoint 拿新 access_token
- 注册到 credentialworker DefaultModeAdapterRegistry 或类似
- Refresher 调用前后由 scheduler 同事务记 audit(P0-4 已落)

**风险测试**:
- T_C3A:mock refresh endpoint 返 200 + 新 token → adapter 返回 new CredentialPayload + 新 expire。mutation:adapter 漏更新 expire → red
- T_C3B:mock refresh endpoint 返 401(invalid_grant)→ adapter 返 ErrPermanentDisable(typed),让 scheduler 走 permanent_disable outcome 而不是单纯 backoff。mutation:401 当 transient → red
- T_C3C(integration_pg):scheduler 跑一次 refresh tick,真 PG 验 audit + ledger 同事务落库

**commit**: `credentialworker adapters anthropic_oauth refresh`

### C4 — Schema(D 决策点定后做)

**spec**:
取决于 D 决策 — 见 §7

**风险测试**:migration 0054 up/down round-trip + sentinel 行验

**commit**: `migrations 0054 anthropic oauth credential fields`

### C5 — outbound adapter:anthropic.OAuthSessionAdapter

**spec**:
- 写 `provider/anthropic/oauth_session.go` — 出站 HTTP request 时挂 `Authorization: Bearer <access_token>`,**不** `x-api-key`
- 凭据形态识别:credential.Kind == "anthropic_oauth" → 走本 adapter;否则走老 PassthroughAdapter
- 出站 URL 走真 Anthropic Messages API endpoint(api.anthropic.com 或 OAuth backend)

**风险测试**:
- T_C5A:OAuth credential 时 outbound request 必带 `Authorization: Bearer xxx` 头,**不带** `x-api-key`。mutation: 复制 PassthroughAdapter 的 x-api-key 行 → red
- T_C5B:OAuth credential expired(在 scheduler refresh 之前)→ adapter 拒绝出站(返 ErrCredentialExpired),不挂 stale token。mutation:expired check 删 → red

**commit**: `provider anthropic OAuthSessionAdapter`

### C6 — admin endpoint + e2e + runbook

**spec**:
- routes.go 加 admin POST /admin/v1/provider-accounts/{id}/oauth/anthropic/start + /callback
- OpenAPI 声明 + 一致性测试
- 真账号 e2e 跑通(Owner 本机 [[feedback_owner_local_verification]])
- 写 runbook(部署 / 抓包 / rollback)

**commit**: `gateway admin anthropic_oauth start/callback + runbook`

## §6 风险测试矩阵

每条测试必须能在它该抓的缺陷出现时变红(符合 CLAUDE.md #14 测试质量纪律)。

| 测试 | 守的缺陷 | 判别 fixture | mutation 自检方法 |
|---|---|---|---|
| T_C2A | code → token 解析丢字段 | 返 fixed JSON {access_token:"a1",refresh_token:"r1",expires_in:3600};断言 cand 三字段都对 | 删 cand.AccessToken=body.AccessToken → red |
| T_C2B | 4xx 错误被掩盖 | mock endpoint 返 400;期望 AnthropicOAuthExchange 返 err 非 nil | 改 `if status != 200 { return cand, nil }` → red |
| T_C2C | OAuth session 状态机错 | 真 PG;Start → Complete → mark consumed | 删 store.MarkConsumed 调用 → red |
| T_C3A | refresh 不写新 expire | mock endpoint 返新 expires_in=7200;断言 new expire 在未来 | 删 cand.ExpiresAt 赋值 → red |
| T_C3B | invalid_grant 当 transient | mock 返 {error:"invalid_grant"} → 期望 typed ErrPermanentDisable | 改成 transient → red |
| T_C3C | refresh audit 不同事务 | 真 PG + trigger 拒 refresh audit;期望 ledger 与 token 都不更新 | (P0-4 已在 credentialworker 验过同事务 - 此处属正向保持) |
| T_C5A | OAuth 出站漏 Bearer 头 | adapter Run 一次 outbound,grep request header | 复制 x-api-key 设置 → red |
| T_C5B | expired token 仍出站 | seed expired credential;期望 ErrCredentialExpired | 删 expired check → red |
| T_C6A(e2e) | 真账号 OAuth 走通 | 真 Anthropic 账号在 Owner 机器跑 anthropic CLI session import → HUAKAI 出站 messages API 返 200 | (端到端) |

## §7 D 决策点(Owner 必拍)

### D-1 OAuth client_id 来源

**A**(推荐)使用 Anthropic 公开 CLI 公开的 client_id(等同 Claude CLI / Claude Code CLI),用户感觉跟自己机器跑 claude 等价;HUAKAI 不申请自己的 client_id。
  - 参考:CLIProxyAPI 走该路径([cite](router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go) — 项目根 README 注明使用 Anthropic CLI 公开 client_id)
  - 风险:Anthropic 可能限速或封禁批量 OAuth(虽然 CLIProxyAPI 34k stars 长期跑无明显事故)
  - 优势:用户无感,商业落地最快;HUAKAI 不背 Anthropic 政策风险账

**B** HUAKAI 自己向 Anthropic 申请 client_id(若 Anthropic 提供 partner program)
  - 参考:Anthropic 当前公开渠道(2026-05-24)未见对第三方 hub / proxy 项目开放官方 OAuth partner program
  - 风险:申请周期不可控;可能直接拒
  - 优势:合规清晰,不依赖逆向工程式公开常量

**C** 让 operator 在自己 deploy 里填 `HUAKAI_ANTHROPIC_OAUTH_CLIENT_ID` 环境变量,HUAKAI 不预置
  - 参考:[[feedback_owner_local_verification]] 的"真实环境测试必须 Owner 本机跑"风格 — operator 自定 client_id
  - 风险:用户体验差(每个 deploy 都得自己搞);Personal Edition 用户基本搞不定
  - 优势:HUAKAI 项目本身完全干净

### D-2 凭据采集 UX

**A**(推荐)CLI import 优先 — 用户在本机跑 anthropic CLI 后,把 `~/.claude/<bundle>` 文件 paste / upload 到 HUAKAI admin 页面
  - 优势:走用户自己机器拿 token,HUAKAI 不需要 host OAuth server;最低安全/合规风险
  - 参考:既有 `credentialacq/cli_import.go` 已铺路

**B** Web OAuth callback — HUAKAI 启 OAuth server,用户在 HUAKAI 域名上 OAuth 一遍
  - 优势:用户体验最好(一键登录)
  - 风险:HUAKAI 域名需要被 Anthropic 接受为 redirect URI;client_id 来源决定能否走通
  - 参考:`credentialacq/oauth.go:44 StartOAuthFlow` 框架已铺

**C** A+B 都做(CLI import 兜底,OAuth callback 优先)
  - 优势:覆盖所有用户
  - 风险:范围扩大,本 wave 切片增多

### D-3 Schema 升级方案

**A**(推荐)account_credentials.payload JSON 加 oauth_* 字段(不改表结构)
  - 优势:无需 migration;现有 JSON payload 字段已支持
  - 风险:JSON 字段不能加 CHECK;查询 access_token expire 需要 JSONPath SQL
  - 参考:`backend/internal/db/billing/account_credentials_*.sql.go` 现有 schema

**B** 加 0054 migration:新列 `oauth_access_token` / `oauth_refresh_token`(加密)+ `oauth_expire_at` + `oauth_id_token`
  - 优势:类型安全 + SQL index expire_at;refresh worker 查询直接 `WHERE oauth_expire_at < NOW() + INTERVAL '15min'`
  - 风险:schema gate(Owner approval),需 migration 测试
  - 参考:`backend/sql/migrations/0006_upstream_credential_management.up.sql` 已建过类似列

**C** 拆新表 `anthropic_oauth_credentials`(1:N with account_credentials)
  - 优势:模型清晰
  - 风险:复杂度上升,FK 多;refresh worker 要 join

### D-4 Transport mimicry(本 wave 做 vs 推后)

**A**(推荐)推后做 — 先标准 net/http 出站,真实 e2e 验通,再决定是否要 uTLS 伪装
  - 优势:scope 收敛,先把 OAuth 反转骨架立起
  - 风险:Anthropic 风控可能在某些 IP 下识别 net/http 指纹封禁;但 Personal Edition 用户量小可能不触发

**B** 本 wave 接 transport mimicry — 用 HUAKAI 既有 `internal/transport/mimicry` 给 Anthropic 出站套指纹
  - 优势:从一开始就跟参考项目对齐(参考 CLIProxyAPI utls_transport)
  - 风险:范围扩大;mimicry 选 OpenSSL backend 还是 Rust sidecar 等是另一 D 决策(见 [[project_l1_tls_boringssl]])

### D-5 Vendoring vs paraphrase

**A**(推荐)Paraphrase — 按 CLAUDE.md #11 clean-room 流程,只读 CLIProxyAPI 提 behavior,HUAKAI 自己写所有代码
  - 优势:最干净,不引第三方依赖,自主可控
  - 参考:CLAUDE.md #11

**B** 部分 vendoring — MIT 允许;把 `router-for-me/CLIProxyAPI/internal/auth/claude/{pkce.go,token.go}` vendor 到 `backend/vendor/cliproxyapi-claude-auth/`,加 NOTICE.md / LICENSE / MODIFICATIONS.md
  - 优势:实施快,跟上游 PKCE / token 行为 byte-identical
  - 参考:CLAUDE.md #12 permitted-license vendoring policy

### D-6 Refresh adapter 接入 credentialworker 还是独立 worker

**A**(推荐)接入既有 credentialworker.Scheduler — 已有 audit 同事务(P0-4)、storm controller、backoff、max_attempts
**B** 独立 anthropic_oauth_refresh_worker 进程
  - A 复用 P0-4 已落地的同事务审计,不需要重做 audit gate

## §8 验证命令

```bash
cd backend && go build ./...
cd backend && go vet ./internal/credentialacq/... ./internal/credentialworker/... ./internal/provider/anthropic/... ./internal/credentialstore/... ./cmd/gateway/...
cd backend && go test -race -count=1 ./internal/credentialacq/... ./internal/credentialworker/... ./internal/provider/anthropic/... ./internal/credentialstore/... ./cmd/gateway/...
HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" cd backend && go test -tags integration_pg ./internal/credentialacq/... ./internal/credentialworker/...
codex exec --sandbox read-only review --uncommitted --full-auto  # per CLAUDE.md #8 每 commit 必跑
```

## §9 Source files read(specifier 完整 list)

CLIProxyAPI(MIT,`router-for-me/CLIProxyAPI@50d19e204fed`):
- internal/auth/claude/anthropic.go(完整,32 行)
- internal/auth/claude/pkce.go(完整,56 行)
- internal/auth/claude/token.go(完整,89 行)
- internal/auth/claude/anthropic_auth.go(部分;502 行未全读 — 抽 behavior 不抽代码)
- internal/auth/claude/oauth_server.go(部分;320 行)
- internal/auth/claude/utls_transport.go(file existence 检查;未深读)
- README.md / go.mod(确认 owner + version + MIT)

HUAKAI 内部锚点:
- backend/internal/credentialacq/oauth.go:44, :90(StartOAuthFlow / CompleteOAuthCallback)
- backend/internal/credentialacq/{cli_import.go, cloud_bootstrap.go, session_store.go, finalizer.go}(列目录)
- backend/internal/credentialstore/{postgres_store.go, types.go}(目录列)
- backend/internal/credentialworker/scheduler.go(主体)+ audit.go(P0-4 已落)+ options.go(WithTxPool 已加)
- backend/internal/provider/anthropic/passthrough.go + passthrough_test.go(API key 路径现状)
- backend/internal/provider/registrydefault/default.go(完整)
- backend/sql/migrations/0006_upstream_credential_management.up.sql(部分 — schema 参考)

**Recency check (CLAUDE.md #12)** — 2026-05-24T07:25Z 重拉:
- CLIProxyAPI: 50d19e204fed @ 2026-05-23 (latest;tarball ~/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/)
- **最新 8 项 anchor 表**:[docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md) — codex cross-discuss 用此表的 latest SHA,不用 ~/refs/ 旧 clone

## §10 Lane attribution + UTC timestamp

- Claude lane = specifier-Anthropic-OAuth,session 81fec8f5-b3e1-465a-95c3-26d6efee9c90
- Plan written without reading codex lane output(parallel-draft per CLAUDE.md #10)
- Timestamp: 2026-05-24T~07:00Z;Recency check 2026-05-24T07:25Z
- Next step: 读 codex 独立 plan → cross-discuss → surface Owner D-1..D-6 → synthesis → 实施切片
