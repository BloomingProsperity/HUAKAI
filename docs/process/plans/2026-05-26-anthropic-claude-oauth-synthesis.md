# 2026-05-26 Anthropic `claude_ai_oauth` 真 OAuth 接通 — Synthesis (合并稿)

> Claude PM synthesize 自:
> - Claude lane: [2026-05-26-anthropic-claude-oauth-claude.md](2026-05-26-anthropic-claude-oauth-claude.md)
> - Codex lane: [2026-05-26-anthropic-claude-oauth-codex.md](2026-05-26-anthropic-claude-oauth-codex.md)
>
> Codex 显著更深(读 22 HUAKAI 文件 vs Claude 7),抓到 1 个我漏的真问题(refresh adapter 已存在 + 含 SSRF),5-slice 结构 + 6 决策点更细。**本 synthesis 主要采用 codex 结构**,补 Claude lane 在公开 CLI ClientID + 工时区间上的 delta。

## 0. 关键修正:Codex 找到我漏的 P0/P1

**[backend/internal/credentialworker/adapters/anthropic.go](backend/internal/credentialworker/adapters/anthropic.go) 已经存在**(line 24/41/53),并且**从 credential payload 兜底读取 `oauth_token_endpoint` 和 `client_id`**。

这跟 Slice 2.6 修的 cursor SSRF + 之前 Round 1 抓的 gemini OAuth token exfiltration 是同一 pattern:**credential-supplied endpoint 信任链漏洞**。Claude PM lane 完全漏读这个文件,Codex lane 在现状盘点 line 12 命中。

> 我跟 Owner 在 Slice 2.6 round 3 已经定过:operator-config-bound OAuth refresh,**不读 credential JSON 中的 endpoint**。`adapters/anthropic.go` 此前没改齐 → 是隐藏的回归窗口,**ANT-3 必修**。

## 1. 现状盘点(取 codex 深度)

| 模块 | 现状 | 评价 |
| --- | --- | --- |
| `credentialstore/types.go:22, 247` | `anthropic/claude_ai_oauth` 已有 handlerSpec,runtime kind = `oauth_access_token`,refreshable + grace OK;**但 refresh_token-only payload 仍能通过校验**(未保证接入前先刷新出 access_token) | 校验偏松 |
| `credentialacq/types.go:151` | `DefaultModePlans` 已含 `anthropic/claude_ai_oauth`,标 OAuth + public CLI client source | OK,产品入口已注册 |
| `credentialacq/oauth.go:53,127,173` | 通用 PKCE start/callback,state hash + encrypted verifier + replay/expiry/state 校验 | OK,可复用 |
| `credentialacq/oauth_authorization_code.go:49,92,210` | store-aware real exchanger,但要求 `source=operator_config` + form body;不适合 public client + JSON body | 不直接复用,需 vendor-specific branch |
| **`credentialacq/vendor_exchangers.go:33`** | **`anthropic/claude_ai_oauth` 仍是 `NewPKCEFakeExchanger(TokenShapeAccessRefresh)`** | **必杀** |
| `provider/anthropic/oauth_session.go:16,33` | OAuth bearer adapter,出站 Anthropic Messages 用 access_token,拒绝 API key credential | 可复用,无需改 |
| **`credentialworker/adapters/anthropic.go:24,41,53`** | **refresh adapter 已存在但从 credential payload 读 oauth_token_endpoint + client_id → SSRF 风险** | **必修 (P0/P1)** |
| `credentialworker/mode_refresh.go:62,68,376` | `claude_ai_oauth` 和 `claude_code` 都走 legacy OAuth refresh;**Gemini/Antigravity operator-bound fail-closed 已先例**,Anthropic 未套用 | ANT-3 必修 |
| `credentialworker/mode_refresh_test.go:129` | Gemini/Antigravity "credential-supplied endpoint 不可信" 判别 test 已有 | 可借鉴 |
| `credentialstore/postgres_store.go:682` | CAS refresh 写回 + audit 同事务;本 session `63c7708` 加 `deleted_at IS NULL` | OK,无需 schema 变更 |

## 2. 缺口分类(7 类,合并两稿 — codex 视角 7 个全保留)

1. **Exchanger**: `anthropic/claude_ai_oauth` 仍是 fake JSON exchanger;需要 dedicated store-aware exchanger,真实使用 authorization code + stored PKCE verifier 调 Anthropic token endpoint
2. **Authorize URL profile**: 通用 `BuildAuthorizeURL` 缺 Anthropic public-client profile 的额外参数、scope 默认、endpoint 决策
3. **Token request shape**: 现有 generic exchanger 用 `application/x-www-form-urlencoded`;CLIProxyAPI 观察 Anthropic 用 **JSON body**(`anthropic_auth.go:269, 364`)—— 需 vendor-specific branch
4. **Worker adapter trust chain**: refresh 不能默认信任 credential payload 内 endpoint/client(P0/P1 SSRF 风险);需 operator-approved profile 或 Owner-approved public profile
5. **Admin real entry**: helper-level test 不够。必须补真实 admin handler 入口 fail-closed test,证明缺 config / 未批准 public client 时不会生成 flow、不落 session、不走 fake exchanger
6. **Runtime wiring**: OAuth bearer adapter 已有,但要证 credentialstore material → provider adapter 选择链路不会把 `claude_ai_oauth` 送到 API-key passthrough
7. **Docs / runbook**: ClientID 来源、redirect_uri、scope、live smoke 条件、全量 `./...` 检查记录(防 cursor C1 教训重演)

## 3. Slice 切片(取 codex 5-slice 结构)

### ANT-1 — Dedicated Anthropic OAuth Exchanger
**范围**: 替换 `anthropic/claude_ai_oauth` fake exchanger 为 dedicated store-aware exchanger;保留 fake 给手工恢复或测试 alias,不再作为默认生产路径。

**文件**:
- Modify `credentialacq/vendor_exchangers.go`
- Create `credentialacq/anthropic_oauth.go` + `anthropic_oauth_test.go`
- Modify `credentialacq/vendor_exchangers_test.go`

**成功标准 (testable, helper)**:
- `DefaultExchangerRegistry().Lookup("anthropic/claude_ai_oauth")` 返回真 exchanger(类型不是 `pkceFakeExchanger`)
- helper test: token server 收到 authorization code + state-bound PKCE verifier + approved client identity + redirect_uri;返回 access/refresh 后 candidate payload 含 access、refresh、expires_at、client identity source
- helper fail-closed test: 缺 token endpoint/client/redirect/scope 或 public-client profile 未批准时,`StartOAuthFlow` 返回 `ErrFeatureDisabled`,authorize URL 为空,不创建 session
- mutation 自检: 把 exchanger 退回 fake JSON 时,`code="not-json-auth-code"` 不应通过,test 必红

**时间估计**: 0.75-1.25 天

**风险**: 直接复用 generic form exchanger 真实 endpoint 可能拒(JSON vs form body 差异);用 dedicated exchanger 隔离

---

### ANT-2 — Admin 真实入口接线 + C1 Fail-Closed 回归

**范围**: 从真实 admin credential acquisition endpoint 发起 `anthropic/claude_ai_oauth`,覆盖 3 路径:缺配置 fail-closed / 成功 callback / fake fallback 禁止。**不在 frozen package (gatewayhttp/gateway/proto) 新增文件;若入口在 frozen 包只改既有文件并 commit 记录原因**。

**文件**:
- Modify admin credential acquisition handler/wiring(执行前先定位;优先 `backend/internal/adminhttp/**`)
- Test: `adminhttp/*_test.go` 或既有 admin handler test
- Modify `credentialacq/finalizer.go` only if needed

**成功标准 (testable, admin real-entry)**:
- admin real-entry fail-closed test: 真实 admin HTTP handler 请求 `anthropic/claude_ai_oauth`,未批准 ClientID/profile 时返 4xx/5xx,session store 无新 started flow,token endpoint mock 未被调
- admin real-entry success test: 真实 admin start + callback/finalize 入口,mock token endpoint 返 access/refresh,credentialstore 保存 encrypted credential,runtime material kind 为 `oauth_access_token`
- admin real-entry anti-fake test: callback code 传入 JSON-shaped fake token payload 时不能绕过 token endpoint,mock endpoint call count = 1 且 body 中 code 是 callback code
- 所有 test 名或注释明确写 `admin real entry`(避免 cursor C1 helper-only 假覆盖)

**时间估计**: 1-1.5 天

**风险**: admin 入口可能在 frozen package → 允许改既有文件,不允许加新文件;handler test 可能需 DB fixture → 先 unit-level fake store,再追加 `integration_pg`

---

### ANT-3 — Refresh Worker Trust-Chain Hardening(**修 SSRF P0/P1**)

**范围**: 让 Anthropic scheduled refresh 不再默认信任 credential-supplied endpoint/client;按 Owner 决策接 operator-approved public profile 或 operator config。保持 refresh payload 更新 access/session token 与 expires_at。

**文件**:
- Modify `credentialworker/adapters/anthropic.go`(去除 line 24/41/53 那种 credential 兜底读取)
- Modify `credentialworker/mode_refresh.go`(可能调 `legacyOAuthModeAdapter` 包装方式)
- Test: `credentialworker/refresher_test.go` + `mode_refresh_test.go`

**成功标准 (testable, helper)**:
- helper fail-closed test: credential payload 含 attacker token endpoint/client,但 operator/public profile 未配置时,refresh 返配置缺失错误,HTTP client call count = 0
- helper positive test: operator/public profile 配好时,refresh 请求只打 approved endpoint、只带 approved client,credential 内 attacker endpoint/client 被忽略
- helper payload test: refresh 成功后 access_token + refresh_token + expires_at 更新;若 runtime 需要 session_token 则同步为 access_token
- scheduled path test: `DefaultModeAdapterRegistry().Lookup(anthropic, claude_ai_oauth)` 不再是无约束 legacy adapter,`AccountCredentialRefresher.Refresh` 失败会记 failure class,不写 success payload

**时间估计**: 0.75-1.25 天

**风险**: 若 Owner 选 public CLI hardcode,test 必须证 hardcode 是 approved profile 而非从 credential 学;过度 fail-closed 可能让历史 fake credential 全部不可刷新,需 runbook 标 migration/manual recovery

---

### ANT-4 — Runtime Adapter Integration + Expiry Behavior

**范围**: 验证 credentialstore material → provider/anthropic OAuth adapter → refresh expiry 行为连成一条链;不碰 gateway hot path 大重构。

**文件**:
- Modify `credentialstore/types_test.go`
- Modify `provider/anthropic/oauth_session_test.go`
- Modify only if needed: `provider/anthropic/oauth_session.go`

**成功标准 (testable, helper)**:
- helper test: `claude_ai_oauth` payload 的 runtime material 必须是 `oauth_access_token`,不能落到 `api_key` 或 upstream passthrough
- helper test: expired access token 在 OAuth adapter 中返 `credentialstore.ErrCredentialExpired`,不构造带 stale bearer 的 request
- helper test: OAuth request 永不写 `X-API-Key`,API-key credential 永不被 OAuth adapter 接受
- integration note: 若 gateway routing 层已有 provider adapter selection,后续必加 admin real-entry 或 gateway-level test;本 slice 不在 frozen package 加新文件

**时间估计**: 0.5 天

**风险**: 只测 provider helper 仍可能漏掉 gateway adapter selection;**因此 ANT-2 的 admin real-entry test 是发布阻断项**

---

### ANT-5 — Docs / Live Smoke / Review Gate

**范围**: runbook + 风险登记 + Owner 决策记录;targeted + full backend checks;per-commit review。

**文件**:
- Create/modify `docs/runbooks/anthropic-claude-oauth-smoke-runbook.md`
- Modify `docs/10_RISK_REGISTER.md` if new risk IDs
- Modify `docs/03_FEATURE_PARITY_MATRIX.md` if status 推进

**成功标准 (testable, process)**:
- docs test checklist 包含 helper + admin real-entry + 全量 `go test ./...`(`cd backend && go test ./... -count=1`)
- live smoke 分两档:mock token endpoint smoke 必跑;真实 Anthropic OAuth smoke 仅在 Owner 提供测试账号/批准 public client/redirect_uri 后执行
- 记录 cursor C1 教训:helper 绿 ≠ 发布,admin real-entry fail-closed 是 release gate
- stage 后执行 `codex exec ... --sandbox read-only`;S0/S1 修完才 commit

**时间估计**: 0.5 天

**风险**: `go test ./...` 漏跑会重演 cursor C1;本 slice 把全量命令列为发布前 hard gate

## 4. 参考项目对照(合并版,基于 codex)

| 设计点 | 参考 cite | HUAKAI delta |
| --- | --- | --- |
| PKCE auth flow | CLIProxyAPI `~/refs/CLIProxyAPI/internal/auth/claude/anthropic_auth.go:23,190`(固定 AuthURL/TokenURL/ClientID + loopback redirect + S256 challenge) | **架构升级**:不复制 const 结构,用 `OAuthClientConfig` / approved profile 注入;PKCE verifier 存 encrypted session store(防进程重启丢) |
| Token exchange / refresh body | CLIProxyAPI `anthropic_auth.go:241, 330, 396`(JSON body + singleflight/backoff + 429 block) | **算法升级**:JSON token exchange + refresh trust-chain;singleflight/backoff 复用 HUAKAI worker scheduler,不照搬实现 |
| Admin 入口 ≠ helper | CLIProxyAPI `auth_files.go:1421,1454,1530,1547`(admin handler 注册 OAuth session + callback forwarder + token exchange + auth record 保存) | **生态升级**:必须 admin real-entry test(防 cursor C1 假阳性);callback storage 走 HUAKAI session/finalizer/vault,不照搬文件轮询 |
| Provider-specific auth header 分离 | Portkey `~/refs/portkey/src/providers/anthropic/api.ts:3,6` + `providerContext.ts:28,96`(API-key 走 `X-API-Key`,bearer 走 Authorization) | HUAKAI 已分,**ANT-4 防 `claude_ai_oauth` 误走 API-key header** |
| Backend auth handler 分派 | Envoy AI Gateway `~/refs/envoy-ai-gateway/internal/backendauth/auth.go:15,17` + `anthropicapikey.go:20` + `api_key.go:22`(按 backend auth type 分派) | **架构升级**:多 auth_mode 同 vendor → 按 credentialstore runtime kind 分派,不按 vendor 粗分 |
| Secret/config trust 边界 | Envoy MCP route `~/refs/envoy-ai-gateway/api/v1beta1/mcp_route.go:201,204`(backend key 必 secretRef 或 inline 二选一) | **生态升级**:HUAKAI public client ID 单一来源原则(operator config 或 approved built-in profile),不允许 credential payload 临时决定 token endpoint/client |

## 5. 风险登记(合并)

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | **`credentialworker/adapters/anthropic.go` 现 SSRF**(credential payload 兜底读 endpoint) | **P0/P1** | ANT-3 必修;Owner 拍 D-4 决定 endpoint 来源 |
| R2 | fake JSON exchanger 默认 registry | P1 | ANT-1 杀;ANT-D5 决定保留方式 |
| R3 | ClientID 来源(public CLI hardcode vs operator-config) | 中 | ANT-D1 拍;监控 CLIProxyAPI 是否变 |
| R4 | redirect_uri 模式(loopback vs admin callback) | 中 | ANT-D2 拍 |
| R5 | scope 范围(过宽 vs 过窄) | 中 | ANT-D3 拍,最小化起步 |
| R6 | vendor changes(Anthropic 改 public client ID/endpoint/scope) | 中 | runbook 含 canary smoke + 切 operator profile 快速路径 |
| R7 | refresh_token 长期失效(用户改密) | 已知 | invalid_grant → state=revoked,admin UI 引导重登 |
| R8 | 漏跑 `./...` 全量(cursor C1 教训) | 高(过程) | ANT-5 把全量列发布 hard gate;commit body 必带 PASS 证据 |
| R9 | helper-only test 假阳性(cursor C1 教训) | **P0**(过程) | ANT-2 admin real-entry test 是发布阻断项 |
| R10 | clean-room 风险 | 低 | 只借行为证据,不复制 CLIProxyAPI 函数名/结构/注释 |
| R11 | 冻结包风险 | 中 | gatewayhttp/gateway/proto 不加新文件;入口改动只改既有文件 |

## 6. Owner 决策点(6 个,取 codex)

### ANT-D1: ClientID policy
- **A** built-in approved public profile (硬编 `9d1c250a-...`,跟 CLIProxyAPI 同款)
- **B** operator config mandatory(Owner 提供配置)
- **C** 二者都有但默认 operator(**codex 推荐**)

参考:
- CLIProxyAPI `anthropic_auth.go:23` 选 A(public CLI hardcode)
- Envoy MCP route `mcp_route.go:204` 选 B/C 风格(trust boundary 强)
- Claude PM 倾向 A(对齐 sub2api/cliproxyapi 行业标准)
- Codex 倾向 C(信任链严格 + 兼容性)

### ANT-D2: redirect_uri
- **A** loopback CLI callback (`http://localhost:54545/callback`)
- **B** admin server callback(HUAKAI admin UI endpoint)
- **C** loopback forwarder → admin callback(**codex 推荐 B/C**)

参考:CLIProxyAPI `auth_files.go:1459` 用 C 风格(management forwarder)

### ANT-D3: scope allowlist
- **A** 按 CLIProxyAPI 观察到的完整 scope(`org:create_api_key user:profile user:inference`)
- **B** 最小 inference/profile scope
- **C** operator-config scope + built-in denylist(**codex 推荐**)

### ANT-D4: refresh config source(**修 SSRF 关键决策**)
- **A** 允许 credential payload endpoint/client(现状 — 不安全,**不推荐**)
- **B** 只信 operator/public approved profile(**codex 推荐;Claude PM 也强推**)
- **C** 兼容历史 payload 但首次 refresh 后迁移

历史 payload 走 manual recovery,新建 credential 全部强制 B。

### ANT-D5: fake JSON fallback 处理
- **A** 完全移除默认 fake
- **B** 迁到 test-only registry(**codex 推荐**)
- **C** 保留 hidden manual recovery endpoint

### ANT-D6: live smoke 条件
是否提供测试 Anthropic 账号、允许真实浏览器/loopback 登录、是否要求 transport mimicry 同步启用?默认:没 Owner 确认时只跑 mock + admin real-entry tests,**不宣称 live upstream PASS**。

## 7. 工时合计 + 推荐起步

| Slice | 工时 | 阻塞 | 谁 |
| --- | --- | --- | --- |
| ANT-1 dedicated exchanger | 0.75-1.25 天 | ANT-D1/D2/D3 拍 | codex 实施 + Claude review |
| ANT-2 admin real-entry | 1-1.5 天 | ANT-1 done | codex + Claude review |
| ANT-3 refresh trust-chain(**SSRF**) | 0.75-1.25 天 | ANT-D4 拍 | codex + Claude review |
| ANT-4 runtime integration | 0.5 天 | ANT-1 + ANT-2 | codex |
| ANT-5 docs + smoke + review gate | 0.5 天 | 全做完 | codex |
| **合计** | **3.5-5 天** | | |

**推荐起步顺序**:
1. **先拍 ANT-D1 / D2 / D3 / D4**(D5 偏向 codex 推荐 B,可直接走;D6 等后续)
2. **ANT-1 + ANT-2 第一 commit group**:杀 fake + admin real-entry C1 回归测,**优先级最高**
3. **ANT-3 第二 commit group**:refresh SSRF hardening,单独 review(token 生命周期敏感)
4. **ANT-4 + ANT-5 第三 commit group**:runtime integration + docs + 全量 hard gate

---

Synthesizer: Claude PM (Opus 4.7)
Time: 2026-05-26T15:35:00Z
Source plans:
- `docs/process/plans/2026-05-26-anthropic-claude-oauth-claude.md` (Claude lane, 7 章 / 3 ANT slice / 3 decisions)
- `docs/process/plans/2026-05-26-anthropic-claude-oauth-codex.md` (Codex lane, 7 章 / 5 ANT slice / 6 decisions / 22 文件 reads)
