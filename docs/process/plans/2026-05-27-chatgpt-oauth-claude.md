# ChatGPT OAuth (vendor=openai, AuthMode=chatgpt_oauth) — Claude lane plan

Lane: claude-pm-spec (账号 / 信任链 invariant — Claude 写 spec, codex 写代码)
Time: 2026-05-27T08:50:00Z
Cross-lane: docs/process/plans/2026-05-27-chatgpt-oauth-codex.md (codex 独立写, parallel-draft 不互相看)

## 0. 元信息

| Item | Value |
|---|---|
| Vendor / AuthMode | `openai/chatgpt_oauth` (HUAKAI 主线第 3 家 vendor) |
| Owner 启动信号 | 2026-05-27 |
| 已采 evidence | ClientID `app_EMoamEEZ73f0CkXaXp7hrann` (双源验证 CLIProxyAPI + openai-codex) |
| Decision points | D-1..D-N (§6), 落地前必须 Owner 拍 |
| 现状起点 commit | `023d67b` (GEM-5 docs 之后) |

## 1. 现状盘点

- [vendor_exchangers.go:44](backend/internal/credentialacq/vendor_exchangers.go#L44): `OpenAI/ChatGPTOAuth` 注册 `NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)` — 占位假实现, 不做真 OAuth code exchange
- [vendor_exchangers.go:45-47](backend/internal/credentialacq/vendor_exchangers.go#L45-L47): `OpenAI/CodexCLIOAuth` 已是 `openAICodexDeviceCodeExchanger` (device-code flow, 与 ChatGPT 不同, **不要碰**)
- [types.go:159](backend/internal/credentialacq/types.go#L159): ProfileBinding ChatGPTOAuth `ClientIdentitySource=PublicCLI`, `AllowedHelpers=[FlowKindOAuth]`
- [anthropic_oauth_test.go:398-433](backend/internal/credentialacq/anthropic_oauth_test.go#L398-L433): P0 守门已锁 — `chatgpt_oauth` mode 拒 paste / cli_import 模式 (OAuth-only invariant 已守)
- [openai.go](backend/internal/credentialworker/adapters/openai.go): `OpenAIRefresh` refresh adapter (ANT-3 R2 S2 mergeTokenResponse scrub 已实施, commit 57356e4)
- [mode_refresh.go:73](backend/internal/credentialworker/mode_refresh.go#L73): ChatGPTOAuth refresh = `legacyOAuthModeAdapter{providerName: "openai", adapter: adapters.OpenAIRefresh{}}` — 未注入 ClientID / Endpoint / HTTPClient, refresh 时从 cred 读 (类似 GEM-3 修前的状态)

## 2. 缺口分类

| 缺口 | 严重度 | vs Gemini |
|---|---|---|
| acquisition `chatgpt_oauth` 是 fake PKCE, 真 OAuth 流不走 | S1 (账号无法接通) | 同 GEM-1 |
| Admin handler 没有 chatgpt_oauth 真接通的 integration test | S1 (无法验证 end-to-end) | 同 GEM-2 |
| Refresh path OpenAIRefresh **可能存在** S1-D 攻击面 (从 cred 读 oauth_token_endpoint 等) — 需要核对当前 openai.go | 可能 S1-D | 类比 GEM-3 (gemini.go 4 攻击面) |
| 启动 wiring 未注入 SSRF-protected HTTP client / 内置 ClientID / 内置 Endpoint | S1 (信任链不全) | 同 GEM-3 wiring fail-loud |
| Mimicry sidecar (codex_cli mimicry profile) — HUAKAI 已验证 W11-F §14b codex 0.128.0 wire ja3 PASS ([[project_huakai_codex_mimicry_verified]]) — 但本切片是否启用 | 视 D-5 | 类比 GEM-4 |
| docs/03 + docs/07 + docs/10 + docs/17 ChatGPT OAuth status | 文档完整性 | 同 GEM-5 |

## 3. Slice 切片 (5 个 CHG-*)

### CHG-1 真 OAuth Exchanger 替换 fake (0.5 天)

新建 `backend/internal/credentialacq/chatgpt_oauth.go`, builtin-profile pattern (类似 [anthropic_oauth.go](backend/internal/credentialacq/anthropic_oauth.go) PKCE-only, **不像 gemini_oauth.go 那样需要 ClientSecret**):

```go
const (
    chatgptOAuthAuthURL          = "https://auth.openai.com/oauth/authorize"
    chatgptOAuthTokenURL         = "https://auth.openai.com/oauth/token"
    chatgptOAuthClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"  // openai-codex@HEAD:codex-rs/login/src/auth/manager.rs:921 + CLIProxyAPI 双源
    chatgptOAuthScope            = "openid email profile offline_access"
    chatgptOAuthLoopbackRedirect = "http://localhost:1455/auth/callback"  // CLIProxyAPI 同款 port
    chatgptApprovedProfileSource = "approved_builtin_profile_chatgpt_oauth"
)
```

关键 invariant:
- **PKCE-only**, validateChatGPTBuiltinProfile 拒绝任何 ClientSecret 注入 (与 anthropic 同款, 反着 gemini)
- `id_token_add_organizations=true` + `codex_cli_simplified_flow=true` + `prompt=login` 是 OpenAI 特定参数 (D-2 决策是否保留)
- Scope 已含 `offline_access` — Google `access_type=offline` 的对应物, refresh_token 必发
- redirect_uri allowlist (D-1)
- Source files read 在 commit msg: `~/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go` + `~/refs/openai-codex/codex-rs/login/src/auth/manager.rs` + Lane: specifier + Time

### CHG-2 Admin real-entry test (0.5 天)

整合到 [admin_credential_acquisition_handler_test.go](backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go): `TestChatGPTOAuthAdminCallbackEndToEnd` (类似 GEM-2)。

Mock RoundTripper 模拟 OpenAI token endpoint 返回 ChatGPT token response (含 access_token + refresh_token + id_token + ChatGPT-specific 字段)。验证 CredentialCandidate.Payload 含必要字段。Mutation: handler 回退到 PKCEFake → 测试红。

### CHG-3 Refresh path SSRF 闭合 (0.5-0.75 天)

核对 [adapters/openai.go](backend/internal/credentialworker/adapters/openai.go) 现状:
- 如果有从 cred 读 oauth_token_endpoint / client_secret / client_id 等 (S1-D 攻击面), 修法同 GEM-3 (内置 Endpoint + 内置 ClientID + 不读 cred + SSRF-protected client)
- 如果已经修过 (commit 57356e4 ANT-3 R2 S2 已 scrub), 仍需 wiring 注入 (mode_refresh.go:73 注入 ClientID + Endpoint + HTTPClient)
- 启动 wiring fail-loud assert (类似 `assertGeminiPublicCLIOAuthExchangersHaveHTTPClient`)

测试 (CLAUDE.md #14 mutation 自检):
- TestChatGPTRefreshIgnoresHostileOAuthTokenEndpoint
- TestChatGPTRefreshHTTPClientIsSSRFProtectedAtWiring
- TestChatGPTRefreshExplicitlyDisablesCrossClientFallback (如有)

### CHG-4 Mimicry (D-4 视决策)

D-4=A 路径 (本轮只接 OAuth, 类似 GEM-4 D-5=A): 标 R-CHG-MIMICRY-001 Mandatory Roadmap, token exchange 走 SSRF-protected standard transport, 不假装 mimicry。

D-4=B 路径 (本轮一并上线反检测): codex_cli mimicry profile 已经 W11-F §14b 验证过 ([[project_huakai_codex_mimicry_verified]]), 但 Rust 分支生产 dispatch 未启用 (Rust review d55fa24 S1-2 Gemini production builder Pending fail-closed, 类似情况 codex 也未上线)。如果 D-4=B, 需 Rust runtime preflight 接通 — 工时不在本切片范围。

**推荐 D-4=A**: 与 GEM-4 一致, 本轮 OAuth 接通 + mimicry 进路线图。

### CHG-5 Docs (0.25-0.5 天)

类似 GEM-5:
- docs/03 F-AUTH-005 / F-CRED-001 Status 加 ChatGPT OAuth 接通 commit ref
- docs/07 加 evidence rows: E-CPA-CODEX-OAUTH-001 (CLIProxyAPI codex/openai_auth.go) + E-OPENAI-CODEX-001 (openai-codex Apache-2.0 双源)
- docs/10 加 R-CHG-MIMICRY-001 (D-4=A 决策)
- 必要时 docs/process/decisions/DR-CHG-OAUTH-*.md 记 D-1..D-4

## 4. 参考项目对照 (CLAUDE.md #15)

| 维度 | CLIProxyAPI codex/openai_auth.go | openai-codex (Apache-2.0) | HUAKAI 选择 |
|---|---|---|---|
| ClientID | `app_EMoamEEZ73f0CkXaXp7hrann` 硬编 (line 26) | `CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann"` (manager.rs:921) | **内置硬编**, 双源验证同值 |
| ClientSecret | 无 (PKCE-only) | 无 (PKCE-only) | **不需要** (与 anthropic 同款, **比 gemini 简单** — 无需 env var) |
| AuthURL | `https://auth.openai.com/oauth/authorize` (line 24) | (同) | 内置硬编 |
| TokenURL | `https://auth.openai.com/oauth/token` (line 25) | `REFRESH_TOKEN_URL = ...` (line 94) | 内置硬编 |
| Scope | `"openid email profile offline_access"` (line 72) | (token_data 解析含 offline) | **内置** (无 env var override, 与 gemini 不同) |
| RedirectURI | `http://localhost:1455/auth/callback` (line 27) | loopback (具体 port 在 web_oauth 那边) | **loopback 默认**, admin allowlist 见 D-1 |
| OpenAI 特定 params | `prompt=login`, `id_token_add_organizations=true`, `codex_cli_simplified_flow=true` | (manager.rs 同款) | D-2 决策是否保留 |
| Token response | access_token + refresh_token + id_token | + chatgpt_user_id / chatgpt_plan_type / chatgpt_account_id | D-3 决策这些字段去哪 |

## 5. 风险登记

| Risk ID | Severity | Mitigation |
|---|---|---|
| R-CHG-MIMICRY-001 | S2 → Mandatory Roadmap | 本轮 D-4=A 不做 mimicry; codex_cli mimicry profile 已 wire test 但 Rust 生产 dispatch 待 F-2.3a runtime preflight 完成 |
| R-CHG-PROMPT-LOGIN-001 | S3 | `prompt=login` 强制每次重新登录 (CLIProxyAPI 同款) — 用户体验可能差; D-2 决策是否保留或改 `prompt=consent` (gemini 同款) |
| R-CHG-CODEX-SIMPLIFIED-001 | S3 | `codex_cli_simplified_flow=true` 是 OpenAI 给 Codex CLI 的特定 query param, HUAKAI 不是真 Codex CLI — 是否复用 (D-2) |
| R-CHG-CHATGPT-METADATA-001 | S3 | chatgpt_user_id / chatgpt_plan_type 字段进 cred (D-3); 用于 RedactedContext audit 还是 admin UI display |
| R-CHG-OPENAI-REFRESH-SSRF-001 | S1 (待 CHG-3 闭合) | OpenAIRefresh adapter 当前可能从 cred 读 endpoint 等 — CHG-3 类似 GEM-3 4 攻击面闭合 |

## 6. Owner 决策点

### D-1: redirect_uri 模式

- **A** loopback only (`http://localhost:1455/auth/callback`, CLIProxyAPI 同款)
- **B** admin server callback only
- **C** 双模式 loopback + admin allowlist (推荐, 与 anthropic D-2=C / gemini D-3=C 一致)

### D-2: OpenAI 特定 query params 处理

- **A** 保留 CLIProxyAPI 三个特定 params (`prompt=login` + `id_token_add_organizations=true` + `codex_cli_simplified_flow=true`) — 推荐, 与 CLIProxyAPI/openai-codex 双源同款, 兼容性最强
- **B** 只保留 OAuth standard params (移除 OpenAI 特定 params) — 风险: OpenAI 可能拒
- **C** 选项性: 内置默认 A 但允许 cfg override 移除 (灵活但增加复杂度)

### D-3: ChatGPT-specific token response 字段 (`chatgpt_user_id` / `chatgpt_plan_type` / `chatgpt_account_id`)

- **A** 全部持久化到 cred + RedactedContext (推荐, 用于 audit 与 admin UI 区分 ChatGPT plan 等级 Plus/Pro/Team/Enterprise)
- **B** 只持久化 chatgpt_account_id, 其他字段每次 refresh 重新拉
- **C** 都不持久化 (信任链最严, 但失去 plan 区分能力)

### D-4: mimicry release gate

- **A** 本轮 = "OAuth 接通", CHG-4 标 Mandatory Roadmap, token exchange 走 SSRF-protected standard transport — 推荐, 与 GEM-4 D-5=A 一致
- **B** 本轮一并上线反检测, 需 Rust §14b runtime preflight 接通 (工时大幅扩, 本切片不范围)

## 7. 工时 + 推荐起步

| Slice | 工时 | Commit groupings |
|---|---|---|
| CHG-1 真 Exchanger | 0.5 天 | 第 1 commit (与 CHG-2 一起) |
| CHG-2 Admin real-entry | 0.5 天 | 同上 |
| CHG-3 Refresh path + S1-D | 0.5-0.75 天 | 第 2 commit |
| CHG-4 mimicry (D-4) | 0 (D-4=A 仅 docs) / 1+ 天 (D-4=B) | 第 3 commit 或路线图 |
| CHG-5 Docs + gate | 0.25-0.5 天 | 第 3 commit |
| **合计 (D-4=A)** | **1.75-2.25 天** | 3 commits |

推荐起步: **Owner 拍 D-1/D-2/D-3/D-4 → CHG-1 + CHG-2 第一 commit (acquisition 闭环) → CHG-3 第二 commit (refresh + SSRF) → CHG-5 第三 commit (docs)**。

## Source files read (CLAUDE.md #11 specifier lane)

- `~/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go` (MIT E-LIC-009, CLIProxyAPI@HEAD)
- `~/refs/openai-codex/codex-rs/login/src/auth/manager.rs` (Apache-2.0, openai-codex@HEAD)
- HUAKAI 自有: backend/internal/credentialacq/{vendor_exchangers,types,anthropic_oauth,gemini_oauth,oauth_authorization_code}.go + backend/internal/credentialworker/{mode_refresh,adapters/openai}.go + backend/internal/gatewayhttp/admin_credential_acquisition_handler.go

Lane: claude-pm-spec
Time: 2026-05-27T08:50:00Z UTC
