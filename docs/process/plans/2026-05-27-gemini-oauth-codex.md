# 2026-05-27 Gemini code_assist + google_one 真 OAuth 接通 — Codex Lane

## 0. Plan 元信息

| 字段 | 内容 |
| --- | --- |
| Owner directive | “写 docs/process/plans/2026-05-27-gemini-oauth-codex.md (Markdown plan, 不写代码)。” |
| Scope | 只起草 Codex lane plan；不写实现代码；不读取 `docs/process/plans/2026-05-27-gemini-oauth-claude.md`；不读取 LGPL refs。 |
| Success criteria | 给 Claude PM 一份可合并的切片计划，覆盖 fake exchanger 替换、admin 真入口测试、refresh SSRF/credential-payload 撤权、mimicry 缺口、docs/review gate。 |
| Time estimate | 计划编写 1h；后续实现建议 3-5 个小 slice，约 1.5-2.5 个开发日，加 Owner 真账号 smoke 时间。 |
| Blast radius | `credentialacq` OAuth 起手/回调、`credentialworker` refresh、`provider/gemini` refresh helper、admin credential acquisition tests、可选 transport mimicry wiring；不涉及 DB schema、billing ledger、quota enforcement、`LICENSE`。 |
| Failure modes | fake JSON callback 继续被接受；refresh 继续读 credential payload endpoint/client；Google redirect/scope/client_secret 不匹配；mimicry 错用 `gemini_advanced` 指纹；测试非判别性；antigravity 暂搁路径被误改。 |
| Decision points | D-1/D-2/D-3/D-4/D-5 见 §6；任何硬编 public client secret、transport mode 新增、生产启动 fail-closed 策略需 Owner 明确确认。 |
| Pre-execution checklist | 先写红测；确认目标包非 frozen；只改小文件；每 slice 本地 test；stage 后跑 `codex exec review --uncommitted --full-auto --sandbox read-only`。 |

证据说明：`~/refs/CLIProxyAPI` 是无 `.git` 的本地快照，引用 SHA 取自 `.huakai-head-sha`，值为 `21fad9dbb447a2ab70d51d0ac3e3d032525a6054`。

## 1. 现状盘点

- `backend/internal/credentialacq/vendor_exchangers.go:40` 和 `:41` 仍把 `gemini/code_assist`、`gemini/google_one` 注册到 `NewPKCEFakeExchanger`；`gemini/oauth` 另走 generic operator-config authorization-code exchanger (`:43`)。
- `backend/internal/credentialacq/oauth_authorization_code.go:216` 起的 operator PKCE 校验已有静态 SSRF 闸门；同文件 `:117` 注释仍记录 dial-time guard 未接入，后续不能把 Gemini 真 OAuth 建在 credential-controlled endpoint 上。
- `backend/internal/credentialworker/mode_refresh.go:79` 和 `:80` 把 `code_assist/google_one` 接到 legacy `adapters.GeminiRefresh`，并开启 cross-client fallback；`antigravity` 包装同类 Gemini refresh (`:81`)。
- `backend/internal/credentialworker/adapters/gemini.go:59` 会从 credential payload 取 `client_id`，`:67` 会从 credential payload 取 `oauth_token_endpoint`，`:70` 会从 credential payload 取 `fallback_client_id`；这是 Gemini S1-D 残留。
- 已有安全等价物在 `backend/internal/provider/gemini/refresher.go:200` 起：`RefreshAdapter` 要求 operator/builtin 传入 token URL、client ID、scope，并在 `:221`-`:229` 组 form，不读 credential endpoint/client/scope；对应判别测试见 `backend/internal/provider/gemini/refresher_test.go:25`。
- `backend/internal/provider/gemini/passthrough.go:29` 起是 Gemini API key/generativelanguage 直通；`backend/internal/provider/gemini/gemini_advanced_session.go:24` 起是 Gemini Advanced 网页 session 占位。当前未观察到 Cloud Code Assist 专用出站 session adapter。
- HUAKAI 已有 `ApprovedGeminiCrossClientFallback` 白名单 (`backend/internal/credentialworker/adapters/gemini.go:111`) 和 refresh 事务内 audit (`backend/internal/credentialworker/mode_refresh.go:262`)；问题不是没有白名单，而是 fallback 目标仍可由 credential payload 提供。
- mimicry 现状：`backend/internal/transport/mimicry/registry.go:206` 能识别 `gemini-advanced` 模板；但 sidecar profile 映射目前只给 Claude (`backend/internal/transport/mimicry/registry.go:35`)；`ProviderGemini` 只允许 standard/diagnostics，不允许 Gemini Advanced mimicry (`backend/internal/transport/policy.go:167`)。

## 2. 缺口分类

- **exchanger**：`code_assist/google_one` 要从 fake JSON payload 切到真实 authorization-code exchange；应复用 Anthropic ANT-1 形态：内置 profile + stored PKCE verifier + callback 必打 token endpoint。
- **refresh adapter**：默认 refresh 不能再信 credential payload 的 `oauth_token_endpoint`、`client_id`、`scope`、`fallback_client_id`；应复用 `provider/gemini.RefreshAdapter` 的 fail-closed 设计，或把同等约束抽成 credentialworker 可用的 builtin adapter。
- **mimicry transport**：未观察到 Gemini CLI/Code Assist 专用 mimicry profile；不能把 `mimicry_gemini_advanced` 当作 Code Assist token/client profile。GEM-4 应先做 fail-loud 判定，再决定是否新增 profile。
- **tests**：需要 admin real-entry 判别测试、fake callback mutation、refresh SSRF mutation、fallback payload mutation、wiring 自检 mutation；每个测试必须能在对应缺陷回归时变红。
- **docs/gates**：需要更新 reference evidence、risk register、parity matrix、deferred review 或 Owner decision 记录；每 commit 走 Codex review。

## 3. Slice 切片

### GEM-1 真 OAuth exchanger 替换 fake

| 项 | 内容 |
| --- | --- |
| 范围 | 只处理 `gemini/code_assist`、`gemini/google_one` acquisition；`antigravity` 保持暂搁，不在本 slice 改行为。 |
| 文件 | 新建 `backend/internal/credentialacq/gemini_oauth.go` / `gemini_oauth_test.go`；修改 `backend/internal/credentialacq/vendor_exchangers.go`。目标包未在 frozen package 列表中。 |
| 成功标准 | `DefaultExchangerRegistry` 中 `code_assist/google_one` 不再是 fake exchanger；callback 必 POST token endpoint；传 fake JSON callback 时 token endpoint hit=1 且返回 exchange failure；成功 payload 含 access/refresh token、expiry、client identity source。 |
| Tests | `go test ./internal/credentialacq -run 'TestGemini.*OAuth|TestDefaultExchangerRegistry'`，红测先覆盖 fake bypass mutation。 |
| 时间估计 | 0.5 天。 |
| 风险 | Google public OAuth companion `client_secret` 是否可硬编未决；若只硬编 ClientID 而 Google endpoint 要 secret，真 smoke 会失败，需 D-1。 |

### GEM-2 admin real-entry test

| 项 | 内容 |
| --- | --- |
| 范围 | 通过 admin credential acquisition handler 走真实 start/callback/finalize 路径，确保路由层没有继续接受 fake paste/JSON helper。 |
| 文件 | 修改 `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go`；必要时给 GEM-1 exchanger 暴露测试注入 client 的 constructor。冻结包规则允许改既有 `gatewayhttp` 测试文件，但不新增该 frozen 包文件。 |
| 成功标准 | `POST oauth-init` 生成 Google authorize URL；`GET/POST callback` 触发 mock token endpoint；fake JSON callback 返回 4xx 并写 `exchange_failed`；成功 finalize 后 credentialstore handler 能解析 runtime material。 |
| Tests | `go test ./internal/gatewayhttp -run 'TestAdminGemini.*OAuth|TestAdminCredentialAcquisition'`。 |
| 时间估计 | 0.5 天。 |
| 风险 | Handler test fixture 不能只测 exchanger helper；必须经过 mounted route，否则会漏掉 admin entry 的 fake bypass。 |

### GEM-3 refresh SSRF 修 + builtin ClientID

| 项 | 内容 |
| --- | --- |
| 范围 | 把 `code_assist/google_one` scheduled refresh 从 legacy `adapters.GeminiRefresh` 切到 fail-closed builtin/operator config；撤回 credential payload `fallback_client_id` 决策权。 |
| 文件 | 修改 `backend/internal/credentialworker/mode_refresh.go`、`backend/internal/credentialworker/mode_refresh_test.go`、`backend/internal/credentialworker/adapters/gemini.go` 或改为复用 `backend/internal/provider/gemini/refresher.go`；必要时新增 `backend/internal/credentialworker/gemini_builtin_mode.go`。 |
| 成功标准 | 恶意 credential payload 中的 endpoint/client/scope/fallback_client_id 不影响请求；默认 token endpoint 为 Google OAuth token endpoint；client family fallback 只能来自 operator/builtin policy + `ApprovedGeminiCrossClientFallback`；antigravity 包装路径测试仍保持现状或显式标记 paused。 |
| Tests | `go test ./internal/credentialworker ./internal/provider/gemini -run 'Test.*Gemini.*Refresh|TestDefaultModeAdapterRegistry'`；mutation：删 endpoint guard、读 payload client、读 payload fallback 均应红。 |
| 时间估计 | 0.5-0.75 天。 |
| 风险 | 直接删除 fallback 会造成实际 Google One/Code Assist 互刷能力缩水；安全做法是把 fallback 改为 operator/builtin allowlist，而不是功能删除。 |

### GEM-4 mimicry transport 接回

| 项 | 内容 |
| --- | --- |
| 范围 | 先判定 Code Assist/Gemini CLI 是否有独立 mimicry profile；未有时不得复用 `gemini_advanced`。若 Owner 要本轮上线强 mimicry，则新增独立 `gemini-cli/code-assist` profile 与 fail-loud wiring。 |
| 文件 | 可能涉及 `backend/internal/transport/policy.go`、`backend/internal/transport/mimicry/registry.go`、`tools/fingerprint-collector/templates/gemini-cli.json`、`backend/cmd/gateway/wiring.go`、`backend/internal/credentialacq/gemini_oauth.go`。这些不是 frozen packages；`wiring.go` 为启动核心，按 medium risk 记录。 |
| 成功标准 | 有 profile：生产 token exchange/client request 使用专用 profile，startup 自检能抓到未安装 client；无 profile：计划记录 `Mandatory Roadmap` 或 feature flag，token exchange 明确走 SSRF-protected standard transport，不 silent 假装 mimicry 已接。 |
| Tests | `go test ./cmd/gateway ./internal/transport/... ./internal/credentialacq -run 'Test.*Gemini.*Mimicry|TestTemplateRegistry|TestWiring'`。 |
| 时间估计 | 判定 0.25 天；新增 profile + wiring 0.75-1 天。 |
| 风险 | 错 profile 比无 profile 更危险，会给反检测假信心；sidecar profile 当前只观察到 Claude 映射，新增需 Owner 认可。 |

### GEM-5 docs + 全量 hard gate

| 项 | 内容 |
| --- | --- |
| 范围 | 收口 docs、review、test gates；不扩大 antigravity。 |
| 文件 | 更新 `docs/07_REFERENCE_EVIDENCE_LEDGER.md`、`docs/03_FEATURE_PARITY_MATRIX.md`、`docs/10_RISK_REGISTER.md`、必要的 `docs/process/decisions/*` 或 deferred review 文件。 |
| 成功标准 | 每个 reference claim 带 `CLIProxyAPI@21fad...:file:line`；Gemini OAuth feature 标为 `Implemented` / `Implemented Better` / `Feature Flag` / `Mandatory Roadmap`；clean-room 风险明确为 MIT source evidence，未复制实现。 |
| Tests | 从 `backend/` 跑 targeted tests；再跑相关 package aggregate；stage 后跑 `codex exec review --uncommitted --full-auto --sandbox read-only`。 |
| 时间估计 | 0.25-0.5 天。 |
| 风险 | Docs 把未完成 mimicry 写成已完成会违反 Truth-First；未跑 Codex review 会违反 per-commit gate。 |

## 4. 参考项目对照 (CLAUDE.md #15)

| 主题 | CLIProxyAPI cite | HUAKAI delta |
| --- | --- | --- |
| Google CLI ClientID | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/gemini/gemini_auth.go:31`; 同 ID 也在 Gemini CLI executor `:38` | 建议同款 public ClientID 进入 HUAKAI builtin profile，但加 profile validation；public companion secret 是否进入 builtin 由 D-1 决定。 |
| Token endpoint | token metadata 写入 `https://oauth2.googleapis.com/token`：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/gemini/gemini_auth.go:178`; refresh fallback 同 endpoint：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/auth/filestore.go:358` | HUAKAI 用同 endpoint，但必须在 static + dial-time SSRF 层 fail-closed；credential payload 不能覆盖 endpoint。 |
| Scope | CLIProxyAPI Gemini scopes 从 `cloud-platform` 起，另含 userinfo email/profile：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/gemini/gemini_auth.go:37`; executor 复用同 scopes：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/gemini_cli_executor.go:42` | HUAKAI D-4 默认建议同组 scope，后续按 least privilege 缩小；scope 必须来自 builtin/operator profile，不能读 credential payload。 |
| Code Assist endpoint | CLIProxyAPI Gemini CLI executor 指向 `cloudcode-pa.googleapis.com` / `v1internal`：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/gemini_cli_executor.go:36`; request URL 组装见 `:186` | HUAKAI 当前 provider/gemini 未观察到 Cloud Code Assist 专用 session adapter；OAuth 接通先解决 credential acquisition/refresh，出站 Code Assist adapter 需单独 scope 或 mandatory roadmap。 |
| Google One mode | CLI login 在无 project 时区分 Code Assist 手选项目与 Google One 自动发现：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/cmd/login.go:103`; management callback 的 Google One 分支见 `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1744` | HUAKAI `google_one` 应共享真 OAuth profile，但 onboarding/project discovery 不应塞进 credential payload；需要后续 account-hub workflow 或 manual-first gate。 |
| Cross-client fallback | exact search 未观察到 CLIProxyAPI 使用 `fallback_client_id`/`cross_client` payload pattern；读到的是同一 Gemini public client 在 auth/executor/management 路径复用：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/api_tools.go:311` | HUAKAI 已有 `ApprovedGeminiCrossClientFallback` 白名单；升级方向是撤回 credential payload `fallback_client_id`，改 operator-config + family allowlist。 |

## 5. 风险登记

- **R-GEM-SSRF-001**：`credentialworker/adapters.GeminiRefresh` 仍可能把 credential payload 当 OAuth trust root；GEM-3 必须关闭，不能等到出站 adapter 后补。
- **R-GEM-CLIENT-001**：Google public ClientID 或 companion secret 变动会导致真实授权失败；用 builtin profile validation + operator override release gate 缓解。
- **R-GEM-REDIRECT-001**：Google OAuth registered redirect 可能不接受 HUAKAI admin callback；D-3 需决定 loopback/admin callback/双模式。
- **R-GEM-SCOPE-001**：`cloud-platform` 权限宽，`userinfo` 对 account identity 有用；D-4 需在兼容与最小权限间取舍。
- **R-GEM-MIMICRY-001**：当前只观察到 Gemini Advanced mimicry，不是 Code Assist/Gemini CLI profile；GEM-4 禁止错用。
- **R-GEM-ANTIGRAVITY-001**：`mode_refresh.go:81` 的 antigravity 包装复用 Gemini refresh；GEM-3 helper 抽取时可能误改 paused path，必须用测试固定现状。
- **R-GEM-SMOKE-001**：没有 Owner Google 真账号/已授权 redirect 时无法证明 live OAuth；可先用 mock + Owner 手动 smoke gate，不伪造通过。

## 6. Owner 决策点

- **D-1 Client identity 来源**：推荐硬编公开 Google CLI ClientID + profile validation；public companion `client_secret` 是否硬编必须 Owner 明确确认，保守路径是 operator-config 注入 secret。
- **D-2 Cross-client fallback**：推荐撤回 credential payload `fallback_client_id`；保留能力但改成 operator-config/builtin family policy + audit。
- **D-3 redirect_uri**：推荐双模式：admin callback 用于 HUAKAI 控制台，loopback 用于 CLI/manual；二者都必须进 allowlist，不接受任意 payload redirect。
- **D-4 scope**：推荐首版同 CLIProxyAPI：`https://www.googleapis.com/auth/cloud-platform` + `userinfo.email` + `userinfo.profile`；若 Owner 要最小权限，先做 Google live smoke 再缩。
- **D-5 mimicry release gate**：若本轮目标是“OAuth 接通”，GEM-4 可标 feature flag/roadmap；若目标包含反检测上线，则必须新增 Gemini CLI/Code Assist profile 后才 release。

## 7. 工时 + 推荐起步

推荐起步顺序：GEM-1 红测和 exchanger 先落；紧接 GEM-2 admin real-entry 防止只测 helper；然后 GEM-3 关 refresh SSRF；GEM-4 作为独立 gate，不阻塞“真 OAuth token acquisition”但阻塞“mimicry 已完成”宣称；GEM-5 收口 docs/review。

推荐首 commit：GEM-1 + GEM-2，只改 `credentialacq` 和既有 `gatewayhttp` test 文件，形成可审查的闭环。第二 commit 做 GEM-3，避免 acquisition 与 refresh 风险混在一起。GEM-4 若 Owner 选“本轮必须 mimicry”，单独 commit，避免 transport profile 变化污染 OAuth correctness review。

Source files read: backend/internal/credentialacq/vendor_exchangers.go; backend/internal/credentialacq/oauth_authorization_code.go; backend/internal/credentialacq/oauth.go; backend/internal/credentialacq/anthropic_oauth.go; backend/internal/credentialacq/anthropic_oauth_test.go; backend/internal/credentialacq/types.go; backend/internal/credentialacq/session_store.go; backend/internal/credentialstore/types.go; backend/internal/credentialworker/mode_refresh.go; backend/internal/credentialworker/mode_refresh_test.go; backend/internal/credentialworker/refresh_adapter.go; backend/internal/credentialworker/refresher_test.go; backend/internal/credentialworker/adapters/gemini.go; backend/internal/provider/gemini/bootstrap.go; backend/internal/provider/gemini/bootstrap_test.go; backend/internal/provider/gemini/refresher.go; backend/internal/provider/gemini/refresher_test.go; backend/internal/provider/gemini/credential_store_adapter.go; backend/internal/provider/gemini/credential_store_adapter_test.go; backend/internal/provider/gemini/passthrough.go; backend/internal/provider/gemini/gemini_advanced_session.go; backend/internal/anthropicoauth/exchanger.go; backend/internal/anthropicoauth/client_id.go; backend/internal/anthropicoauth/transport.go; backend/internal/anthropicoauth/token.go; backend/internal/transport/policy.go; backend/internal/transport/factory.go; backend/internal/transport/mimicry/registry.go; backend/internal/transport/mimicry/template.go; backend/internal/transport/mimicry/registry_test.go; backend/cmd/gateway/main.go; backend/cmd/gateway/wiring.go; backend/cmd/gateway/wiring_test.go; ~/refs/CLIProxyAPI/.huakai-head-sha; ~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go; ~/refs/CLIProxyAPI/internal/auth/gemini/gemini_token.go; ~/refs/CLIProxyAPI/internal/runtime/executor/gemini_cli_executor.go; ~/refs/CLIProxyAPI/internal/api/handlers/management/api_tools.go; ~/refs/CLIProxyAPI/internal/api/handlers/management/auth_files.go; ~/refs/CLIProxyAPI/sdk/auth/filestore.go; ~/refs/CLIProxyAPI/internal/cmd/login.go; Lane: codex-specifier; Time: 2026-05-27T01:23:39Z
