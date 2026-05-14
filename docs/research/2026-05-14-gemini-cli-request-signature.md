# 2026-05-14 Gemini CLI 请求签名抓包分析

| 字段 | 值 |
|---|---|
| 任务 | R-3：记录 Gemini CLI 的 TLS、HTTP/1.1、header 顺序、User-Agent 和鉴权机制 |
| 数据来源 | Owner 自己账号、自己机器上的 mitmproxy 解密流量与被动 TLS ClientHello 抓包 |
| 合规边界 | 协议逆向分析；不读取、复制或翻译参考项目源码；真实 token、project、user_prompt_id 已脱敏 |
| HTTP 抓包 | `/tmp/fingerprint-data/gemini-http-capture.jsonl`，95 行事件：48 个 request、47 个 response |
| TLS 抓包 | `/tmp/fingerprint-data/gemini-tls/clienthello-template.json`，6 个 ClientHello 样本 |
| Observed regions | 7 |
| Inferences | 4 |
| Open questions | 3 |

## 结论摘要

1. Gemini CLI 模型 API 主请求是 `POST https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse`，返回 `text/event-stream`；同一抓包还观察到非流式 `POST /v1internal:generateContent`。
2. 业务 HTTP 协议在 mitmproxy 中观察为 `HTTP/1.1`。48 个 request 全部是 `HTTP/1.1`，`cloudcode-pa.googleapis.com` 的 39 个 request 使用同一组 9-header 顺序。
3. 模型 API header 名保持真实大小写：`Content-Type, User-Agent, Authorization, x-goog-api-client, Accept, Content-Length, Accept-Encoding, Host, Connection`。这点与 Kiro 的全小写 header 栈不同。
4. 模型 API 使用 Google OAuth Bearer：`Authorization: Bearer <google_oauth_access_token>`。抓包中 access token 长度约 260 字符；文档和模板只保留占位。
5. TLS 层是 Node.js TLS 栈，底层由 Node.js 包装 OpenSSL；cipher suite 列表 52 个值稳定，未观察到 GREASE 值。
6. TLS 有两个变体：模型 API 使用 JA3 `55ba290366f110228d176d92fe6f6180` / JA4 `t13d5212_ht_9b003dc3eba7_4e5c652b160e`；辅助连接使用 JA3 `def5ca499f59da2c07dafe0a40545011` / JA4 `t13d5211_00_9b003dc3eba7_ef824704554f`。两者共享 JA4 cipher hash `9b003dc3eba7`。

## TLS ClientHello

TLS 捕获目录是 `/tmp/fingerprint-data/gemini-tls/`。

主模板口径采用模型 API 连接，也就是带 ALPN 的 `ht` 变体：

| 字段 | 值 |
|---|---|
| `sample_count` | 6 |
| `target_host` | `cloudcode-pa.googleapis.com` |
| `tls_backend` | `nodejs` |
| `grease` | `false` |
| `extension_order` | `stable`，但存在按连接类别区分的两个变体 |
| `supported_versions` | `772, 771` |
| `supported_groups` | `4588, 29, 23, 30, 24, 25, 256, 257` |
| `key_share_groups` | `4588, 29` |

52 个 cipher suite 的顺序稳定：

```text
4866,4867,4865,49199,49195,49200,49196,158,49191,103,49192,107,163,159,52393,52392,52394,49325,49311,49245,49249,49239,49235,162,49324,49310,49244,49248,49238,49234,49188,106,49187,64,49162,49172,57,56,49161,49171,51,50,157,49309,49233,156,49308,49232,61,60,53,47
```

模型 API `ht` 变体的 extension 顺序是：

```text
65281,0,11,10,35,16,22,23,13,43,45,51
```

辅助 `00` 变体的 extension 顺序少了 ALPN extension `16`：

```text
65281,0,11,10,35,22,23,13,43,45,51
```

6 个样本的 JA3 hash 是：

```text
def5ca499f59da2c07dafe0a40545011
def5ca499f59da2c07dafe0a40545011
def5ca499f59da2c07dafe0a40545011
55ba290366f110228d176d92fe6f6180
def5ca499f59da2c07dafe0a40545011
def5ca499f59da2c07dafe0a40545011
```

对应 JA4 是：

```text
t13d5211_00_9b003dc3eba7_ef824704554f
t13d5211_00_9b003dc3eba7_ef824704554f
t13d5211_00_9b003dc3eba7_ef824704554f
t13d5212_ht_9b003dc3eba7_4e5c652b160e
t13d5211_00_9b003dc3eba7_ef824704554f
t13d5211_00_9b003dc3eba7_ef824704554f
```

`ht` 变体的 ALPN advertised list 是 `h2` + `http/1.1`；`00` 变体未观察到 ALPN。模板将模型 API 变体放在顶层字段，并在 `tls_variants` 中保留两种变体，避免后续只靠单个 JA3 hash 做错误判定。

HTTP/2 SETTINGS 未从被动 TLS 抓包取得。`http2-settings.json` 明确说明 SETTINGS 帧在 TLS 记录层内加密；本次业务请求的 HTTP 层由 mitmproxy 观察为 HTTP/1.1，因此模板的 `http_layer.protocol` 写 `http1.1`。

## HTTP/1.1 模型 API

模型请求摘要文件 `/tmp/fingerprint-data/gemini-model-request-detail.txt` 给出主请求：

```text
POST cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse
Content-Type: application/json
User-Agent: GeminiCLI/0.41.2/gemini-3.1-pro-preview (linux; x64; terminal) google-api-nodejs-client/9.15.1
Authorization: Bearer <google_oauth_access_token>
x-goog-api-client: gl-node/22.22.2
Connection: close
```

模型 API 的 header 顺序必须保持为：

```text
Content-Type
User-Agent
Authorization
x-goog-api-client
Accept
Content-Length
Accept-Encoding
Host
Connection
```

模型 API 主请求的关键 header：

| Header | 值 |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `GeminiCLI/0.41.2/gemini-3.1-pro-preview (linux; x64; terminal) google-api-nodejs-client/9.15.1` |
| `Authorization` | `Bearer <google_oauth_access_token>` |
| `x-goog-api-client` | `gl-node/22.22.2` |
| `Accept` | `*/*` |
| `Accept-Encoding` | `gzip,deflate` |
| `Host` | `cloudcode-pa.googleapis.com` |
| `Connection` | `close` |

请求体是 `application/json`，顶层结构包括：

```text
model
project
user_prompt_id
request.contents
```

模板不保存真实 `project`、`user_prompt_id` 或 prompt 内容。对于 HUAKAI mimicry，`Content-Length` 必须按真实 body 动态计算，不应从样本硬编码。

## 辅助 Endpoint

同一次 JSONL 抓包中观察到的辅助 endpoint：

| Host | Path | 说明 |
|---|---|---|
| `cloudcode-pa.googleapis.com` | `/v1internal:loadCodeAssist` | Code Assist 初始化 |
| `cloudcode-pa.googleapis.com` | `/v1internal:retrieveUserQuota` | quota 查询 |
| `cloudcode-pa.googleapis.com` | `/v1internal:listExperiments` | 实验开关 |
| `cloudcode-pa.googleapis.com` | `/v1internal:fetchAdminControls` | 管控配置 |
| `oauth2.googleapis.com` | `/tokeninfo` | OAuth token introspection |
| `play.googleapis.com` | `/log?format=json&hasfast=true` | telemetry/log |

`cloudcode-pa.googleapis.com` 的辅助请求与模型 API 同样使用 Google OAuth Bearer 和 `x-goog-api-client: gl-node/22.22.2`，但 `Accept` 多数是 `application/json`。`play.googleapis.com` 日志链路的 header 栈不同，包含小写 `host/connection/accept/user-agent`，不应混入模型 API 模板。

## 鉴权机制

模型 API 鉴权：

```text
Authorization: Bearer <google_oauth_access_token>
```

本次抓包显示这是 Google OAuth access token，长度约 260 字符。Gemini CLI 的本地凭据结构由 Owner 提供：

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

`security.auth.selectedType` 支持 `vertex-ai`、`oauth-personal` 和 `gemini-api-key`。本次模型 API 抓包对应 `oauth-personal`，也就是 Login with Google 后保存的个人 OAuth token。

## HUAKAI Mimicry 实施建议

1. 顶层 TLS 模板使用模型 API 的 `ht` 变体：JA3 `55ba290366f110228d176d92fe6f6180`，JA4 `t13d5212_ht_9b003dc3eba7_4e5c652b160e`，ALPN advertised list 为 `h2,http/1.1`。
2. `tls_variants` 必须保留辅助 `00` 变体，因为 tokeninfo、quota、admin controls 等链路可能不带 ALPN。监控判定应允许这两个 Gemini CLI 变体并存。
3. TLS GREASE 写 `false`。本次样本未观察到 GREASE cipher、extension 或 supported group；不要把 `65281` 误判成 GREASE。
4. HTTP 层按 mitmproxy 实测写 `http1.1`。即使 TLS ALPN advertised `h2`，模型 API 的业务请求在本次解密抓包中仍是 HTTP/1.1。
5. Header 顺序和大小写要精确保留。Gemini 是 Title-Case 混合 `x-goog-api-client`，不能复用 Kiro 的全小写发送器。
6. `Authorization` 只注入与当前账号匹配的 Google OAuth access token。不要跨账号复用 token，不要把 API key 模式与 OAuth Bearer 模式混写。
7. `x-goog-api-client` 当前样本是 `gl-node/22.22.2`，应跟 Node.js runtime 版本绑定；Gemini CLI 或 Node 版本升级后必须重新抓包。
8. 模板不得保存真实 token、project、user_prompt_id、prompt 内容、主机名或本机路径。日志和审计输出只能显示 provider、账号 ID、token version 和脱敏摘要。

## Open Questions

1. `oauth-personal` refresh 请求没有在模型 API 主抓包中展开；HUAKAI 可按 Google OAuth refresh_token grant 实现，但需要单独用 Owner 账号验证 refresh 成功路径。
2. `vertex-ai` 与 `gemini-api-key` 两种模式的 header、endpoint 和凭据结构未在本次样本中观察；不能用本模板直接代表它们。
3. 如果未来 Gemini CLI 在无代理环境协商出 HTTP/2，需要另抓 SSLKEYLOGFILE 或 TLS 终止代理样本补 HTTP/2 SETTINGS 与 wire header 顺序。

## Source Coverage Proof

- `/tmp/fingerprint-data/gemini-tls/clienthello-template.json`：52 个 cipher suite、extensions、supported groups、signature algorithms、辅助 `00` 变体 JA3/JA4 代表样本。
- `/tmp/fingerprint-data/gemini-tls/ja3-hashes.txt`：6 个 JA3 样本；第 4 个为模型 API `ht` 变体，其余为辅助 `00` 变体。
- `/tmp/fingerprint-data/gemini-tls/ja4-hashes.txt`：两种 JA4 变体和共享 cipher hash `9b003dc3eba7`。
- `/tmp/fingerprint-data/gemini-tls/metadata.json`：目标 host、样本数、抓包时间、mitm check 结果。
- `/tmp/fingerprint-data/gemini-tls/http2-settings.json`：HTTP/2 SETTINGS 未捕获原因。
- `/tmp/fingerprint-data/gemini-model-request-detail.txt`：模型 API 主请求、9-header 顺序、UA、Bearer 摘要、body 形状。
- `/tmp/fingerprint-data/gemini-http-capture.jsonl:94`：`streamGenerateContent?alt=sse` 模型请求；`:95` 为 SSE 响应；`:92` 和 `:93` 为非流式 `generateContent` 请求/响应；`:1`、`:3`、`:5`、`:6`、`:9`、`:15` 为辅助链路样本。

Source files read: `/tmp/fingerprint-data/gemini-tls/clienthello-template.json`; `/tmp/fingerprint-data/gemini-tls/ja3-hashes.txt`; `/tmp/fingerprint-data/gemini-tls/ja4-hashes.txt`; `/tmp/fingerprint-data/gemini-tls/metadata.json`; `/tmp/fingerprint-data/gemini-tls/http2-settings.json`; `/tmp/fingerprint-data/gemini-model-request-detail.txt`; `/tmp/fingerprint-data/gemini-http-capture.jsonl`.

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-14T00:00:00Z

中文总结：本文件的真实观察包括 Gemini CLI 的 Node.js/OpenSSL TLS 栈、52 cipher 稳定列表、`ht`/`00` 两个 JA3/JA4 变体、HTTP/1.1 模型 API、9-header Title-Case 顺序、GeminiCLI UA、Google OAuth Bearer 鉴权、Code Assist API endpoint 与辅助 endpoint；合理推断有 4 项，主要是 HUAKAI 顶层模板应以模型 API `ht` 变体为准、辅助链路应允许 `00` 变体、GREASE=false、Node 版本升级需重抓；open question 有 3 个，分别是 OAuth refresh 实测、另外两种 Gemini auth 模式、以及未来 HTTP/2 SETTINGS 捕获。
