# 2026-05-27 Gemini code_assist + google_one 真 OAuth 接通 — Claude Lane

> Parallel-draft plan (CLAUDE.md #10), 与 `2026-05-27-gemini-oauth-codex.md` 互不参看。
> Owner 2026-05-26 主线 = claude/gemini/codex 三家; anthropic 已落地 (commits 827de58/c201cb4/39c66a3, public CLI ID + mimicry uTLS + 信任链 SSRF 修复); 本切片轮到 gemini。
> Antigravity 暂搁 (Owner 5-26 决策, 不在本 plan 范围)。

## 1. 现状

| 模块 | 现状 | 评价 |
| --- | --- | --- |
| [vendor_exchangers.go:40-41](../../../backend/internal/credentialacq/vendor_exchangers.go#L40-L41) | gemini/code_assist + gemini/google_one 用 `NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)` | **fake** — 同 ANT-1 修复前的 anthropic 病况 |
| [mode_refresh.go:79-80](../../../backend/internal/credentialworker/mode_refresh.go#L79-L80) | `GeminiRefresh{AllowCrossClientFallback: true, SourceClientFamily: "..."}`, ClientID/ClientSecret/Endpoint **全空** | 生产 refresh 依赖 credential payload 提供 client_id / client_secret / oauth_token_endpoint (S1-D defer) |
| [adapters/gemini.go](../../../backend/internal/credentialworker/adapters/gemini.go) | refresh body 读 `credentialString(cred, "client_id")` / `"client_secret"` / `"oauth_token_endpoint"` fallback | 同 ANT-3 修复前的 anthropic.go SSRF 病况, 跨 client fallback 走 `cred["fallback_client_id"]` 也是 cred payload 攻击面 |
| [provider/gemini/**](../../../backend/internal/provider/gemini) | 出站 session adapter | OK, 接 access_token 出 google API |
| 跨 client fallback | `ApprovedGeminiCrossClientFallback` 白名单 (code_assist↔ai_studio / google_one / 互通) | family 校验有, 但 client_id 字符串值来自 cred (SSRF) |

## 2. 缺口

### 缺口 A — 真 OAuth Exchanger (类 ANT-1)
fake → 真 dedicated exchanger 用 Google CLI public OAuth profile:
- AuthURL: `https://accounts.google.com/o/oauth2/v2/auth`
- TokenURL: `https://oauth2.googleapis.com/token`
- ClientID: `681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com` (Google CLI public client; per CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:31)
- ClientSecret: `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` (per CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:32; 注: "Public CLI ClientSecret" 是 Google 已知约定 — 不是真 secret, 用于 desktop / installed-app flow)
- Scope: `cloud-platform userinfo.email userinfo.profile`
- RedirectURI: `http://localhost:8085/oauth2callback` (CLIProxyAPI 同款 loopback) + admin server callback (双模式 同 ANT D-2=C)

### 缺口 B — Refresh adapter SSRF 修 (类 ANT-3 + S1-D 闭合)
- 移除 `credentialString(cred, "client_id" | "client_secret" | "oauth_token_endpoint")` fallback
- 改成 `firstNonEmpty(r.ClientID, geminiPublicCLIClientID)` 同款
- cross-client fallback 的 `cred["fallback_client_id"]` **保留** family 白名单校验, 但 **不取 cred 提供的 client_id 字符串**; 改成 internal mapping `cross_client_id_map[targetFamily]` 由 HUAKAI 自定义 (Owner 决策点 D-2)

### 缺口 C — mimicry transport 接回 (类 ANT-4)
当前 `internal/anthropicoauth/transport.go` 有 mimicry sidecar (`anthropic_cli_mimicry_v1`); gemini 是否有对应 mimicry profile? 看 `internal/transport/mimicry/` 是否有 `gemini_cli_mimicry_v1` 或类似 sidecar profile。若无: 留 follow-up + 文档化降级路径 (Owner 决策点 D-3)。

### 缺口 D — Tests (cursor C1 教训 + ANT-2 模板)
- helper 层 test: validateBuiltinProfile fail-closed (类 ANT-1 mutation 1-2)
- admin real-entry test: POST `/admin/v1/credentials/oauth-init` 走真 dispatch
- mutation 自检: 临时改 builtin ClientID / TokenURL 各让 test 红
- SSRF 测试: credential payload 含 attacker client_id / oauth_token_endpoint → refresh 仍用 builtin

## 3. Slice 切片 (5 个 GEM-*)

### GEM-1 — 真 OAuth Exchanger 替换 fake (1.5 天)
- 新文件 [backend/internal/credentialacq/gemini_oauth.go](../../../backend/internal/credentialacq/gemini_oauth.go) (credentialacq 不在 frozen)
- 类型 `geminiOAuthExchanger`:
  - `StartOAuthFlow`: builtinProfileConfig + validateBuiltinProfile + startStoredPKCEOAuthFlow
  - `ExchangeOAuthCodeWithStore`: 复用 anthropic_oauth.go 的 exchangeAuthorizationCodeJSON 模板 (Google 用 form-urlencoded 不是 JSON, **关键 vendor 差异**, 看 CLIProxyAPI 实现)
  - `tokenCandidatePayload` 写 `client_identity_source="approved_builtin_profile"`
- vendor_exchangers.go:40-41: fake → `newGeminiOAuthExchanger()`
- 判别 test (4 个, 同 ANT-1 模板):
  - `TestGeminiOAuthExchangerUsesBuiltinProfile`
  - `TestGeminiOAuthExchangerRejectsRuntimeEndpointOverride`
  - `TestGeminiOAuthExchangeUsesFormBody` (Google 用 form, 与 anthropic JSON 对照)
  - `TestGeminiOAuthExchangeRejectsInvalidGrant`

### GEM-2 — Admin real-entry test (0.5 天)
- 在 [gatewayhttp/admin_credential_acquisition_handler_test.go](../../../backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go) 加 (类 ANT-2 模板):
  - `TestAdminGeminiOAuthFullFlowEncryptsAndSavesCredential`
  - `TestAdminGeminiOAuthRejectsFakeJSONCallback`
  - 默认 registry 防 fake bypass

### GEM-3 — Refresh adapter SSRF 修 + builtin ClientID 接入 (1 天)
- [adapters/gemini.go](../../../backend/internal/credentialworker/adapters/gemini.go): 移除 cred payload `client_id` / `client_secret` / `oauth_token_endpoint` fallback
- 新建 `internal/geminioauth/` 子包 (类 anthropicoauth):
  - `geminioauth.GeminiPublicCLIClientID = "681255809395-..."`
  - `geminioauth.GeminiPublicCLIClientSecret = "GOCSPX-..."`
  - `geminioauth.GeminiTokenURL = "https://oauth2.googleapis.com/token"`
- adapter 用 `firstNonEmpty(r.ClientID, geminioauth.GeminiPublicCLIClientID)` 同 ANT-3 anthropic 修法
- 判别 test (4 个, 同 ANT-3 模板):
  - `TestGeminiRefreshIgnoresCredentialOAuthTokenEndpoint`
  - `TestGeminiRefreshIgnoresCredentialClientID`
  - `TestGeminiRefreshSurfacesUpstream401InvalidGrant`
  - `TestGeminiRefreshOperatorEndpointOverrideUsedForOutbound`
- cross-client fallback `fallback_client_id` 处理 (Owner 决策 D-2)

### GEM-4 — Mimicry transport 接回 (类 ANT-4) (0.5 天)
- 若 `internal/transport/mimicry/` 有 `gemini_cli_mimicry_v1` profile: 加 `geminioauth.DefaultHTTPClient()` + wiring `installGeminiOAuthMimicryExchanger` + fail-loud assert
- 若无: 留文档化 + 进 DEFERRED (Owner 决策点 D-3)

### GEM-5 — Docs + 全量 hard gate (0.5 天)
- docs/03_FEATURE_PARITY_MATRIX.md 加 row (`gemini/code_assist` + `gemini/google_one` Implemented Better)
- docs/10_RISK_REGISTER.md 加 R-GEM-OAUTH-001 等 (类似 anthropic)
- 全量 ./... PASS + Owner Docker fresh PG verify
- per-commit codex review ≤ 2 轮

## 4. 参考项目对照 (CLAUDE.md #15)

| 主题 | 参考 cite | HUAKAI delta |
| --- | --- | --- |
| Google CLI ClientID | CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:31 = `681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j` | HUAKAI 同款 + **架构升级** sealed builtin profile fail-closed (caller 不能 runtime override AuthURL/TokenURL/ClientID/Scope, 仅 RedirectURI 双模式), 同 anthropic_oauth.go validateBuiltinProfile pattern |
| ClientSecret | CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:32 = `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` (Google "public CLI" 约定 secret, desktop/installed-app flow 用) | 同款硬编 — Google 设计 ClientSecret 是公开的 (per RFC 8252 native app); **生态升级**: 写进 NOTICE 文件标记非真 secret + 不进密钥库存储 |
| Scope set | CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:36-40 | 同款 (cloud-platform + userinfo.email + userinfo.profile) |
| Token endpoint | CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:178 = `https://oauth2.googleapis.com/token` | 同 endpoint + **算法升级** SSRF 静态层 (validateOAuthEndpointURL — 已在 commit 39c66a3 落地基础设施, 复用) |
| Refresh body shape | CLIProxyAPI@<sha>:internal/auth/gemini/<find:refresh fn> | 同 form-urlencoded shape, **生态升级** failure outcome 分类 (auth_expired / rate_limit / transient) + advisory lock |
| Cross-client fallback | CLIProxyAPI 无对应 (HUAKAI 自研, 已实施 ApprovedGeminiCrossClientFallback 白名单) | 本切片继续: cred payload `fallback_client_id` 撤回, 改 internal mapping (Owner D-2) |

## 5. 风险登记

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R-GEM-1 | Google 改 public CLI ClientID/Secret (虽是 desktop CLI public 约定但仍可变) | 中 | 监控 CLIProxyAPI + cloud-cli 仓库;若变热改硬编 + 评估是否动 operator-config |
| R-GEM-2 | cross-client fallback fallback_client_id cred 取信 (S1-D 残留) | 高 | GEM-3 落地时撤回 cred 取信, 改 internal mapping |
| R-GEM-3 | mimicry profile gemini 可能不存在 (anthropicoauth 有 anthropic_cli_mimicry_v1, gemini 是否有未验证) | 中 | GEM-4 调研前置, 若无则文档化降级 |
| R-GEM-4 | Google 用 form-urlencoded 不是 JSON (与 anthropic 不同), exchangeAuthorizationCodeJSON 不能直接复用 | 低 | GEM-1 加 exchangeAuthorizationCodeForm helper 复用 oauth_authorization_code.go pattern |
| R-GEM-5 | gemini 有 3 mode (code_assist / google_one / antigravity 暂搁), 本切片要保 mode 隔离 | 中 | 两个 mode 同 builtin profile, 通过 vendor+auth_mode 双键路由; antigravity 仍走 fake 留独立切片 |

## 6. Owner 决策点

### D-1: ClientID 来源
- **A** 公开 Google CLI ClientID 硬编 (跟 CLIProxyAPI 同款 — 推荐)
  - 参考: CLIProxyAPI@<sha>:internal/auth/gemini/gemini_auth.go:31 + Google 公开 desktop CLI 约定 (RFC 8252)
- **B** operator-config (信任链原则 — 但 Google CLI public 约定就是公开, B 增 admin friction 无收益)
- **C** Per-account override (过度灵活)

### D-2: cross-client fallback fallback_client_id 处理
- **A** 撤回 cred 取信, internal mapping 由 HUAKAI 硬编 family→clientID (推荐 — 闭 S1-D 残留)
  - 参考: HUAKAI 自身 ApprovedGeminiCrossClientFallback 白名单 + ANT-3 anthropic 修法
- **B** 保留 cred 取信 (S1-D 风险继续残留)
- **C** 删除 cross-client fallback (功能减少, 违反 Feature Preservation Rule)

### D-3: mimicry transport
- **A** 调研后存在 → 接入同 ANT-4 (推荐)
- **B** 调研后不存在 → 留 DEFERRED + 文档化降级 (生产 fingerprint 走 stdlib http.DefaultTransport)
- **C** 单独 sidecar profile 切片 (本切片不做)

### D-4: redirect_uri 模式
- **A** loopback `http://localhost:8085/oauth2callback` (跟 CLIProxyAPI 同款)
- **B** admin server callback (内部 admin UI)
- **C** 双模式 (loopback + admin callback) — 推荐 (同 anthropic D-2=C)

### D-5: refresh failure 分类
- **A** 同 anthropic mode_refresh outcome classifier (推荐 — invalid_grant→auth_expired / 429→rate_limit / 5xx→transient)
  - 参考: HUAKAI [auth.ClassifyRefreshError](../../../backend/internal/auth/audit.go) 已支持 vendor="gemini" + 401 short-circuit

## 7. 工时 + 起步建议

| Slice | 工时 | 谁 |
| --- | --- | --- |
| GEM-1 真 Exchanger | 1.5 天 | Claude 实施 + Codex review |
| GEM-2 Admin real-entry | 0.5 天 | Claude + Codex review |
| GEM-3 Refresh SSRF + builtin | 1 天 | Claude + Codex review (闭 S1-D gemini 部分) |
| GEM-4 Mimicry transport | 0.5 天 (若无则 0.1 天文档) | Claude + Codex review |
| GEM-5 E2E + docs | 0.5 天 | Claude + Codex review |
| **合计** | **4 天** (若 GEM-4 mimicry 不存在则 3.5 天) | |

推荐起步: **GEM-1 + GEM-3 并行** (D-1/D-2/D-5 拍后) — anthropic ANT-1+ANT-3 顺序教训是 ANT-3 修 SSRF 时发现 stale test 必须返工; gemini 一起做避免 stale test 反复修。

---

Source files read:
- /home/codex/HUAKAI/backend/internal/credentialacq/vendor_exchangers.go (lines 40-41)
- /home/codex/HUAKAI/backend/internal/credentialworker/mode_refresh.go (lines 79-80)
- /home/codex/HUAKAI/backend/internal/credentialworker/adapters/gemini.go (full)
- /home/codex/HUAKAI/backend/internal/credentialacq/anthropic_oauth.go (ANT-1 模板 reference)
- /home/codex/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go (lines 31-32, 36-40, 178-179)

Lane: claude-specifier
Time: 2026-05-27T05:00:00Z
