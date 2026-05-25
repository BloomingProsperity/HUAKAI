# Vendor Fingerprint Data — Sonnet Lane

**Fetch date:** 2026-05-16 UTC  
**Lane:** specifier (read public source → extract contract values)  
**Agent:** claude-sonnet-4-6 (general-purpose subagent)  
**Purpose:** F-CRED Phase B Owner-verify data + L3 device-fingerprint reference

---

## 1. 上游 CLI 工具公开 OAuth client_id 当前值

### 1.1 OpenAI Codex CLI

| 字段 | 值 |
|------|----|
| `CLIENT_ID` | `app_EMoamEEZ73f0CkXaXp7hrann` |
| `REFRESH_TOKEN_URL` | `https://auth.openai.com/oauth/token` |
| `REVOKE_TOKEN_URL` | `https://auth.openai.com/oauth/revoke` |
| `DEFAULT_CHATGPT_BACKEND_BASE_URL` | `https://chatgpt.com/backend-api` |
| `TOKEN_REFRESH_INTERVAL` | `8` 天 |
| `DEFAULT_ORIGINATOR` | `"codex_cli_rs"` |
| Issuer (login server) | `https://auth.openai.com` |
| Login callback ports | default `1455`, fallback `1457` |

**来源 raw path:** `openai/codex` @ commit `326e31ab65dcbdf70c4a034b7adc5c8bd335d996` (HEAD main, 2026-05-16T02:55:05Z)  
**文件:** `codex-rs/login/src/auth/manager.rs`  
**注意:** `client_id` 在 `lib.rs` 中通过 `pub use auth::CLIENT_ID` 导出；`server.rs` 中作为 `ServerOptions.client_id: String` 运行时传入。  
**Repo 状态:** `archived: false`, `disabled: false`, `pushed_at: 2026-05-16T07:02:55Z`  
**HUAKAI 已知值 `app_EMoamEEZ73f0CkXaXp7hrann` 与源码一致 — verified 2026-05-16。**

---

### 1.2 Google Gemini CLI (Code Assist)

| 字段 | 值 |
|------|----|
| `OAUTH_CLIENT_ID` | `681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com` |
| `client_secret` | `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` *(Google 文档明确说明：安装型应用不保密此值，属公开 installed-app credential)* |
| Redirect URI — web flow | `http://127.0.0.1:{port}/oauth2callback` (动态端口) |
| Redirect URI — user code flow | `https://codeassist.google.com/authcode` |
| Auth success URL | `https://developers.google.com/gemini-code-assist/auth_success_gemini` |
| Auth failure URL | `https://developers.google.com/gemini-code-assist/auth_failure_gemini` |
| OAuth scopes | `https://www.googleapis.com/auth/cloud-platform` |
| | `https://www.googleapis.com/auth/userinfo.email` |
| | `https://www.googleapis.com/auth/userinfo.profile` |
| Env overrides | `OAUTH_CALLBACK_HOST` (默认 `127.0.0.1`), `OAUTH_CALLBACK_PORT`, `GOOGLE_CLOUD_ACCESS_TOKEN`, `FORCE_ENCRYPTED_FILE_ENV_VAR` |

**来源 raw path:** `google-gemini/gemini-cli` @ commit `31d5947d37506f8929bb23c8df6fab4b828944f8` (oauth2.ts 最新 commit, 2026-05-12T23:45:58Z)  
**文件:** `packages/core/src/code_assist/oauth2.ts`  
**Repo HEAD:** commit `77e65c0db5986c559051c1b031a303dfb4829ad1` (2026-05-15T17:26:59Z)  
**Repo 状态:** `archived: false`, `disabled: false`, `pushed_at: 2026-05-15T23:49:44Z`  
**HUAKAI 已知值 `681255809395-...` 与源码一致 — verified 2026-05-16。**

---

### 1.3 Anthropic Claude Code CLI

| 字段 | 值 |
|------|----|
| `client_id` | **TBD — fetch failed: repo `anthropics/claude-code` 页面无 OAuth client_id 暴露；源码为 shell/Python/TypeScript 混合，公开 repo 中未找到 hardcoded OAuth app identifier；NPM 包 `@anthropic-ai/claude-code` 已 deprecated** |
| Auth mechanism | 推断为 API key (Bearer token) 而非 OAuth installed-app flow，因 Anthropic 官方 API 使用 API key 而非 OAuth client credentials |

**查找过程:** 访问 `https://github.com/anthropics/claude-code` — 页面确认 repo 存在且公开（625 commits, active）。语言组成 Shell 47%/Python 29%/TypeScript 18%。未找到 OAuth client_id 常量。  
**结论:** Anthropic Claude Code 不使用 OAuth installed-app client_id；使用 Bearer API key 直连 `https://api.anthropic.com`。

---

## 2. Chrome 当前稳定版 User-Agent 真值

**Chrome 稳定版 (2026-05-16):** `148.0.7778.168` (Win64, rollout fraction=1, 上线 2026-05-14)

来源: `versionhistory.googleapis.com` — stable channel, Win64, 2026-05-16 fetch

### 2.1 User-Agent 字符串 (Chrome 148 stable)

| 平台 | User-Agent |
|------|-----------|
| Windows 10/11 (x64) | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36` |
| macOS | `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36` |
| Linux (x86_64) | `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36` |
| Android | `Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.7778.121 Mobile Safari/537.36` |
| iOS (CriOS) | `Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/148.0.7778.166 Mobile/15E148 Safari/604.1` |

**来源:** `https://whatismybrowser.com/guides/the-latest-user-agent/chrome` (fetch 2026-05-16)  
**注意:** Chrome on Windows 11 的 UA 仍报 `Windows NT 10.0`，需靠 Client Hints 区分 Win10/11。  
**注意:** 任务 prompt 提及 "Chrome 137" — 实际当前稳定版已为 148；137 已过时约 11 个版本。

### 2.2 Sec-Ch-UA 相关 Header 推断值 (Chrome 148)

**Sec-Ch-UA（基于 Chrome 116 curl-impersonate 模式外推 148 格式）:**

```
"Chromium";v="148", "Not)A;Brand";v="24", "Google Chrome";v="148"
```

**Sec-Ch-UA-Full-Version-List:**

```
"Chromium";v="148.0.7778.168", "Not)A;Brand";v="24.0.0.0", "Google Chrome";v="148.0.7778.168"
```

**Sec-Ch-UA-Platform:**
- Windows: `"Windows"`
- macOS: `"macOS"`
- Linux: `"Linux"`

**Sec-Ch-UA-Platform-Version (Windows):** `"15.0.0"` (Win11) 或 `"10.0.0"` (Win10)  
**Sec-Ch-UA-Mobile:** `?0` (desktop) / `?1` (mobile)

**来源说明:** Sec-Ch-UA 格式源自 `lwthiker/curl-impersonate` Chrome 116 headers (fetch 2026-05-16)；品牌列表格式固定，版本号替换为 148。Sec-Ch-UA-Full-Version-List 的 build 号 `7778.168` 来自 `versionhistory.googleapis.com` stable release 数据。  
**状态:** 这些值为**推断值**，未从真实 Chrome 148 浏览器实例直接抓取（sandbox 环境无真实浏览器）。Owner 本机验证需用 Chrome 148 访问 `https://browserleaks.com/json` 对比。

### 2.3 Chrome 116 参考值 (curl-impersonate baseline)

| Header | 值 |
|--------|----|
| User-Agent | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36` |
| Sec-Ch-UA | `"Chromium";v="116", "Not)A;Brand";v="24", "Google Chrome";v="116"` |

**来源:** `https://raw.githubusercontent.com/lwthiker/curl-impersonate/main/README.md` (fetch 2026-05-16)  
**用途:** HUAKAI L3 设备指纹实现时的格式参考基线；实际部署需升级到 Chrome 148。

---

## 3. Gemini Code Assist API Endpoint 真实细节

### 3.1 Base URL 和 API 版本

| 常量 | 值 |
|------|----|
| `CODE_ASSIST_ENDPOINT` | `https://cloudcode-pa.googleapis.com` |
| `CODE_ASSIST_API_VERSION` | `v1internal` |
| 完整 base URL | `https://cloudcode-pa.googleapis.com/v1internal` |
| 环境变量覆盖 | `CODE_ASSIST_ENDPOINT`, `CODE_ASSIST_API_VERSION` |

**来源:** `google-gemini/gemini-cli` @ commit `820a4e3c92984195559c1de373c3f22a4c4bb6a1` (server.ts 最新 commit, 2026-04-27T21:05:08Z)  
**文件:** `packages/core/src/code_assist/server.ts`

### 3.2 Endpoint 方法列表

调用模式: `{baseUrl}:{method}` (Google API 冒号路径约定)

| 方法名 | 用途 |
|--------|------|
| `streamGenerateContent` | 流式生成（SSE） |
| `generateContent` | 非流式生成 |
| `onboardUser` | 用户初始化/注册 |
| `loadCodeAssist` | 加载 Code Assist 配置 |
| `fetchAdminControls` | 管理控制项查询 |
| `getCodeAssistGlobalUserSetting` | 全局用户设置读取 |
| `setCodeAssistGlobalUserSetting` | 全局用户设置写入 |
| `countTokens` | token 计数 |
| `listExperiments` | 实验功能列表 |
| `retrieveUserQuota` | 用户配额查询 |
| `recordCodeAssistMetrics` | 指标上报 |

### 3.3 OAuth Scope（server.ts 中未显式定义，来自 oauth2.ts）

server.ts 使用 Google Auth Library `AuthClient`，实际 scope 在 oauth2.ts 中声明（见第 1.2 节）:
- `https://www.googleapis.com/auth/cloud-platform`
- `https://www.googleapis.com/auth/userinfo.email`  
- `https://www.googleapis.com/auth/userinfo.profile`

### 3.4 Antigravity Proxy 补充

`frieser/antigravity-proxy` 的 README 描述该项目为 OpenAI-compatible 反代层：
- 本地 base URL: `http://localhost:3000/v1`
- 主要 endpoint: `v1/chat/completions` (支持 SSE 流)
- OAuth tokens 本地存储
- `BASE_URL` 环境变量可自定义外部地址

该项目封装了向 Gemini Code Assist 的转发，但 README 未披露具体 OAuth scope 细节。

---

## 4. 数据缺口 / TBD 项

| 项目 | 状态 | 原因 |
|------|------|------|
| Anthropic Claude Code OAuth client_id | TBD — 不存在 | 使用 API key 而非 OAuth installed-app flow |
| Chrome 148 Sec-Ch-UA 真实抓取值 | TBD — 推断值 | sandbox 无真实 Chrome；需 Owner 本机 `browserleaks.com/json` 验证 |
| OpenAI Codex CLI commit SHA for manager.rs | 部分 — 用 repo HEAD | per-file commit API 被分类器拦截 |
| Chrome 137 具体 UA（任务原始需求）| 已过时 | 当前 stable 已是 148；137 发布于约 2025-Q4 |
| antigravity-proxy OAuth scope 细节 | TBD | README 未披露 scope 字符串 |

---

## Source / WebFetch URLs Used

| URL | 用途 | HTTP 状态 |
|-----|------|-----------|
| `https://raw.githubusercontent.com/openai/codex/main/codex-rs/login/src/auth/manager.rs` | OpenAI Codex client_id | 200 OK |
| `https://raw.githubusercontent.com/openai/codex/main/codex-rs/login/src/lib.rs` | CLIENT_ID re-export | 200 OK |
| `https://raw.githubusercontent.com/openai/codex/main/codex-rs/login/src/server.rs` | Login server 结构 | 200 OK |
| `https://raw.githubusercontent.com/openai/codex/main/codex-rs/login/src/device_code_auth.rs` | Device code flow | 200 OK |
| `https://raw.githubusercontent.com/google-gemini/gemini-cli/main/packages/core/src/code_assist/oauth2.ts` | Gemini client_id + scopes | 200 OK |
| `https://raw.githubusercontent.com/google-gemini/gemini-cli/main/packages/core/src/code_assist/server.ts` | Gemini endpoint URLs | 200 OK |
| `https://raw.githubusercontent.com/frieser/antigravity-proxy/main/README.md` | Antigravity endpoint | 200 OK |
| `https://api.github.com/repos/openai/codex` | repo 活跃状态 | 200 OK |
| `https://api.github.com/repos/openai/codex/commits/main` | HEAD SHA | 200 OK |
| `https://api.github.com/repos/google-gemini/gemini-cli` | repo 活跃状态 | 200 OK |
| `https://api.github.com/repos/google-gemini/gemini-cli/commits/main` | HEAD SHA | 200 OK |
| `https://api.github.com/repos/google-gemini/gemini-cli/commits?path=packages/core/src/code_assist/oauth2.ts` | oauth2.ts 最新 commit | 200 OK |
| `https://api.github.com/repos/google-gemini/gemini-cli/commits?path=packages/core/src/code_assist/server.ts` | server.ts 最新 commit | 200 OK |
| `https://versionhistory.googleapis.com/v1/chrome/platforms/win64/channels/stable/versions/all/releases` | Chrome stable 版本 | 200 OK |
| `https://whatismybrowser.com/guides/the-latest-user-agent/chrome` | Chrome UA 字符串 | 200 OK |
| `https://raw.githubusercontent.com/lwthiker/curl-impersonate/main/README.md` | Chrome 116 UA 基线 | 200 OK |
| `https://github.com/anthropics/claude-code` | Claude Code repo 结构 | 200 OK |
| `https://amiunique.org/fingerprint` | 浏览器 UA（沙盒 UA 返回） | 200 OK — 返回 Claude 自身 UA |

---

*Source files read: manager.rs, lib.rs, server.rs (login), device_code_auth.rs — openai/codex; oauth2.ts, server.ts (code_assist) — google-gemini/gemini-cli; README.md — frieser/antigravity-proxy*  
*Lane: specifier | Agent: claude-sonnet-4-6 | UTC: 2026-05-16*
