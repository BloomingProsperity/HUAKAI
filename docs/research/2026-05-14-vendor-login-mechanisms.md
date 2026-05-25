# 2026-05-14 三 vendor 登录机制汇总

| 字段 | 值 |
|---|---|
| 任务 | 汇总 codex、kiro、gemini 三个 CLI/vendor 的登录方式、凭据存储、token 结构、refresh 机制和 HUAKAI 账号入库建议 |
| 数据来源 | Owner 提供的已查证事实、本轮 Gemini 抓包、既有 codex/kiro 请求签名文档、HUAKAI L2 credentialworker adapter 代码 |
| 安全边界 | 不写真实 token、refresh token、client secret、账号 ID；只写字段结构、长度级别、路径和机制 |
| Observed regions | 9 |
| Inferences | 6 |
| Open questions | 4 |

## 汇总表

| Vendor | 登录方式 | 本地凭据路径 | token 结构 | refresh 机制 | 多模式支持 |
|---|---|---|---|---|---|
| codex | `codex login` 浏览器 OAuth；也支持 `codex login --with-api-key` 从 stdin 读 API key | `~/.codex/auth.json` 或 `$CODEX_HOME/auth.json` | `{auth_mode, OPENAI_API_KEY, tokens, last_refresh}`；`tokens` 约 4243 字符级，含 access/refresh/id token 和账号元数据 | ChatGPT OAuth refresh 到 `https://auth.openai.com/oauth/token`；HUAKAI L2 当前有 `CodexRefresh`，复用 OpenAI refresh adapter | `chatgpt` OAuth、API key；另有独立 agent identity 形态，不等同普通 ChatGPT bearer |
| kiro | device-flow OAuth；`kiro-cli login --license free` 走 Builder ID/Google/GitHub；`--license pro` 走 Identity Center；`--use-device-flow` 支持 headless | `~/.aws/sso/cache/<hash>.json` 和 `~/.aws/sso/cache/kiro-auth-token-cli.json` | client registration 文件含 `{clientId, clientSecret, expiresAt}`；token 文件含 `{accessToken, refreshToken, expiresAt, region, authMethod, clientIdHash}` | 有 refresh token，但本轮未确认刷新 endpoint；HUAKAI L2 当前把 `kiro` 登记为 mock-only，需要后续补真 adapter | free Builder ID/Google/GitHub；pro Identity Center |
| gemini | Login with Google，`oauth-personal` 模式 | `~/.gemini/oauth_creds.json`、`~/.gemini/google_accounts.json`、`~/.gemini/settings.json` | OAuth 文件含 `{access_token, id_token, refresh_token, expiry_date, scope, token_type}`；accounts 文件含 `{active, old}`；settings 控制 `security.auth.selectedType` | Google OAuth refresh_token grant；HUAKAI L2 当前有 `GeminiRefresh`，默认 endpoint `https://oauth2.googleapis.com/token` | `vertex-ai`、`oauth-personal`、`gemini-api-key` |

## codex 登录机制

codex 的主登录方式是浏览器 OAuth。`codex login` 获取 ChatGPT OAuth token 后写入本地 `auth.json`。API key 路径由 `codex login --with-api-key` 支持，key 从 stdin 读入，避免 shell history 直接留 key。

本地凭据结构：

```text
~/.codex/auth.json
  auth_mode
  OPENAI_API_KEY
  tokens
  last_refresh
```

`auth_mode=chatgpt` 时，模型请求使用 `tokens.access_token` 作为 `Authorization: Bearer <access_token>`。已有 codex 请求签名文档还记录了账号相关 header：存在 account id 时会加 `ChatGPT-Account-ID: <account_id>`；FedRAMP 账号会有条件 header。HUAKAI 入库时必须把这些账号绑定字段和 token 同版本保存，不能让 bearer token 与账号 header 错配。

Refresh 机制：

```text
POST https://auth.openai.com/oauth/token
grant_type=refresh_token
refresh_token=<refresh_token>
client_id=<codex client id>
```

HUAKAI L2 当前代码里 `backend/internal/credentialworker/adapters/codex.go` 让 `CodexRefresh` 复用 `OpenAIRefresh`。这对 ChatGPT/OAuth refresh 是合理的保守实现：先复用标准 refresh_token grant，若后续 codex 独立 endpoint 被确认，再通过 endpoint 注入替换。

## kiro 登录机制

kiro 使用 device-flow OAuth。交互式或 headless 场景都能通过 CLI 发起：

```text
kiro-cli login --license free
kiro-cli login --license pro
kiro-cli login --use-device-flow
```

free 路径面向 Builder ID、Google、GitHub 登录；pro 路径面向 Identity Center。Owner 已查证的本地缓存是两类文件：

```text
~/.aws/sso/cache/<hash>.json
  clientId
  clientSecret
  expiresAt

~/.aws/sso/cache/kiro-auth-token-cli.json
  accessToken
  refreshToken
  expiresAt
  region
  authMethod: "IdC"
  clientIdHash
```

`<hash>.json` 是 OAuth client registration，`clientSecret` 是长 secret，不能进入日志、文档或未加密数据库字段。`kiro-auth-token-cli.json` 是当前会话 token 文件，`accessToken` 与 `refreshToken` 都是短 token 量级，仍必须按 secret 处理。

Refresh 机制目前只确认本地有 refresh token 和 client registration。模型 API 抓包显示请求使用 `authorization: Bearer <Builder ID token>`，但本轮未把 token refresh endpoint 展开到可上线级别。HUAKAI L2 代码当前在 `backend/internal/credentialworker/refresh_adapter.go` 的 `MockOnlyProviders` 中包含 `kiro`，这表示当前生产代码不应假装支持 Kiro 真刷新。

Kiro 的账号入库建议是先允许手工导入当前 token 结构做短期会话测试，但真实 refresh adapter 进入前要标为 `Manual First` 或 `Mandatory Roadmap`，不能在后台 silently fail。

## gemini 登录机制

Gemini CLI 本轮样本是 Login with Google 后的 `oauth-personal` 模式。模型 API 请求使用：

```text
Authorization: Bearer <google_oauth_access_token>
```

本地凭据结构由 Owner 提供：

```text
~/.gemini/oauth_creds.json
  access_token
  id_token
  refresh_token
  expiry_date
  scope
  token_type

~/.gemini/google_accounts.json
  active
  old

~/.gemini/settings.json
  security.auth.selectedType
```

`security.auth.selectedType` 决定 auth 模式：

```text
vertex-ai
oauth-personal
gemini-api-key
```

本轮 Gemini 模型 API 抓包只覆盖 `oauth-personal`。`vertex-ai` 和 `gemini-api-key` 的请求 header、endpoint、quota 语义可能不同，不能直接用 `oauth-personal` 的 Bearer 模板代表。

HUAKAI L2 当前代码里 `backend/internal/credentialworker/adapters/gemini.go` 已有 `GeminiRefresh`：默认 endpoint 是 `https://oauth2.googleapis.com/token`，请求使用 `grant_type=refresh_token`，并可从 credential JSON 或 adapter 配置读取 `client_id/client_secret`。这和 Google OAuth refresh_token grant 对齐，但仍需要 Owner 真账号跑一次 refresh smoke，确认 Gemini CLI 保存的 refresh token、client id 和 client secret 组合是否满足 refresh 条件。

## HUAKAI 账号入库建议

账号表里的 `provider_accounts.credentials` 应保存 provider-specific JSON，但字段名要归一到 credentialworker 可读的最低公共集：

```text
provider: codex
credential_kind: oauth_chatgpt
credentials:
  access_token
  refresh_token
  id_token
  expires_at
  account_id
  auth_mode
  oauth_token_endpoint
  client_id
  scope

provider: kiro
credential_kind: oauth_device_builder_id_or_idc
credentials:
  access_token
  refresh_token
  expires_at
  region
  auth_method
  client_id
  client_secret
  client_id_hash
  registration_expires_at

provider: gemini
credential_kind: oauth_google_personal
credentials:
  access_token
  refresh_token
  id_token
  expires_at
  expiry_date_original
  scope
  token_type
  selected_type
  oauth_token_endpoint
  client_id
  client_secret
```

实现建议：

1. `access_token`、`refresh_token`、`id_token`、`client_secret` 全部按 secret 加密存储；日志只允许 provider、account_id、token_version、expires_at 和错误类别。
2. `expires_at` 统一为 RFC3339 UTC，导入 Gemini 的 `expiry_date` 时保留原字段到 `expiry_date_original` 便于排障，但调度只读 `expires_at`。
3. credentialworker adapter 只负责 refresh 和写回 credential JSON；请求发送层另由 CredentialInjector 根据 `(provider, credential_kind)` 注入 header。
4. codex 请求注入必须绑定 `Authorization` 与 `ChatGPT-Account-ID`；gemini 请求注入必须绑定 `Authorization` 与当前 Google account；kiro 请求注入必须绑定 Builder ID/IdC token 与 region/authMethod。
5. L2 refresh adapter 当前可直接承接 codex 与 gemini：`CodexRefresh`、`GeminiRefresh` 已注册到 gateway 的 refresh registry。Kiro 仍是 mock-only，不能标记为 full production refresh。
6. 手工导入时，UI/API 应显示字段是否存在和到期时间，不显示 token 内容。导入后立刻做一次只读 token validation 或最小 smoke，失败则账号状态进 `invalid` 或 `manual_reauth_required`。
7. refresh 失败要区分 malformed credential、missing refresh token、401/invalid_grant、429/5xx retryable、mock-only provider。不要把所有失败都压成 `unknown auth failed`。
8. refresh 成功写回必须更新 token version，并写审计事件；并发 refresh 用 CAS 或 winning credential 处理，避免多个 worker 覆盖彼此的新 token。

## 安全要求

1. 文档、模板、测试 fixture 不得包含真实 access token、refresh token、id token、client secret、账号 ID、项目 ID、prompt ID。
2. `/tmp/fingerprint-data/*` 是本机原始材料，可能包含真实 token；只能从中摘取脱敏后的结构、header 名、顺序、长度级别和 endpoint。
3. 后端日志、audit reason、test failure message 不能打印 `credentials` 原文。
4. 长 token 字段做长度级别描述即可，例如 `~260 chars`、`~232 chars`、`~4243 chars`，不要保存前后缀。
5. Kiro 的 `clientSecret` 虽然是本地 OAuth client registration 的一部分，仍应作为 secret，不应降级成普通 metadata。

## Open Questions

1. Kiro refresh endpoint、request body 和 client registration 轮换策略仍需 Owner 真账号抓包或官方协议确认。
2. Gemini `vertex-ai` 模式是否使用 ADC、service account、gcloud token 或不同 quota project，未在本轮覆盖。
3. Gemini `gemini-api-key` 模式的 header/query 注入方式与 Code Assist API 是否共用 endpoint，未在本轮覆盖。
4. codex API-key 模式是否要进入同一个账号池，还是作为独立 credential_kind 与 ChatGPT subscription OAuth 分池，需要产品层决定。

## Source Coverage Proof

- `docs/research/2026-05-14-codex-cli-request-signature-codex.md`：codex `auth.json` 结构、ChatGPT Bearer、Account-ID、refresh endpoint、API-key login 支持。
- `docs/research/2026-05-14-kiro-cli-request-signature.md`：kiro 模型 API Bearer、Builder ID token、device-flow 登录与 AWS SSO cache 结构摘要。
- `/tmp/fingerprint-data/gemini-model-request-detail.txt`：Gemini CLI 模型 API 的 OAuth Bearer、Code Assist API endpoint、UA 和 header 顺序。
- `/tmp/fingerprint-data/gemini-http-capture.jsonl`：Gemini CLI 48 个 HTTP/1.1 request、cloudcode-pa 辅助 endpoint、tokeninfo 和 play logging 链路。
- `backend/internal/credentialworker/refresh_adapter.go`：RefreshAdapter 契约、mock-only provider 列表、`kiro` 当前 mock-only。
- `backend/internal/credentialworker/adapters/codex.go`：CodexRefresh 复用 OpenAIRefresh。
- `backend/internal/credentialworker/adapters/openai.go`：OpenAIRefresh 默认 token endpoint、refresh_token grant、token response merge。
- `backend/internal/credentialworker/adapters/gemini.go`：GeminiRefresh 默认 Google OAuth token endpoint 和 refresh_token grant。
- `backend/cmd/gateway/main.go`：gateway refresh registry 注册 `codex`、`gemini` 和 mock-only vendors。

Source files read: `docs/research/2026-05-14-codex-cli-request-signature-codex.md`; `docs/research/2026-05-14-kiro-cli-request-signature.md`; `/tmp/fingerprint-data/gemini-model-request-detail.txt`; `/tmp/fingerprint-data/gemini-http-capture.jsonl`; `backend/internal/credentialworker/refresh_adapter.go`; `backend/internal/credentialworker/adapters/codex.go`; `backend/internal/credentialworker/adapters/openai.go`; `backend/internal/credentialworker/adapters/gemini.go`; `backend/cmd/gateway/main.go`.

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-14T00:00:00Z

中文总结：本文件的真实观察包括 codex 浏览器 OAuth/API-key 双入口、kiro device-flow 与 AWS SSO cache 双文件、gemini oauth-personal 三文件结构、codex/gemini 现有 L2 refresh adapter、kiro 当前 mock-only；合理推断有 6 项，主要是 HUAKAI 账号入库字段归一、secret 加密、CredentialInjector 与 RefreshAdapter 分层、Kiro 先 Manual First；open question 有 4 个，最高优先级是补 Kiro 真 refresh endpoint 与 Gemini 非 oauth-personal 模式样本。
