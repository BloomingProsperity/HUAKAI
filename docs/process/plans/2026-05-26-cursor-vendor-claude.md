# 2026-05-26 Cursor Vendor 集成方案 — Claude Lane

> Parallel-draft plan(CLAUDE.md #10),与 `2026-05-26-cursor-vendor-codex.md` 互不参看。
> Owner 触发上下文: 2026-05-26 "继续做 cursor vendor / hash-lock"。

## 1. 现状盘点(read-from-HUAKAI-self)

HUAKAI 已经有 cursor 子系统的骨架,**不是从零起**:

| 模块 | 文件 | 现状 | 评价 |
| --- | --- | --- | --- |
| OAuth client config | [backend/internal/provider/cursor/bootstrap.go](backend/internal/provider/cursor/bootstrap.go) | DefaultOAuthConfig + Validate + BuildAuthorizeURL,redirect 默认 `127.0.0.1:1455/auth/callback` | OK,可复用 |
| 出站请求构造 | [backend/internal/provider/cursor/cursor_session.go](backend/internal/provider/cursor/cursor_session.go) | POST `api2.cursor.sh/aiserver.v1.AiService/StreamChat`,Content-Type `application/connect+proto`,接受 `session_token` / `upstream_passthrough` | **TODO(OCAW) 三个反封禁 header 未做** |
| OAuth refresh | [backend/internal/provider/cursor/refresher.go](backend/internal/provider/cursor/refresher.go) | 完整 OAuth refresh_token grant flow,有 RefreshError 分类(auth_expired/rate_limit/transient),WithRefreshLock 事务保护 | 结构 OK,但 `cred["session_token"] = accessToken` 可能与 Cursor 真实 cookie session 模型不符 |
| credentialstore 适配 | [backend/internal/provider/cursor/credential_store_adapter.go](backend/internal/provider/cursor/credential_store_adapter.go) | 凭据读写桥 | OK |
| **OAuth exchanger 注册** | [backend/internal/credentialacq/vendor_exchangers.go:52](backend/internal/credentialacq/vendor_exchangers.go#L52) | `register("cursor/oauth", NewPKCEFakeExchanger(TokenShapeSession))` | **fake! admin 端走 PKCE 加凭证时不会真去 Cursor 兑换 token** |
| Adapter 注册 | [backend/internal/provider/registrydefault/default.go](backend/internal/provider/registrydefault/default.go) | `ProtocolCursorSession = "cursor_session"`,挂 `cursor.CursorSessionAdapter` | OK |

## 2. 缺口分类

### 缺口 A — OCAW 反封禁 header(cursor_session.go:133–136 三个 TODO)
- `x-amzn-trace-id` (AWS X-Ray 风格 trace,Cursor 上游用 AWS,无此头有概率被 WAF 拦)
- `x-cursor-timezone` (客户端时区,Cursor 风控会比对)
- `x-cursor-request-id` (UUID v4,客户端去重 / 链路追踪)

### 缺口 B — 协议转换层(完全缺失)
Cursor 上游是 Connect-RPC over HTTP/2,wire 是 protobuf;HUAKAI 入口是 OpenAI Chat Completions JSON。现 adapter 是 `bytes.NewReader(in.InboundBody)` 纯透传 —— **caller 必须先把 OpenAI 请求 marshal 成 Cursor protobuf,但 HUAKAI 没有这层 marshaler**。响应方向同样缺(Cursor protobuf chunk → OpenAI SSE)。

### 缺口 C — cursor/oauth fake exchanger 替换为真 OAuth
现 fake exchanger 只是为 admin 加凭证 UI 占位,不会去 Cursor `token_url` 真兑换;结果 credentialstore 里存的 cursor refresh_token 是假数据,`refresher.go` 启动后第一次 refresh 必 fail invalid_grant。

### 缺口 D — `x-cursor-checksum` 没采集器
Cursor 客户端 checksum 基于本机 fingerprint + 时间 + path 计算。`cursor_session.go` 从 `Credential.Extra["cursor_checksum"]` 直接读,**但没人写这个字段** —— 整个 HUAKAI 仓库没有 checksum 生成模块。要么逆向算法,要么截存桌面客户端 checksum + 周期轮换。

### 缺口 E — session_token 字段语义可能不对
`refresher.go:255-256`:
```go
cred["access_token"] = accessToken
cred["session_token"] = accessToken
```
但 Cursor 真实长期 session 是 cookie `WorkosCursorSessionToken`(WorkOS SSO 颁的),与 OAuth `access_token` 不同。这块需要 Owner 本机用真账号 verify 一下到底哪种 token 真能 hit `api2.cursor.sh`。

## 3. Slice 切片建议

### Slice 3.0 — 现状审计 + Owner 本机抓真流量(0.5–1 天,Owner 主导)
**沙箱无法做**,必须 Owner 本机:
- 用真 Cursor IDE 客户端打开 → 网络面板抓一次 `StreamChat` 请求 → 记录所有 request header(尤其 checksum/trace_id/request_id/cookie 全套)
- 验证当前 HUAKAI cursor adapter 拿真 session_token 出站能否拿到 200(不指望工作,验证缺口面)
- 产物:`docs/process/notes/cursor-real-traffic-2026-05-XX.md`(可加 .gitignore 不进库),含 sanitize 后的 header 样本

不做这步,Slice 3.1/3.3 全是猜。

### Slice 3.1 — OCAW 反封禁 header 补全(1.5 天)
- 新包:`backend/internal/provider/cursor/ocaw/`(避开冻结包 gatewayhttp/gateway/proto)
- 新文件:
  - `signer.go` —— `RequestSigner` 接口:`Sign(req *http.Request, cred Credential, now time.Time) error`
  - `trace_id.go` —— X-Ray 格式 `Root=1-<8hex>-<24hex>`
  - `request_id.go` —— UUID v4
  - `timezone.go` —— 从 `Credential.Extra["timezone"]` 读,否则用 `time.Local`
- 修 cursor_session.go BuildRequest 末段调 `signer.Sign(req, cred, now)`
- **判别测试**(CLAUDE.md #14):构造一个 signer,跑前请求 header 缺 3 个;跑后 3 个全在 + 格式合规;mutation:把 signer.Sign 改成 no-op,test 必红

### Slice 3.2 — cursor/oauth 真 Exchanger 接通(2 天)
- 在 credentialacq 新增 `cursor_oauth_exchanger.go`(credentialacq 不冻结)
- 实现 Exchanger 接口:
  - `StartOAuthFlow`:生成 PKCE verifier/challenge → 用 bootstrap.go BuildOAuthAuthorizeURL → 写 session 表
  - `ExchangeOAuthCode`:POST `token_url` form-urlencoded grant_type=authorization_code,拿 `access_token` + `refresh_token` → 写 credentialstore
- vendor_exchangers.go:52 把 `NewPKCEFakeExchanger` 替换为 `NewCursorOAuthExchanger(...)`
- **判别测试**:
  - 注入 HTTP mock 返回 `{access_token: "ok", refresh_token: "rt"}` → ExchangeOAuthCode 返回 CredentialCandidate 含两 token
  - 注入 mock 返回 `{error: "invalid_grant"}` → ExchangeOAuthCode 返回 ErrAuthExpired
  - mutation:删 refresh_token 校验后 test 必红
- 风险:cursor `token_url` / `client_id` 由 Owner 配在 operator-config,沙箱无法 E2E,只能 unit + mock

### Slice 3.3 — 协议转换层(5–7 天,大切片,**Owner 拍板再开**)
- 拆 3.3.a:Cursor protobuf 响应 → OpenAI SSE 解码(新包 `backend/internal/provider/cursor/proto/decoder.go`)
- 拆 3.3.b:OpenAI Chat Completions → Cursor protobuf 编码(`encoder.go`)
- 拆 3.3.c:接入 `cursor_session.go` BuildRequest(用 encoder 而非透传 InboundBody)
- **前置阻塞**:protobuf 描述符没 MIT 来源 → 必须 Owner 本机抓真流量提取 wire 字段编号 → HUAKAI 自写 .proto 文件
- **风险等级**:中-高(逆向 + ToS 灰色),建议放到 Slice 3.1/3.2 闭合后再 Owner 决策

## 4. 参考项目对照(CLAUDE.md #15)

### Slice 3.1 OCAW header 处理对照

| 项目 | 处理方式 | citation |
| --- | --- | --- |
| HUAKAI | 现状 TODO,Slice 3.1 拟新建 signer 接口 | [cursor_session.go:133-136](backend/internal/provider/cursor/cursor_session.go#L133) |
| CLIProxyAPI(MIT) | codex 反向用 hardcoded ClientID + TokenURL (PKCE auth flow);header 注入散在 token.go / openai_auth.go,不集中走 RoundTripper | `~/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:24-26` (ClientID/TokenURL/AuthURL 硬编) |
| litellm(MIT) | 对 cursor BYOK 走 OpenAI 兼容,无反封禁 header(用户拿合法 key 走 cursor 后端,不模拟 IDE) | `~/refs/litellm/litellm/provider_endpoints_support_backup.json` "cursor BYOK" |
| **delta** | HUAKAI 是 IDE-session 出站(不是 BYOK),需在 cursor adapter 出站前集中注入 trace_id/timezone/request_id —— 引入 signer 接口,**架构升级**(集中化 header 注入点,RoundTripper 之外;后续 checksum 生成也接到同接口);本 Slice 不含 checksum 算法,checksum 见缺口 D | n/a |

### Slice 3.2 OAuth Exchanger 对照

| 项目 | 处理方式 | citation |
| --- | --- | --- |
| HUAKAI | 现状 fake exchanger,拟切真 OAuth (authorization_code grant + PKCE) | [vendor_exchangers.go:52](backend/internal/credentialacq/vendor_exchangers.go#L52) |
| CLIProxyAPI(MIT) | codex 走 PKCE,token_url + client_id 硬编进二进制 (`ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"`) | `~/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:24-26` |
| HUAKAI 现 anthropic/claude-ai-oauth | PKCE fake + operator-config 注入 token_url(与 cursor 应同模式) | [vendor_exchangers.go:39](backend/internal/credentialacq/vendor_exchangers.go#L39) |
| **delta** | HUAKAI 的 token_url / client_id 是 operator-config 而非硬编 —— 这是 HUAKAI 信任链原则的产物([[project_core_trust_chain_differentiator]]),**生态升级** | n/a |

### Slice 3.3 协议转换对照

| 项目 | 处理方式 | citation |
| --- | --- | --- |
| HUAKAI | 现状 InboundBody 透传,Slice 3.3 拟自写 protobuf encoder/decoder | [cursor_session.go:97-101](backend/internal/provider/cursor/cursor_session.go#L97) |
| 所有 MIT 参考项目 | **无等价能力** —— 没有 MIT-licensed Cursor IDE session 反代项目 | n/a |
| (LGPL refs 不可读) | sub2api 有 `openai_cursor_warmup_pipeline_test.go` 但 LGPL 不可借鉴 | (clean-room 不读) |
| **delta** | HUAKAI 自研 + 自抓 wire,**架构升级**(0 → 1) | n/a |

## 5. 风险登记

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | Cursor protobuf schema 无 MIT 参考,Slice 3.3 工作量可能超估 | 中 | Slice 3.0 先抓真流量,工作量重估;若超 7 天则拆 3.3.a/b/c 串行 |
| R2 | `x-cursor-checksum` 算法逆向 ToS 灰色 | 高 | 不写逆向算法,只读真 IDE 客户端 checksum 然后周期轮换;Owner 法务前置确认 |
| R3 | Cursor EULA 是否禁第三方代理出站 | 高 | Owner 拍板;若禁则改走 BYOK 模式(用户拿 Cursor API key,不模拟 IDE) |
| R4 | `cred["session_token"] = accessToken`(refresher.go:256)语义可能不对 | 中 | Slice 3.0 Owner 本机用真账号 verify;如不对则 Slice 3.2 修正 token 字段映射 |
| R5 | fake exchanger 替换为真后,现存 cursor 凭证(若有)会因为 refresh_token 是假数据而 invalid_grant | 低 | Slice 3.2 增 cleanup migration 把 fake credentials 标 `requires_reauth=true` |

## 6. Owner 决策点

### D-1:Slice 3.3 协议转换是否做?
- **选 A 做** —— HUAKAI 走 IDE-session 出站,真用户拿 OpenAI 接口能调 cursor 后端;工作量 5–7 天 + 高 ToS 风险
  - 参考项目对照:**无 MIT 等价**(litellm 是 BYOK,CLIProxyAPI 是 codex/gemini);自研全新
- **选 B 不做** —— 只做 Slice 3.1 + 3.2(打磨现有透传 + 接通 OAuth),用户必须自己组 protobuf 进来才能用;低风险
  - 参考项目对照:litellm BYOK 模式([provider_endpoints_support_backup.json](~/refs/litellm/litellm/provider_endpoints_support_backup.json) "cursor")用户拿 API key 直连,适合"用户已有合法访问"场景
- **选 C 先做 A 的逆向,但只在 Owner 本机跑,不进主代码** —— 探索性 Owner-local sidecar,主仓不进 protobuf 代码

### D-2:Slice 3.0 真流量抓取由谁?
- **选 A Owner 本机抓**(只 Owner 能跑真 Cursor IDE)
- **选 B 直接跳过 3.0,先做 3.1/3.2,3.3 时再补**

### D-3:R-3 Rust sidecar 是否纳入 Cursor 出站?
- **选 A 用 Rust sidecar**(per [[project_r3_rust_sidecar]] Rust 出站走 rquest)—— 与其他 mimicry 一致
- **选 B 用 Go utls** —— 与 Slice 3.1 OCAW 包共存

### D-4:cursor vendor 是否纳入 phase-1 4 vendor 真账号验证?
- phase-1 4 vendor (anthropic/openai/gemini/codex) per [[project_real_vendor_account_scope]] —— **cursor 不在内**,Slice 3.0 默认在 phase-2 前先 mock E2E
- 选 A 维持 mock-only;选 B 扩到 5 vendor 真账号验证

## 7. 时间合计 + 推荐起步

| Slice | 工时 | 谁 |
| --- | --- | --- |
| 3.0 Owner 抓真流量 | 0.5–1 天 | Owner |
| 3.1 OCAW signer | 1.5 天 | codex 实施 + Claude review |
| 3.2 真 OAuth exchanger | 2 天 | codex 实施 + Claude review |
| 3.3 协议转换 | 5–7 天 | **Owner 拍板再开** |
| **合计**(3.0 + 3.1 + 3.2,不含 3.3) | **4–4.5 天** | |

**推荐起步:Slice 3.1 OCAW signer**
- 不依赖 Owner 抓流量(trace_id/request_id/timezone 都是客户端可独立生成的)
- 不动 OAuth flow,blast radius 限制在新建包内
- 修复现有 TODO 是显式技术债

Slice 3.2 紧跟,等 D-1 决策再开 3.3。

---

Source files read:
- `/home/codex/HUAKAI/backend/internal/provider/cursor/bootstrap.go`
- `/home/codex/HUAKAI/backend/internal/provider/cursor/cursor_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/cursor/refresher.go`
- `/home/codex/HUAKAI/backend/internal/credentialacq/vendor_exchangers.go`
- `/home/codex/HUAKAI/backend/internal/provider/registrydefault/default.go`(grep only)
- `/home/codex/HUAKAI/backend/deploy/hermes-runner/requirements.txt`
- `/home/codex/HUAKAI/docs/process/reviews/DEFERRED-hermes-runner-hash-lock.md`

Lane: claude-specifier
Time: 2026-05-26T08:30:00Z
