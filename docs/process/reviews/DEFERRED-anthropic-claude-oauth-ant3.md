# DEFERRED — Anthropic claude_ai_oauth ANT-3 留尾

> 创建: 2026-05-26 (commit group 2 落地)
> 关联 plan: [2026-05-26-anthropic-claude-oauth-synthesis.md](../plans/2026-05-26-anthropic-claude-oauth-synthesis.md)
> 关联 codex review:
> - R1: `/tmp/codex-ant3-review-r1.txt` (sandbox read-only)
> - R2: `/tmp/codex-ant3-review-r2.txt` (sandbox read-only) — verdict 可 commit, 0 S0/S1

## 来源

Codex per-commit review Round 1 标 4 个 S1; Claude PM 处理结果:
- S1-A (adapter 401 丢 body) → **本 commit 内修复** (tokenHTTPError 透 body + 集成 test)
- S1-B (旧 test 期望过期) → **本 commit 内修复** (test fixture 更新到接受 builtin client_id)
- S1-C (legacy endpoint console.anthropic.com 错) → **本 commit 内修复** (统一到 api.anthropic.com)
- S1-D (openai/gemini 同类 SSRF) → **DEFERRED 此文档** (范围外)

Codex 另标 2 个 S2/S3 全部 DEFERRED 此文档。

## DEFERRED 清单

### S1→DEFERRED: OpenAI / Gemini adapter 同类 SSRF (existing risk)

- 文件:
  - [backend/internal/credentialworker/adapters/openai.go:43](../../../backend/internal/credentialworker/adapters/openai.go#L43) — OpenAI refresh adapter
  - [backend/internal/credentialworker/adapters/gemini.go:59](../../../backend/internal/credentialworker/adapters/gemini.go#L59) — Gemini refresh adapter
- 现状: 与本 commit 修复前的 anthropic.go 同病, 取信 credential payload 中的 `client_id` / `oauth_token_endpoint`。
- 风险评估: 跟 anthropic SSRF 同等级 (S1) 但**不在 ANT-3 范围内**, 范围限定在 anthropic/claude_ai_oauth (Owner 5-26 决定主线只做 claude/gemini/codex 三家, gemini OAuth fake → 真 留 ANT-x 切片)。

#### 2026-05-27 二次评估 — 为何不能 quick fix

尝试在本 session 收尾时 simple-revert (移除 cred fallback) 发现真实阻塞:
- [mode_refresh.go:73](../../../backend/internal/credentialworker/mode_refresh.go#L73): `adapters.OpenAIRefresh{}` 生产 wiring 是**空配置** (Endpoint/ClientID 全空)。
- [mode_refresh.go:79-80](../../../backend/internal/credentialworker/mode_refresh.go#L79): `GeminiRefresh{}` 也只设 SourceClientFamily/TierCacheTTL, 没设 Endpoint/ClientID/ClientSecret。
- 生产**实际靠 credential payload 提供 client_id** (用户 OAuth 登录时浏览器 / CLI 写入 cred)。简单移除 cred fallback 会让所有现存账号 refresh 立断。
- 真正闭合需类似 ANT-1 模板: 接入 ChatGPT mobile app public ClientID + Google CLI public ClientID 硬编 (清 anthropicoauth.AnthropicPublicCLIClientID 同款), 给 OpenAIRefresh / GeminiRefresh 加 builtin profile + fail-closed validation。

- 收口路径 (修正):
  - 选项 A (推荐): 等 **gemini OAuth real-OAuth 切片** 落地 (Owner 主线第 2 家) 时连带做, ChatGPT public ClientID 接入同步 OpenAI 那边。
  - 选项 B: 独立 `S1-D-openai-gemini-builtin-profile` 切片, 先研究 ChatGPT mobile / Google CLI public ClientID 在 CLIProxyAPI / cliproxy 仓库的引用 (类似 anthropic 9d1c250a-... 的硬编值), 然后类 ANT-1 模板做 5 个文件改 (openai/gemini adapter 加 builtin profile, mode_refresh wiring 加 RegisterOrReplace, 加 4 个 mutation 自检 test)。

- Adapter 状态 (codex review 抽样核实):
  - [codex.go:30](../../../backend/internal/credentialworker/adapters/codex.go#L30): **OK** (已 fail-closed — endpoint/clientID/scope 必须 operator config)
  - [anthropic.go](../../../backend/internal/credentialworker/adapters/anthropic.go): **commit c201cb4 已修**
  - openai.go / gemini.go: **DEFERRED** (阻塞: 缺 public ClientID built-in profile, 需研究 + 切片实施)

### S2-DEFERRED: mergeTokenResponse 保留 hostile credential 字段

- 文件: [backend/internal/credentialworker/adapters/openai.go:220](../../../backend/internal/credentialworker/adapters/openai.go#L220)
- 现状: 共享 `mergeTokenResponse` 在 round-trip 后保留 credential payload 中原有 `client_id` / `oauth_token_endpoint` 字段, 写回 store。
- 风险评估: 出站路径已不再用这些字段 (本 commit fix), 但持久化攻击面仍在 — 下次 refresh 仍会读到 hostile 字段然后忽略, 一旦未来增加新读取路径就会重现 SSRF。
- 收口路径: 模仿 [anthropicoauth/refresher.go:242](../../../backend/internal/anthropicoauth/refresher.go#L242) 在 mergeTokenResponse 内主动 scrub `client_id` / `oauth_token_endpoint` / `setup_token` / `long_lived_setup_token` 字段, 落 credential payload 前清理。

### S3-DEFERRED: operator override test body 断言加强

- 文件: [backend/internal/credentialworker/adapters/anthropic_test.go:111](../../../backend/internal/credentialworker/adapters/anthropic_test.go#L111)
- 现状: `TestAnthropicRefreshOperatorEndpointOverrideUsedForOutbound` 验证 URL 与 access_token, 未断言 body 中 client_id 是 operator-injected (而非 builtin 默认)。
- 收口路径: 在 ANT-4 或后续切片加一行 `if capturedBody["client_id"] != "operator-injected-cid" { ... }`。

### S2-DEFERRED (Round 2 新增): error body sanitize 前已被 Error() 暴露

- 文件: [backend/internal/credentialworker/adapters/openai.go:177](../../../backend/internal/credentialworker/adapters/openai.go#L177)
- 现状: tokenHTTPError.Error() 直接拼上游 body 到错误字符串; [audit.go](../../../backend/internal/credentialworker/audit.go) 写审计前会 sanitize, 但 error 本身仍可能被其他 caller / log 看到。
- 风险评估: 上游响应通常只含 `invalid_grant` 等公共错误码 + 描述, 但若上游回 access_token 或 PII 进 body, 未 sanitize 路径可能泄。
- 收口路径: ANT-4 让 body 先走 `auth.SanitizeOAuthMessage` 再写入 tokenHTTPError.body, 同时保留 `invalid_grant` 子串判别能力。

### S2-DEFERRED (Round 2 新增): AccountCredentialRefresher 级别 failure_class 测试缺口

- 文件: [backend/internal/credentialworker/mode_refresh.go:216](../../../backend/internal/credentialworker/mode_refresh.go#L216) / [:605](../../../backend/internal/credentialworker/mode_refresh.go#L605)
- 现状: 当前 ANT-3 集成 test 覆盖 `AnthropicRefresh -> ClassifyRefreshError` 直接调用; 真生产 generic mode path 走 `AccountCredentialRefresher.refresh` → outcome 落库, 这条 wiring 没有专门 fixture 验证 401 invalid_grant → failure_class=auth_expired 落 store。
- 风险评估: 因 body 透传 + invalid_grant 字符串 + isKnownRefreshVendor 双兜底, 本切片代码逻辑闭合; test 缺口是 cursor C1 教训的下一道防线。
- 收口路径: ANT-4 加一个 mode_refresh 集成 test (mock store + AnthropicRefresh 401 → 验证 SaveRefreshFailure 收到 failure_class=auth_expired)。

## 不属于 DEFERRED 的项 (本 commit 已闭合)

- adapters/anthropic.go: 移除 credential `client_id` + `oauth_token_endpoint` fallback ✓
- anthropicoauth/refresher.go: 同上 credential `client_id` fallback 移除 ✓
- anthropicoauth/refresher.go: AnthropicRefreshTokenURL 收敛到 api.anthropic.com ✓
- adapters/openai.go postTokenOnce: tokenHTTPError 透 body, 让 ClassifyRefreshError 抓 invalid_grant ✓
- 新 test 7 个: 5 个 adapters/anthropic_test.go (SSRF endpoint reject / SSRF clientID reject / 401 invalid_grant body 透传 / operator endpoint override / missing refresh_token fail-closed) + 1 个 anthropicoauth/refresher_test.go (TestRefresherIgnoresCredentialPayloadClientID) + 1 个 credentialworker/outcome_test.go (TestClassifyRefreshErrorIntegratesAnthropicAdapter401InvalidGrant 集成 adapter→classifier) ✓
- 4 mutation 自检全红:
  1. 恢复 adapter credential `oauth_token_endpoint` fallback → SSRF endpoint test 红
  2. 恢复 adapter credential `client_id` fallback → SSRF clientID test 红
  3. 恢复 anthropicoauth credential `client_id` fallback → anthropicoauth SSRF test 红
  4. 组合 mutation (删 isKnownRefreshVendor 中 anthropic + strip tokenHTTPError body 透传) → integration test outcome 退到 OutcomeUnknown 变红, 证明 defense-in-depth 两条 fallback path (短路 + msg 子串) 至少需一条活
- 全量 ./... PASS ✓

## 收口检查清单

- [ ] gemini / openai adapter SSRF 修复 (gemini OAuth 真接通切片或独立切片)
- [ ] mergeTokenResponse 主动 scrub hostile credential 字段
- [ ] ANT-4 加强 operator override test body 断言
