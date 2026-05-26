# DEFERRED — Anthropic claude_ai_oauth ANT-1 + ANT-2 S2 留尾

> 创建: 2026-05-26 (commit group 1 落地)
> 关联 plan: [2026-05-26-anthropic-claude-oauth-synthesis.md](../plans/2026-05-26-anthropic-claude-oauth-synthesis.md)
> 关联 codex review: `/tmp/codex-ant12-review-r1.txt` (R1, 沙箱 read-only)

## 来源

Codex per-commit review (CLAUDE.md #8 修订 2026-05-24) Round 1 在第一 commit group 出 0 S0/S1 + 2 S2; 按 severity gate, S2 不阻塞 commit, 但必须在后续切片或文档收口。

## S2 留尾清单

### S2-1 redirect_uri allowlist 未显式

- 文件: [backend/internal/credentialacq/anthropic_oauth.go:115](../../../backend/internal/credentialacq/anthropic_oauth.go#L115), [:144](../../../backend/internal/credentialacq/anthropic_oauth.go#L144)
- 现状: `validateBuiltinProfile` 仅判 `RedirectURI != ""`; caller 传任意非空 redirect 均通过。
- Owner 决策对照: D-2 = C 双模式 callback (loopback `http://localhost:54545/callback` + admin server callback URI)。spec 期望"loopback + admin 任意之一"白名单, 当前实现接受任意值。
- 风险评估: PKCE 校验已绑定 redirect_uri (token endpoint 拒收不匹配 verifier 的 code), 攻击者无法借任意 redirect 套出可用 token; 但仍是 defense-in-depth 缺口。
- 收口路径:
  - 选项 A (推荐): ANT-4 切片在 anthropic_oauth.go 加 `validateRedirectURI` allowlist — 接受 (a) `claudeAIOAuthLoopbackRedirect` (b) operator config 配置的 admin callback host 白名单。
  - 选项 B: 仅文档化"redirect_uri 信任运营 admin 进程",在 docs/10_RISK_REGISTER.md 登记。

### S2-2 mimicry transport 回归 (anthropic_oauth.go 用 http.DefaultClient)

- 文件: [backend/internal/credentialacq/anthropic_oauth.go:174](../../../backend/internal/credentialacq/anthropic_oauth.go#L174), [:180](../../../backend/internal/credentialacq/anthropic_oauth.go#L180)
- 现状: `exchangeAuthorizationCodeJSON` 用 `http.DefaultClient.Do`, 没接 `anthropicoauth.DefaultHTTPClient()` 的 mimicry uTLS transport (`mimicry.SidecarProfileAnthropicCLIMimicryV1`)。
- 副作用: 本 commit 删除了 wiring.go 中 `anthropicoauth.RegisterInto(...)` 调用 (它原本会用带 mimicry 的 Exchanger 覆盖 fake registry); 现在 default registry 注册的是 `claudeAIOAuthExchanger`, mimicry 路径事实上失活。
- 风险评估: token exchange 出站可能被 Anthropic 端 fingerprint 拒 (与 [[project_huakai_codex_mimicry_verified]] 的 HUAKAI 核心差异化冲突)。属于"生产可靠性 + 伪装传输回归", 非本切片 fail-closed 闸门。
- 收口路径 (强制):
  - ANT-4 切片必须把 mimicry httpClient 注入 `claudeAIOAuthExchanger` (新增 struct field `httpClient *http.Client` + 测试覆盖)。
  - 在 ANT-4/ANT-5 完成前, **不宣称 live upstream PASS** — 即使本地 mock + 全量 ./... 绿, 真上游出站验证留到 mimicry 接回。

## 不属于 DEFERRED 的项 (本 commit 已闭合)

- ANT-1: fake exchanger 移除 + dedicated built-in profile + fail-closed (3 mutation 自检红) ✓
- ANT-2: admin real-entry 路由 (POST `/admin/v1/credentials/oauth-init` + GET `/admin/v1/credentials/oauth-callback`) 真接通 + 拒 fake JSON callback ✓
- wiring 防回归 test (`TestWiring_AnthropicClaudeAIOAuthKeepsCredentialAcqBuiltinProfile`) ✓
- ./... 全量 PASS ✓

## 收口检查清单

- [ ] S2-1: ANT-4 加 redirect_uri allowlist 或 docs/10_RISK_REGISTER.md 登记
- [ ] S2-2: ANT-4 注入 mimicry httpClient + 测试覆盖 live exchange 路径
- [ ] live upstream smoke: Owner Docker 真账号 + mimicry on-wire ja3 验证 (per [[feedback_owner_local_verification]])
