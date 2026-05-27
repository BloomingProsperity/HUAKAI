# GEM-3 实施 Spec — Gemini Refresh Path SSRF + S1-D 闭合

Lane: claude-pm-spec (账号 / 信任链敏感 — Claude 写 spec, codex 写代码)
Time: 2026-05-27T05:45:00Z (草稿; GEM-1+2 落地后 finalize)
依赖: GEM-1+2 必须先完成 (本 spec 依赖 gemini_oauth.go 的 builtin ClientID 常量)
Owner 决策依据: D-2=A (撤回 cred 取信, HUAKAI-maintained family→ClientID mapping)

## 0. 范围

修 [backend/internal/credentialworker/adapters/gemini.go:46-96](backend/internal/credentialworker/adapters/gemini.go#L46-L96) 的 4 个 S1-D 信任链攻击面, 把 anthropic ANT-3 R2 S2 / openai (commit 57356e4 + 641c9b3) 的同款修复扩到 gemini refresh 路径。

## 1. 攻击面盘点

### A1. oauth_token_endpoint 从 cred 读 (P0 SSRF — 同 ANT-3 c201cb4 已修的 anthropic)

[gemini.go:67](backend/internal/credentialworker/adapters/gemini.go#L67): `firstNonEmpty(r.Endpoint, credentialString(cred, "oauth_token_endpoint"), defaultGeminiTokenEndpoint)`

攻击者 plant cred 里 `oauth_token_endpoint=http://attacker.test/...`, refresh 时 OAuth token + refresh_token + client_secret 全部明文渗出。

**修**: 与 anthropic 一致, **只信 r.Endpoint** (operator-config 注入) 或 `defaultGeminiTokenEndpoint`, 不读 cred:
```go
endpoint := firstNonEmpty(r.Endpoint, defaultGeminiTokenEndpoint)
```

### A2. client_secret 从 cred 读 (P0 SSRF + 信任链反转)

[gemini.go:63](backend/internal/credentialworker/adapters/gemini.go#L63): `firstNonEmpty(r.ClientSecret, credentialString(cred, "client_secret"))`

D-1=A 要求 ClientSecret 走 operator-config; cred 里读 client_secret 是回退路径, 攻击者可 plant attacker secret 让 refresh 发到 attacker controlled 客户端。

**修**: **只信 r.ClientSecret**, 不读 cred:
```go
if clientSecret := strings.TrimSpace(r.ClientSecret); clientSecret != "" {
    form.Set("client_secret", clientSecret)
}
```

### A3. fallback_client_id 从 cred 读 (S1-D 主线)

[gemini.go:70](backend/internal/credentialworker/adapters/gemini.go#L70): `credentialString(cred, "fallback_client_id")`

ApprovedGeminiCrossClientFallback 只白名单 from→to family **字符串名字**, 没校验 fallback_client_id **字符串值**。攻击者 plant `fallback_client_family=google_one` + `fallback_client_id=attacker-cid`, refresh 失败触发 cross-client fallback, 真 Google endpoint 收到 attacker ClientID 发起的 grant — 取决于 Google 侧策略, 可能换出 attacker 控制的 token 或泄露 refresh_token 元数据。

**修** (D-2=A): HUAKAI-maintained family→ClientID 内置 mapping, 撤回 cred 取信:
```go
// gemini.go 新 helper (常量值在 GEM-1 实现的 gemini_oauth.go 内置)
// HUAKAI 自维护跨 family 内置 ClientID, fallback 时按 toClient 查表, 不读 cred
func approvedGeminiCrossClientID(toClient string) string {
    switch normalizeGeminiClientFamily(toClient) {
    case "code_assist":
        return credentialacq.GeminiPublicCLIClientIDForCodeAssist()  // GEM-1 export
    case "google_one":
        return credentialacq.GeminiPublicCLIClientIDForGoogleOne()
    case "ai_studio":
        return credentialacq.GeminiPublicCLIClientIDForAIStudio()
    }
    return ""
}
```
**注**: 如果 GEM-1 实现只用单一 ClientID(Google CLI 公开 secret 设计上是单一 ClientID 跨所有 desktop CLI feature), 那么 fallback 用的还是同一个 ClientID, fallback_client_id 实际只是 vendor-side feature flag 区分, 不需要不同 ClientID。这种情况下 helper 可简化为:
```go
func approvedGeminiCrossClientID(toClient string) string {
    if normalizeGeminiClientFamily(toClient) == "" { return "" }
    return credentialacq.GeminiPublicCLIClientID  // 单一内置
}
```
**Codex 任务**: 读 `~/refs/CLIProxyAPI/internal/auth/gemini/` 看 CLIProxyAPI 是用 single ClientID 还是 multi-family ClientID, 按 evidence 实现。

### A4. HTTPClient 默认 http.DefaultClient (无 SSRF guard)

[gemini.go:99-102](backend/internal/credentialworker/adapters/gemini.go#L99-L102): `if r.HTTPClient != nil { return r.HTTPClient } return http.DefaultClient`

A1 修后 endpoint 不来自 cred 也 P0 SSRF 已闭, 但 defense-in-depth 仍应在 wiring 时注入 `auth.NewSSRFProtectedOAuthClient`, 防 operator-config endpoint 被改 / DNS rebind。

**修**: wiring 处 (`backend/internal/credentialworker/mode_refresh.go:79-82`) 注入:
```go
register(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, legacyOAuthModeAdapter{
    providerName: "gemini",
    adapter: adapters.GeminiRefresh{
        AllowCrossClientFallback: true,
        SourceClientFamily:       "code_assist",
        TierCacheTTL:             24 * time.Hour,
        HTTPClient:               auth.NewSSRFProtectedOAuthClient(http.DefaultClient),  // 新增
        ClientID:                 credentialacq.GeminiPublicCLIClientID,  // GEM-1 export
        ClientSecret:             /* operator-config 注入, 见 §2.1 */,
        Endpoint:                 adapters.DefaultGeminiTokenEndpoint,  // 或 operator override
    },
})
```

## 2. 信任链元数据 (mergeTokenResponse scrub 已 cover)

ANT-3 R2 S2 (commit 57356e4) 的 mergeTokenResponse scrub 已经清 `oauth_token_endpoint` / `client_secret` / `fallback_client_id` / `setup_token` / `long_lived_setup_token` 等 hostile 字段。GEM-3 refresh 后写回 store 走同一 mergeTokenResponse 路径, **不需要新增 scrub** — 已经 covered。

但需要新增测试断言: gemini refresh 写回的 cred 也无这些 hostile 字段(已经被 mergeTokenResponse 删, 但加 gemini 专属 regression test 防 mergeTokenResponse helper 被回退)。

### 2.1 ClientSecret operator-config 接入

D-1=A 要求 ClientSecret 走 operator-config。本切片需要:
- `mode_refresh.go` wiring 读 operator-config 的 gemini secret(具体来源: 已有 OAuthClientConfig 还是新增 RefresherConfig 字段, 取决于现状 — codex 调研)
- 没配 ClientSecret 时 refresh 必须 fail-closed, **不**回退到 cred.client_secret

## 3. 测试

新建 `backend/internal/credentialworker/adapters/gemini_ssrf_test.go`:

1. **TestGeminiRefreshIgnoresHostileOAuthTokenEndpoint** — A1 mutation 守门
   - cred plant `oauth_token_endpoint=http://attacker.test/...`, r.Endpoint=`https://oauth2.googleapis.com/token`
   - mock RoundTripper 抓到 req.URL.Host == oauth2.googleapis.com (不是 attacker.test) → 绿
   - Mutation: 改 A1 的修复回到 firstNonEmpty(..., cred[...]) → req 发到 attacker.test → 红

2. **TestGeminiRefreshIgnoresHostileClientSecret** — A2 mutation 守门
   - cred plant `client_secret=leaked-from-cred`, r.ClientSecret="operator-secret"
   - mock 抓到 form.Get("client_secret") == "operator-secret" → 绿
   - Mutation: 改 A2 回到 firstNonEmpty(r.ClientSecret, cred[client_secret]) → red

3. **TestGeminiRefreshFallbackUsesBuiltinClientIDNotCredField** — A3 mutation 守门
   - cred plant `fallback_client_id=attacker-cid`, fallback_client_family=google_one, refresh primary 失败
   - mock 抓到 retry 用的 client_id == GeminiPublicCLIClientID (or whatever GEM-1 exports) , NOT attacker-cid → 绿
   - Mutation: 改 A3 回到 cred[fallback_client_id] → 红

4. **TestGeminiRefreshClientSecretFromCredRejectedWhenOperatorNotConfigured** — 信任链 fail-closed
   - r.ClientSecret="" (operator 没配), cred plant client_secret="leaked"
   - refresh 必须返 ErrFeatureDisabled or 类似 → 不发出 request (用 mock 验证 0 calls) → 绿
   - Mutation: A2 修复改成只看 r.ClientSecret != "" 时设, 但 fallback 仍走 cred → 红

5. **TestGeminiRefreshHostileCredScrubedAfterRefresh** (ANT-3 R2 S2 regression)
   - cred plant 5 hostile keys + 完整 refresh path mock token response
   - 验证返回的 newCredential 不含 hostile keys (mergeTokenResponse scrub 仍生效)
   - Mutation: mergeTokenResponse 删 scrub → 红 (但这是 adapters/openai.go scrub_test.go 已守, 本 test 是 gemini regression)

6. **TestGeminiRefreshHTTPClientIsSSRFProtectedAtWiring** — wiring fail-loud
   - 跑 DefaultModeAdapterRegistry().Lookup(gemini, code_assist), assert adapter 类型是 legacyOAuthModeAdapter 包了 GeminiRefresh 且 GeminiRefresh.HTTPClient != nil
   - Mutation: mode_refresh.go 注册时移除 HTTPClient → 红

## 4. Codex 实施指令

**预备**: GEM-1+2 落地后才启动 GEM-3, 因为本 spec 引用 GEM-1 export 的常量 (`credentialacq.GeminiPublicCLIClientID` etc.)。

**Source files to read**:
- `~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go` (single ClientID vs multi-family 判定)
- `backend/internal/credentialworker/adapters/gemini.go` (修改对象)
- `backend/internal/credentialworker/adapters/openai.go` (mergeTokenResponse scrub 参考)
- `backend/internal/credentialworker/adapters/anthropic.go` (P0 SSRF 修复 pattern)
- `backend/internal/credentialworker/mode_refresh.go` (wiring 改)
- `backend/internal/credentialacq/gemini_oauth.go` (GEM-1 落地后 export 的常量)
- `backend/internal/auth/antigravity_token_provider.go` (NewSSRFProtectedOAuthClient helper)

**禁止**: LGPL refs (sub2api / new-api / all-api-hub / one-api)

**Clean-room**: 常量值可用; 函数 / 注释 / 结构 HUAKAI 自创

**测试纪律**: 每条测试 mutation 自检, fixture 必须能让缺陷出现时红

**不要 commit**

## 5. 验收

```bash
go build ./...
go test ./backend/internal/credentialworker/... -count=1 -run "GeminiRefresh\|GeminiSSRF"
go test ./... -count=1
```

- 6 个 GEM-3 测试绿
- 现有 gemini refresh 测试调整后保持绿
- 全包 regression 绿
- mutation 自检列证据 in commit message

## 6. GEM-1 实际现状摸底 (2026-05-27 89947e0 落地后 finalize)

GEM-1 实际 export (gemini_oauth.go 89947e0):
- `geminiPublicCLIClientID` (**lowercase 私有**) = `"681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"`
- `geminiOAuthTokenURL` (**lowercase 私有**) = `"https://oauth2.googleapis.com/token"`
- `NewGeminiPublicCLIOAuthExchangerWithClient` (exported, 但不暴露常量)

GEM-3 选项:
- **A** (推荐): GEM-3 实施时 export `GeminiPublicCLIClientID` + `DefaultGeminiTokenEndpoint` 在 credentialacq 包, 让 adapters 复用同一常量值 (避免 drift)
- B: adapters 包内独立定义同样字符串 (易 drift, 不推荐)

mode_refresh.go 现状 (line 79-81):
```go
register(VendorGemini, AuthModeCodeAssist, legacyOAuthModeAdapter{
    providerName: "gemini",
    adapter: adapters.GeminiRefresh{AllowCrossClientFallback: true, SourceClientFamily: "code_assist", TierCacheTTL: 24 * time.Hour},
})
```
**没注入** Endpoint / ClientID / ClientSecret / HTTPClient — GeminiRefresh.RefreshForProvider 内部从 cred 读 (S1-D 攻击面)。

GEM-3 wiring 改:
- code_assist + google_one 改成新的 `builtinClientOAuthModeAdapter` (或直接扩 legacyOAuthModeAdapter 注入 builtin 字段) — codex 自行决定 pattern, 但必须满足:
  - Endpoint 必须 = `adapters.DefaultGeminiTokenEndpoint` (新 export) 或 `credentialacq.DefaultGeminiTokenEndpoint`
  - ClientID 必须 = `credentialacq.GeminiPublicCLIClientID` (GEM-3 升 export)
  - ClientSecret 从 operator-config / env var 读 (类似 mode_refresh.go:82-93 operatorOAuthModeAdapter pattern; lazy load 防启动 race)
  - HTTPClient 必须 = `auth.NewSSRFProtectedOAuthClient(http.DefaultClient)`
- antigravity 暂跳 (Owner 决策)

ClientSecret 来源 (codex 调研 + 决策):
- 现有 `appconfig.VendorOAuthGemini` + `geminiOAuthClientIDEnv` / `geminiOAuthTokenURLEnv` 已有 env mapping
- 新增 `geminiOAuthClientSecretEnv` env var (e.g. `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET`) 或 appconfig 字段
- 启动时缺 ClientSecret 配置 → wiring fail-loud (类似 GEM-1 assertGeminiPublicCLIOAuthExchangersHaveHTTPClient pattern)

Per-commit codex review 强制: CLAUDE.md #8 2-round cap, S0/S1 阻塞。
