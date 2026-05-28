# 双模式 admin OAuth callback spec (loopback + 远程 web) — S1-002 + S2-059

Lane: claude (auth-sensitive spec, 直接 Write per AGENTS clean-room prompt 规则)
UTC: 2026-05-28
来源: 外部 AI bug/drift 评审 S1-002 + S2-059 (Claude 独立读代码确认 + Owner "本地和远端 web 都做")

## 0. 读源结论 (回应 Owner "LGPL 也必须读源码")

vendor account OAuth callback 模式,全部读源确认:

| 项目 | redirect 模式 | session 关联 | cite |
|---|---|---|---|
| CLIProxyAPI (MIT) | loopback `localhost:<port>/oauth2callback` | 本地 server 直收 | `~/refs/CLIProxyAPI/internal/auth/gemini/gemini_auth.go:79,219,222` |
| sub2api (LGPL) | loopback `localhost:1455/auth/callback` | SessionID(前端持) + State ConstantTimeCompare | `~/refs/sub2api/backend/internal/service/openai_oauth_service.go:133,140` + `pkg/openai/oauth.go:26` |
| new-api (LGPL) | loopback `localhost:1455/auth/callback` | state hex | `~/refs/new-api/service/codex_oauth.go:23,67,233` |
| one-api (LGPL) | 无 vendor OAuth (只 GitHub/Lark/OIDC 用户登录 SSO) | n/a | `~/refs/one-api/controller/auth/{github,lark,oidc}.go` |
| portkey (MIT) | 机器密钥 (JWT/client-credentials, 无浏览器) | n/a | `~/refs/portkey-gateway/src/providers/google-vertex-ai/utils.ts:131` |
| litellm (MIT) | 用户 SSO 登录 (非 vendor account) | n/a | `~/refs/litellm/litellm/proxy/management_endpoints/` |

**结论**: 所有借鉴项目 vendor account OAuth 都用 loopback (`localhost:1455` 是 OpenAI Codex 事实标准)。**没有一个做远程 web admin OAuth callback**。HUAKAI 远程 web 是超越借鉴的 SaaS 原创升级。

## 1. HUAKAI 现状

- ChatGPT 已有双模式骨架: `chatgptRedirectURIWithFlowID` 只对 https admin callback path 加 `flow_id` query, loopback http 不加 (`backend/internal/credentialacq/chatgpt_oauth.go:270-287`); authorize URL 带 state + redirect_uri(含 flow_id) (`:289-307`)
- ChatGPT allowlist 构造函数 `NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist` (`:66`), 无 allowlist 时拒 https redirect (`validateChatGPTBuiltinProfileWithHTTPSAdminAllowlist`)
- **缺口 1 (S2-059)**: Gemini 无 flow_id preserve — `buildGeminiAuthorizeURL` 用静态 RedirectURI, 不加 flow_id (`gemini_oauth.go:285-298`)
- **缺口 2 (S1-002a)**: callback handler 全过 `resolveCredentialAcqAdmin` admin Bearer (`admin_credential_acquisition_handler.go:291`) — 浏览器 provider redirect 无 Bearer, 进不来
- **缺口 3 (S1-002b)**: production wiring 用非 allowlist 构造函数 (`wiring.go:565,610`) — https redirect 被拒

## 2. 模式 A: loopback (保留, 兼容借鉴 + 本地 admin)

- redirect_uri = `http://localhost:<port>/callback` (admin 本地起 server)
- 现状已工作, 不改
- 适用: 自部署/本地运营 admin

## 3. 模式 B: 远程 web (原创升级, SaaS 远程 admin)

### 3.1 flow 关联 (S2-059: Gemini 照搬 ChatGPT)

- Gemini 新增 `geminiRedirectURIWithFlowID` (照 `chatgptRedirectURIWithFlowID`): 仅对 https admin callback path 加 `flow_id` query
- `buildGeminiAuthorizeURL` 接受 flowID 参数, redirect_uri 带 flow_id
- provider 原样返回 redirect (浏览器跳回 `https://gateway/admin/oauth-callback?flow_id=X&state=Y&code=Z`)
- callback 从 query 提取 flow_id 查 session (现 handler `:295` 已读 flow_id)

### 3.2 callback 无 admin Bearer (S1-002a 核心)

新增 browser callback 路径 (不过 resolveCredentialAcqAdmin), 用以下**组合验证**替代 admin auth:

1. **flow_id 查 session**: flow_id 是 server 发起时生成的 uuid (不可猜), 查到的 session 已绑发起 admin identity
2. **state CSRF 校验**: ConstantTimeCompare(query.state, session.state) (照 sub2api `:140`)
3. **redirect_uri allowlist**: 收到的 redirect 必在 operator 配置的 https allowlist (防 open redirect)
4. **code 一次性**: session 完成即废 (防重放)
5. **session 短 TTL**: 发起后 N 分钟过期 (防劫持窗口)

完成后用 session 绑定的 admin identity 写 audit (不丢 admin 归属)。

### 3.3 state 编码方案 (Owner 决策点 D-A)

- **方案 1 (推荐)**: flow_id-in-redirect + DB session 查 (复用 ChatGPT 现模式 + sub2api 印证)。flow_id 在 URL, 靠短 TTL + 一次性 + 还需 provider 发的 code 才能完成, 缓解 URL 泄露
- 方案 2: state HMAC 自包含 (`state = base64(session_id + nonce + HMAC)`), 不依赖 URL flow_id, 更标准但要新签名密钥管理

## 4. 安全攻击面 + 缓解

| 攻击 | 缓解 |
|---|---|
| open redirect (骗 redirect 到攻击者域) | redirect_uri 必在 operator https allowlist; 非 allowlist 拒 |
| CSRF (攻击者诱导 admin 完成攻击者的 flow) | state ConstantTimeCompare; state 随机 server 生成 |
| flow_id / state 伪造 | flow_id 是 server uuid 不可猜; session DB 查; state 随机 |
| session fixation / code 重放 | code 一次性; session 完成即废 + 短 TTL |
| flow_id URL 泄露 (log/referer/历史) | 短 TTL + 一次性 + 还需 provider code (攻击者拿不到) |
| 越权 (非 admin 完成 flow) | session 绑发起 admin identity; 完成审计记原 admin |

## 5. production wiring (S1-002b)

- ChatGPT + Gemini 都改用 allowlist 构造函数
- operator config 新增 `HUAKAI_ADMIN_OAUTH_CALLBACK_ALLOWLIST` (https redirect 白名单, 逗号分隔)
- 空 allowlist → 仅 loopback 模式 (退化兼容, 不破现状)

## 6. 切片拆分 (每片 TDD + R1/R2 review, auth core 严格)

| 切片 | 范围 | 文件 (非冻结/既改) | 工时 |
|---|---|---|---|
| OAUTH-WEB-1 (S2-059) | Gemini flow_id preserve (照 ChatGPT) | `credentialacq/gemini_oauth.go` 既改 | 0.5 天 |
| OAUTH-WEB-2 (S1-002a) | browser callback 无 Bearer 路径 + flow_id/state/allowlist/code 一次性 验证 | `gatewayhttp/admin_credential_acquisition_handler.go` 既改 (frozen 既改 OK) + 可能新 `credentialacq` helper | 1 天 |
| OAUTH-WEB-3 (S1-002b) | production wiring 接 allowlist 构造函数 + operator config | `cmd/gateway/wiring.go` 既改 + config | 0.5 天 |

## 7. 测试点 (discriminating, 每个安全属性可变红)

- **flow_id 关联** (S2-059): Gemini https redirect 含 flow_id, callback 提取查到 session。Mutation: 不写 flow_id → 查不到 session → red
- **callback 无 Bearer** (S1-002a): browser callback (无 admin token) + 合法 flow_id+state+code → 200 完成。Mutation: 删 flow_id/state 验证 → 任意请求能完成 → red
- **CSRF**: state 不匹配 → 拒。Mutation: 不校 state → 错 state 通过 → red
- **open redirect**: redirect 不在 allowlist → 拒。Mutation: 不校 allowlist → 任意 redirect 通过 → red
- **code 重放**: 用过的 session 再用 → 拒。Mutation: session 不置废 → 重放成功 → red
- **admin 归属**: 完成审计记发起 admin identity。Mutation: 丢 identity → 审计 actor 空 → red
- **loopback 不回归**: loopback (http) flow 仍工作, 不加 flow_id。Mutation: 强制所有 redirect 加 flow_id → loopback 测试 red

## 8. Owner 决策点

- **D-A**: state 编码方案 (方案 1 flow_id+DB 查 / 方案 2 HMAC 自包含)
- **D-B**: flow_id / session TTL 时长 (建议 10 分钟, 够 admin 完成授权)
- **D-C**: 两模式并存策略 (operator 按 redirect_uri 自动选 loopback/web, 还是显式开关)
- **D-D**: allowlist 配置粒度 (全局 https 白名单 / per-tenant)

## 9. 不做 / 边界

- 不动 loopback 现状 (兼容借鉴 + 本地 admin)
- 不引入新 OAuth provider (仅 ChatGPT + Gemini 双模式补齐)
- 6 vendor 暂停 flow (cursor/windsurf/codex CLI/kiro/gemini Advanced/antigravity) 不在本 spec
- Merkle / 信任链无关
