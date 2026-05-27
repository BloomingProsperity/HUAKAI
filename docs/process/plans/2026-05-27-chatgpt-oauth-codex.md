# 2026-05-27 ChatGPT OAuth Codex Lane Plan

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI / openai-codex

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors -
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## 0. 元信息

| Item | Value |
| --- | --- |
| Lane | codex plan lane; reference-source role = specifier |
| Time | 2026-05-27T09:18:21Z |
| Owner directive | "Owner 2026-05-27 启动 ChatGPT OAuth (HUAKAI 主线第 3 家 vendor)。按 CLAUDE.md #10 parallel-draft, codex 独立写 codex lane plan" |
| Scope in | 独立确认 HUAKAI 现状、读取 CLIProxyAPI + openai-codex source evidence、规划 ChatGPT OAuth 切片、列出风险/Owner 决策/测试 mutation fixtures |
| Scope out | 不读 Claude lane plan；不实现；不改业务代码；不 commit；不读 LGPL/AGPL/GPL references |
| Observed regions | 24 |
| Inferences | 10 |
| Open questions | 7 |

Owner 已采 evidence，本 lane 已重新读源确认：

- CLIProxyAPI 本地导出目录不是 git worktree，但存在 `.huakai-head-sha=21fad9dbb447a2ab70d51d0ac3e3d032525a6054`；以下引用写作 `CLIProxyAPI@21fad9d-local-export`，不伪造 git 仓库状态。证据：`/home/codex/refs/CLIProxyAPI/.huakai-head-sha:1`。
- CLIProxyAPI 观察到 ChatGPT/Codex OAuth 的 authorization endpoint、token endpoint、public app id、loopback redirect：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:24-27`。
- CLIProxyAPI 观察到 authorization request scope 和额外 query 参数：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:68-79`。
- CLIProxyAPI 观察到 code exchange 使用 form body、PKCE verifier、无 client_secret：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:103-118`。
- CLIProxyAPI 观察到 token response 含 access/refresh/id token 并从 id token 派生 account/email：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:138-178`。
- CLIProxyAPI 观察到 refresh request 带同一 public app id、refresh grant、refresh token 和缩窄 scope：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:191-196`。
- openai-codex HEAD = `6a225e4005209f2325ab3c681c7c6beba2907d4d`，commit time `2026-05-13T21:03:19-07:00`。
- openai-codex 观察到 ChatGPT backend base、refresh endpoint、revoke endpoint：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93-95`。
- openai-codex 观察到同一 public app id：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:921`。
- openai-codex 观察到 ChatGPT metadata 被用于 user/account/plan/workspace 语义：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:108-118`, `:364-385`, `:656-677`, `:928-949`。
- openai-codex 观察到 refresh 使用 JSON body；这与 CLIProxyAPI/HUAKAI 当前 form refresh 路径不同，必须进入风险登记而不是假设等价：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:806-825`。

## 1. 现状盘点

### 1.1 HUAKAI credential acquisition 注册现状

- `backend/internal/credentialacq/vendor_exchangers.go` 默认 registry 已把 Anthropic/Gemini 真 OAuth exchanger 注册进去，但 OpenAI `chatgpt_oauth` 仍注册为 PKCE fake exchanger；OpenAI Codex CLI device-code 已有单独 exchanger：`backend/internal/credentialacq/vendor_exchangers.go:38-45`。
- fake exchanger 的 start 路径会生成 PKCE flow，但 callback 直接解析 caller 传入的 JSON token payload；这就是 ChatGPT OAuth 切片要替换的危险占位：`backend/internal/credentialacq/vendor_exchangers.go:162-188`。
- fake exchanger 只做 token shape 校验，不会对真实 OpenAI token endpoint 做 authorization-code exchange：`backend/internal/credentialacq/vendor_exchangers.go:191-225`。

### 1.2 ModePlan / ProfileBindings 等价现状

- `chatgpt_oauth` 已在 mode plan 中声明为 OAuth-only、PublicCLI identity、AllowedHelpers 仅 OAuth：`backend/internal/credentialacq/types.go:151-160`。
- session 创建层已用 `AllowedHelpers` 阻断 paste/cli_import/json_import 绕过 OAuth-only 模式：`backend/internal/credentialacq/session_store.go:110-129`。
- 现有 P0 回归明确覆盖 `chatgpt_oauth` 拒绝 paste/cli_import，并有合法 OAuth 反向控制：`backend/internal/credentialacq/anthropic_oauth_test.go:398-437`。
- `credentialstore` 已把 `chatgpt_oauth` 作为 OpenAI vendor 的 refreshable session-token runtime handler：`backend/internal/credentialstore/types.go:247-258`。
- `credentialworker` 已把 `chatgpt_oauth` 接到 OpenAI refresh adapter：`backend/internal/credentialworker/mode_refresh.go:64-78`。

### 1.3 Anthropic/Gemini 可复用的 HUAKAI 模式

- Anthropic `claude_ai_oauth` 走 HUAKAI 内置 public profile、stored PKCE、store-aware callback；普通 `ExchangeOAuthCode` fail-closed，防止无 store fake bypass：`backend/internal/credentialacq/anthropic_oauth.go:57-71`。
- Anthropic callback 解密 stored PKCE payload 后二次校验 profile，再发真实 token exchange，并把 client identity source 写入 payload 和 RedactedContext：`backend/internal/credentialacq/anthropic_oauth.go:75-113`, `:181-236`。
- Anthropic default registry 回归测试证明 fake JSON callback 不应被接受，必须转发到真实 token exchange 并由上游拒绝：`backend/internal/credentialacq/anthropic_oauth_test.go:188-262`。
- Gemini public CLI OAuth 走内置 profile、ignore request client_secret、要求生产注入 secret、支持 loopback + allowlisted admin callback 双模式：`backend/internal/credentialacq/gemini_oauth.go:92-107`, `:161-213`, `:219-259`。
- Gemini 对 authorize URL 增加 vendor-specific query，并有 callback form exchange、refresh_token 强校验、client identity source 写回：`backend/internal/credentialacq/gemini_oauth.go:285-359`。
- Gemini 测试已经提供可复用的质量基线：覆盖 public profile override、offline/consent query、secret 注入、redirect allowlist、store-aware callback、missing refresh_token、RedactedContext、自检 helper、stored payload tamper：`backend/internal/credentialacq/gemini_oauth_test.go:17-359`。

### 1.4 通用 authorization-code OAuth 现状

- `oauth_authorization_code.go` 已有 stored PKCE payload、token response 解析、form POST、SSRF-protected fallback client、authorize URL builder：`backend/internal/credentialacq/oauth_authorization_code.go:27-43`, `:95-147`, `:149-202`。
- 该通用 exchanger 面向 operator-config OAuth；它会把 `oauth_token_endpoint` 写入 credential payload，适合 operator-owned endpoint，不适合内置 ChatGPT public profile 直接复用：`backend/internal/credentialacq/oauth_authorization_code.go:221-285`。
- `oauth.go` 的 generic `BuildAuthorizeURL` 只含标准 PKCE 参数，不支持 ChatGPT observed 额外 query，需要 ChatGPT 自有 builder 或 wrapper：`backend/internal/credentialacq/oauth.go:180-200`。

### 1.5 OpenAI refresh / Codex device-code 现状

- OpenAI refresh adapter 默认 token endpoint 是 OpenAI auth token endpoint；若 credential payload 带 endpoint，会优先使用 payload endpoint：`backend/internal/credentialworker/adapters/openai.go:17-53`。
- OpenAI adapter 当前用 form-urlencoded refresh grant，并会把非 2xx response body 带入错误，保留 `invalid_grant` 分类信号：`backend/internal/credentialworker/adapters/openai.go:147-207`。
- `mergeTokenResponse` 会在成功 refresh 后 scrub hostile fields，包括 stored endpoint/client secret 等：`backend/internal/credentialworker/adapters/openai.go:218-257`。
- OpenAI Codex device-code 已有专门 exchanger 和 OpenAI-specific poll/code-exchange 路径，不等同于 `chatgpt_oauth` browser PKCE slice：`backend/internal/credentialacq/oauth_devicecode.go:21-25`, `:41-55`, `:413-512`。

## 2. 缺口分类

### G-1: `chatgpt_oauth` 还不是 OAuth-only 真交换

现状 P0 已禁止 paste/cli_import start 绕过，但 default registry 仍让 OAuth callback 可由 fake JSON token 直接完成。Anthropic 已有 default-registry fake-bypass 回归，ChatGPT 需要同级别测试和真 exchanger。证据：`backend/internal/credentialacq/vendor_exchangers.go:44`, `backend/internal/credentialacq/vendor_exchangers.go:177-188`, `backend/internal/credentialacq/anthropic_oauth_test.go:188-262`。

### G-2: ChatGPT OAuth profile 与 Gemini/Anthropic 不同

- Anthropic: PKCE-only，JSON token exchange，内置 profile 可覆盖 redirect；证据：`backend/internal/credentialacq/anthropic_oauth.go:116-179`, `:181-202`。
- Gemini: public CLI client 但要求 operator env secret，form token exchange，authorize URL 需要 Google-specific offline/consent，redirect 支持 loopback + allowlisted admin 双模式；证据：`backend/internal/credentialacq/gemini_oauth.go:92-107`, `:219-259`, `:285-345`。
- ChatGPT: 两个参考源确认 public app id；CLIProxyAPI 观察到 PKCE-only、无 client secret、OpenAI auth/token endpoints、loopback `http://localhost:1455/auth/callback`、scope `openid email profile offline_access`、额外 query `prompt=login` / org id-token flag / simplified flow flag：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:24-27`, `:68-79`, `:103-118`; openai-codex 确认同一 public app id 和 token endpoint：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93-95`, `:921`。

### G-3: redirect_uri 不能直接照 Gemini 双模式

CLIProxyAPI only-observed redirect 是 loopback `localhost:1455`。当前没有 source evidence 证明 OpenAI 这个 public app id 接受 HUAKAI admin callback URL。Gemini 双模式是 Google slice 的 Owner decision，不自动迁移到 ChatGPT。默认计划应从 loopback-only 起步，若 Owner 选双模式，必须把 admin callback 作为 D-1 明确接受风险/实验项。

### G-4: refresh content-type / endpoint trust 有来源差异

CLIProxyAPI refresh 走 form-urlencoded，openai-codex refresh 走 JSON body，HUAKAI 当前 `OpenAIRefresh` 也走 form-urlencoded。不能凭一个来源覆盖另一个来源；计划先用 HUAKAI 既有 OpenAI adapter + CLIProxyAPI-compatible form path，记录 openai-codex JSON refresh 为 follow-up compatibility test/decision。证据：`CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:191-204`, `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:806-825`, `backend/internal/credentialworker/adapters/openai.go:40-53`。

### G-5: ChatGPT metadata 是新差异，不应默默丢失

openai-codex 使用 ChatGPT-specific user/account/plan/workspace metadata 做用户身份、计划分类和 workspace 限制。HUAKAI 目前 `CredentialCandidate` 和 encrypted payload 可承载 JSON metadata；是否进入 RedactedContext 涉及 PII/ops 可见性，需要 Owner 决策。证据：`openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:108-118`, `:364-385`, `:656-677`, `:928-949`; HUAKAI RedactedContext 会过滤 secret-like keys/values：`backend/internal/credentialacq/audit.go:62-72`, `:101-110`。

### G-6: mimicry/transport 不是自然继承

Anthropic/Gemini 已有显式 client injection / fail-loud helper 模式；ChatGPT 新 exchanger 如果只用 default client，会缺少后续 mimicry 和 production wiring 自检入口。证据：`backend/internal/credentialacq/anthropic_oauth.go:36-55`, `backend/internal/credentialacq/gemini_oauth.go:44-90`, `backend/internal/credentialacq/gemini_oauth_test.go:301-313`。

## 3. Slice 切片

### CHG-1: ChatGPT built-in profile 与 default registry 替换 (0.50 天)

目标：创建 ChatGPT 专用 OAuth exchanger，替换 `openai/chatgpt_oauth` 的 fake registration；start 阶段只接受 HUAKAI 内置 public profile，禁止 caller 覆盖 endpoint/client_id/scope/source/client_secret。

未来文件范围：

- Create: `backend/internal/credentialacq/chatgpt_oauth.go`。`credentialacq` 当前 16 个非测试 Go 文件 / 3415 行，未命中冻结包或 20 文件/5000 行预算；新增 1 个实现文件仍在预算内。
- Create: `backend/internal/credentialacq/chatgpt_oauth_test.go`。
- Modify: `backend/internal/credentialacq/vendor_exchangers.go`。

验收点：

- `DefaultExchangerRegistry()` lookup `openai/chatgpt_oauth` 返回 ChatGPT 真 exchanger，不再是 fake exchanger。
- `StartOAuthFlow` 生成 stored PKCE flow，session vendor/auth_mode 固定为 `openai/chatgpt_oauth`，client identity source 固定为 `public_cli`。
- caller 传入任意 client_id/auth_url/token_url/scope/client_secret/source 覆盖时 fail-closed，且不会创建 flow row。

Mutation 自检 fixture：

- 删除 registry 替换行：default-registry test 必须失败。
- 让 built-in profile 接受 caller client_id：override test 必须失败。
- 让 built-in profile 接受 client_secret：secret-reject test 必须失败。
- 把 profile source 改成 operator_config：session source test 必须失败。

### CHG-2: ChatGPT authorize URL builder 与 redirect decision gate (0.50 天)

目标：在 start 阶段生成 ChatGPT-specific authorize URL。默认 plan 推荐 loopback-only；如果 Owner 选 D-1=C，必须新增 allowlisted admin callback path 校验，且不能从 request body 自报 allowlist。

未来文件范围：

- Modify/Create in `backend/internal/credentialacq/chatgpt_oauth.go`。
- Test in `backend/internal/credentialacq/chatgpt_oauth_test.go`。
- Optional if D-1=C: update admin callback docs only after synthesis; no schema change.

验收点：

- URL 包含标准 PKCE 参数：response type code、state、S256 challenge、redirect_uri、scope。
- URL 包含 observed ChatGPT query：login prompt、id-token org flag、simplified-flow flag。
- loopback redirect 固定为 `http://localhost:1455/auth/callback` unless Owner D-1 permits admin mode.
- admin HTTPS callback 如果启用，只允许 operator static allowlist + mounted route，不接受 arbitrary host/private IP/localhost alias。

Mutation 自检 fixture：

- 删除 `offline_access` scope：scope test 必须失败。
- 删除 simplified-flow query：query test 必须失败。
- 删除 org id-token query：query test 必须失败。
- 放宽 redirect host 为 attacker/public arbitrary host：redirect table test 必须失败。
- 如果 D-1=A，任何 HTTPS admin callback 都必须被拒；若 D-1=C，allowlist match/mismatch 双向断言必须能区分。

### CHG-3: Store-aware authorization-code callback 真交换 (0.75 天)

目标：callback 不再接受 fake JSON token；必须解密 stored PKCE payload、二次校验 built-in profile、向 OpenAI token endpoint 发 authorization_code exchange，并要求 refreshable token shape。

未来文件范围：

- Modify/Create in `backend/internal/credentialacq/chatgpt_oauth.go`。
- Test in `backend/internal/credentialacq/chatgpt_oauth_test.go`。

验收点：

- 普通 `ExchangeOAuthCode` fail-closed，提示需要 stored PKCE verifier。
- `ExchangeOAuthCodeWithStore` 解密 stored payload 后 revalidate profile；tampered token_url/client_id/redirect_uri/scope/code_verifier 缺失时 fail-closed，且不发 HTTP。
- token request 用 form-urlencoded body，包含 grant、authorization code、redirect_uri、public app id、PKCE verifier；不带 client_secret。
- 非 2xx response body 的 `invalid_grant` 信号保留到 error text，session 进入 failed/exchange_failed。
- 2xx response 至少要求 access_token + refresh_token；id_token/token_type/expires_in/scope 按现有 payload convention 保存。
- default registry fake JSON code 测试必须证明攻击者 JSON 被当成 raw authorization code 送上游，而非本地解析为 token。

Mutation 自检 fixture：

- 把 default registry 留在 fake exchanger：fake JSON code test 必须失败。
- 删除 stored payload revalidation：tampered payload test 必须失败，且 HTTP hit counter 会暴露错误。
- 改成 JSON body 或写错 Content-Type：form-body test 必须失败。
- 添加 client_secret 到 request：no-secret test 必须失败。
- 删除 refresh_token required check：missing-refresh test 必须失败。
- 吞掉 upstream body：invalid_grant classification fixture 必须失败。

### CHG-4: Credential payload / OpenAI refresh integration hardening (0.50 天)

目标：让 callback 产物能被现有 `credentialstore` 与 `credentialworker` refresh path 使用，同时避免把 built-in token endpoint 当成可被 credential payload 改写的信任输入。

未来文件范围：

- Modify/Create in `backend/internal/credentialacq/chatgpt_oauth.go`。
- Maybe modify: `backend/internal/credentialworker/adapters/openai.go` only if tests show acquisition-side omission is insufficient.
- Test: `backend/internal/credentialacq/chatgpt_oauth_test.go`; optional targeted adapter test in `backend/internal/credentialworker/adapters/openai_chatgpt_test.go` only if adapter behavior must change. `credentialworker/adapters` 当前 6 个非测试文件 / 683 行，新增测试文件不影响 source budget。

验收点：

- Callback payload contains access_token, session_token alias, refresh_token, client_id, expires_at when upstream supplies TTL.
- Built-in ChatGPT payload should not store `oauth_token_endpoint` by default; refresh should use `OpenAIRefresh` default endpoint unless Owner explicitly wants per-account override.
- Current `OpenAIRefresh` invalid_grant body propagation and hostile-field scrub remain intact.
- If adapter change is required, it must be scoped to ChatGPT mode or safely preserve `openai/refresh_token` operator override semantics.

Mutation 自检 fixture：

- Reintroduce `oauth_token_endpoint` into ChatGPT payload: payload test must fail.
- Remove client_id from payload while registry adapter lacks explicit ClientID: refresh request fixture must fail by observing missing client_id.
- Delete `mergeTokenResponse` scrub: existing scrub test remains red; if new fixture added, it must detect stale hostile endpoint/client_secret.
- Return 401 invalid_grant body from refresh: classifier fixture must still classify auth_expired, not temporary/unknown.

### CHG-5: ChatGPT metadata preservation decision (0.50 天)

目标：按 Owner D-3 决定是否保存 ChatGPT user/account/plan metadata。默认 plan 不建 schema，不改 DB；仅使用 encrypted credential payload 和/or RedactedContext。

未来文件范围：

- Modify/Create in `backend/internal/credentialacq/chatgpt_oauth.go`。
- Test in `backend/internal/credentialacq/chatgpt_oauth_test.go` and existing audit tests if RedactedContext keys are added.

Options:

- D-3=A: encrypted credential payload 保存 ChatGPT metadata，RedactedContext 只写 `chatgpt_metadata_present=true` 和 non-identifying plan bucket。
- D-3=B: encrypted credential payload + RedactedContext 都保存 exact ChatGPT user/account/plan keys，便于 Admin Ops 扫描和 workspace mismatch 运营。
- D-3=C: 本轮不保存 metadata，只保存 tokens，metadata 标 Mandatory Roadmap。

Codex recommendation: A for first landing unless Owner explicitly accepts PII in RedactedContext. This preserves capability in encrypted material and avoids silently dropping metadata; Admin Ops display can read through credential-safe projection later.

Mutation 自检 fixture：

- Upstream token response/JWT includes account and plan values; payload test asserts selected D-3 fields are present.
- RedactedContext test asserts no access/refresh/session token or authorization code can pass `ValidateRedactedContext`.
- If D-3=A, adding raw user/account identifiers to RedactedContext must fail the expectation.
- If D-3=B, deleting any selected metadata key must fail, and a secret-looking value must be rejected by `ValidateRedactedContext`.

### CHG-6: Production wiring, mimicry posture, docs/roadmap sync (0.25-0.75 天)

目标：把 ChatGPT exchanger 接到 production registry/wiring，并按 D-4 决定 mimicry 是本轮做、仅留 injection seam，还是 Mandatory Roadmap。

未来文件范围：

- Modify: `backend/internal/credentialacq/vendor_exchangers.go` and any production wiring file discovered during synthesis.
- Optional docs: `docs/10_RISK_REGISTER.md`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/07_REFERENCE_EVIDENCE_LEDGER.md`, `docs/11_ACCEPTANCE_TEST_MATRIX.md` if Owner wants release-gate artifacts in same slice.
- No frozen package additions. Do not add files to `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

Options:

- D-4=A: mimicry full implementation is Mandatory Roadmap; this slice adds only an injected HTTP client seam and fail-loud self-check pattern.
- D-4=B: this slice wires an existing controlled OAuth HTTP client, but no new mimicry transport.
- D-4=C: full ChatGPT mimicry transport in this slice. This is higher risk and may require separate parallel-draft plan.

Codex recommendation: A or B. Do not block OAuth correctness on mimicry, but do not hide the gap.

Mutation 自检 fixture:

- Zero-value exchanger reported as production-ready: self-check test must fail.
- Injected test client ignored during exchange: hit-counter test must fail.
- Production registry accidentally falls back to fake exchanger: default-registry fake-bypass test must fail.

## 4. 参考项目对照

| Dimension | CLIProxyAPI evidence | openai-codex evidence | HUAKAI implication |
| --- | --- | --- | --- |
| Public app identity | Same OpenAI app id observed with auth/token/redirect constants: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:24-27` | Same app id observed independently: `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:921` | Treat value as two-source verified protocol constant; do not copy surrounding code or names. |
| Token endpoints | Auth/token endpoints observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:24-25` | Refresh/revoke endpoints observed: `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93-95` | Use auth/token constants for exchange; record revoke as roadmap unless Owner scopes revocation. |
| Redirect | Loopback callback observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:27` | No admin callback evidence in the read manager region; openai-codex cited region is refresh/backend oriented: `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93-95` | Default D-1=A loopback-only. Admin callback needs separate proof/Owner acceptance. |
| Authorization request | Scope and extra query observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:68-79` | Not observed in manager.rs read; no contrary source evidence. | Implement as behavior evidence from MIT source; add tests so future removal is visible. |
| Code exchange | Form body, PKCE verifier, no client secret observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:103-118` | openai-codex manager read covers refresh, not browser auth exchange. | Use HUAKAI/Gemini form exchange pattern; require no client secret. |
| Refresh body shape | Form refresh observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:191-204` | JSON refresh observed: `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:806-825` | Do not conflate. Keep current HUAKAI OpenAI form refresh for first slice; add compatibility risk/decision. |
| Metadata | id token parsing to account/email observed: `CLIProxyAPI@21fad9d-local-export:internal/auth/codex/openai_auth.go:151-178`, `:235-255` | ChatGPT account/plan/user/workspace metadata observed: `openai-codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:108-118`, `:364-385`, `:656-677`, `:928-949` | Preserve metadata outcome via HUAKAI-owned payload shape; D-3 decides RedactedContext exposure. |
| License | Local LICENSE says MIT: `/home/codex/refs/CLIProxyAPI/LICENSE:1-9` | Local LICENSE says Apache-2.0: `/home/codex/refs/openai-codex/LICENSE:1-4` | Constants/behavior evidence acceptable; no source structure, code, comments, or tests copied. |

## 5. 风险登记

| ID | Risk | Severity | Mitigation |
| --- | --- | --- | --- |
| R-CHG-1 | ChatGPT OAuth callback remains fake JSON accept path if registry replacement misses default entry. | S1 | CHG-1/3 default-registry fake-bypass test modeled after Anthropic. |
| R-CHG-2 | Admin callback redirect may not be registered with OpenAI public app; using it without evidence breaks login or leaks code. | S1 | D-1 default loopback-only; dual mode requires Owner decision and allowlist tests. |
| R-CHG-3 | `codex_cli_simplified_flow=true` may be necessary for observed app but could be brittle if OpenAI changes behavior. | S2 | D-2 explicit decision; test presence; feature flag only if Owner rejects hardcoded behavior. |
| R-CHG-4 | Missing refresh_token would create a credential that passes runtime but expires without recovery. | S1 | CHG-3 require refresh_token for `chatgpt_oauth`; access-only is not accepted unless Owner creates a manual-first mode. |
| R-CHG-5 | Metadata keys may expose PII if copied into RedactedContext/audit surfaces. | S1/S2 depending D-3 | Default encrypted payload only; if RedactedContext selected, add audit sanitizer tests and Owner acceptance. |
| R-CHG-6 | `oauth_token_endpoint` in credential payload can become SSRF/auth-leak input on refresh. | S1 | Built-in ChatGPT payload must omit endpoint; existing OpenAI scrub remains; adapter change only if needed. |
| R-CHG-7 | Refresh request body differs between references (form vs JSON). | S2 | Start with HUAKAI existing adapter + CLIProxyAPI evidence; add canary/live manual validation before release. |
| R-CHG-8 | No ChatGPT mimicry transport in first OAuth slice may work functionally but leave anti-abuse/ban-resilience gap. | S2 | D-4 decide seam vs roadmap vs full implementation; never mark release parity complete without recorded posture. |
| R-CHG-9 | Clean-room contamination from reading reference source. | S0 if violated | Constants and protocol keys only; HUAKAI-owned file/function/test names; no copied comments, structs, function names, layouts, or upstream tests. |
| R-CHG-10 | Package growth discipline violation. | S1 | `credentialacq` is under budget; add at most one focused implementation file. Do not add files to frozen packages. |

## 6. Owner 决策点

### D-1: redirect_uri 模式

- A: Loopback only, `http://localhost:1455/auth/callback`. Fastest and source-supported.
- B: Admin HTTPS callback only. Not recommended without OpenAI app registration proof.
- C: Dual mode like Gemini: loopback + allowlisted admin callback. Higher ops value, but source evidence absent for OpenAI app acceptance.

Codex recommendation: A for first landing; record C as follow-up unless Owner already has OpenAI-side callback proof.

### D-2: simplified-flow query flag

- A: Keep observed `codex_cli_simplified_flow=true` in authorize URL.
- B: Omit and attempt standard OAuth flow.
- C: Feature-flag query inclusion.

Codex recommendation: A. It is observed in CLIProxyAPI and likely part of the public app's intended flow. If Owner worries about brittleness, choose C but default on.

### D-3: ChatGPT-specific metadata persistence

- A: Persist selected metadata only in encrypted credential payload; RedactedContext stores non-identifying summary.
- B: Persist selected metadata in encrypted payload and RedactedContext for Admin Ops visibility.
- C: Do not persist metadata now; mark Mandatory Roadmap.

Codex recommendation: A unless Owner explicitly approves B's PII tradeoff.

### D-4: mimicry path

- A: Mandatory Roadmap; this slice only adds client injection seam/fail-loud guard.
- B: Wire existing controlled OAuth HTTP client now; defer full mimicry.
- C: Full ChatGPT mimicry in this slice.

Codex recommendation: A or B. C deserves its own plan because mimicry can expand transport/security blast radius.

### D-5: refresh endpoint trust

- A: ChatGPT built-in payload does not store endpoint; refresh adapter uses default OpenAI endpoint.
- B: Dedicated ChatGPT refresh adapter ignores credential endpoint and pins endpoint/client id internally.
- C: Store endpoint in credential payload and rely on scrub after first successful refresh.

Codex recommendation: A for smallest safe slice; B if synthesis wants stronger defense-in-depth. Do not choose C.

### D-6: refresh_token requirement

- A: Require refresh_token in callback token response.
- B: Accept access-only response and mark credential non-refreshable/manual-first.

Codex recommendation: A. Current handler declares `chatgpt_oauth` refreshable; access-only would be a feature-quality regression.

### D-7: revoke endpoint

- A: Record revoke support as Mandatory Roadmap.
- B: Include revoke/logout capability in this slice.

Codex recommendation: A. openai-codex source confirms endpoint, but HUAKAI current credential lifecycle slice is acquisition/refresh, not revocation.

## 7. 工时估 + commit groupings

Total implementation estimate after synthesis: 2.50-3.25 engineer-days, excluding live OpenAI callback validation.

Proposed commit groups:

1. `test(credentialacq): guard chatgpt oauth registry and builtin profile` (CHG-1 tests first, then minimal exchanger/registry replacement) - 0.50d.
2. `feat(credentialacq): build chatgpt oauth authorization URL` (CHG-2 authorize URL, redirect validator per D-1) - 0.50d.
3. `feat(credentialacq): exchange chatgpt oauth authorization codes` (CHG-3 store-aware callback, token request, fake-bypass/tamper tests) - 0.75d.
4. `fix(credentialacq): persist chatgpt refreshable token payload safely` (CHG-4 payload shaping, refresh compatibility tests) - 0.50d.
5. `feat(credentialacq): preserve chatgpt account metadata per owner decision` (CHG-5 D-3 implementation) - 0.50d.
6. `docs(process): record chatgpt oauth risks and roadmap decisions` (CHG-6 docs/risk/parity updates if Owner wants same slice) - 0.25-0.50d.

Per-commit gate:

- Stage intended diff only.
- Run targeted Go tests first: `go test ./backend/internal/credentialacq ./backend/internal/credentialworker ./backend/internal/credentialworker/adapters ./backend/internal/credentialstore`.
- Run broader backend tests if targeted changes touch shared OAuth/refresh behavior.
- Run `codex exec review --uncommitted --full-auto --sandbox read-only` before commit per AGENTS per-commit review discipline.
- No commit may land with unresolved S0/S1: fake callback accept path, weak/non-discriminating test, clean-room leak, endpoint SSRF, auth/billing/quota/security regression.

## 8. clean-room 约束

- 可用内容：public OAuth endpoint URLs、public app id value、scope strings、standard OAuth protocol keys、observed JSON protocol keys that HUAKAI must interoperate with.
- 不可用内容：reference function names, Rust/Go type names, struct field layout, comments, test cases, file organization, algorithmic line-by-line shape.
- HUAKAI implementation must use local naming and local patterns: `chatgpt_oauth.go` can follow HUAKAI `anthropic_oauth.go` / `gemini_oauth.go` responsibilities, but not copy CLIProxyAPI/openai-codex source structure.
- Reference-derived behavior must be phrased as observed behavior with citations, not as copied source.
- Do not read LGPL/GPL/AGPL refs for this slice. Current plan only read local CLIProxyAPI MIT export and local openai-codex Apache-2.0 clone.
- Constants are acceptable because they are external protocol interoperability values; surrounding source code, comments, and internal names are not.
- If live OpenAI behavior contradicts references, update plan/risk with observed outcome; do not invent parity claims from memory.

## Open Questions

1. 是否已有 OpenAI app registration 证明支持 HUAKAI admin callback URL？若无，D-1 应默认 loopback-only。
2. OpenAI token endpoint 对 refresh grant 是否同时接受 form 和 JSON？当前 sources 分歧，release 前需要 canary/live validation。
3. `codex_cli_simplified_flow=true` 是否应永久 hardcode，还是以 feature flag 默认开启？
4. ChatGPT metadata 是否属于 Owner 可接受的 RedactedContext PII？
5. ChatGPT OAuth 是否必须用 mimicry transport 才能进入 release，还是可先以 functional OAuth + roadmap 进入 internal beta？
6. 是否需要在本切片加入 revoke/logout，还是等 Account Hub lifecycle slice？
7. 是否需要把 `chatgpt_oauth` refresh adapter 从 generic OpenAIRefresh 独立出来，彻底 pin endpoint/client id？

## Source Coverage Proof

- `/home/codex/refs/CLIProxyAPI/.huakai-head-sha:1` - confirmed local export SHA marker.
- `/home/codex/refs/CLIProxyAPI/LICENSE:1-9` - confirmed MIT license text.
- `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:24-27` - observed auth/token endpoint, public app id, loopback redirect.
- `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:68-79` - observed scope and extra authorization query behavior.
- `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:103-118` - observed authorization-code exchange request shape.
- `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:138-178` - observed token response fields and account/email derivation.
- `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:191-204` - observed refresh request shape.
- `/home/codex/refs/openai-codex/LICENSE:1-4` - confirmed Apache-2.0 license text.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:93-95` - observed ChatGPT backend/refresh/revoke endpoints.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:108-118` - observed external ChatGPT metadata container behavior.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:364-385` - observed user id and plan-type read behavior.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:656-677` - observed workspace/account restriction behavior.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:806-825` - observed refresh request body/content type behavior.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:921` - observed public app id.
- `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:928-949` - observed external-token metadata merge into stored auth semantics.
- `backend/internal/credentialacq/vendor_exchangers.go:38-45`, `:162-188`, `:191-225` - confirmed ChatGPT fake registration and fake callback behavior.
- `backend/internal/credentialacq/types.go:151-160`, `:194-206` - confirmed mode plan and OAuth-only helper enforcement function.
- `backend/internal/credentialacq/session_store.go:110-129` - confirmed CreateFromStart P0 gate.
- `backend/internal/credentialacq/anthropic_oauth.go:57-113`, `:116-202` - confirmed Anthropic built-in/store-aware pattern.
- `backend/internal/credentialacq/gemini_oauth.go:92-107`, `:161-259`, `:285-359` - confirmed Gemini built-in/profile/redirect/query/exchange pattern.
- `backend/internal/credentialacq/oauth_authorization_code.go:27-43`, `:95-147`, `:149-202`, `:221-285` - confirmed generic authorization-code OAuth helper and endpoint payload behavior.
- `backend/internal/credentialacq/oauth.go:180-200` - confirmed generic authorize URL lacks ChatGPT extra query params.
- `backend/internal/credentialworker/adapters/openai.go:17-53`, `:147-207`, `:218-257` - confirmed OpenAI refresh endpoint behavior, error body propagation, and scrub.
- `backend/internal/credentialacq/oauth_devicecode.go:21-25`, `:41-55`, `:413-512` - confirmed Codex device-code path is separate.

Source files read: /home/codex/refs/CLIProxyAPI/.huakai-head-sha; /home/codex/refs/CLIProxyAPI/LICENSE; /home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go; /home/codex/refs/openai-codex/LICENSE; /home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs; backend/internal/credentialacq/vendor_exchangers.go; backend/internal/credentialacq/types.go; backend/internal/credentialacq/session_store.go; backend/internal/credentialacq/anthropic_oauth.go; backend/internal/credentialacq/anthropic_oauth_test.go; backend/internal/credentialacq/gemini_oauth.go; backend/internal/credentialacq/gemini_oauth_test.go; backend/internal/credentialacq/oauth_authorization_code.go; backend/internal/credentialacq/oauth.go; backend/internal/credentialacq/oauth_devicecode.go; backend/internal/credentialacq/oauth_devicecode_test.go; backend/internal/credentialacq/audit.go; backend/internal/credentialworker/adapters/openai.go; backend/internal/credentialworker/mode_refresh.go; backend/internal/credentialstore/types.go

Lane: specifier

Agent: Codex GPT-5

UTC timestamp: 2026-05-27T09:18:21Z

Owner 中文摘要：本计划只做 codex lane 独立规划，真实观察包括 HUAKAI 当前 `chatgpt_oauth` 仍是 fake exchanger、已有 OAuth-only start gate、Anthropic/Gemini 的 store-aware OAuth 模式、OpenAI refresh 现状，以及 CLIProxyAPI/openai-codex 对 ChatGPT OAuth constants/metadata/refresh 的源码证据；合理推断包括优先 loopback-only、先复用 HUAKAI OpenAI form refresh、metadata 默认进 encrypted payload 不进 RedactedContext；open questions 共 7 个，主要卡在 redirect admin callback、refresh body shape、metadata PII、mimicry 和 revoke 是否入本轮。
