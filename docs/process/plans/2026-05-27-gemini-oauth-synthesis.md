# 2026-05-27 Gemini code_assist + google_one 真 OAuth 接通 — 合并稿 (Claude PM synthesis)

> Source lanes (parallel-draft, CLAUDE.md #10):
> - [Claude lane](2026-05-27-gemini-oauth-claude.md) — 12K, 强 anthropic 模板对照 + 工时
> - [Codex lane](2026-05-27-gemini-oauth-codex.md) — 18K, 强 evidence 引证 + risk register + 抓到 Gemini Advanced ≠ Code Assist mimicry 陷阱
>
> Synthesis 采 Codex lane 主架 (更精细 + 安全保守), Claude lane 补 evidence 与工时。
> Owner 2026-05-26 主线 = claude/gemini/codex 三家; anthropic 已落地 (827de58/c201cb4/39c66a3/8e9e5b0/391b825/fa7fcb5); 本切片轮到 gemini。
> Antigravity 暂搁不在本范围 (Owner 5-26)。

## 0. 元信息

| 字段 | 内容 |
| --- | --- |
| Scope | gemini/code_assist + gemini/google_one acquisition+refresh 真 OAuth 接通; mimicry transport 视调研定; antigravity 保现状 (paused) |
| Success | fake exchanger 全清; 默认 registry 走真 OAuth; refresh fail-closed; admin 真入口防 fake bypass; mutation 自检全红; ./... PASS |
| 不在范围 | DB schema/billing ledger/quota; Cloud Code Assist 出站 session adapter (`cloudcode-pa.googleapis.com`); antigravity OAuth |
| Time | 计划完成 1h; 实施 4 切片合计 ~2.5-3 开发日 (无 mimicry profile 时) / +1 天 (Owner 选要做 mimicry profile 新增) |
| Blast radius | `credentialacq` / `credentialworker` / `provider/gemini` / `cmd/gateway/wiring` / admin handler test; 可选 `transport/mimicry`; 不动 frozen 包 |
| Decision points | D-1..D-5 见 §6, 落地前必须 Owner 拍 D-1/D-3/D-5 |

CLIProxyAPI 引用 SHA: `21fad9dbb447a2ab70d51d0ac3e3d032525a6054` (per `~/refs/CLIProxyAPI/.huakai-head-sha`)。

## 1. 现状盘点 (Codex 主, Claude 补 cross-ref)

| 模块 | 现状 | 评价 |
| --- | --- | --- |
| [vendor_exchangers.go:40-41](../../../backend/internal/credentialacq/vendor_exchangers.go#L40-L41) | `code_assist/google_one` 注册 `NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess)` | **fake** 同 ANT-1 修前的 anthropic |
| [vendor_exchangers.go:43](../../../backend/internal/credentialacq/vendor_exchangers.go#L43) | `gemini/oauth` 走 `newAuthorizationCodeOAuthExchanger` (operator-config) | 已有 operator-config 模板,可参考但不直接复用 (code_assist/google_one 是 Google CLI public client, 不是 operator-config) |
| [oauth_authorization_code.go:117 + :216](../../../backend/internal/credentialacq/oauth_authorization_code.go) | 静态 SSRF allowlist 已落地 (commit 39c66a3); dial-time guard DEFERRED | 复用 39c66a3 静态层 |
| [mode_refresh.go:79-80](../../../backend/internal/credentialworker/mode_refresh.go#L79-L80) | `GeminiRefresh{AllowCrossClientFallback:true, ...}`, Endpoint/ClientID/ClientSecret 全空 | refresh 依赖 cred payload (S1-D 残留) |
| [adapters/gemini.go:59, :67, :70](../../../backend/internal/credentialworker/adapters/gemini.go) | refresh body 读 cred payload `client_id` / `oauth_token_endpoint` / `fallback_client_id` | 同 ANT-3 修前 anthropic, **加上** cross-client fallback 取 cred payload `fallback_client_id` 这条新攻击面 |
| [provider/gemini/refresher.go:200-229](../../../backend/internal/provider/gemini/refresher.go#L200) | `RefreshAdapter` 已 fail-closed: token URL/client ID/scope 必 operator/builtin | **重要发现**: HUAKAI 已有 fail-closed 范式可复用,GEM-3 可直接拿这个抽 helper |
| [ApprovedGeminiCrossClientFallback (adapters/gemini.go:111)](../../../backend/internal/credentialworker/adapters/gemini.go#L111) | family 白名单 (code_assist↔ai_studio / google_one / 互通) | family 校验 OK, 但 client_id 字符串值取 cred (SSRF) |
| [transport/mimicry/registry.go:35, :206](../../../backend/internal/transport/mimicry/registry.go) | sidecar profile 映射只给 Claude; `gemini-advanced` 模板存在但不是 Code Assist | **关键** Gemini Advanced ≠ Code Assist, 不能错用 (Codex lane 抓出) |
| [transport/policy.go:167](../../../backend/internal/transport/policy.go#L167) | `ProviderGemini` 仅允 standard/diagnostics, 禁 Gemini Advanced mimicry | 同上, 没有 Code Assist mimicry profile |

## 2. 缺口分类

- **A exchanger** (GEM-1): code_assist/google_one fake → 真 dedicated exchanger, 复用 ANT-1 模板 (built-in profile + stored PKCE + JSON/form body)
- **B refresh** (GEM-3): adapters/gemini.go 撤回 cred payload `client_id`/`oauth_token_endpoint`; cross-client fallback 改 operator/builtin family allowlist; 复用 provider/gemini/refresher.go 已有 fail-closed 范式
- **C mimicry** (GEM-4): 先调研 Code Assist / Gemini CLI 是否有独立 mimicry profile; **不可错用 gemini-advanced**; 无则 release gate flag 或 Mandatory Roadmap
- **D tests** (GEM-1/2): admin real-entry + 5 mutation 自检 (fake bypass / endpoint guard / client guard / fallback guard / wiring assert)
- **E docs** (GEM-5): docs/03 + docs/07 evidence ledger + docs/10 risk register + DEFERRED

## 3. Slice 切片 (5 个 GEM-*)

### GEM-1 真 OAuth Exchanger 替换 fake (0.5 天)

| 项 | 内容 |
| --- | --- |
| 范围 | code_assist + google_one acquisition; antigravity 保现状 |
| 文件 | NEW `backend/internal/credentialacq/gemini_oauth.go` + `gemini_oauth_test.go`; 修 `vendor_exchangers.go:40-41` |
| 类型 | `geminiOAuthExchanger` (与 `claudeAIOAuthExchanger` 同 ANT-1 模板) — built-in profile constants + validateBuiltinProfile fail-closed + storedPKCE + form-urlencoded exchange (Google 用 form, 与 anthropic JSON 对照) + client_identity_source="approved_builtin_profile" |
| 常量 (per CLIProxyAPI 21fad9d:internal/auth/gemini/gemini_auth.go:31-40) | `geminiOAuthAuthURL=https://accounts.google.com/o/oauth2/v2/auth`; `geminiOAuthTokenURL=https://oauth2.googleapis.com/token`; `geminiPublicCLIClientID=681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com`; `geminiOAuthScope="https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"`; `geminiOAuthLoopbackRedirect=http://localhost:8085/oauth2callback` |
| ClientSecret | **见 D-1** — Owner 拍是否硬编 (CLIProxyAPI 硬编 `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`); 推荐 Codex lane 保守路径: operator-config 注入 |
| 成功标准 | DefaultExchangerRegistry 中 code_assist/google_one 不再 fake; callback 必 POST token endpoint; fake JSON callback hit=1 + return failure |
| 判别 test (4 个) | `TestGeminiOAuthExchangerUsesBuiltinProfile` / `TestGeminiOAuthExchangerRejectsRuntimeEndpointOverride` / `TestGeminiOAuthExchangeUsesFormBody` (与 anthropic JSON 对照) / `TestGeminiOAuthExchangeRejectsInvalidGrant` |

### GEM-2 Admin real-entry test (0.5 天)

| 项 | 内容 |
| --- | --- |
| 范围 | 走真实 POST `/admin/v1/credentials/oauth-init` + callback handler, 防 fake paste/JSON helper 旁路 |
| 文件 | 修 `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go` (frozen 包内既有 _test.go OK) |
| 判别 test | `TestAdminGeminiOAuthFullFlowEncryptsAndSavesCredential` / `TestAdminGeminiOAuthRejectsFakeJSONCallback` / `TestAdminGeminiOAuthDefaultRegistryRejectsFakeJSON` (cursor C1 防 default registry 退化) |

### GEM-3 Refresh SSRF 修 + builtin ClientID + S1-D gemini 闭合 (0.5-0.75 天)

| 项 | 内容 |
| --- | --- |
| 范围 | code_assist/google_one scheduled refresh 从 cred-payload-trust 切到 builtin/operator; 撤回 `fallback_client_id` cred 取信; antigravity 包装路径保持现状 (Codex R-GEM-ANTIGRAVITY-001) |
| 文件 | 修 `adapters/gemini.go` (撤 cred fallback); 修 `mode_refresh.go:79-80` (用新 helper); 可选新建 `internal/geminioauth/` 子包 (类比 anthropicoauth) — 抽 ClientID/TokenURL 常量 + DefaultHTTPClient |
| 关键设计 | **复用 [provider/gemini/refresher.go:200-229](../../../backend/internal/provider/gemini/refresher.go#L200) fail-closed 范式**, 而不是从头造 (Codex lane 发现) — 抽 helper 让 credentialworker 与 provider 共用 |
| Cross-client fallback | D-2 拍后实施 — 推荐 Codex 选项: 撤回 cred `fallback_client_id`, 改 operator-config / builtin family→clientID mapping + ApprovedGeminiCrossClientFallback 双门 |
| 判别 test (5 个) | `TestGeminiRefreshIgnoresCredentialOAuthTokenEndpoint` / `TestGeminiRefreshIgnoresCredentialClientID` / `TestGeminiRefreshIgnoresCredentialFallbackClientID` / `TestGeminiRefreshSurfacesUpstream401InvalidGrant` / `TestGeminiRefreshAntigravityPathUnchanged` (paused-path regression) |

### GEM-4 Mimicry transport (条件性,见 D-5) (0.25-1 天)

| 项 | 内容 |
| --- | --- |
| **关键约束** | **gemini-advanced 模板不可当 Code Assist profile 用** (Codex lane 抓出) |
| 路径 A (D-5=A 推荐 OAuth-接通 release) | 标 feature flag / Mandatory Roadmap; token exchange 走 SSRF-protected standard transport (不假装 mimicry); 0.25 天文档化 |
| 路径 B (D-5=B Owner 要本轮反检测一并上) | 新增 `mimicry_gemini_cli_v1` / `mimicry_code_assist_v1` sidecar profile + transport/policy.go 开 ProviderGemini mimicry 模式 + wiring fail-loud assert; 1 天 |
| 文件 | 路径 A: docs only; 路径 B: `transport/policy.go` + `transport/mimicry/registry.go` + `tools/fingerprint-collector/templates/gemini-cli.json` + `cmd/gateway/wiring.go` + `gemini_oauth.go` 注入 mimicry HTTP client |
| 判别 test (路径 B) | `TestWiring_InstallGeminiOAuthMimicryExchangerReplacesDefault` (同 anthropic ANT-4) + `TestTemplateRegistryRefusesGeminiAdvancedForCodeAssist` (混用防御) |

### GEM-5 Docs + 全量 hard gate (0.25-0.5 天)

| 项 | 内容 |
| --- | --- |
| 文件 | `docs/03_FEATURE_PARITY_MATRIX.md` 加 row (gemini/code_assist + gemini/google_one); `docs/07_REFERENCE_EVIDENCE_LEDGER.md` 加 CLIProxyAPI Gemini cites; `docs/10_RISK_REGISTER.md` 加 R-GEM-* (Codex lane §5 已列); 必要时 `docs/process/decisions/DR-GEM-OAUTH-*.md` 记 D-1..D-5 |
| Gates | 全量 `./...` + Owner Docker fresh PG verify + per-commit codex review ≤ 2 轮 |

## 4. 参考项目对照 (CLAUDE.md #15) — 双 lane 合并

| 主题 | CLIProxyAPI cite (21fad9d) | HUAKAI delta + 维度 |
| --- | --- | --- |
| Google CLI ClientID | `internal/auth/gemini/gemini_auth.go:31` = `681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com`; executor 复用见 `internal/runtime/executor/gemini_cli_executor.go:38` | 同款 + **架构升级** sealed builtin profile fail-closed (validateBuiltinProfile, 同 anthropic 模板) |
| ClientSecret 公开值 | `internal/auth/gemini/gemini_auth.go:32` = `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` (Google desktop CLI 公开 secret, RFC 8252 native app) | 默认**保守** D-1: operator-config 注入, 不硬编; 若 Owner 明确硬编则 NOTICE 文件标记非真 secret |
| Token endpoint | `internal/auth/gemini/gemini_auth.go:178` + `sdk/auth/filestore.go:358` = `https://oauth2.googleapis.com/token` | 同 + **算法升级** SSRF 静态层 (validateOAuthEndpointURL, 39c66a3 已落地) |
| Scopes | `internal/auth/gemini/gemini_auth.go:37-40` + `internal/runtime/executor/gemini_cli_executor.go:42` | 同款 (cloud-platform/userinfo.email/userinfo.profile); 推荐先兼容再 least-privilege 缩 |
| Code Assist OUTBOUND | `internal/runtime/executor/gemini_cli_executor.go:36` = `cloudcode-pa.googleapis.com/v1internal`; URL 组装 `:186` | **不在本切片范围** (Codex lane 抓出) — HUAKAI provider/gemini/passthrough.go 是 generativelanguage 直通, Cloud Code Assist 出站 adapter 缺失, 是 next-next 切片或 Mandatory Roadmap |
| Google One mode | `internal/cmd/login.go:103` + `internal/api/handlers/management/auth_files.go:1744` 区分 Code Assist 手选项目 / Google One 自动发现 | google_one 共享真 OAuth profile, **生态升级** project discovery 不进 cred payload, 留 account-hub workflow / manual-first gate |
| Cross-client fallback | exact pattern 未观察 (CLIProxyAPI 没用 fallback_client_id payload pattern) | HUAKAI 自研 ApprovedGeminiCrossClientFallback (此项 HUAKAI 已经独立, **架构升级** 撤回 cred 取信改 operator-config 由 D-2 拍) |

## 5. 风险登记 (Codex lane 主, Claude lane 补)

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R-GEM-SSRF-001 | adapters/gemini.go cred payload 仍 trust root | HIGH | GEM-3 关闭, 不等下一切片 |
| R-GEM-CLIENT-001 | Google public ClientID 或 companion secret 变动 | 中 | builtin profile validation + operator override release gate |
| R-GEM-REDIRECT-001 | Google OAuth registered redirect 可能不接 HUAKAI admin callback | 中 | D-3 双模式 allowlist |
| R-GEM-SCOPE-001 | cloud-platform 权限宽 vs least-privilege | 低 | D-4 先兼容后缩 |
| R-GEM-MIMICRY-001 | gemini-advanced 不是 Code Assist; 错用比无更危险 | HIGH | GEM-4 禁错用; 路径 A 文档化, 路径 B 新增 profile |
| R-GEM-ANTIGRAVITY-001 | mode_refresh.go:81 antigravity 包装复用 Gemini refresh; helper 抽取可能误改 paused path | 中 | GEM-3 测试固定 antigravity 现状 |
| R-GEM-SMOKE-001 | 没有 Owner Google 真账号 + 已授权 redirect 时无法证明 live OAuth | 中 | mock + Owner 手动 smoke gate, 不伪造通过 |
| R-GEM-CODEASSIST-001 | Cloud Code Assist 出站 adapter 缺失 (cloudcode-pa.googleapis.com) | 中 | 本切片不做, 标 Mandatory Roadmap (next-next 切片) |

## 6. Owner 决策点

### D-1: Client identity 来源 + ClientSecret 处理

参考项目对照:
- **A** 公开 Google CLI ClientID 硬编 + ClientSecret **operator-config 注入** (推荐 — Codex lane 保守)
  - CLIProxyAPI@21fad9d:internal/auth/gemini/gemini_auth.go:31-32 硬编了 secret, 但 Google "desktop CLI public secret" 设计本意是可公开 (RFC 8252 §8.4)
  - HUAKAI 选 operator-config 注入 secret 增加运营弹性 (Google 改 secret 时 ops 热改, 不需要 code release)
- **B** ClientID + ClientSecret 都硬编 (CLIProxyAPI 同款)
  - 优: 0 ops 友好; 劣: Google secret 变动需要 code release
- **C** 都 operator-config 注入 (信任链最严)
  - 优: 0 信任 vendor; 劣: 用户 admin friction 高

### D-2: Cross-client fallback fallback_client_id 处理

- **A** 撤回 cred 取信, operator-config / builtin family→clientID mapping (推荐 — 闭 S1-D gemini 部分)
  - HUAKAI 自研 ApprovedGeminiCrossClientFallback 白名单 family 校验 OK, 但 client_id 字符串值取 cred 是 SSRF; **架构升级** mapping 由 HUAKAI 维护
- **B** 保留 cred 取信 (S1-D 残留)
- **C** 删除 cross-client fallback (功能减少, 违 Feature Preservation Rule)

### D-3: redirect_uri 模式

- **A** loopback only (`http://localhost:8085/oauth2callback`, CLIProxyAPI 同款)
- **B** admin server callback only
- **C** 双模式 loopback + admin callback (推荐, 同 anthropic D-2=C)
  - 注: 二者都进 allowlist, 不接 payload 任意 redirect (39c66a3 静态层已守)

### D-4: Scope 范围

- **A** 同 CLIProxyAPI 三项 (cloud-platform + userinfo.email + userinfo.profile, 推荐 — 兼容 first)
- **B** 仅 cloud-platform (最小权限但可能 vendor 拒 userinfo lookup)
- **C** 加 drive / calendar 等 (无必要)

### D-5: mimicry release gate

- **A** 本轮目标 = "OAuth 接通", GEM-4 标 feature flag/Mandatory Roadmap; token exchange 走 SSRF-protected standard transport (推荐 — 不假装 mimicry 已完成)
- **B** 本轮一并上线反检测, GEM-4 必须新增 mimicry_gemini_cli_v1/code_assist 独立 profile + 真 fingerprint capture
  - 注: gemini-advanced 模板不可当 Code Assist 用 (R-GEM-MIMICRY-001)

## 7. 工时 + 推荐起步

| Slice | 工时 | Commit groupings |
| --- | --- | --- |
| GEM-1 真 Exchanger | 0.5 天 | 第 1 commit (与 GEM-2 一起, Codex 推荐 acquisition 闭环 review) |
| GEM-2 Admin real-entry | 0.5 天 | 同上 |
| GEM-3 Refresh SSRF + S1-D | 0.5-0.75 天 | 第 2 commit (refresh 风险与 acquisition 分离 review) |
| GEM-4 Mimicry (条件) | 0.25 / 1 天 | 第 3 commit (D-5=A) 或 单独 (D-5=B) |
| GEM-5 Docs + gate | 0.25-0.5 天 | 第 4 commit |
| **合计** | **2.0-3.25 天** (D-5=A) / **+0.75 天** (D-5=B) | |

推荐起步: **Owner 拍 D-1/D-3/D-5 → GEM-1 + GEM-2 第一 commit (acquisition 闭环) → GEM-3 第二 commit (refresh) → GEM-4 按 D-5 路径 → GEM-5 收口**。Codex lane 推荐顺序 (acquisition / refresh 分 commit) 比 Claude lane "GEM-1+3 并行" 更稳, 采 Codex 顺序。

---

## Lane 差异 + Synthesis 决定

| 差异点 | Claude lane | Codex lane | Synthesis 选择 | 理由 |
| --- | --- | --- | --- | --- |
| 起步顺序 | GEM-1+3 并行 | GEM-1+2 acquisition 闭环, GEM-3 第二 commit | **Codex** | 关闭面分 commit, 防 stale test 反复修 (ANT 教训) |
| ClientSecret | 硬编 (称非真 secret) | D-1 operator-config 倾向 | **Codex** | 安全保守; HUAKAI 信任链原则 |
| mimicry profile | 调研后接入 | 抓出 gemini-advanced ≠ Code Assist, 不可错用 | **Codex** | Codex evidence 更准 |
| Code Assist 出站 | 没提 | 抓出 cloudcode-pa.googleapis.com 缺 outbound adapter | **Codex** | Next-next 切片范围 |
| 测试覆盖 | 4-5 个 mutation | 5+ 含 wiring 自检 + paused-path regression | **Codex** | 覆盖更广 |
| ClientSecret evidence | 提供 GOCSPX-... CLIProxyAPI cite | 提供 + 标 RFC 8252 公开 secret 性质 | **合并** | 两 lane 互补 |
| ChatGPT OAuth evidence (next-next) | 在 plan 内提了 ANT-1 anthropic 模板 | 未涉及 (本 plan 范围) | **保留** | ChatGPT OAuth 进 next-next plan (codex/chatgpt_oauth, ClientID `app_EMoamEEZ73f0CkXaXp7hrann` 已知) |

Source files read:
- Both lanes (见 各 lane plan §末尾 Source files)
- Synthesis: Claude PM 同时读 Claude lane + Codex lane + cross-verify
Lane: claude-pm-synthesis
Time: 2026-05-27T05:30:00Z
