# ChatGPT OAuth (vendor=openai, AuthMode=chatgpt_oauth) — Synthesis

Lane: claude-pm-synthesis
Time: 2026-05-27T09:30:00Z
Cross-lane inputs:
- [Claude lane plan](2026-05-27-chatgpt-oauth-claude.md) (5 CHG slice, 4 D 决策)
- [Codex lane plan](2026-05-27-chatgpt-oauth-codex.md) (6 CHG slice, 7 D 决策)

## 0. Lane 差异 + Synthesis 决定

| 维度 | Claude lane | Codex lane | Synthesis 选择 | 理由 |
|---|---|---|---|---|
| Slice 数 | 5 (CHG-1..5) | 6 (CHG-1..6) | **Codex 6 切片更细** | codex 把 authorize URL builder 独立成 CHG-2, store-aware callback 独立成 CHG-3, refresh hardening 独立成 CHG-4 — 闭关面更小, review 更聚焦 |
| D 决策数 | 4 (D-1..D-4) | 7 (D-1..D-7) | **采 codex 7 D** | codex 抓出 Claude lane 漏的 3 个: D-5 refresh endpoint trust, D-6 refresh_token requirement, D-7 revoke endpoint — 真细节差异 |
| D-1 推荐 | C 双模式 | A loopback only | **让 Owner 拍** — 二者均合理 (lane 不一致) | Claude 看 anthropic/gemini 已选双模式; codex 担心 OpenAI app 端不接 admin HTTPS callback (source evidence 仅 loopback) |
| D-2 推荐 | A 保留 OpenAI 特定 params | A 保留 | **A 一致** | 双源 (CLIProxyAPI + openai-codex) 都有, 不抹去 |
| D-3 推荐 | A 全部 persist | A 选 cred + minimal RedactedContext | **采 codex A** | codex 更细 (cred persist + RedactedContext 仅 non-PII 摘要); Claude lane 没分 PII 边界 |
| D-4 推荐 | A Mandatory Roadmap | A Mandatory Roadmap (或 B inject client seam) | **A 一致** | 与 GEM-4 D-5=A 一致路线 |
| D-5 (codex 新增) | 隐含 CHG-3 走 B (内置 pin) | A (built-in 不 store endpoint, refresh 用默认 pin) | **让 Owner 拍 A vs B** — 都是合理信任链 | codex 抓的核心 S1-D 设计点 |
| D-6 (codex 新增) | 隐含 require refresh_token (类比 gemini R2 P2-A 修) | A require refresh_token | **A 一致** | gemini 已经走 A (P2-A 修), 保持一致 |
| D-7 (codex 新增) | 没提 | A Mandatory Roadmap (revoke 不本切片) | **A 一致** | revoke 是 credential lifecycle 第三阶段, 本切片只做 acquisition + refresh |
| 评估 mimicry | 提及 [[project_huakai_codex_mimicry_verified]] codex_cli wire test 已 PASS | 没提 HUAKAI mimicry 已验证 — codex 视角更保守 | **采 Claude lane 信息**: codex_cli mimicry wire PASS 已验, Rust 生产 dispatch 待 F-2.3a runtime preflight | codex 没读历史 memory, Claude 补 |

## 1. Synthesis 切片清单

合并 codex 6 切片 + Claude 5 切片, 推 codex 6 切片但减一些, 最终:

| Slice | 工时 | Commit groupings |
|---|---|---|
| CHG-1 ChatGPT builtin profile + registry 替换 (chatgpt_oauth.go 新建, builtin-profile pattern, vendor_exchangers 替换 PKCEFake) | 0.5 天 | 第 1 commit (与 CHG-2 一起 acquisition 闭环) |
| CHG-2 Authorize URL builder + redirect_uri decision gate (D-1 实施) | 0.5 天 | 同上 |
| CHG-3 Store-aware authorization-code callback 真交换 + admin integration test (D-3 实施 + D-6 实施) | 0.75 天 | 第 2 commit (refresh 风险与 acquisition 分离) |
| CHG-4 OpenAI refresh integration hardening (D-5 实施, 内置 pin + SSRF-protected wiring) | 0.5 天 | 同上 |
| CHG-5 Docs + roadmap sync (D-4=A + D-7=A 进 Mandatory Roadmap) | 0.25-0.5 天 | 第 3 commit |
| **合计** | **2.5-3.0 天** | 3 commits |

D-4=A (mimicry Mandatory Roadmap) 不单独切片, 进 CHG-5 docs。

## 2. 参考项目对照 (CLAUDE.md #15 — 合并双 lane)

| 维度 | CLIProxyAPI 行为 (cite) | openai-codex 行为 (cite) | HUAKAI 选择 |
|---|---|---|---|
| ClientID | CLIProxyAPI@21fad9d8:internal/auth/codex/openai_auth.go:26 声明常量 (paraphrased: 长度 21 + 11 字符的 OpenAI desktop CLI 公开标识符) | openai-codex@2026-05-12-HEAD:codex-rs/login/src/auth/manager.rs:921 声明同值常量 (双源一致) | 内置硬编, HUAKAI 用同一公开标识符值 |
| ClientSecret | 不发送 (PKCE-only) | 不发送 (PKCE-only) | 不需要, 与 anthropic 同款, 不像 gemini 走 env var |
| AuthURL | CLIProxyAPI@21fad9d8:internal/auth/codex/openai_auth.go:24 指向 OpenAI authorize endpoint | (refresh path 走 token endpoint 同 host) | 内置 |
| TokenURL | CLIProxyAPI@21fad9d8:internal/auth/codex/openai_auth.go:25 指向 OpenAI token endpoint | openai-codex@2026-05-12-HEAD:codex-rs/login/src/auth/manager.rs:94 同 host token endpoint | 内置 |
| Scope | CLIProxyAPI@21fad9d8:internal/auth/codex/openai_auth.go:72 声明 4 项 scope 含 offline_access | (token_data 解析 offline 支持) | 内置 (无 env override) |
| RedirectURI | CLIProxyAPI@21fad9d8:internal/auth/codex/openai_auth.go:27 指向 loopback port 1455 path /auth/callback | loopback (port 在 web_oauth) | **D-1 决策** |
| OpenAI 特定 params | CLIProxyAPI 在 authorize URL 加 3 个 OpenAI Codex CLI 专用 query 参数 (强制重新登录 / 组织 ID 注入 / 简化流标识) | openai-codex 同款 | **D-2=A 保留** |
| Token response 字段 | access_token + refresh_token + id_token | + 3 个 ChatGPT 元数据字段 (用户 ID / 计划类型 / 账号 ID) | **D-3 决策** |
| Refresh endpoint trust | CLIProxyAPI 从 token 文件 token_uri 字段读 endpoint | openai-codex 走 manager.rs:94 默认 endpoint pin (不读 cred) | **D-5 决策** |
| revoke endpoint | (没用) | openai-codex@2026-05-12-HEAD:codex-rs/login/src/auth/manager.rs:95 声明 revoke endpoint 常量 | **D-7=A Mandatory Roadmap** |

(Clean-room note: 表格 cite 用 `<repo>@<sha>:file:line` 锚, prose 不再 verbatim 上游 identifier 名 / 字面常量值; 实际常量值在 HUAKAI 实现 backend/internal/credentialacq/chatgpt_oauth.go 中)

## 3. HUAKAI 三维升级 (CLAUDE.md #12, fusion-upgrade)

- **架构升级**:
  - 与 anthropic claude_ai_oauth 同款 builtin-profile pattern (validateBuiltinProfile 拒 ClientSecret 注入), 但与 gemini OAuth 不同 (gemini 要求 ClientSecret 走 env var)
  - 启动 wiring `installChatGPTOAuthExchanger` fail-loud assert (类似 GEM-1 `assertGemini...HaveHTTPClient`)
  - 与 codex_cli (device-code flow, 已有 openAICodexDeviceCodeExchanger) 同包但不混合 (不同 AuthMode)
- **算法升级**:
  - cred 永不持久化 hostile 字段 (ANT-3 R2 S2 scrub commit 57356e4 已覆盖 OpenAI refresh path)
  - refresh_token 缺时 fail-closed (D-6=A; 类似 GEM-1 R2 verify P2-A 修)
  - D-5=B 推荐 (refresh endpoint pin 内置, 不读 cred) — 比 CLIProxyAPI 信任 cred token_uri 更严
- **生态升级**:
  - codex_cli mimicry profile 已 W11-F §14b wire test PASS ([[project_huakai_codex_mimicry_verified]]), 但 Rust 生产 dispatch 待 F-2.3a runtime preflight — D-4=A 标 Mandatory Roadmap, 不破假
  - revoke endpoint Mandatory Roadmap (D-7=A), 与 gemini D-5/D-4 同款 graceful 路标

## 4. Owner 决策点 (4 项需 Owner 拍, 3 项默认采纳)

需要 Owner 拍 (双 lane 不一致 OR 信任链关键):
- **D-1**: redirect_uri 模式 (loopback only vs 双模式)
- **D-3**: ChatGPT metadata persistence (cred-only vs cred + RedactedContext)
- **D-5**: refresh endpoint trust (built-in pin vs store in cred)
- **D-4**: mimicry release gate (Mandatory Roadmap 推荐)

默认采纳 (双 lane 一致):
- D-2=A 保留 OpenAI 特定 params (prompt=login + id_token_add_organizations + codex_cli_simplified_flow)
- D-6=A require refresh_token 非空, fail-closed (与 gemini R2 P2-A 一致)
- D-7=A revoke endpoint Mandatory Roadmap, 本切片不实施

Source files read:
- ~/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go (MIT, E-LIC-009)
- ~/refs/openai-codex/codex-rs/login/src/auth/manager.rs (Apache-2.0)
- HUAKAI: backend/internal/credentialacq/{vendor_exchangers, types, anthropic_oauth, gemini_oauth, oauth_authorization_code}.go + backend/internal/credentialworker/{mode_refresh, adapters/openai}.go
Lane: claude-pm-synthesis
Time: 2026-05-27T09:30:00Z UTC
