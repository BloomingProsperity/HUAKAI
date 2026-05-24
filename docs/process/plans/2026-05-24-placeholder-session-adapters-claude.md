# Placeholder Session Adapter × 6 实落 + 默认启用 — Claude Lane Plan

- Lane: Claude PM-orchestrator (plan-only,本文不动代码)
- UTC: 2026-05-24T06:55Z
- 互补 lane: docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md (codex 视角,独立起草后做 synthesis)
- CLAUDE.md 适用条款: #10 (parallel-draft) / #11 (clean-room 双 lane) / #12 (源码必读+许可) / #13 (包结构) / #14 (测试质量) / #15 (Owner 决策必带 ref 对照)

## §1 目标与范围

`backend/internal/provider/registrydefault/default.go:130-137` 用环境变量 `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=true` 把以下 6 个订阅 session adapter 默认关闭。HUAKAI 现在只有 API key passthrough 是真生产链路,订阅账号反 API 形态都是骨架。

| Vendor | adapter 文件 | 当前状态 | 主要缺口 |
|---|---|---|---|
| cursor | [backend/internal/provider/cursor/cursor_session.go](backend/internal/provider/cursor/cursor_session.go) | endpoint 已填 `api2.cursor.sh/aiserver.v1.AiService/StreamChat`;header `TODO(OCAW)` 全占位 | gRPC-gateway 协议体 / `x-amzn-trace-id` 等 AWS 路由头 / refresh 协议 |
| copilot | [backend/internal/provider/copilot/copilot_session.go](backend/internal/provider/copilot/copilot_session.go) | endpoint 已填 `api.githubcopilot.com/chat/completions`;header TODO 全占位;Authorization 直接走 `Bearer` | 真实 `Copilot-Integration-Id` / `Editor-Version` / 设备指纹 / device-code OAuth refresh |
| gemini_advanced | [backend/internal/provider/gemini/gemini_advanced_session.go](backend/internal/provider/gemini/gemini_advanced_session.go) | endpoint 占位 BardChatUi;`bl=` 动态版本号 TODO;`SAPISIDHASH` 计算未实现;cookie 直透 | `bl=boq_assistant-bard-web-server_*` 周期采集;SAPISIDHASH 三段哈希;3 cookie 联合鉴权 |
| antigravity | [backend/internal/provider/antigravity/antigravity_session.go](backend/internal/provider/antigravity/antigravity_session.go) | endpoint 全占位 `api.antigravity.ai/v1/chat/completions`;UA `antigravity-client/1.0.0` 全猜 | 真 endpoint / Google OAuth 信任链 / API key 派生 / CodeAssist 后端 |
| kiro | [backend/internal/provider/kiro/kiro_session.go](backend/internal/provider/kiro/kiro_session.go) | endpoint 全占位 `api.kiro.aws/v1/chat/completions`;Auth Bearer 猜测;AWS SigV4 未实现 | 真 endpoint (AWS API Gateway) / Amazon Q Developer SSO 链路 / SigV4 签名 / region 路由 |
| windsurf | [backend/internal/provider/windsurf/windsurf_session.go](backend/internal/provider/windsurf/windsurf_session.go) | endpoint 占位 `api.codeium.com/exa/windsurf_v2/chat/completions`;UA 待确认 | 真 endpoint (内部 protobuf) / Codeium account-token / 设备绑定 |

**统一闭环条件 (每 vendor 完工标准)**:
1. 真 endpoint 抓出来 (Owner 本机抓包,见 [[feedback_owner_local_verification]])
2. auth header / token / cookie shape 准确
3. 接 `credentialworker.Scheduler` refresh adapter (每 vendor 一个 `Refresher` 实现)
4. 默认启用 (Owner 决策 D-2:撤 gate / 翻 default true / 保持 opt-in)
5. integration 测试:mock endpoint 接受+拒绝;真账号 e2e 由 Owner 本机跑

**ceremony 分层** (按 [[feedback_ceremony_tiered]]):
- copilot / cursor / gemini_advanced: **中难度** — 有 MIT ref 可对照,Claude 起 plan + Owner 决策 D
- antigravity / kiro / windsurf: **高难度** — 没有完整 ref,需 Owner 本机抓包后才能动 (本 plan 列研究切片,不落实施)

## §2 现状与缺口锚点

### 2.1 registry 注册点

[`backend/internal/provider/registrydefault/default.go`](backend/internal/provider/registrydefault/default.go) (上轮已读) — 6 个 session adapter 在 line 130-137 注册,gated by `os.Getenv("HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS") == "true"`。Owner D-7 directive 2026-05-06 立的护栏:placeholder adapter 默认 off,不让生产误用。

### 2.2 credentialworker 现状

[`backend/internal/credentialworker/scheduler.go`](backend/internal/credentialworker/scheduler.go) (P0-4 已落地) — `Scheduler` 已经有:
- audit 同事务路径 (`audit.go:51-65` pgx.BeginFunc + InsertOAuthRefreshAuditEvent + AppendInTransaction)
- legacy 2-step 回退路径 (audit.go:67-76)
- refresh 选项注入:`WithTxPool` / `WithAuditQueries` / `WithAuditLedgerSigner` / `WithRefreshQueries` / `withAuditWriter`

缺口:**没有 vendor-specific Refresher 接口注入点**。当前 `Scheduler` 只跑一个泛型 refresh,6 个 vendor 各自的 refresh endpoint / payload / claim 解析都没接。

### 2.3 credentialacq 现状

[`backend/internal/credentialacq/`](backend/internal/credentialacq/) — OAuth / cli_import / cloud_bootstrap handler 框架在 oauth.go(line 44 `StartOAuthFlow`,line 90 `CompleteOAuthCallback`)。每 vendor 没有 vendor-specific bootstrap handler;cursor / copilot / kiro 各家登录方式都不同 (cursor=Web OAuth,copilot=GitHub device-code,kiro=AWS SSO)。

### 2.4 protocol family

[`backend/internal/proto/`](backend/internal/proto/) (god-package 冻结,不加新文件) — 现有 4 个 protocol family:`ProtocolAnthropicMessages` / `ProtocolOpenAIChatCompletions` / `ProtocolGeminiV1` / `ProtocolCohere`。6 个 vendor 各自 native 协议:
- cursor: 自定 gRPC-gateway (StreamChat protobuf)
- copilot: OpenAI Chat Completions 形态 (但 endpoint 在 githubcopilot)
- gemini_advanced: BardFrontendService protobuf (网页 Gemini 内部协议)
- antigravity: 未确认,推测 Google CodeAssist 后端
- kiro: 未确认,推测 Amazon Q Developer 内部协议
- windsurf: 未确认,推测 Codeium 自定 protobuf

→ **gateway 入站要把 OpenAI/Anthropic 形态请求转成 vendor native;出站把 vendor 响应转回 OpenAI/Anthropic SSE**。这块逻辑在 `backend/internal/proto/` 但 proto 是冻结包,不加新文件 → 每 vendor 的协议适配代码放 `backend/internal/provider/<vendor>/` 内部就近,不进 proto。

## §3 参考项目 (per vendor,带 cite + 一句话总结)

### 3.1 cursor

**主要 ref**: 没找到完整 MIT 项目。Cursor 官方客户端是闭源 Electron + protobuf,需要 Owner 本机抓 macOS app TLS 流量。

**次要 ref**:
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/` — Codex CLI 也用 OpenAI 内部 OAuth,可借鉴 PKCE 起手 + token 持久化(file:line 见 §9)
- `~/refs/sub2api/backend/internal/repository/` (LGPL paraphrase only) — sub2api 有 cursor 字符串但只是 ops/monitoring 引用,**没真做 cursor 反转**

**Claude 的判断**:cursor 必须 Owner 本机抓包,不抓 plan 写不下去 — D-5 让 Owner 决策是不是先放 vendor 列表后排。

### 3.2 copilot

**主要 ref**: `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py` (Apache-2.0,商用可 vendor) — GitHub Copilot device-code OAuth + token refresh + telemetry header 完整实现。

**次要 ref**:
- `BerriAI/litellm@HEAD:litellm/llms/github_copilot/common_utils.py` — header constants (Copilot-Integration-Id / Editor-Version)
- `BerriAI/litellm@HEAD:litellm/llms/github_copilot/responses/transformation.py` — 响应转换器

**Claude 的判断**:copilot 是 6 个 vendor 里 **最完备 ref 覆盖**,直接 paraphrase 或 vendor 都 OK(litellm 是 Apache-2.0 → CLAUDE.md #12 允许 vendoring 到 `backend/vendor/litellm/github_copilot/`)。**这条是先开切片首选**。

### 3.3 gemini_advanced (网页版 Gemini)

**主要 ref**: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go + gemini_token.go` — gemini 反转的 OAuth + token shape (MIT)。

**次要 ref**:
- `~/refs/sub2api/backend/internal/repository/geminicli_codeassist_client.go` (LGPL paraphrase only) — gemini 网页/CodeAssist 后端调用
- `~/refs/sub2api/backend/internal/integration/e2e_gateway_test.go` — gemini 端到端测试拓扑

**注意**:CLIProxyAPI 做的 gemini 是 **CLI 反转**(Gemini CLI 工具),不是 **网页 gemini**(gemini.google.com)。这俩协议不同 → HUAKAI gemini_advanced 是 **网页 gemini**,需要 BardFrontendService protobuf + SAPISIDHASH。CLIProxyAPI gemini 只能 partial 借鉴,主体仍要 Owner 抓包。

### 3.4 antigravity

**主要 ref**: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/{auth.go,constants.go,filename.go}` (MIT)。

**次要 ref**:
- `~/refs/sub2api/backend/internal/repository/geminicli_codeassist_client.go` — Google CodeAssist 后端机制(antigravity 推测复用 CodeAssist)
- `~/refs/sub2api/backend/internal/repository/account_repo_integration_test.go` — antigravity 账号 repo 测试拓扑

**Claude 的判断**:antigravity 实际是 Google 的 IDE 产品,backend 复用 CodeAssist,signaling 应该跟 gemini_advanced 强相关。CLIProxyAPI antigravity 包提供 OAuth 入口 + 常量,**够撑 plan**,但仍需 Owner 抓包确认 endpoint 没变。

### 3.5 kiro

**主要 ref**: **没有 MIT ref**。Amazon Kiro 是 AWS 内部产品,beta 阶段,SDK 闭源。

**次要 ref**:
- `~/refs/CLIProxyAPI/internal/auth/vertex/vertex_credentials.go` — Vertex AI 凭据 / GCP service account 模式(Amazon SDK 模式类比)
- AWS SigV4 公开规范: docs.aws.amazon.com/general/latest/gr/sigv4_signing.html (公开协议,不是 ref-project source)

**Claude 的判断**:kiro 没法在本 plan 里给具体切片 — **建议改成研究切片**,Owner 用 AWS 账号实测后,再决定是否做 (R&D 区块,先放后排,不让它阻塞别的 vendor)。

### 3.6 windsurf

**主要 ref**: **没有 MIT ref**。Windsurf (Codeium) 客户端闭源,VSCode fork。

**次要 ref**:
- `BerriAI/litellm@HEAD:litellm/llms/codestral/` — Codeium 子公司 Codestral SDK 协议(同公司,可能共用部分协议)
- `~/refs/oktsec/internal/discover/scanner.go` — scanner 提到 codeium 但只是 IDE 发现,不是反转

**Claude 的判断**:跟 kiro 一样 — 研究切片,不在本轮强推。

### 3.7 ref 总结表

| Vendor | MIT/Apache ref 强度 | 可 vendor | 抓包需求 | Claude 优先级 |
|---|---|---|---|---|
| copilot | 强 (litellm Apache) | ✓ | 低 | **P1 (先开)** |
| gemini_advanced | 中 (CLIProxyAPI cli 子集) | × (协议不同) | 高 | P2 |
| antigravity | 中 (CLIProxyAPI) | △ | 中 | P2 |
| cursor | 弱 | × | **高** | P3 |
| windsurf | 弱 (Codestral 远亲) | × | 高 | P4 (研究) |
| kiro | 无 | × | **极高** | P4 (研究) |

## §4 文件级范围

**新文件 (per vendor)** — 都在已有 `backend/internal/provider/<vendor>/` 内,**不动冻结包** [[feedback_rust_clear_structure]]:

```
backend/internal/provider/copilot/
  copilot_refresher.go            (新,实现 credentialworker Refresher)
  copilot_bootstrap.go            (新,device-code OAuth bootstrap)
  copilot_protocol.go             (新,OpenAI Chat Completions ↔ GitHub Copilot 协议头适配)
  copilot_refresher_test.go       (新,mutation-discriminating test)
  copilot_bootstrap_test.go       (新)

backend/internal/provider/cursor/
  cursor_refresher.go             (新,若 Owner 抓包后回归 P1)
  cursor_bootstrap.go             (新)
  cursor_grpc_gateway.go          (新,protobuf 适配)
  ...

backend/internal/provider/gemini/
  gemini_advanced_refresher.go    (新)
  gemini_advanced_sapisidhash.go  (新,SAPISIDHASH 算法 — 公开规范不是 ref-project)
  gemini_advanced_bl_capture.go   (新,bl= 动态版本号采集)
  ...

backend/internal/provider/antigravity/
  antigravity_refresher.go        (新)
  antigravity_bootstrap.go        (新)
  ...

backend/internal/provider/kiro/         <-- 研究切片产物,可能不创建
  kiro_research_notes.md          (Owner 抓包后由 Claude 整理)

backend/internal/provider/windsurf/     <-- 研究切片产物
  windsurf_research_notes.md
```

**修改文件 (跨 vendor 共享)**:

- [`backend/internal/credentialworker/options.go`](backend/internal/credentialworker/options.go):加 `WithVendorRefresher(name string, r Refresher)` 注入点,scheduler 内 map 派发
- [`backend/internal/credentialworker/scheduler.go`](backend/internal/credentialworker/scheduler.go):refresh tick 时按 account.VendorName 路由 to 对应 Refresher
- [`backend/internal/provider/registrydefault/default.go`](backend/internal/provider/registrydefault/default.go):line 130-137 gate 翻 default true (Owner D-2 决策后)
- [`backend/cmd/gateway/wiring.go`](backend/cmd/gateway/wiring.go):把 6 个 vendor Refresher wire 进 Scheduler

**禁止新增**:
- ❌ `backend/internal/proto/` (冻结)
- ❌ `backend/internal/gateway/` (冻结)
- ❌ `backend/internal/gatewayhttp/` (冻结)
- ❌ `backend/vendor/sub2api/` (LGPL,不许 vendoring)
- ❌ 任何 reference-project 源码原样复制

**允许 vendoring (Apache-2.0/MIT)**:
- ✓ `backend/vendor/litellm/github_copilot/` (Apache-2.0,litellm) → 配 NOTICE + LICENSE + MODIFICATIONS.md (CLAUDE.md #12)
- ✓ `backend/vendor/cliproxyapi/<subpath>/` (MIT,如 Owner 在 D-1 拍板使用)

## §5 切片建议

### 切片 P-A: copilot device-code OAuth + refresh 闭环 (P1,ceremony=中)

**Spec 要点**:
1. 实现 GitHub device-code OAuth 流程 (verification_url + user_code + 轮询)
2. 解析 token shape:`access_token` / `refresh_token` / `expires_in` / `endpoint.api` 等 endpoint 重定向字段
3. 实现 `CopilotRefresher.Refresh(ctx, accountID) → token` 接入 `credentialworker.Scheduler`
4. 在 adapter (`copilot_session.go`) 注入实测 header:`Copilot-Integration-Id` / `Editor-Version` / `User-Agent` / `Openai-Intent` / `X-Github-Api-Version`
5. 翻 registry default 注释 (本切片不动 default 翻,留给 P-Z)

**风险测试**(每条带判别 fixture + mutation 自检):
- **R-A1: device-code OAuth 拒签**:mock GitHub returns `slow_down` → 客户端必须按文档要求降频。Fixture:wireshark 抓包看真客户端 `interval` 字段。Mutation:把降频逻辑改成固定 sleep → test 必须红(响应延迟分布不同)。
- **R-A2: 真 token 过期解码**:fixture 用 mock `access_token` 解析 `endpoint.api` 字段 → 必须把 endpoint 路由到真 GitHub Copilot API。Mutation:把 `endpoint.api` 解析删掉,test 必须红 (路由失败)。
- **R-A3: refresh failure → fail-closed audit**:把 mock 改成 401 → `recordAudit` 必须写一行 `outcome=auth_expired` 且 audit_ledger 同事务存在。Mutation:把 `auth.go:51-65` tx 路径删 → test 必须红 (sidecar 行缺失)。
- **R-A4: header tamper**:真测试用 mock backend 检查必须收到 `Copilot-Integration-Id: vscode-chat`,若 adapter 漏发 → mock 返回 400 → test 必须红。

**文件**:`backend/internal/provider/copilot/copilot_refresher.go` + `copilot_bootstrap.go` + 上述 test 文件。

**Lane=specifier 读源码**:`BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py:1-end` (paraphrase) 或 vendor 整个 `litellm/llms/github_copilot/` 到 `backend/vendor/litellm/github_copilot/` (Apache-2.0 allowed,Owner D-3)

### 切片 P-B: gemini_advanced 网页协议 + SAPISIDHASH + bl= 采集 (P2,ceremony=中)

**Spec 要点**:
1. 实现 `SAPISIDHASH`:`SHA1(timestamp + " " + SAPISID + " " + origin)` (公开规范,非 ref-project 借鉴)
2. 实现 `bl=` 动态采集:首次访问 https://gemini.google.com 抓 HTML 提取 `f.req` 中的 `boq_assistant-bard-web-server_*` 版本号,缓存 7 天
3. BardFrontendService protobuf 适配:OpenAI Chat Completions JSON → `f.req` form-encoded JSON
4. 实现 `GeminiAdvancedRefresher`:cookie 3 件套 (`__Secure-1PSID` / `__Secure-1PSIDTS` / `__Secure-1PSIDCC`) 检测过期 + 自动 ping `/_/BardChatUi/data/ping` 续期

**风险测试**:
- **R-B1: SAPISIDHASH 时间窗口**:fixture 用过期 timestamp 计算 → backend 返回 `__SAPISID_HASH_INVALID`。Mutation:固定 timestamp → test 必须红。
- **R-B2: bl= 采集失败 → fail-closed**:mock HTML 返回不含 `boq_assistant-bard-web-server_` → adapter 必须 refuse 发请求且 audit 写 `bl_capture_failed`。
- **R-B3: cookie 3 件套缺一**:fixture 删 `__Secure-1PSIDTS` → adapter 必须返回 `auth_incomplete` 不发出站。Mutation:跳过缺一检查 → test 必须红 (上游 redirect 后 cookie 暴露)。
- **R-B4: response decoded**:fixture 用真录制响应(Owner 本机录) → adapter 必须把 `[[null,"..." ...]]` form-encoded array 解出 OpenAI SSE。Mutation:JSON parser fallback 关掉 → test 必须红。

**文件**:`gemini_advanced_refresher.go` + `gemini_advanced_sapisidhash.go` + `gemini_advanced_bl_capture.go` + test。

### 切片 P-C: antigravity Google CodeAssist 集成 (P2,ceremony=中)

**Spec 要点**:
1. 抓 OAuth scope (推测复用 CodeAssist `https://www.googleapis.com/auth/cloud-platform`)
2. token shape 跟 gemini_advanced 类似 (Google OAuth `access_token` + `refresh_token`)
3. endpoint 抓真 (Owner 本机) → 替换 `defaultAntigravityEndpoint` 占位
4. `AntigravityRefresher` 用 Google OAuth refresh endpoint `https://oauth2.googleapis.com/token`

**风险测试**:
- **R-C1: scope drift**:OAuth 起手 scope 拼错 → backend 返回 `insufficient_scope`。Mutation:scope 删一段 → test 必须红。
- **R-C2: token type=Bearer 还是 OAuth**:fixture mock backend 严格检查 `Authorization` 前缀 → wrong prefix → 401。
- **R-C3: refresh response 缺 refresh_token (Google 默认不返新 refresh_token)**:Refresher 必须保留旧 refresh_token 不覆盖 nil。Mutation:盲覆盖 → test 必须红 (下次 refresh fail)。

**文件**:`antigravity_refresher.go` + `antigravity_bootstrap.go` + test。

### 切片 P-D: cursor 抓包后回归 (P3,blocked on Owner 本机抓包)

**Spec 要点**:
- (待 Owner 抓包)
- 重写 `defaultCursorEndpoint` 若变更
- 实现 cursor 自定 protobuf 适配 (StreamChat.proto schema 抓出来)
- `CursorRefresher`:cursor token 据观察走 JWT,refresh 用 cursor.sh OAuth

**研究产物**:`backend/internal/provider/cursor/cursor_research_notes.md`,Owner 抓包后由 Claude 整理。

### 切片 P-E / P-F: windsurf / kiro 研究切片 (P4)

**Spec**:不实施代码,只产 `windsurf_research_notes.md` / `kiro_research_notes.md`,内容:
- Owner 本机抓的 endpoint
- 抓的 header 实例
- 抓的 token shape
- 推测的 refresh 协议
- ref 项目对照 (litellm Codestral / CLIProxyAPI vertex)

**完工才能进 P3 切片实施。**

### 切片 P-Z: registry default 翻 (压轴)

**Spec**:`registrydefault/default.go:130-137` 的 gate 翻 default true,或加 per-vendor gate (每 vendor 单独 env var,允许逐家上线)。**这条等 P-A / P-B / P-C 都过了 verify 才动**,P-D/E/F 不阻塞 P-Z。

## §6 风险测试矩阵汇总

| ID | 风险 | 真实损失 | mutation 自检 | 判别 fixture |
|---|---|---|---|---|
| R-A1 | Copilot device-code 降频违规 | 账号被 GitHub 拉黑 | 改成固定 sleep | wireshark 抓真客户端 interval |
| R-A2 | endpoint.api 路由失败 | 请求打错域名,响应解析失败 | 删 endpoint.api 解析 | mock token 含/不含字段两组 |
| R-A3 | refresh 失败未同事务 audit | 财务对不上账 | 删 audit.go 同事务路径 | 写之前/之后 query audit row 比对 |
| R-A4 | Copilot header 漏发 | mock backend 400 | 漏发 Integration-Id | mock backend 强校 header |
| R-B1 | SAPISIDHASH 时间漂移 | gemini 拒签 | 固定 timestamp | now() vs now()-2h 差异 |
| R-B2 | bl= 采集失败盲发 | 上游 404 | 缺 bl= 不阻塞 | HTML mock 双版本 |
| R-B3 | cookie 缺一暴露 | 上游 redirect 后泄漏 | 跳过缺一检查 | fixture 删一个 cookie |
| R-B4 | response array parser fallback 关 | 输出错乱 | 关 fallback | 真录制 response vs 异常 response |
| R-C1 | scope 漂移 | refresh permission denied | 删一段 scope | 完整 scope vs 缺段 |
| R-C2 | token type prefix | 401 | 改 Bearer→OAuth | 严格 prefix 校验 mock |
| R-C3 | refresh_token nil 覆盖 | 下次 refresh fail | 盲覆盖 | response 有/无 refresh_token |

每条都满足 CLAUDE.md #14:测试在该抓的缺陷出现时必须变红,自证测试。

## §7 D 决策点 (Owner pick)

### D-1: copilot ref 源策略

| 选项 | 大白话 | ref 项目对照 | trade-off |
|---|---|---|---|
| (A) **vendor litellm github_copilot/** | 整个 litellm 这块代码直接拷到 `backend/vendor/litellm/github_copilot/`,改成 Go 后调用 | `BerriAI/litellm@HEAD:litellm/llms/github_copilot/authenticator.py`(Apache-2.0,CLAUDE.md #12 允许)<br>`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/` (MIT,无 copilot 但可参考 device-code 模板) | 快 (现成);要写 NOTICE + MODIFICATIONS.md;litellm 是 Python,要翻 Go |
| (B) **paraphrase 从零写** | 读 litellm 提炼行为,Go 风格重写,不拷 | 同上 | 慢;不需 NOTICE;独立 |
| (C) **混合**:vendor litellm constants (header 名 / endpoint URL) 但 OAuth 逻辑 paraphrase | 拷常量,逻辑独立写 | 同上 | 折中,适合 Apache-2.0 |

**Claude 推荐**:(B) paraphrase 从零写。**Why**:litellm 是 Python,翻 Go 比 paraphrase 还慢;copilot 的核心是 device-code OAuth + 几个 header,paraphrase 200 LoC 能搞定。**[[feedback_relax_self_constraints_for_project_benefit]]** — vendoring 是用,paraphrase 也是用,选轻的。

### D-2: registry default 启用策略

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) **撤 gate,所有 6 个默认 on** | `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS` 这变量删,registry 直接注册 | `router-for-me/CLIProxyAPI@50d19e204fed:cmd/server/main.go` — CLIProxyAPI 全部 vendor 默认注册,无 gate |
| (B) **保留 gate,默认翻 true** | gate 仍在(留逃生口),但默认 on | `BerriAI/litellm@HEAD:litellm/proxy/_experimental/` 实验功能 gate 模式 |
| (C) **per-vendor gate**:6 个 vendor 各一个 env var | 例如 `HUAKAI_ENABLE_COPILOT_SESSION=true` 单独翻 | `~/refs/portkey-gateway` 有 per-vendor feature flag |
| (D) **完工逐个上**:cursor/copilot/gemini_advanced 翻 true (P1/P2 完工后);antigravity/windsurf/kiro 维持 false 直到 P3/P4 | 切片驱动 | (无完全等价 ref) |

**Claude 推荐**:(D) 完工逐个上。**Why**:Owner [[feedback_no_fake_pass]] — 没完工的不能假装能用。**[[project_real_vendor_account_scope]]** — 只 anthropic/openai/gemini/codex 4 vendor 能真测,其他全 mock,放 default true 误导用户。

### D-3: cursor / kiro / windsurf 研究切片优先级

| 选项 | 大白话 |
|---|---|
| (A) 三家全等 Owner 抓包,plan 里 P3/P4 全 placeholder | 不阻塞其它 vendor |
| (B) 先开 cursor (用户呼声高),kiro/windsurf 完全 dropped | 聚焦 |
| (C) 都 dropped,只做 copilot+gemini_advanced+antigravity 3 家 | 减负 |

**Claude 推荐**:(A) 全等 Owner 抓包。**Why**:[[feedback_owner_local_verification]] — 没 Owner 本机数据写也是空想。

### D-4: vendor Refresher 注入方式

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) **`WithVendorRefresher(name, r)` 单条注入** | Scheduler 内部 `map[string]Refresher`,按 account.VendorName 派发 | `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/manager.go`(假设有)— 单 map 调度 |
| (B) **Refresher 走 provider Registry** | 复用 `provider.Registry`,把 Refresher 当 provider 能力一起注册 | (无直接 ref,HUAKAI 自研) |
| (C) **vendor-specific Scheduler 子类型** | 每 vendor 一个 `CopilotScheduler / GeminiScheduler` | (无 ref,过度设计) |

**Claude 推荐**:(A) 单 map。**Why**:简洁,符合 [[feedback_huakai_better_than_sub2api]] 比 sub2api 更简洁。

### D-5: copilot/cursor 协议适配落在哪

| 选项 | 大白话 |
|---|---|
| (A) **`backend/internal/provider/<vendor>/` 各自一个 `*_protocol.go`** | 就近 vendor,proto 包不动 |
| (B) **新建 `backend/internal/provideradapter/` 包** | 集中 vendor → core protocol 转换 |

**Claude 推荐**:(A) 就近。**Why**:CLAUDE.md #13 — vendor 自治,新包暂不引入避免扩散。冻结的 proto 包不动。

### D-6: 错误响应 / 风控被封 handling

当 vendor backend 返回 `403 / account_disabled / risk_control_triggered`:

| 选项 | 大白话 | ref 项目对照 |
|---|---|---|
| (A) **直接 soft-disable 账号 + 报警** | 一次封禁立即关 | `~/refs/sub2api/backend/internal/service/channel_monitor_service.go` (LGPL paraphrase only) — sub2api 有 channel monitor |
| (B) **N 连续封才 soft-disable** | 防误判 | (无具体 ref,推断常见) |
| (C) **soft-disable + 自动放 cooldown 后再尝试** | 防永久封 | `~/refs/sub2api/backend/internal/repository/account_repo_integration_test.go` 暗示 |

**Claude 推荐**:(B) N 连续封 (N=3) 才 disable。**Why**:[[project_trust_ledger_failclosed_policy]] — fail-closed 但不要 jumpy,trust ledger 写每次 outcome,3 连封是 audit-driven decision。

## §8 验证

- 单元:`go test -C backend ./internal/provider/copilot/... ./internal/provider/cursor/... ./internal/provider/gemini/... ./internal/provider/antigravity/... ./internal/credentialworker/...`
- 集成 PG:`go test -C backend -tags integration_pg ./internal/credentialworker/...` (audit 同事务跨 vendor 复测)
- mutation 自检:每个 refresher 写 `*_mutation_test.go`,显式 mock 错误注入 + 断言变红
- 全量:`go test -C backend ./...` 满足 [[feedback_full_suite_verification]]
- Owner 本机 e2e:6 个 vendor 各跑一次真账号 request,**Owner 操作**,Claude 只准备 curl 脚本

## §9 Source files read

**HUAKAI**:
- `backend/internal/provider/registrydefault/default.go:106` (上轮 read,记忆)
- `backend/internal/provider/registrydefault/default.go:130-137`
- `backend/internal/provider/cursor/cursor_session.go:1-160` (grep)
- `backend/internal/provider/copilot/copilot_session.go:1-160` (grep)
- `backend/internal/provider/gemini/gemini_advanced_session.go:1-160` (grep)
- `backend/internal/provider/antigravity/antigravity_session.go:1-130` (grep)
- `backend/internal/provider/kiro/kiro_session.go:1-140` (grep)
- `backend/internal/provider/windsurf/windsurf_session.go:1-140` (grep)
- `backend/internal/credentialworker/audit.go:1-120` (本轮 read)
- `backend/internal/credentialworker/options.go:1-115` (本轮 read)

**Refs (lane=specifier,paraphrase only)**:
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/{auth.go,constants.go,filename.go}` (MIT)
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/{gemini_auth.go,gemini_token.go}` (MIT)
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go` (MIT,device-code template)
- `BerriAI/litellm@HEAD:litellm/llms/github_copilot/{authenticator.py,common_utils.py,embedding/transformation.py,responses/transformation.py}` (Apache-2.0)
- `~/refs/sub2api/backend/internal/repository/geminicli_codeassist_client.go` (LGPL — behavior summary only,no copy)
- `~/refs/sub2api/backend/internal/service/content_moderation.go` (LGPL — cursor refs but ops-monitoring,not auth)

**Recency check (CLAUDE.md #12)**:
- CLIProxyAPI: pushed_at 2026-05-23 (上轮已 verify,30 天内)
- **最新 anchor 表**:[docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md) — 本日 07:25Z 重拉 8 个 ref latest SHA(含 Wei-Shaw/sub2api / QuantumNous/new-api 正确 owner)
- litellm: BerriAI/litellm pushed_at 待 fresh check (codex synthesis 时一并 fetch);Apache-2.0 license 已 verify

## §10 Lane + agent attribution

- Agent: claude-opus-4-7 (本对话)
- Session: HUAKAI 2026-05-24,接 2026-05-23 sub2api scaling pivot
- Lane: PM-orchestrator + specifier (本 plan 是 plan-only,不动代码;切片落地时 lane 切 codex executor)
- UTC: 2026-05-24T06:55Z (写入时间)
- Cross-discuss target: `docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md` (codex lane,后台跑中)
- Synthesis 文件: `docs/process/plans/2026-05-24-placeholder-session-adapters-synthesis.md` (codex 完工后写)
