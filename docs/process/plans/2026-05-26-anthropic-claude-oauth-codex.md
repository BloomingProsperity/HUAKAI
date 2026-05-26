# 2026-05-26 Anthropic claude_ai_oauth 真 OAuth 接通 — Codex Lane

## 1. 现状盘点

- `backend/internal/credentialstore/types.go`: 已有 `anthropic/claude_ai_oauth` 常量、runtime kind=`oauth_access_token`、`access_token|refresh_token` payload 校验、refreshable+grace 语义，但 refresh_token-only payload 仍能通过校验，运行时没有保证接入前已刷新出 access token（`backend/internal/credentialstore/types.go:22`, `backend/internal/credentialstore/types.go:247`）。
- `backend/internal/credentialacq/types.go`: `DefaultModePlans` 已把 `anthropic/claude_ai_oauth` 标成 OAuth + public CLI client source，这是“真 OAuth”入口的产品占位（`backend/internal/credentialacq/types.go:151`）。
- `backend/internal/credentialacq/oauth.go`: 通用 PKCE start/callback 已有 state hash、PKCE verifier 加密、callback replay/expiry/state 校验，但默认路径仍只生成标准 authorize URL，不支持 Anthropic 额外 authorize 参数与 vendor-specific token exchange（`backend/internal/credentialacq/oauth.go:53`, `backend/internal/credentialacq/oauth.go:127`, `backend/internal/credentialacq/oauth.go:173`）。
- `backend/internal/credentialacq/oauth_authorization_code.go`: 已有 store-aware 真 authorization-code exchanger，但它强制 `source=operator_config` 且用 form body 交换 token；这适合 operator OAuth，不等于 `claude_ai_oauth` public client 路径（`backend/internal/credentialacq/oauth_authorization_code.go:49`, `backend/internal/credentialacq/oauth_authorization_code.go:92`, `backend/internal/credentialacq/oauth_authorization_code.go:210`）。
- `backend/internal/credentialacq/vendor_exchangers.go`: 目前 `anthropic/claude_ai_oauth` 注册到 fake JSON token exchanger，真实 callback 传入 JSON 即可过关；这是本次必须替换的核心缺口（`backend/internal/credentialacq/vendor_exchangers.go:33`, `backend/internal/credentialacq/vendor_exchangers.go:177`）。
- `backend/internal/provider/anthropic/oauth_session.go`: OAuth runtime adapter 已能用 bearer token 构造 Anthropic Messages 请求，且拒绝 API key credential；这是可复用的出站层（`backend/internal/provider/anthropic/oauth_session.go:16`, `backend/internal/provider/anthropic/oauth_session.go:33`）。
- `backend/internal/provider/anthropic/oauth_session_test.go`: 现有测试只覆盖 adapter helper 层，不证明 admin 入口能把 OAuth credential 真正接到该 runtime adapter（`backend/internal/provider/anthropic/oauth_session_test.go:15`）。
- `backend/internal/credentialworker/adapters/anthropic.go`: refresh adapter 已按 Anthropic refresh-token grant 发 JSON 请求，但会从 credential payload 兜底读取 `oauth_token_endpoint` 和 `client_id`，存在 credential-supplied endpoint/client 信任链风险（`backend/internal/credentialworker/adapters/anthropic.go:24`, `backend/internal/credentialworker/adapters/anthropic.go:41`, `backend/internal/credentialworker/adapters/anthropic.go:53`）。
- `backend/internal/credentialworker/mode_refresh.go`: `claude_ai_oauth` 和 `claude_code` 当前都走 legacy OAuth refresh adapter；Gemini/Antigravity 的 operator-bound fail-closed 模式已有先例，但 Anthropic 未套用（`backend/internal/credentialworker/mode_refresh.go:62`, `backend/internal/credentialworker/mode_refresh.go:68`, `backend/internal/credentialworker/mode_refresh.go:376`）。
- `backend/internal/credentialworker/mode_refresh_test.go`: 已有 Gemini/Antigravity “credential-supplied endpoint 不能被信任”的判别性测试，可作为 Anthropic refresh 风险的测试风格参考（`backend/internal/credentialworker/mode_refresh_test.go:129`）。
- `backend/internal/credentialstore/postgres_store.go`: credential vault、CAS refresh 写回、audit 同事务基础已存在；本次不需要 schema 变更，优先复用现有 encrypted credential 记录（`backend/internal/credentialstore/postgres_store.go:58`, `backend/internal/credentialstore/postgres_store.go:682`）。

## 2. 缺口分类

1. **exchanger**: `anthropic/claude_ai_oauth` 仍是 fake JSON exchanger；需要 dedicated store-aware exchanger，真实使用 authorization code + stored PKCE verifier 调 Anthropic token endpoint。
2. **authorize URL profile**: 通用 `BuildAuthorizeURL` 缺少 Anthropic public-client profile 的额外参数、scope 默认值和 endpoint 决策；不能把 CLIProxyAPI 结构搬进来，只抽行为。
3. **token request shape**: 现有 generic true exchanger 用 `application/x-www-form-urlencoded`；CLIProxyAPI 观察到 Anthropic token exchange/refresh 使用 JSON body（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:269`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:364`）。HUAKAI 应做 vendor-specific branch。
4. **worker adapter trust chain**: refresh 不能默认信任 credential payload 里的 endpoint/client；至少要有 operator-approved profile 或 Owner 批准的 public client profile。
5. **admin real entry**: helper-level test 不够。必须补“真实 admin credential acquisition 入口” fail-closed 测试，证明缺 config / 未批准 public client 时不会生成可用 flow、不会落 session、不会走 fake exchanger。
6. **runtime wiring**: OAuth bearer adapter 已有，但还要证明 credentialstore material -> provider adapter 的选择链路不会把 `claude_ai_oauth` 送到 API-key passthrough。
7. **docs/runbook**: 需要记录 ClientID 来源、redirect_uri、scope、live smoke 条件、全量 `./...` 检查；不能只写 helper 测试通过。

## 3. Slice 切片

### ANT-1 — Dedicated Anthropic OAuth Exchanger

**范围**: 替换 `anthropic/claude_ai_oauth` fake exchanger 为 dedicated store-aware exchanger；保留 fake exchanger 给手工恢复或测试专用 alias，不再作为默认生产路径。

**文件**:
- Modify: `backend/internal/credentialacq/vendor_exchangers.go`
- Modify or create: `backend/internal/credentialacq/anthropic_oauth.go`
- Test: `backend/internal/credentialacq/anthropic_oauth_test.go`
- Test: `backend/internal/credentialacq/vendor_exchangers_test.go`

**成功标准**:
- `DefaultExchangerRegistry().Lookup("anthropic/claude_ai_oauth")` 返回真 exchanger，不是 fake JSON exchanger。
- helper test: token server 收到 authorization code、state-bound PKCE verifier、approved client identity、redirect_uri；返回 access/refresh 后 candidate payload 含 access、refresh、expires_at、client identity source。
- helper fail-closed test: 缺 token endpoint/client/redirect/scope 或 public-client profile 未批准时，`StartOAuthFlow` 返回 `ErrFeatureDisabled`，authorize URL 为空，且不创建 session。
- helper mutation self-check: 把 exchanger 退回 fake JSON 时，`code="not-json-auth-code"` 不应通过；否则测试必须红。

**时间估计**: 0.75-1.25 天。

**风险**:
- 如果直接复用 generic form exchanger，真实 Anthropic endpoint 可能不接受；用 dedicated exchanger 隔离。
- 如果 hardcode public client，可能违反 Owner 信任链预期；ANT-1 只做 profile gate，具体默认由 Owner 决策。

### ANT-2 — Admin 真实入口接线 + C1 Fail-Closed 回归

**范围**: 从真实 admin credential acquisition endpoint 发起 `anthropic/claude_ai_oauth`，覆盖缺配置 fail-closed、成功 callback、fake fallback 禁止三条路径。不得在 frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 新增文件；若必须改入口层 frozen 包，只改既有文件并记录原因。

**文件**:
- Modify: 现有 admin credential acquisition handler/wiring 文件（执行前先定位；优先 `backend/internal/adminhttp/**`，如真实入口在 frozen 包则只改既有文件）
- Test: 现有 admin handler 测试文件或新增非 frozen `backend/internal/adminhttp/*_test.go`
- Modify: `backend/internal/credentialacq/finalizer.go` only if finalization needs metadata normalization

**成功标准**:
- admin real-entry fail-closed test: 通过真实 admin HTTP handler 请求 `anthropic/claude_ai_oauth`，在未批准 ClientID/profile 时返回明确 4xx/5xx，session store 无新增 started flow，token endpoint mock 未被调用。
- admin real-entry success test: 通过真实 admin start + callback/finalize 入口，mock token endpoint 返回 access/refresh，credentialstore 保存 encrypted credential，runtime material kind 为 `oauth_access_token`。
- admin real-entry anti-fake test: callback code 传入 JSON-shaped fake token payload 时不能绕过 token endpoint；mock endpoint call count 必须为 1，且 body 中 code 是 callback code。
- helper/admin 标注: 本 slice 所有测试名或注释明确写 `admin real entry`，避免 cursor C1 那种只测 helper 的假覆盖。

**时间估计**: 1-1.5 天。

**风险**:
- 真实 admin 入口可能在 frozen package；允许改既有文件，不允许加新文件。
- Handler 测试可能需要 DB/test store fixture；如果真 PG 才能覆盖，先写 unit-level admin handler fake store，再追加 `integration_pg`。

### ANT-3 — Refresh Worker Trust-Chain Hardening

**范围**: 让 Anthropic scheduled refresh 不再默认信任 credential-supplied endpoint/client；按 Owner 决策接 operator-approved public profile 或 operator config。保持 refresh payload 更新 access/session token 与 expires_at。

**文件**:
- Modify: `backend/internal/credentialworker/adapters/anthropic.go`
- Modify: `backend/internal/credentialworker/mode_refresh.go`
- Test: `backend/internal/credentialworker/refresher_test.go`
- Test: `backend/internal/credentialworker/mode_refresh_test.go`

**成功标准**:
- helper fail-closed test: credential payload 含 attacker token endpoint/client，但 operator/public profile 未配置时，refresh 返回配置缺失错误，HTTP client call count 为 0。
- helper positive test: operator/public profile 配好时，refresh 请求只打 approved endpoint、只带 approved client，credential 内 attacker endpoint/client 被忽略。
- helper payload test: refresh 成功后 access_token、refresh_token、expires_at 被更新；如果 runtime 需要 session_token，则同步为 access_token。
- scheduled path test: `DefaultModeAdapterRegistry().Lookup(anthropic, claude_ai_oauth)` 不再是无约束 legacy adapter，且 `AccountCredentialRefresher.Refresh` 失败会记录 failure class，不写 success payload。

**时间估计**: 0.75-1.25 天。

**风险**:
- 如果 Owner 选择 public CLI hardcode，test 必须证明 hardcode 是 approved profile，不是从 credential payload 学来的。
- 过度 fail-closed 可能让历史 fake credential 全部不可刷新；需 runbook 标明 migration/manual recovery。

### ANT-4 — Runtime Adapter Integration + Expiry Behavior

**范围**: 验证 credentialstore material、provider/anthropic OAuth adapter、refresh expiry 行为连成一条链；不碰 gateway hot path 大重构。

**文件**:
- Modify: `backend/internal/credentialstore/types_test.go`
- Modify: `backend/internal/provider/anthropic/oauth_session_test.go`
- Modify only if needed: `backend/internal/provider/anthropic/oauth_session.go`

**成功标准**:
- helper test: `claude_ai_oauth` payload 的 runtime material 必须是 `oauth_access_token`，不能落到 `api_key` 或 upstream passthrough。
- helper test: expired access token 在 OAuth adapter 中返回 `credentialstore.ErrCredentialExpired`，不会构造带 stale bearer 的 request。
- helper test: OAuth request 永远不写 `X-API-Key`，API-key credential 永远不被 OAuth adapter 接受。
- integration note: 若 gateway routing 层已有 provider adapter selection，后续必须加 admin real-entry 或 gateway-level test；本 slice 不新增 frozen package 文件。

**时间估计**: 0.5 天。

**风险**:
- 只测 provider helper 仍可能漏掉 gateway adapter selection；因此 ANT-2 的 admin real-entry test 是发布阻断项。

### ANT-5 — Docs, Live Smoke, Review Gate

**范围**: 写接通 runbook、风险登记、Owner 决策记录；跑 targeted + full backend checks；准备 per-commit review。

**文件**:
- Create/modify: `docs/runbooks/anthropic-claude-oauth-smoke-runbook.md`
- Modify: `docs/10_RISK_REGISTER.md` if new risk IDs are needed
- Modify: `docs/03_FEATURE_PARITY_MATRIX.md` or related status doc only if Owner/PM 要求状态推进
- No code files unless earlier slices missed docs hooks

**成功标准**:
- docs test checklist 明确包含 helper tests、admin real-entry tests、`cd backend && go test ./internal/credentialacq ./internal/credentialworker ./internal/provider/anthropic -count=1`、`cd backend && go test ./... -count=1`。
- live smoke 分两档: mock token endpoint smoke 必跑；真实 Anthropic OAuth smoke 只在 Owner 提供测试账号/批准 public client/redirect_uri 后执行。
- 记录 cursor C1 教训: helper 绿不等于发布，admin real-entry fail-closed 为 release gate。
- stage 后执行 `codex exec review --uncommitted --full-auto --sandbox read-only`；S0/S1 修完才 commit。

**时间估计**: 0.5 天。

**风险**:
- `go test ./...` 漏跑会重复 cursor C1 类型事故；本 slice 把全量命令列为发布前 hard gate。

## 4. 参考项目对照 (CLAUDE.md #15)

| 设计点 | 参考项目行为证据 | HUAKAI delta |
| --- | --- | --- |
| PKCE authorization-code 主流程 | CLIProxyAPI 的 Claude path 固定授权/令牌 endpoint、public client、loopback redirect，并把 authorize URL 拼出 response_type、scope、PKCE challenge、state（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:23`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:190`）。 | HUAKAI 不复制常量结构；用 `OAuthClientConfig` / approved profile 注入，并把 PKCE verifier 存在 encrypted session store。 |
| Token exchange / refresh body shape | CLIProxyAPI 的 Claude token exchange 和 refresh 都向 token endpoint 发 JSON body，refresh 有 singleflight/backoff 和 429 block 行为（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:241`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:330`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:396`）。 | HUAKAI 先做 JSON token exchange + refresh trust-chain；singleflight/backoff 可复用现有 worker scheduler，不照搬实现。 |
| Admin 入口不等于 helper | CLIProxyAPI 的 management handler 从 admin 请求生成 PKCE/state、注册 OAuth session、启动 callback forwarder、等待 callback 文件、交换 token、保存 auth record（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1421`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1454`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1530`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1547`）。 | HUAKAI 必须有 admin real-entry test，但 callback storage 应走 HUAKAI session/finalizer/vault，不借文件轮询结构。 |
| Provider-specific auth header 分离 | Portkey 的 Anthropic provider config 从 provider options 取 key，写 `X-API-Key`、version/beta headers，并由 provider context 统一取 headers/base URL/endpoint（`Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/providers/anthropic/api.ts:3`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/providers/anthropic/api.ts:6`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/providerContext.ts:28`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/providerContext.ts:96`）。 | HUAKAI 已把 API-key passthrough 和 OAuth bearer adapter 分开；本次要防止 `claude_ai_oauth` 误走 API-key header。 |
| Backend auth handler 明确分派 | Envoy AI Gateway 通过 backend auth config 分派 AWS/API key/Azure/GCP/Anthropic API key handler；Anthropic API-key handler 写 `x-api-key`，通用 API-key handler 写 bearer（`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/backendauth/auth.go:15`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/backendauth/auth.go:17`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/backendauth/anthropicapikey.go:20`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/backendauth/api_key.go:22`）。 | HUAKAI delta 是多 auth_mode 同 vendor：`api_key` 与 `claude_ai_oauth` 必须按 credentialstore runtime kind 分派，不按 vendor 粗分。 |
| Secret/config trust boundary | Envoy MCP route API 对 backend key 配置要求 secretRef/inline 二选一，且 header/query 注入二选一（`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/mcp_route.go:201`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/mcp_route.go:204`）。 | HUAKAI 对 public client ID 也应有单一来源原则：operator config 或 approved built-in profile，不允许 credential payload 临时决定 token endpoint/client。 |

## 5. 风险登记

- **ClientID 来源**: CLIProxyAPI 观察到 public client hardcode，但 HUAKAI 信任链原则要求 Owner 决定是 built-in approved public profile，还是 operator-config mandatory。默认建议: built-in profile feature flag off，Owner 开启后才可用。
- **redirect_uri 模式**: loopback redirect 贴近 CLI 工具；admin callback 更适合 HUAKAI server。Owner 需决定真实回调是 `http://localhost:<port>/callback`、admin server callback，还是双模式。
- **scope 范围**: CLIProxyAPI 观察到较宽 scope；HUAKAI 应先最小化到 `user:profile user:inference` + Claude Code/session 必需项，超出最小范围要 Owner 显式批准。
- **vendor changes**: Anthropic 改 public client ID、scope、token body shape、redirect allowlist 时，hardcode 会失效；runbook 必须有 canary smoke 和快速切 operator profile 的路径。
- **credential-supplied endpoint SSRF**: 当前 refresh adapter 会读 credential 内 token endpoint；ANT-3 必须改成 fail-closed 或 approved profile，否则可被恶意 credential 重定向。
- **fake exchanger 回流**: 默认 registry 若保留 fake JSON 路径，admin callback 可以绕过真实 token endpoint；ANT-1/ANT-2 必须用判别性 test 杀掉。
- **漏跑 `./...`**: cursor C1 教训是 helper 绿掩盖入口层失败；ANT-5 把 full backend `go test ./...` 和 admin real-entry test 列为 release gate。
- **clean-room 风险**: 只借行为证据，不复制 CLIProxyAPI 函数名、结构、文件组织、注释、测试；本 plan 的实现建议均使用 HUAKAI 现有 `credentialacq` / `credentialstore` / `credentialworker` 边界。
- **冻结包风险**: 不给 `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 新增文件；必要入口改动只改既有文件并在 commit 记录原因。

## 6. Owner 决策点

1. **ANT-D1 ClientID policy**: A) built-in approved public profile（快，需承担 vendor change）；B) operator config mandatory（更干净，Owner 需提供配置）；C) 二者都有但默认 operator。CLIProxyAPI 选择 A（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/anthropic_auth.go:23`）；Envoy-style trust boundary 更接近 B/C（`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/mcp_route.go:204`）。Codex 推荐 C。
2. **ANT-D2 redirect_uri**: A) loopback CLI callback；B) admin server callback；C) loopback forwarder -> admin callback。CLIProxyAPI 采用 C 风格的 management flow（`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/auth_files.go:1459`）；HUAKAI 推荐 B/C，避免 serverless 环境卡死。
3. **ANT-D3 scope allowlist**: A) 按 CLIProxyAPI 观察到的完整 scope；B) 最小 inference/profile scope；C) operator-config scope + built-in denylist。Codex 推荐 C，第一版 smoke 用最小集。
4. **ANT-D4 refresh config source**: A) 允许 credential payload endpoint/client；B) 只信 operator/public approved profile；C) 兼容历史 payload 但首次 refresh 后迁移。Codex 推荐 B，历史 payload 走 manual recovery。
5. **ANT-D5 fake JSON fallback**: A) 完全移除默认 fake；B) 迁到测试-only registry；C) 保留 hidden manual recovery endpoint。Codex 推荐 B；生产默认 registry 不应包含 fake for `anthropic/claude_ai_oauth`。
6. **ANT-D6 live smoke 条件**: 是否提供测试 Anthropic 账号、允许真实浏览器/loopback 登录、是否要求 transport mimicry 同步启用。没有 Owner 确认时，只跑 mock token endpoint 和 admin real-entry tests，不宣称 live upstream 通过。

## 7. 工时 + 推荐起步

总估时 3.5-5 个工程日，不含 Owner 等待和真实 Anthropic 登录 smoke。

推荐起步顺序:

1. 先做 ANT-D1/D2/D3 决策；没有这三项，ANT-1 只能做 fail-closed profile gate。
2. 开 ANT-1 + ANT-2 作为第一 commit group：先杀 fake exchanger，再用 admin real-entry test 防 C1 类假绿。
3. 第二 commit group 做 ANT-3 refresh hardening；它涉及 token 生命周期，单独 review。
4. 第三 commit group 做 ANT-4/ANT-5，补 runtime integration、runbook、全量检查和 Codex review。

Source files read: backend/internal/credentialacq/types.go; backend/internal/credentialacq/oauth.go; backend/internal/credentialacq/oauth_authorization_code.go; backend/internal/credentialacq/vendor_exchangers.go; backend/internal/provider/anthropic/oauth_session.go; backend/internal/provider/anthropic/oauth_session_test.go; backend/internal/credentialworker/adapters/anthropic.go; backend/internal/credentialworker/refresh_adapter.go; backend/internal/credentialworker/refresher.go; backend/internal/credentialworker/refresher_test.go; backend/internal/credentialworker/mode_refresh.go; backend/internal/credentialworker/mode_refresh_test.go; backend/internal/credentialstore/types.go; backend/internal/credentialstore/postgres_store.go; backend/internal/credentialstore/crypto.go; /home/codex/refs/CLIProxyAPI/.huakai-head-sha; /home/codex/refs/CLIProxyAPI/internal/auth/claude/anthropic_auth.go; /home/codex/refs/CLIProxyAPI/internal/auth/claude/token.go; /home/codex/refs/CLIProxyAPI/internal/auth/claude/pkce.go; /home/codex/refs/CLIProxyAPI/internal/api/handlers/management/oauth_sessions.go; /home/codex/refs/CLIProxyAPI/internal/api/handlers/management/auth_files.go; /home/codex/refs/portkey/src/providers/anthropic/api.ts; /home/codex/refs/portkey/src/handlers/services/providerContext.ts; /home/codex/refs/portkey/src/handlers/services/preRequestValidatorService.ts; /home/codex/refs/envoy-ai-gateway/api/v1beta1/mcp_route.go; /home/codex/refs/envoy-ai-gateway/internal/backendauth/auth.go; /home/codex/refs/envoy-ai-gateway/internal/backendauth/api_key.go; /home/codex/refs/envoy-ai-gateway/internal/backendauth/anthropicapikey.go. Broad `rg` scans also touched allowed HUAKAI directories and allowed MIT/Apache refs only for location; behavior claims above rely on the cited targeted reads. Lane: codex-specifier; Time: 2026-05-26T15:28:14Z
