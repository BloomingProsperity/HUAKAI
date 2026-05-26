# 2026-05-26 Anthropic `claude_ai_oauth` 真 OAuth 接通 — Claude Lane

> Parallel-draft plan (CLAUDE.md #10), 与 `2026-05-26-anthropic-claude-oauth-codex.md` 互不参看。
> Owner 2026-05-26 拍板: 主线做 claude/gemini/codex 三家, claude_ai_oauth 起步, 然后 gemini code_assist/google_one (antigravity 搁置)。

## 1. 现状

| 模块 | 现状 | 评价 |
| --- | --- | --- |
| [vendor_exchangers.go:39](backend/internal/credentialacq/vendor_exchangers.go#L39) | `NewPKCEFakeExchanger(TokenShapeAccessRefresh)` | **fake** — admin 用户加 Claude 账号时 HUAKAI 不真去 Anthropic 兑换 token,只 parse 用户手粘的 JSON |
| [provider/anthropic/oauth_session.go](backend/internal/provider/anthropic/oauth_session.go) | 出站 session adapter | OK,接 access_token 出 api.anthropic.com |
| [provider/anthropic/passthrough.go](backend/internal/provider/anthropic/passthrough.go) | API key 透传 | OK,与 claude_ai_oauth 无关 |
| `provider/anthropic/refresher.go` | **不存在** | 缺 — Claude OAuth refresh_token grant 没接 |
| `authorizationCodeOAuthExchanger` (acq) | gemini/oauth 用的 real exchanger | 要求 operator-config endpoint,不适合 claude_ai_oauth(Anthropic 公开 endpoint 固定) |

## 2. 缺口

### 缺口 A — 真 OAuth Exchanger
fake → 真。需要新建 `publicCLIOAuthExchanger` 类型(或 `claudeAIOAuthExchanger`),硬编 public Anthropic Claude Code CLI 客户端身份:
- `AuthURL = "https://claude.ai/oauth/authorize"`
- `TokenURL = "https://api.anthropic.com/v1/oauth/token"`
- `ClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"` (公开 Claude Code CLI public client)
- `RedirectURI` = HUAKAI admin callback (localhost 模式或 admin 后台 callback)
- `Scope` = `org:create_api_key user:profile user:inference` (per CLIProxyAPI 观察)

### 缺口 B — Refresher
新建 `provider/anthropic/refresher.go`,POST `TokenURL` grant_type=refresh_token,接 anthropic.RefreshError 分类(auth_expired / rate_limit / transient)。复用 [credentialacq.WithRefreshLock](backend/internal/credentialacq/refresh_lock.go) advisory lock + [postgres_store SaveRefreshSuccess](backend/internal/credentialstore/postgres_store.go#L682) (本 session `63c7708` 修过 CAS race)。

### 缺口 C — refresh_adapter 注册
[credentialworker/mode_refresh.go DefaultModeAdapterRegistry](backend/internal/credentialworker/mode_refresh.go) 当前 `claude_ai_oauth` 没注册 worker adapter (跟 cursor C1 同病). 加 `legacyOAuthModeAdapter{providerName: "anthropic", adapter: anthropic.OAuthRefresh{...}}`。

### 缺口 D — Tests (避免 cursor C1 判别性假阳性覆辙)
- helper 层 test: 直接调 ValidateOAuthConfig (类似 cursor bootstrap_test) — **不够,必须有 ↓**
- **真实入口** test: 通过 `StartOAuthFlow` → `CompleteOAuthCallback` 通用 admin 入口验证 fail-closed + 成功路径
- mutation 自检: 临时 break ClientID → test 必红;break TokenURL → test 必红
- PG integration test: credential round trip,refresh CAS race 仍守

## 3. Slice 切片 (3 个 ANT-*)

### ANT-1 — 真 OAuth Exchanger 替换 fake (1.5 天)
- 新文件 `backend/internal/credentialacq/anthropic_oauth_exchanger.go`(credentialacq 不在冻结)
- 类型 `claudeAIOAuthExchanger`:
  - `StartOAuthFlow`: 复用 `startStoredPKCEOAuthFlow`,但 cfg 硬编 AuthURL/TokenURL/ClientID/Scope(不走 validateOperatorPKCEConfig)
  - `ExchangeOAuthCodeWithStore`: 复用 `exchangeAuthorizationCode` pattern,POST 拿 access_token + refresh_token + expires_in
  - `tokenCandidatePayload` 写 `{"access_token": ..., "refresh_token": ..., "expires_at": ...}`
- vendor_exchangers.go:39 替换 `NewPKCEFakeExchanger(TokenShapeAccessRefresh)` → `newClaudeAIOAuthExchanger()`
- 判别 test:
  - `TestClaudeAIOAuthExchangerHardcodedClientID` — assertion fixed ClientID = `9d1c250a-...`
  - `TestClaudeAIOAuthExchangeRejectsInvalidGrant` — mock HTTP 上游返 `{"error":"invalid_grant"}` → ExchangeOAuthCode 返 ErrAuthExpired
  - `TestClaudeAIOAuthAdminFlowFailsClosedWithoutCode` — **真实入口**:`StartOAuthFlow` 后 callback 缺 code → 必报错
  - mutation: 临时 break ClientID → 上游会拒,test 必红

### ANT-2 — Refresher (1 天)
- 新文件 `backend/internal/provider/anthropic/refresher.go` (类似 cursor refresher.go 模式)
- `RefreshAdapter.RefreshForProvider`: POST TokenURL form `grant_type=refresh_token&refresh_token=X&client_id=...`
- HTTP 错误分类 (跟 cursor/refresher.go 同 pattern):
  - 401 / `invalid_grant` → ErrAuthExpired (state=revoked)
  - 429 → ErrRateLimited + Retry-After
  - 5xx → ErrTransient
- `mode_refresh.go DefaultModeAdapterRegistry` 加 `legacyOAuthModeAdapter{providerName: "anthropic", adapter: anthropic.OAuthRefresh{}}`
- 判别 test:
  - `TestAnthropicRefreshRotatesAccessToken` — 模拟 refresh 成功,新 access_token 写入 store
  - `TestAnthropicRefreshInvalidGrantSetsAuthExpired` — `{"error":"invalid_grant"}` → state 标 revoked,failure_class=auth_expired
  - PG integration: refresh + delete race 仍守 (SaveRefreshSuccess 已加 deleted_at IS NULL by `63c7708`)

### ANT-3 — End-to-end + 文档 (0.5 天)
- 跑全套 + Owner Docker fresh PG verify
- 更新 `docs/03_FEATURE_PARITY_MATRIX.md` (anthropic/claude_ai_oauth: fake → real)
- 更新 `docs/10_RISK_REGISTER.md` (anthropic OAuth 风险登记)
- per-commit codex review ≤ 2 轮

## 4. 参考项目对照 (CLAUDE.md #15)

| 主题 | 参考项目 cite | HUAKAI delta |
| --- | --- | --- |
| Public CLI ClientID | CLIProxyAPI `~/refs/CLIProxyAPI/internal/auth/claude/anthropic_auth.go:25-28` 硬编 `9d1c250a-e61b-44d9-88ed-5944d1962f5e` + AuthURL `claude.ai/oauth/authorize` + TokenURL `api.anthropic.com/v1/oauth/token` | HUAKAI 同款 ClientID/endpoint(对齐行业;不能创新,这是 Anthropic 公开 CLI),**架构升级**:exchanger 进 credentialacq 统一注册,refresh 走 advisory lock + storm control + audit outcome |
| PKCE flow shape | CLIProxyAPI `anthropic_auth.go:178-207` (GenerateAuthURL state + S256 challenge) | HUAKAI 已有 `startStoredPKCEOAuthFlow` (encrypted verifier),**算法升级**:PKCE verifier 入 encrypted PostgresSessionStore 而非内存(防 admin 进程崩重启丢) |
| Token exchange | CLIProxyAPI `anthropic_auth.go:247-269` (POST JSON to TokenURL) | HUAKAI 同 endpoint,**生态升级**:exchange 失败进 audit + DLQ(信任链原则 [[project_core_trust_chain_differentiator]]) |
| Refresh shape | CLIProxyAPI `anthropic_auth.go:365-375` (refresh_token grant) | HUAKAI 同 shape,**生态升级**:refresh advisory lock(防 thundering herd) + 失败分类 audit (auth_expired/rate_limit/transient) |
| (LGPL ref) sub2api / new-api | **禁读** (LGPL 不能借鉴) | n/a |

## 5. 风险登记

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | Anthropic 改 public CLI ClientID(假设永远不变) | 中 | 监控 CLIProxyAPI / sub2api 是否变;若变需热改硬编 + 评估是否动 operator-config |
| R2 | Claude Code CLI scope (`org:create_api_key`) 可能 deprecate | 低 | Anthropic 公开变更走 deprecation cycle,先 audit log 标记再 follow-up |
| R3 | refresh_token 长期失效(用户在 claude.ai 改密) | 已知 | invalid_grant → state=revoked,admin UI 引导重登 |
| R4 | redirect_uri 模式不对 vendor 拒(`http://localhost:port/callback` vs callback URI scheme) | 中 | 跟 CLIProxyAPI 同款 `localhost:54545/callback` 或 HUAKAI admin callback;ANT-1 test 必跑成功路径 |
| R5 | 漏跑 ./... 全量(cursor C1 教训) | 高(过程风险) | ANT-1/2/3 commit 前必 `go test ./...` 全量,evidence 进 commit body |

## 6. Owner 决策点

### D-1: redirect_uri 模式
- **A** `http://localhost:54545/callback`(跟 CLIProxyAPI 同款 — 推荐)
- **B** HUAKAI admin 后台 callback URI(需 admin UI 端点支持)
- **C** operator-configurable(过度灵活,增 admin friction)

参考: CLIProxyAPI `anthropic_auth.go:28` 用 localhost:54545; sub2api 不可读 (LGPL); litellm 是 BYOK 不走 OAuth.

### D-2: scope 范围
- **A** `org:create_api_key user:profile user:inference`(跟 CLIProxyAPI 同款 — 推荐)
- **B** 仅 `user:inference`(最小权限,但可能 vendor 拒)
- **C** 加更多 scope(无必要)

### D-3: refresh_token rotation 时机
- **A** TTL 临 expiry 前 10 min refresh(跟 cursor 同模式 — 推荐)
- **B** 401 时被动 refresh
- **C** 每次出站前主动 refresh(过度)

## 7. 工时 + 起步建议

| Slice | 工时 | 谁 |
| --- | --- | --- |
| ANT-1 真 Exchanger | 1.5 天 | codex 实施 + Claude review |
| ANT-2 Refresher | 1 天 | codex + Claude review |
| ANT-3 E2E + docs | 0.5 天 | codex + Claude review |
| **合计** | **3 天** | |

推荐起步: **ANT-1 ASAP**(无 prestudy 阻塞,Owner 拍 D-1/D-2/D-3 即可开)。ANT-2/3 ANT-1 落地后连开。

---

Source files read:
- `/home/codex/HUAKAI/backend/internal/credentialacq/vendor_exchangers.go` (lines 39, 166-205)
- `/home/codex/HUAKAI/backend/internal/credentialacq/oauth_authorization_code.go` (lines 42, 49-92, 210)
- `/home/codex/HUAKAI/backend/internal/credentialacq/oauth.go` (lines 31, 90-91, 184)
- `/home/codex/HUAKAI/backend/internal/credentialacq/oauth_devicecode.go` (lines 23, 183, 247)
- `/home/codex/HUAKAI/backend/internal/provider/anthropic/oauth_session.go` (grep)
- `/home/codex/HUAKAI/backend/internal/credentialstore/postgres_store.go:682` (SaveRefreshSuccess)
- `/home/codex/refs/CLIProxyAPI/internal/auth/claude/anthropic_auth.go` (lines 25-28, 178-207, 247-269, 365-375)

Lane: claude-specifier
Time: 2026-05-26T15:00:00Z
