# GEM-1 + GEM-2 实施 Spec — Gemini code_assist + google_one OAuth 接通

Lane: claude-pm-spec (账号敏感 / 信任链 invariant — Claude 写 spec, codex 写代码)
Time: 2026-05-27T05:30:00Z
Owner 决策依据: D-1=A (ClientID 内置 + Secret 运营员填), D-3=C (双模式 redirect_uri), D-4=A (默认 scope 三项), D-5=A (本轮只接 OAuth, mimicry 进路线图)

## 0. 范围 + 现状

**现状** (本 commit 起点 = d55fa24):

- `gemini/code_assist`, `gemini/google_one`: 在 [vendor_exchangers.go:40-41](backend/internal/credentialacq/vendor_exchangers.go#L40-L41) 注册 `NewPKCEFakeExchanger` — 占位假 exchanger, 不做真 OAuth code exchange
- `gemini/oauth` (operator-config 路径): 在 [vendor_exchangers.go:43](backend/internal/credentialacq/vendor_exchangers.go#L43) 注册 `authorizationCodeOAuthExchanger`, 已经走真 OAuth + SSRF-protected (641c9b3)
- ProfileBindings [types.go:165-166](backend/internal/credentialacq/types.go#L165-L166): code_assist / google_one ClientIdentitySource=PublicCLI

**范围**:

GEM-1: 把 `gemini/code_assist` + `gemini/google_one` 从 PKCEFake 升级到 **builtin-profile pattern OAuth exchanger** (类似 `claudeAIOAuthExchanger`), 内置 Google CLI 公开 ClientID, ClientSecret 从 operator-config 注入。

GEM-2: Admin callback handler real-entry integration test, 验证 ExchangeOAuthCodeWithStore 路径用 stored PKCE payload 真走 token exchange (不是 fake JSON 占位)。

**不在本 commit 范围**:

- GEM-3 refresh 路径 SSRF / S1-D gemini 闭合 (下一 commit)
- GEM-4 mimicry transport (D-5=A — 本轮不做, GEM-5 docs 中标 Mandatory Roadmap)
- GEM-5 parity matrix + risk register + reference ledger (separate commit)
- antigravity OAuth (Owner: "antigravity 暂时搁置 不做")

## 1. 设计 invariant

### 1.1 builtin-profile pattern (D-1=A 实现)

类似 [anthropic_oauth.go:116-150 builtinProfileConfig](backend/internal/credentialacq/anthropic_oauth.go#L116-L150), 但 **关键差异**: anthropic 的 `validateBuiltinProfile` 拒绝任何 ClientSecret (PKCE-only), gemini 必须 **要求** ClientSecret 非空 (RFC 8252 desktop CLI + Google 设计本意需要 secret, 即使 secret 是 public)。

```
geminiPublicCLIOAuthExchanger:
  - ClientID 写死内置常量 (从 CLIProxyAPI@HEAD:internal/auth/gemini/gemini_auth.go 取 fresh evidence)
  - ClientSecret 必须从 cfg.ClientSecret 注入, 不能空; validateBuiltinProfile 强制
  - AuthURL / TokenURL / Scopes 写死
  - RedirectURI 走 allowlist (loopback OR admin callback, 见 §1.2)
  - Source=ClientSourcePublicCLI (与 ProfileBinding 一致)
```

**为什么不复用 `authorizationCodeOAuthExchanger`**: 后者要求 `Source=ClientSourceOperatorConfig` 且 ClientID 也走 operator (validateOperatorPKCEConfig:232 强制), 与 D-1=A "ClientID 内置 + Secret operator" 的混合模式冲突。新建专门 exchanger 比改通用 exchanger 安全。

### 1.2 redirect_uri 双模式 allowlist (D-3=C)

允许的 RedirectURI **目标形态**有两种，但本切片只默认启用 loopback:
- **loopback**: `http://localhost:<port>/oauth2callback`, port ∈ [1024, 65535] (符合 RFC 8252 §7.3, 但本 commit 接受任意非系统 port; **不接受** `127.0.0.1` 字面 — Google OAuth 注册侧可能区分)
- **admin callback**: 需要 operator-config/ProfileBindings 静态注入完整 HTTPS callback URL allowlist (scheme + host + path 完全匹配), 并经过 `validateOAuthEndpointURL` (oauth_authorization_code.go:309) 静态检查 — scheme=https + host 非私网 / 非 metadata。本切片尚未接通该 wiring, 所以默认空 allowlist 拒绝所有 HTTPS admin redirect, 只保留 loopback；后续 GEM-X 接静态配置后再启用。

新增 helper `validateGeminiRedirectURI(raw string) error`:
```go
// 默认接 loopback (http://localhost:<port>/oauth2callback)；HTTPS admin callback 必须命中静态 allowlist。
// 任一不符 → ErrFeatureDisabled
```

判别性 fixture (CLAUDE.md #14 mutation test):
- "https://attacker.test/cb" → 必须拒 (host 非 allowlist, validateOAuthEndpointURL 通过但 D-3 allowlist 拒)
- "http://localhost:8085/oauth2callback" → 必须接
- "http://127.0.0.1:8085/oauth2callback" → 必须拒 (Google 区分 localhost vs 127.0.0.1, 见 CLIProxyAPI 选 localhost)
- "https://huakai.example/admin/oauth/gemini/callback" + 空 allowlist → 必须拒 (本切片默认关闭 admin HTTPS 模式)
- "https://huakai.example/admin/oauth/gemini/callback" + 静态 allowlist 命中 → 必须接 (admin 模式后续 wiring 可启用)
- "https://attacker.test/admin/oauth/gemini/callback" + 静态 allowlist 不命中 → 必须拒
- "https://192.168.1.1/cb" → 必须拒 (validateOAuthEndpointURL 已守, 这里二层)
- "http://localhost/" (无 port) → 必须拒 (要求 explicit port)

### 1.3 Builtin constants (codex 必须读 fresh evidence)

**Codex 任务**: 读 `~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go` 取以下常量 (CLAUDE.md #11 specifier lane):

- `geminiPublicCLIClientID` (Google desktop CLI 公开 ClientID, 格式 `<numeric>-<random>.apps.googleusercontent.com`)
- `geminiOAuthAuthURL` (https://accounts.google.com/o/oauth2/v2/auth 通常)
- `geminiOAuthTokenURL` (https://oauth2.googleapis.com/token 通常)
- `geminiOAuthScopes` (D-4=A: cloud-platform + userinfo.email + userinfo.profile)

**Clean-room 约束**: 不要逐字搬 CLIProxyAPI 函数名 / struct field / 注释 / 代码块。常量值 (ClientID 字符串、URL) 是 Google 公开 endpoint, **不是** CLIProxyAPI 创作 — 可以用, 但常量名要 HUAKAI 风格 (例: `geminiPublicCLIClientID`, 而 CLIProxyAPI 可能叫 `clientID`)。注释必须 HUAKAI 自创, 不抄。

记录引用: Source files read: `~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go` + commit SHA, Lane: specifier, Time: 2026-05-27 UTC。

### 1.4 信任链 invariant (P0 OAuth-only 绕过 + ANT-4 mimicry 借鉴)

参考 [anthropic_oauth.go:49-55 IsClaudeAIOAuthExchangerWithExplicitClient](backend/internal/credentialacq/anthropic_oauth.go#L49-L55), gemini exchanger 同样需要:

- `NewGeminiPublicCLIOAuthExchangerWithClient(client *http.Client) Exchanger` — wiring 时注入 SSRF-protected HTTP client
- `IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc Exchanger) bool` — wiring fail-loud 自检 helper (生产启动断言 client 非 nil)

**为什么这样**: HUAKAI 信任链原则 — OAuth token exchange 必须走受控 transport (SSRF-protected + 可观察), 防止 install 调用被删 / helper 退化导致 fallback http.DefaultClient 沉默通过。

ExchangeOAuthCode (无 store) 必须返回 `ErrOAuthExchangerMissing: gemini code_assist/google_one requires stored PKCE verifier` — 与 anthropic 同款防绕过 (P0).

## 2. 实现拆分

### 2.1 新文件: `backend/internal/credentialacq/gemini_oauth.go`

按照 anthropic_oauth.go pattern 设计, 关键差异点列在 §1.1。

骨架:

```go
package credentialacq

const (
    geminiPublicCLIClientID     = "<from-cliproxyapi-evidence>"
    geminiOAuthAuthURL          = "https://accounts.google.com/o/oauth2/v2/auth"
    geminiOAuthTokenURL         = "https://oauth2.googleapis.com/token"
    geminiOAuthScope            = "cloud-platform userinfo.email userinfo.profile"  // D-4=A
    geminiOAuthLoopbackRedirect = "http://localhost:8085/oauth2callback"  // CLIProxyAPI 同款 port; operator 可 override
    geminiApprovedProfileSource = "approved_builtin_profile_gemini_public_cli"
)

type geminiPublicCLIOAuthExchanger struct {
    now        func() time.Time
    httpClient *http.Client
    authMode   string  // "code_assist" 或 "google_one" — RedactedContext 区分
}

func newGeminiPublicCLIOAuthExchanger(authMode string) geminiPublicCLIOAuthExchanger { ... }

func NewGeminiPublicCLIOAuthExchangerWithClient(authMode string, client *http.Client) Exchanger { ... }

func IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc Exchanger) bool { ... }

func (e geminiPublicCLIOAuthExchanger) StartOAuthFlow(...) (...) {
    cfg = geminiBuiltinProfileConfig(cfg)
    if err := validateGeminiBuiltinProfile(cfg); err != nil { ... }
    in.Vendor = credentialstore.VendorGemini
    in.AuthMode = e.authMode
    return startStoredPKCEOAuthFlow(...)  // 复用 oauth_authorization_code.go 现成 flow
}

func (e geminiPublicCLIOAuthExchanger) ExchangeOAuthCode(...) (...) {
    return CredentialCandidate{}, fmt.Errorf("%w: gemini %s requires stored PKCE verifier", ErrOAuthExchangerMissing, e.authMode)
}

func (e geminiPublicCLIOAuthExchanger) ExchangeOAuthCodeWithStore(ctx, store, session, _, code) (...) {
    // 1. decryptStoredPKCEPayload (复用)
    // 2. 重新跑 geminiBuiltinProfileConfig + validateGeminiBuiltinProfile 防 stored payload 被改写绕过 (ANT-4 mimicry 借鉴)
    // 3. 校验 stored.RedirectURI 仍符合 §1.2 allowlist (validateGeminiRedirectURI)
    // 4. exchangeAuthorizationCodeForm — Google OAuth 用 application/x-www-form-urlencoded (不像 anthropic 用 JSON)
    // 5. tokenCandidatePayload + RedactedContext={"client_identity_source": geminiApprovedProfileSource}
    // 6. validateTokenShape(TokenShapeAnySessionOrAccess)
}

func geminiBuiltinProfileConfig(override OAuthClientConfig) OAuthClientConfig {
    cfg := OAuthClientConfig{
        ClientID: geminiPublicCLIClientID,
        AuthURL: geminiOAuthAuthURL,
        TokenURL: geminiOAuthTokenURL,
        RedirectURI: geminiOAuthLoopbackRedirect,
        Scopes: strings.Fields(geminiOAuthScope),
        Source: ClientSourcePublicCLI,
    }
    // ClientID/AuthURL/TokenURL/Scopes/Source 内置, override 不能改
    // ClientSecret 必须从 override 注入 (D-1=A)
    // RedirectURI 允许 override (D-3=C 双模式)
    if strings.TrimSpace(override.ClientSecret) != "" {
        cfg.ClientSecret = strings.TrimSpace(override.ClientSecret)
    }
    if strings.TrimSpace(override.RedirectURI) != "" {
        cfg.RedirectURI = strings.TrimSpace(override.RedirectURI)
    }
    if override.HTTPClient != nil {
        cfg.HTTPClient = override.HTTPClient
    }
    return cfg
}

func validateGeminiBuiltinProfile(cfg OAuthClientConfig) error {
    var mismatches []string
    if strings.TrimSpace(cfg.ClientID) != geminiPublicCLIClientID {
        mismatches = append(mismatches, "client_id")
    }
    if strings.TrimSpace(cfg.ClientSecret) == "" {
        mismatches = append(mismatches, "client_secret")  // D-1=A 强制
    }
    if strings.TrimSpace(cfg.AuthURL) != geminiOAuthAuthURL { mismatches = append(...) }
    if strings.TrimSpace(cfg.TokenURL) != geminiOAuthTokenURL { mismatches = append(...) }
    if strings.Join(trimmedFields(cfg.Scopes), " ") != geminiOAuthScope { mismatches = append(...) }
    if err := validateGeminiRedirectURI(cfg.RedirectURI); err != nil {
        mismatches = append(mismatches, fmt.Sprintf("redirect_uri (%v)", err))
    }
    if source := strings.TrimSpace(cfg.Source); source != "" && source != ClientSourcePublicCLI {
        mismatches = append(mismatches, "source")
    }
    if len(mismatches) > 0 {
        return fmt.Errorf("%w: gemini public CLI built-in profile mismatch: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
    }
    return nil
}

func validateGeminiRedirectURI(raw string) error {
    // §1.2 双模式 allowlist 判别逻辑
    parsed, err := url.Parse(strings.TrimSpace(raw))
    if err != nil { return ... }
    switch {
    case parsed.Scheme == "http":
        // 必须是 localhost + explicit port + path=/oauth2callback (CLIProxyAPI 同款)
        if parsed.Hostname() != "localhost" {
            return fmt.Errorf("http scheme requires host=localhost, got %q", parsed.Hostname())
        }
        if parsed.Port() == "" {
            return fmt.Errorf("loopback redirect requires explicit port")
        }
        // 接受任何 port (RFC 8252 推荐 ephemeral); 不强制 path 防 admin 集成弹性
        return nil
    case parsed.Scheme == "https":
        // admin server callback 模式: 走 validateOAuthEndpointURL 静态层
        return validateOAuthEndpointURL(raw)
    default:
        return fmt.Errorf("scheme=%q unsupported (require http loopback or https admin)", parsed.Scheme)
    }
}

// exchangeAuthorizationCodeForm: Google OAuth 用 form encoding, 与 anthropic JSON 不同
func (e geminiPublicCLIOAuthExchanger) exchangeAuthorizationCodeForm(ctx, payload, code) (oauthTokenResponse, error) {
    form := url.Values{}
    form.Set("grant_type", "authorization_code")
    form.Set("code", code)
    form.Set("redirect_uri", payload.RedirectURI)
    form.Set("client_id", geminiPublicCLIClientID)
    form.Set("client_secret", payload.ClientSecret)
    form.Set("code_verifier", payload.CodeVerifier)
    // POST 到 geminiOAuthTokenURL, Content-Type: application/x-www-form-urlencoded
    // ... 走 e.client().Do; resp.Body 限 1MB
    // 检查 status 2xx; Unmarshal oauthTokenResponse; AccessToken 必非空
}

func (e geminiPublicCLIOAuthExchanger) client() *http.Client {
    if e.httpClient != nil { return e.httpClient }
    return http.DefaultClient
}

func (e geminiPublicCLIOAuthExchanger) nowTime() time.Time { ... }
```

### 2.2 修改 vendor_exchangers.go (registration)

```go
// 旧 line 40-41:
register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))
register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))

// 新:
register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeCodeAssist))
register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne), newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeGoogleOne))
// antigravity 暂时保留 PKCEFake (Owner: "antigravity 搁置")
```

### 2.3 测试: `backend/internal/credentialacq/gemini_oauth_test.go` (新文件)

**判别性测试** (CLAUDE.md #14 — 每条测试单句描述它抓的缺陷):

1. **TestGeminiBuiltinProfileRejectsMissingClientSecret** — 缺陷: D-1=A 忘了强制 ClientSecret 必填 (回退到 anthropic 风格 PKCE-only). Mutation: 删 validateGeminiBuiltinProfile 的 ClientSecret check → 红.
2. **TestGeminiBuiltinProfileRejectsOverriddenClientID** — 缺陷: ClientID 被 operator override 绕过 builtin allowlist. Fixture: cfg.ClientID="attacker-cid", cfg.ClientSecret="filled". Mutation: builtinProfileConfig 改 ClientID override → 红.
3. **TestGeminiRedirectURIDualModeAllowlist** (table-driven) — §1.2 fixture 全部六条, 每条一个 sub-test. Mutation: 删 http vs https 分支 / 接受任意 host → 至少一条变红.
4. **TestGeminiPublicCLIOAuthExchangerExchangeOAuthCodeRequiresStore** — 缺陷: 无 store 路径漏了 fallback 到 FakeExchanger, 绕过真 token exchange. Mutation: ExchangeOAuthCode 改成 fallthrough 到 NewPKCEFakeExchanger → 红.
5. **TestGeminiOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint** — 缺陷: token endpoint 走错 host 或没用 form encoding. Mutation: 写错 TokenURL / 改 JSON body → mock RoundTripper 看到错误 host 或 Content-Type 不匹配 → 红.
   - 用 SwapOAuthIPLookupForTesting (auth helper) 保证不真 DNS lookup.
   - mock RoundTripper 验证 req.URL == geminiOAuthTokenURL + Content-Type: application/x-www-form-urlencoded + form 字段齐 (grant_type/code/redirect_uri/client_id/client_secret/code_verifier).
6. **TestGeminiOAuthCallbackSetsClientIdentitySourceInRedactedContext** — 缺陷: RedactedContext 漏了 client_identity_source, 信任链可观察性丢. Mutation: 删 RedactedContext 写 → 红.
7. **TestIsGeminiPublicCLIOAuthExchangerWithExplicitClientDistinguishesInjectedClient** — wiring fail-loud helper 判别. Mutation: 让 helper 总返 true → 红 (zero-value httpClient 应返 false).
8. **TestExchangeOAuthCodeWithStoreRevalidatesBuiltinProfileAfterDecrypt** — 缺陷 (ANT-4 借鉴): 攻击者改写 stored PKCE payload 的 ClientID/TokenURL, 解密后未再走 validateGeminiBuiltinProfile 直接信. Mutation: 把 §2.1 ExchangeOAuthCodeWithStore step 2 的 re-validate 删掉 → 红 (fixture: 解密出 attacker ClientID, 应被拒).

### 2.4 GEM-2 Admin real-entry integration test

新建或扩展 `backend/internal/credentialacq/gemini_oauth_integration_test.go`:

- **TestGeminiCodeAssistAdminCallbackEndToEnd** — 模拟 admin server callback path:
  - Setup: 在 ephemeral PG (走 sandbox PG, 见 [[reference_local_pg_verification]]) 起 PostgresSessionStore
  - Step 1: StartOAuthFlow(in.Vendor=gemini, in.AuthMode=code_assist, cfg.ClientSecret="operator-secret", cfg.RedirectURI="https://huakai.example/admin/oauth/gemini/callback")
  - Step 2: 拿 session.ID + state, 模拟 admin server 收到 callback (code=mock-code), 调 ExchangeOAuthCodeWithStore
  - Step 3: mock RoundTripper 返合法 token response, 验证 CredentialCandidate.Payload 含 access_token + refresh_token + client_identity_source
  - Mutation: 让 session encryption AAD 被改 (PostgresSessionStore 解密失败) → 红 (decryptStoredPKCEPayload 报错)

**判别力**: 这条测试如果只看"无错误"就过, 但 fixture 必须断言 Payload 含 client_identity_source 且非 fake JSON 占位; 改 ExchangeOAuthCodeWithStore 回退到 fake → 红.

## 3. Codex 实施指令

**Lane**: executor (clean-room paraphrase + code-write)

**Source files to read** (CLAUDE.md #11 specifier lane allowed read):
- `~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go` (MIT, 拿 ClientID / AuthURL / TokenURL / Scope evidence)
- `backend/internal/credentialacq/anthropic_oauth.go` (HUAKAI 自己写的 pattern 模板)
- `backend/internal/credentialacq/oauth_authorization_code.go` (helpers 复用)
- `backend/internal/credentialacq/oauth.go` (OAuthClientConfig struct + BuildAuthorizeURL)
- `backend/internal/credentialacq/types.go` (ProfileBindings + ErrFeatureDisabled / ErrOAuthExchangerMissing)
- `backend/internal/credentialacq/anthropic_oauth_test.go` (test pattern)
- `backend/internal/auth/antigravity_token_provider.go` (NewSSRFProtectedOAuthClient + SwapOAuthIPLookupForTesting helpers)

**禁止**: 读 sub2api / new-api / all-api-hub / one-api (LGPL)

**Clean-room**:
- 常量值 (ClientID 字符串、Google endpoint URL) 可用 — 这是 Google 公开 endpoint
- 函数名 / struct field / 注释 / 代码块结构 **必须** HUAKAI 风格, 不抄 CLIProxyAPI
- 注释全部中文 ([[feedback_chinese_comments]])
- Source files read 在 commit message 中列出 + Lane + Time

**测试纪律** (CLAUDE.md #14):
- 每条测试单句描述抓的缺陷写注释
- 每条测试做 mutation 自检 (在测试运行前手动验证: 改实现使缺陷出现 → 测试必须变红)
- 不用 nil-returning stub 屏蔽真风险

**Per-commit review** (CLAUDE.md #8): 实施完成后 BEFORE commit 跑 `codex exec review --uncommitted --full-auto --sandbox read-only`, 2-round cap, S0/S1 阻塞, S2/S3 记票.

**不要 commit**: 写完代码 + 测试, **不**调 git commit; Claude 手动 commit (规则 [[feedback_codex_overstep_git_guard]]).

## 4. 验收

```
go build ./...
go test ./backend/internal/credentialacq/... -count=1 -run "Gemini"
go test ./backend/internal/credentialacq/... -count=1  # 全包 regression
```

- 所有 8 个 GEM-1 测试 + 1 个 GEM-2 integration test 绿
- 任何既有 gemini 相关测试 (oauth_test.go:166) 调整为新 exchanger 后保持绿 (或调整 fixture)
- mutation 自检每条测试都能在它声明的 mutation 下变红 (commit message 列证据)
- 全包 `go test ./...` 绿
