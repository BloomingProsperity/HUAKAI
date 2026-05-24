# Anthropic Pro/Max OAuth 反转 — Synthesis (Claude × Codex Cross-Discuss)

- UTC: 2026-05-24T07:42Z
- 输入:
  - Claude lane: [2026-05-24-anthropic-oauth-inversion-claude.md](2026-05-24-anthropic-oauth-inversion-claude.md) (24K,6 个 D 决策)
  - Codex lane: [2026-05-24-anthropic-oauth-inversion-codex.md](2026-05-24-anthropic-oauth-inversion-codex.md) (58K,8 个 D 决策)
- Ref anchor: [docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md) (latest SHA per CLAUDE.md #12)
- CLAUDE.md 条款:#10 parallel-draft → cross-discuss → synthesis

## §A 共识区(直接落地)

| 主题 | Claude 立场 | Codex 立场 | 一致结论 |
|---|---|---|---|
| 实现基础 | D-5 paraphrase 从零写 (#feedback_relax_self_constraints) | D1-A Paraphrased HUAKAI implementation (Recommended) | **paraphrase**,不 vendor CLIProxyAPI |
| Refresh 适配 | D-6 复用 credentialworker.Scheduler | D6-A Upgrade existing credentialworker adapter (Recommended) | **复用 Scheduler**,加 Anthropic 专用 retry-after / non-retryable 分类 |
| Callback UX | D-2 Web OAuth (HUAKAI 服务端中转) | D4-A HUAKAI server-side callback (Recommended) | **服务端 callback**,manual paste 留作 Personal Edition 兜底 |
| Long-lived setup token | (Claude 未列) | D8-A Keep disabled by default | **保持禁用**,作为后续 Owner-flag 项 |
| Frozen package | Claude 强调 gatewayhttp/gateway/proto 不加新文件 | Codex 同 | **冻结包不动**,新代码进 `backend/internal/anthropicoauth/` 或 `provider/anthropic/` |

## §B 冲突区(必 Owner 拍板)

### B-1 Transport mimicry 时机

- **Claude D-4 立场**:本 plan 不决,refer-out [[project_l1_tls_boringssl]],等 transport backend 决策完才接入 mimicry。
- **Codex D7-B 立场**:本切片直接用 mimicry transport(`transport.Factory` 非 stub Anthropic 模板),否则订阅可靠性不够。
- **冲突点**:Anthropic Pro/Max 是否会在 OAuth + 首请求阶段就触发 vendor 风控(JA3/H2 指纹检测)?
- **赌注**:
  - 若 Anthropic backend 真做 transport 指纹检测 → Codex 立场对(没 mimicry = 当场风控)
  - 若仅在高频请求阶段做 → Claude 立场对(可先上 plain transport,后续加 mimicry profile)
- **Owner 拍板维度**:
  - 接 mimicry → 需要 prerequisite [[project_l1_tls_boringssl]] 选型(uTLS/BoringSSL/wreq)先就位
  - 不接 → 切片可立即开工,但留 D-4 二期切片
- **借鉴对照**:
  - `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31-158` 全做 mimicry,但 CLIProxyAPI 是单机 CLI,不是 SaaS 中转
  - `Wei-Shaw/sub2api@63b0631a5827`(本日 anchor):有 TLS profile resolver 但默认 disabled,跟 Claude 立场一致

### B-2 Protocol family 拆不拆

- **Claude D 未列**(隐含:passthroughadapter 加 OAuth 分支即可)
- **Codex D2-A 立场**:新加 `anthropic_claude_session` protocol family,跟 `anthropic_messages`(API key)并列。
- **冲突点**:HUAKAI 现有 protocol family 4 个(anthropic_messages / openai_chat_completions / gemini_v1 / cohere),加新 family 需要改 model binding 配置 + registry。
- **codex 论点**:OAuth subscription 的语义跟 API key 不同,合并会造成 audit / billing / dispatch 三层判别混乱。
- **Claude 反驳**:passthroughadapter 内部 if-credential-type 分支已经够,新 family 是过度设计。
- **Owner 拍板维度**:架构整洁 vs 改动面;若 Owner 想未来分流计费(订阅账号不计 token 费,API key 算 token 费),拆新 family 是更早的对齐点。

### B-3 Token exchange 时机

- **Claude D 未列**(隐含:callback 时立刻 exchange)
- **Codex D5-A 立场**:Recommended — callback 时 exchange,把 validated candidate 存 finalize-pending 表。
- **Codex D5-B Alternative**:callback 只存加密 code,finalize 时 exchange。
- **冲突点**:加密 code 存数据库期间被泄露的风险 vs callback 时 exchange 失败后用户体验(回到 OAuth 起手)。
- **Owner 拍板维度**:operator 友好(callback 时一次性完成)vs 安全(finalize 时手工确认)。

## §C 单边维度(Owner 可独立拍)

### C-1 Auth mode runtime mapping (Codex D3)

Codex 提:Anthropic OAuth 有两种 mode (`claude_ai_oauth` 网页 / `claude_code` CLI),三种处理:
- A: 一个 adapter + mode metadata in Extra (Recommended)
- B: 两个 adapter (web / CLI)
- C: `claude_code` 当 session token,只 OAuth `claude_ai_oauth`

**Claude 不曾考虑**;**该决策影响 schema 是否要 mode 列**(参 C-4)。

### C-2 Long-lived setup token (Codex D8)

Anthropic 文档里有"setup token"作为 long-lived 凭证。是否支持:
- A: 默认禁用 (Recommended)
- B: Owner-enabled feature flag
- C: 总允许(高风险,reject)

Claude 不曾考虑,Codex 推 A。

### C-3 Client_id 来源 (Claude D-1)

订阅账号反 API 的 OAuth client_id:
- 来自 Anthropic 公开文档 / CLI binary 抓取 / HUAKAI 注册新 OAuth app
- 哪条路径合规、合 Anthropic ToS、对 Owner 私有部署友好

Codex 未列;但 D1-A 推 paraphrase 暗示用 CLIProxyAPI 用的同一个公开 client_id(那是 Anthropic CLI 公开识别符)。

### C-4 Schema 升级 (Claude D-3)

是否要 migration 加列:
- A: 不动 schema,新字段进 encrypted_metadata JSON
- B: 加 `auth_mode` / `client_id_source` 等显式列

Codex 偏 A(D-SCHEMA-001 元规则:任何 DB schema 都需 Owner 高风险确认)。Claude 倾向 B(显式更好查 audit)。

## §D 推荐执行序

按 cross-discuss 后建议优先序:

1. **C0 mimicry transport 决策** — Owner 先拍 B-1(参考 [[project_l1_tls_boringssl]] 是否已定);影响 C1 切片能否启动
2. **C1 基础 OAuth flow** — paraphrase 实现 PKCE + state + callback exchange (B-3 选 A);依赖 B-2 决策
3. **C2 token storage** — 跟 C-1 (auth mode) 联动
4. **C3 Refresh scheduler 接入** — 复用 P0-4 audit 同事务路径
5. **C4 Transport mimicry profile** — B-1 拍板后才动
6. **C5 setup token gate** — Owner-flag (C-2 选 B);后排
7. **C6 Schema 显式列** — C-4 拍板后

## §E 借鉴项目对照(CLAUDE.md #15 — 给 Owner 横向对比)

| 维度 | CLIProxyAPI (MIT @50d19e204fed) | sub2api (LGPL @63b0631a5827) | litellm (Apache-2.0 @414866767176) |
|---|---|---|---|
| OAuth 反转 | ✓ 完整 (anthropic_auth.go + pkce.go + token.go + oauth_server.go + utls_transport.go) | ✗ 无 Anthropic 反转(只有 API key 中转) | ✗ 无 Anthropic OAuth 反转 |
| Transport mimicry | utls_transport.go:31 完整 mimicry | tls_fingerprint_profile_service.go:171 默认 disabled | 无 transport mimicry |
| Refresh 策略 | anthropic_auth.go:355 JSON refresh + rate-limit block | channel_monitor_service.go:269 monitor + 重试 | proxy/_experimental/mcp_server/oauth2_token_cache.py:80 lock + double-check |
| Callback 架构 | oauth_server.go:168 本地 localhost callback (CLI 模式) | 无 (LGPL,SaaS 模式不同) | mcp_server 内 callback |
| 适合 HUAKAI | **主要 ref**(paraphrase 蓝本) | 仅 refresh / monitor 行为参考 | refresh cache 思路参考 |

## §F Owner 决策清单(Surface 用)

| ID | 决策 | 推荐选项 | 必要性 |
|---|---|---|---|
| AS-D1 (B-1) | Transport mimicry 现在做还是后排 | 后排(等 transport backend 决策) | **必须先决**,影响切片启动 |
| AS-D2 (B-2) | 拆新 protocol family 还是 if-branch | 拆新(`anthropic_claude_session`)— 跟 Codex | 影响 schema + registry |
| AS-D3 (B-3) | Exchange 时机 | callback 时 — 跟 Codex | 影响 callback 设计 |
| AS-D4 (C-1) | Auth mode runtime | A:一个 adapter + Extra metadata | 影响代码组织 |
| AS-D5 (C-2) | Long-lived setup token | A:默认禁用 | 后排 feature-flag |
| AS-D6 (C-3) | Client_id 来源 | 用 CLIProxyAPI 同公开 client_id (待 Owner 法务确认) | 启动前必决 |
| AS-D7 (C-4) | Schema 显式列 vs JSON | A:JSON metadata 先,后续 schema 切片 | Codex 推 A |
| AS-D8 (Claude D-5/Codex D1) | Paraphrase vs Vendoring | paraphrase | **共识**,无需 surface |

## §G Lane + UTC

- Synthesis written by: Claude (claude-opus-4-7)
- UTC: 2026-05-24T07:42Z
- Inputs read: Claude plan + Codex plan §1-§7
- Anchor: ref-anchor.md (latest SHA 8 ref)
- Next: Owner 拍 AS-D1..AS-D8 7 决策 → 进入 C1 切片
