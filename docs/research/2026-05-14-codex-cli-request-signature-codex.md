# 2026-05-14 openai/codex CLI 请求签名源码分析

| 字段 | 值 |
|---|---|
| 任务 | R-3：分析 openai/codex CLI 的 HTTP 请求、header、鉴权、endpoint、TLS 证据 |
| 本次源码 | `openai/codex` shallow clone at `6a225e4005209f2325ab3c681c7c6beba2907d4d` |
| 0.128.0 tag | `rust-v0.128.0` deref at `e4310be51f617f5e60382038fa9cbf53a2429ca4` |
| License | Apache-2.0；根 `LICENSE` 是 Apache License 2.0，package metadata 也声明 Apache-2.0。`NOTICE` 另注明包含 Ratatui MIT 派生代码。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:LICENSE:1`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-cli/package.json:2`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-cli/package.json:4`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:118`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:125`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:NOTICE:1`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:NOTICE:4` |
| 版本口径 | 本次 HEAD 是 dev metadata：workspace `0.0.0`、npm package `0.0.0-dev`。用户指定的 `codex-cli 0.128.0` 对应 tag `rust-v0.128.0` 的 workspace version。2026-05-14 UTC 另用 `npm view @openai/codex version` 观察到 npm latest 为 `0.130.0`，因此“当前版本号”需要按 Owner pin 还是 npm latest 分开处理。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:118`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-cli/package.json:3`, `openai/codex@e4310be51f617f5e60382038fa9cbf53a2429ca4:codex-rs/Cargo.toml:111`, `openai/codex@e4310be51f617f5e60382038fa9cbf53a2429ca4:codex-rs/Cargo.toml:112` |
| Observed regions | 35 |
| Inferences | 6 |
| Open questions | 4 |

## 结论摘要

1. 主 HTTP transport 是 `reqwest` 0.12，经 Codex 自己的薄封装和 `ReqwestTransport` 发送；Responses API 主路径是 `POST /backend-api/codex/responses` 或 API-key 模式的 `POST /v1/responses`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:339`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/Cargo.toml:15`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/transport.rs:32`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/transport.rs:38`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:102`
2. 源码没有观察到显式 HTTP/2-only、HTTP/1-only、ALPN、cipher suite、HPACK、h2 SETTINGS 配置；这些由 `reqwest`/底层 TLS 与 HTTP 栈默认协商。要得到可用于封号风险控制的 wire-level HTTP/2 SETTINGS 和 header 编码顺序，必须用 0.128.0 目标平台二进制抓包复核。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:222`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:223`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:229`
3. `auth_mode=chatgpt` 使用 `tokens.access_token` 做 `Authorization: Bearer ...`，并在有 account id 时加 `ChatGPT-Account-ID`；FedRAMP 账号再加 `X-OpenAI-Fedramp: true`。没有在 ChatGPT token 路径观察到 HMAC 或额外签名；`AgentIdentity` 是独立 auth mode，才会生成签名式 Authorization。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:324`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:329`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:31`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:36`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:41`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:21`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:24`
4. TLS 不能从源码简单确认为“固定 OpenSSL”。普通 HTTP client 从 `reqwest::Client::builder()` 开始；workspace 同时启用 `reqwest` 默认特性和 `rustls-tls-native-roots`，自定义 CA 路径会强制 `rustls`。源码没有证据显示 cert pinning、自定义 cipher、自定义 ALPN。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:339`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/Cargo.toml:15`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:275`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:282`

## HTTP Client 与协议层

主路径的依赖关系是：workspace 级 `reqwest = 0.12`，`codex-client` 对它启用 `json`、`rustls-tls-native-roots`、`stream`，`codex-api` 也依赖 `reqwest` 的 `json`、`stream`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:339`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/Cargo.toml:15`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/Cargo.toml:18`

Codex 构造普通 HTTP client 时从 `reqwest::Client::builder()` 开始，先设置默认 headers；在 seatbelt sandbox 下禁用 proxy；再挂 ChatGPT Cloudflare cookie jar；最后进入自定义 CA 处理。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:222`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:223`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:224`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:227`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:229`

Codex 的 request builder 在发送前注入 OpenTelemetry 文本传播 headers；具体 header 名由进程安装的 propagator 决定，源码这里不是硬编码固定 header 列表。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:113`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:114`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:157`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:160`

源码可见的 HTTP protocol 配置结论：

| 项 | 观察 |
|---|---|
| HTTP crate | `reqwest` 封装；底层由 reqwest/hyper 族处理。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:16`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:45` |
| HTTP/2/HTTP/1.1 | 未观察到显式强制；由 TLS ALPN 和 reqwest 默认协商。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:222`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:229` |
| h2 SETTINGS | 未观察到源码显式配置。必须抓目标 binary。 |
| header wire 顺序 | 源码只能给出 HeaderMap 构造/extend 顺序；HTTP/2 实际编码顺序、pseudo-header 顺序、HPACK 动态表行为不是源码层稳定契约。 |

## TLS 与 Cookie

普通 HTTP 路径没有显式 cert pinning、自定义 cipher list 或自定义 ALPN。无自定义 CA 时，Codex 记录“使用系统 root certificates”并直接 build 传入的 reqwest builder。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:325`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:331`

当 `CODEX_CA_CERTIFICATE` 或 `SSL_CERT_FILE` 选择 CA bundle 时，HTTP client 路径调用 `use_rustls_tls()`，并把自定义 CA 注册进 reqwest root store；WebSocket sibling 会构造 rustls config，从 native roots 加载，再叠加 custom CA，且无 client auth。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:185`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:196`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:222`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:227`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:257`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:260`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:275`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/custom_ca.rs:282`

WebSocket 路径使用 `tokio-tungstenite`，启用 `rustls-tls-native-roots` feature；连接前确保 rustls provider，必要时带 custom CA connector。WebSocket config 显式启用 permessage-deflate。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:390`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/Cargo.toml:392`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:13`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:34`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:476`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:488`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:492`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:542`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:544`

Codex 有进程级 ChatGPT Cloudflare cookie jar，但只允许 Cloudflare cookie 名称，且只对 HTTPS ChatGPT host 生效；源码明确拒绝把 ChatGPT auth/session/account cookie 放入这个共享 jar。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:10`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:46`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:52`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:58`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:104`

## User-Agent 精确格式

默认 `originator` 是 `codex_cli_rs`，可被环境变量 `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` 或进程内设置覆盖；first-party originator 还包括 `codex-tui`、`codex_vscode` 以及以 `Codex ` 开头的值，ChatGPT 桌面/Atlas 有单独 first-party chat originator。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:36`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:57`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:122`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:129`

UA 格式是：

```text
{originator}/{CARGO_PKG_VERSION} ({os_type} {os_version}; {arch}) {terminal_user_agent}{optional_suffix}
```

其中 `{optional_suffix}` 在存在且非空时追加为 `" ({suffix})"`；最终字符串会做 ASCII/header-value 安全清洗。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:133`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:134`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:137`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:145`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:153`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:155`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:164`

对 Owner 指定的 `codex-cli 0.128.0`，如果无 originator override、无 suffix，则 UA 版本位应为：

```text
codex_cli_rs/0.128.0 (<OS> <OS_VERSION>; <ARCH>) <TERMINAL_TOKEN>
```

版本位来自 `CARGO_PKG_VERSION`；`rust-v0.128.0` 的 workspace package version 是 `0.128.0`。`openai/codex@e4310be51f617f5e60382038fa9cbf53a2429ca4:codex-rs/Cargo.toml:111`, `openai/codex@e4310be51f617f5e60382038fa9cbf53a2429ca4:codex-rs/Cargo.toml:112`

## Header 列表与构造顺序

下面是源码可观察的构造顺序。注意：这是 application 层 `HeaderMap` 插入/extend 顺序，不等价于 HTTP/2 wire 序、pseudo-header 序或 HPACK 编码序。

### 1. Client 默认 headers

| 顺序 | Header | 值格式 | 动态性 | 证据 |
|---:|---|---|---|---|
| 1 | `originator` | 默认 `codex_cli_rs`，可被 override | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:232`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:234` |
| 2 | `User-Agent` | 见上一节 | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:235`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:236` |
| 3 | `x-openai-internal-codex-residency` | 当前只观察到 `us` | 可选 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:238`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:243`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/default_client.rs:245` |

### 2. Built-in OpenAI provider headers

| Header | 值格式 | 动态性 | 证据 |
|---|---|---|---|
| `version` | `CARGO_PKG_VERSION`；0.128.0 tag 下为 `0.128.0` | 版本动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:329`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:330` |
| `OpenAI-Organization` | 环境变量 `OPENAI_ORGANIZATION` | API-key 场景可选 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:334`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:337`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:338` |
| `OpenAI-Project` | 环境变量 `OPENAI_PROJECT` | API-key 场景可选 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:340` |

Provider 先用 base URL 和 provider headers 构造 request，然后额外 headers extend 进去。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/provider.rs:77`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/provider.rs:80`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/provider.rs:81`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/session.rs:54`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/session.rs:55`

### 3. Responses HTTP request-specific headers

| 构造阶段 | Header | 值格式 | 动态性 | 证据 |
|---|---|---|---|---|
| Core shared responses headers | `x-codex-beta-features` | comma-separated enabled experimental feature keys | 可选 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/session/mod.rs:903`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/session/mod.rs:921`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1657`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1661` |
| Core shared responses headers | `x-codex-turn-state` | previous response/WS turn-state token | 可选动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:136`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1663`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1667` |
| Core shared responses headers | `x-codex-turn-metadata` | ASCII JSON，包含 session/thread/turn/sandbox/workspace/git metadata 等 | 可选动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:137`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1669`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1670`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/turn_metadata.rs:67`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/turn_metadata.rs:76`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/turn_metadata.rs:80` |
| Identity headers | `x-openai-subagent` | `review` / `compact` / `memory_consolidation` / `collab_spawn` / custom label | 可选动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:140`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:594`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:599`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1675`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1682` |
| Identity headers | `x-openai-memgen-request` | `true` | internal memory consolidation only | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:140`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:601`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:607` |
| Identity headers | `x-codex-parent-thread-id` | parent thread id | subagent thread spawn only | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:138`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:615`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:618`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1696`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1700` |
| Identity headers | `x-codex-window-id` | `{thread_id}:{generation}`（由 `current_window_id()` 产生） | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:139`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:613`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:620`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:621` |
| Endpoint layer | `x-client-request-id` | `thread_id` | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:90`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:92` |
| Endpoint layer | `session-id` | session id | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/requests/headers.rs:5`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/requests/headers.rs:8` |
| Endpoint layer | `thread-id` | thread id | 动态 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/requests/headers.rs:10`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/requests/headers.rs:11` |
| Stream request | `Accept` | `text/event-stream` | 固定于 HTTP streaming Responses | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:137`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:139` |
| Body prepare | `Content-Type` | `application/json` unless already present | body-dependent | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/request.rs:134`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/request.rs:137` |
| Body prepare | `Content-Encoding` | `zstd` | compression enabled only | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/request.rs:109`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/request.rs:114`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/request.rs:120` |

Responses HTTP 主请求的顺序大致是：provider headers -> core extra headers -> endpoint session/client headers -> stream Accept/compression/body headers -> auth headers -> reqwest/default headers 与自动 headers。这个顺序是源码构造路径合成，wire 仍需抓包确认。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/session.rs:90`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/session.rs:104`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/session.rs:145`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/transport.rs:65`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-client/src/default_client.rs:116`

### 4. WebSocket-specific headers

WebSocket handshake 合并 provider headers、extra headers 和 default headers；default headers 只在同名 header 缺失时补入。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:456`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:461`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:462`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses_websocket.rs:463`

WebSocket 额外包括：

| Header | 值格式 | 动态性 | 证据 |
|---|---|---|---|
| `OpenAI-Beta` | `responses_websockets=2026-02-06` | WebSocket only | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:134`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:146`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:911`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:913` |
| `x-responsesapi-include-timing-metrics` | `true` | optional config | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:142`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:915`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:918` |
| `x-codex-ws-stream-request-start-ms` | client_metadata field, not handshake header | per WS request payload | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:144`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1631`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1639` |

### 5. ChatGPT auxiliary GET headers

ChatGPT 辅助 GET 使用同一个 default client，URL 是 `config.chatgpt_base_url` 加传入 path，并要求 Codex backend auth 与 account id；额外加 `OAI-Product-Sku: codex` 与 `Content-Type: application/json`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:20`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:25`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:32`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:43`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:51`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:52`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:53`

Observed auxiliary paths include `/wham/tasks/{task_id}`, `/accounts/{account_id}/settings`, `/connectors/directory/list?external_logos=true`, paginated `/connectors/directory/list?token=...&external_logos=true`, and workspace connector list. `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/get_task.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/get_task.rs:38`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/workspace_settings.rs:117`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/workspace_settings.rs:120`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/connectors/src/lib.rs:201`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/connectors/src/lib.rs:212`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/connectors/src/lib.rs:214`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/connectors/src/lib.rs:240`

## 鉴权流程

`$CODEX_HOME/auth.json` 包含 `auth_mode`、`OPENAI_API_KEY`、`tokens`、`last_refresh`、`agent_identity`。`tokens` 内包含解析后的 ID token 信息、`access_token`、`refresh_token`、`account_id`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/storage.rs:31`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/storage.rs:35`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/storage.rs:41`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/storage.rs:44`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/token_data.rs:10`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/token_data.rs:20`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/token_data.rs:22`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/token_data.rs:24`

Auth load precedence observed:

1. API key env can win when enabled.
2. External ChatGPT tokens in ephemeral store are checked before persisted credentials.
3. Explicit ephemeral mode has no persisted fallback.
4. `CODEX_ACCESS_TOKEN` maps to `AgentIdentity`, not ordinary ChatGPT bearer mode.
5. Persistent store falls back to auth.json/keyring/auto mode.

`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:724`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:731`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:735`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:741`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:752`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:757`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:763`

ChatGPT auth 使用 Codex backend；`get_token()` 对 ChatGPT/ChatgptAuthTokens 返回 `tokens.access_token`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:292`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:295`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:324`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:329`

Bearer auth header 行为：

| Header | 值格式 | 条件 | 证据 |
|---|---|---|---|
| `Authorization` | `Bearer {access_token}` | ChatGPT / ChatgptAuthTokens / API-key provider bearer | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:106`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:112`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:33`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:36` |
| `ChatGPT-Account-ID` | token metadata/account id | account_id present | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:338`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:342`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:38`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:41` |
| `X-OpenAI-Fedramp` | `true` | FedRAMP account claim true | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:346`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:352`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:43`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/bearer_auth_provider.rs:44` |

Refresh 机制：

| 项 | 观察 | 证据 |
|---|---|---|
| refresh endpoint | 默认 `https://auth.openai.com/oauth/token`，可由 `CODEX_REFRESH_TOKEN_URL_OVERRIDE` 覆盖 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:94`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:96`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:923` |
| request body | JSON: client id、grant type `refresh_token`、refresh token | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:806`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:812`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:814`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:906` |
| client id | `app_EMoamEEZ73f0CkXaXp7hrann` | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:920`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:921` |
| refresh request headers | shared default client headers plus explicit `Content-Type: application/json` | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:820`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:822`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:823`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:824` |
| response persistence | returned id/access/refresh tokens overwrite stored token fields; `last_refresh` is set to now | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:780`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:791`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:795`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:798`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:801` |
| proactive trigger | ChatGPT auth only；access_token exp 到期，或 last_refresh 超过 8 天 | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:85`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1786`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1797`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1799`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1805` |
| 401 recovery | request path 有一次 refresh/retry 语义；外部 ChatGPT token 用 external refresh，普通 ChatGPT 用 stored refresh token | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1883`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:1885`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1707`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1720`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1724`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:1730` |

No-HMAC 结论：普通 ChatGPT token 模式只走 bearer provider。源码中签名式 Authorization 出现在 `AgentIdentity` provider，且 `AgentIdentity` 不暴露普通 bearer token；这是独立 auth mode，不是 `auth_mode=chatgpt`。`openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:21`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:24`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:39`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider/src/auth.rs:109`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:332`

## Endpoint 与 Path

| 场景 | Base URL | Path | Method | 证据 |
|---|---|---|---|---|
| ChatGPT Codex Responses | `https://chatgpt.com/backend-api/codex` | `responses` -> `/backend-api/codex/responses` | POST | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:37`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:236`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:241`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/provider.rs:53`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:102`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/codex-api/src/endpoint/responses.rs:132` |
| OpenAI API-key Responses | `https://api.openai.com/v1` | `responses` -> `/v1/responses` | POST | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:243`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:49`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/model-provider-info/src/lib.rs:53` |
| Compact | provider base | `/responses/compact` | POST | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:147`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:148`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:488` |
| Memory trace summarize | provider base | `/memories/trace_summarize` | POST | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:149`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/core/src/client.rs:588` |
| ChatGPT aux GET | default `https://chatgpt.com/backend-api/` | caller path | GET | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:93`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/chatgpt/src/chatgpt_client.rs:43` |
| Token refresh | `https://auth.openai.com` | `/oauth/token` | POST | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:94`, `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:822` |
| Token revoke | `https://auth.openai.com` | `/oauth/revoke` | not expanded here | `openai/codex@6a225e4005209f2325ab3c681c7c6beba2907d4d:codex-rs/login/src/auth/manager.rs:95` |

## HUAKAI Mimicry 实施建议

1. 必须 1:1 的语义层 headers：`originator`、`User-Agent` 格式、provider `version`、`Authorization` bearer、`ChatGPT-Account-ID`、`session-id`、`thread-id`、`x-client-request-id`、`x-codex-window-id`、HTTP streaming `Accept: text/event-stream`、JSON `Content-Type`。这些是 Codex backend 识别请求、账号、线程、窗口和流式协议的核心信号。
2. 条件性 1:1：`x-codex-turn-state` 只能回放后端返回的真实值；`x-codex-turn-metadata` 要按真实 session/thread/turn/sandbox/workspace 构造；`x-codex-beta-features` 只能发送实际启用功能；`OpenAI-Beta: responses_websockets=2026-02-06` 只在 WebSocket 路径发送；`X-OpenAI-Fedramp` 只在 token claims 表示 FedRAMP 时发送；`Content-Encoding: zstd` 只在请求体实际 zstd 压缩时发送。
3. 不建议伪造的 headers：`ChatGPT-Account-ID` 不应和 bearer token 不匹配；Cloudflare cookies 不应跨账号或跨 session 复用；`x-oai-attestation` 这类 attestation header 若无真实 attestation provider，不应编造。
4. header 顺序只能先按源码构造路径做候选：client default -> provider -> extra responses -> session/thread -> Accept/body -> auth -> tracing/auto headers。因为 reqwest/http `HeaderMap` 与 HTTP/2 编码层可能改写顺序，最终必须以同平台同版本 binary 抓包为准。
5. TLS/HTTP2 不能靠源码完成 mimic。源码没有自定义 h2 SETTINGS/cipher/ALPN；如果 Owner 已有 `0e0088de...` JA3 与 OpenSSL 证据，应继续以目标 release binary 的 packet capture 作为 transport fingerprint 真值。源码只能确认没有显式 pinning/handwritten TLS 参数，且 custom CA 路径会切到 rustls，不应用于复刻普通生产请求。
6. 版本风险：Owner 指定 `codex-cli 0.128.0`，但 2026-05-14 UTC npm latest 观察为 `0.130.0`，本次 HEAD metadata 又是 dev `0.0.0`。HUAKAI 若 pin 0.128.0，就必须使用 `rust-v0.128.0` 的 UA/version；若 mimic latest，应重新抓最新 binary 和 tag，而不是把 `0.128.0` 与 latest transport 混用。

## Open Questions

1. Exact HTTP/2 SETTINGS、pseudo-header 顺序、普通 header wire 顺序、HPACK dynamic table 行为：源码未观察到显式设置，必须抓包。
2. Exact TLS backend for npm Linux binary 0.128.0：源码同时出现 reqwest 默认 TLS 能力和 rustls feature/custom CA path，不能仅凭源码断言普通请求固定 OpenSSL。Owner 已抓到的 JA3/OpenSSL 证据应作为 binary-level 事实。
3. `x-oai-attestation` 的具体生成：本次只确认调用点，未展开 attestation provider，因为任务主轴是 HTTP/header/auth/endpoint/TLS；实际 mimic 不应伪造。
4. Latest release drift：2026-05-14 UTC npm latest 是 `0.130.0`，而用户要求写 `codex-cli 0.128.0`。如果后续要“今天最新版” mimic，需要另起一次按 `0.130.0` binary/tag 的复核。

## Source Coverage Proof

本次读过并用于结论的主要区域：

- `LICENSE`, `NOTICE`, `codex-cli/package.json`, `codex-rs/Cargo.toml`：license、版本、依赖、tokio-tungstenite feature。
- `rust-v0.128.0:codex-rs/Cargo.toml`：0.128.0 tag 的 workspace version/license。
- `codex-rs/codex-client/Cargo.toml`, `codex-rs/codex-api/Cargo.toml`, `codex-rs/login/Cargo.toml`：reqwest/tungstenite 依赖与 features。
- `codex-rs/login/src/auth/default_client.rs`：default client、originator、UA、default headers、reqwest builder。
- `codex-rs/codex-client/src/default_client.rs`：CodexHttpClient wrapper、trace header 注入。
- `codex-rs/codex-client/src/transport.rs`：ReqwestTransport 构造、send/stream。
- `codex-rs/codex-client/src/request.rs`：JSON body、Content-Type、zstd Content-Encoding。
- `codex-rs/codex-client/src/custom_ca.rs`, `codex-rs/utils/rustls-provider/src/lib.rs`：custom CA、rustls provider、TLS root behavior。
- `codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs`：Cloudflare cookie allowlist。
- `codex-rs/model-provider-info/src/lib.rs`：OpenAI/ChatGPT base URL、provider headers、Responses wire API。
- `codex-rs/codex-api/src/provider.rs`, `codex-rs/codex-api/src/endpoint/session.rs`, `codex-rs/codex-api/src/endpoint/responses.rs`, `codex-rs/codex-api/src/requests/headers.rs`：path 拼接、headers 合并、Responses request。
- `codex-rs/core/src/client.rs`, `codex-rs/core/src/session/mod.rs`, `codex-rs/core/src/turn_metadata.rs`：Codex-specific Responses headers、WebSocket beta header、turn metadata、401 recovery note。
- `codex-rs/codex-api/src/endpoint/responses_websocket.rs`：WebSocket connector/header merge/TLS/custom CA/permessage-deflate。
- `codex-rs/chatgpt/src/chatgpt_client.rs`, `codex-rs/chatgpt/src/get_task.rs`, `codex-rs/chatgpt/src/workspace_settings.rs`, `codex-rs/chatgpt/src/connectors.rs`, `codex-rs/connectors/src/lib.rs`：ChatGPT auxiliary GET paths and headers。
- `codex-rs/login/src/auth/storage.rs`, `codex-rs/login/src/token_data.rs`, `codex-rs/login/src/auth/manager.rs`：auth.json shape、token fields、refresh、load precedence。
- `codex-rs/model-provider/src/auth.rs`, `codex-rs/model-provider/src/bearer_auth_provider.rs`：Bearer headers、AgentIdentity separate signed auth path。

Source files read: `LICENSE`; `NOTICE`; `codex-cli/package.json`; `codex-rs/Cargo.toml`; `rust-v0.128.0:codex-rs/Cargo.toml`; `codex-rs/codex-client/Cargo.toml`; `codex-rs/codex-api/Cargo.toml`; `codex-rs/login/Cargo.toml`; `codex-rs/login/src/auth/default_client.rs`; `codex-rs/codex-client/src/default_client.rs`; `codex-rs/codex-client/src/transport.rs`; `codex-rs/codex-client/src/request.rs`; `codex-rs/codex-client/src/custom_ca.rs`; `codex-rs/utils/rustls-provider/src/lib.rs`; `codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs`; `codex-rs/model-provider-info/src/lib.rs`; `codex-rs/codex-api/src/provider.rs`; `codex-rs/codex-api/src/endpoint/session.rs`; `codex-rs/codex-api/src/endpoint/responses.rs`; `codex-rs/codex-api/src/endpoint/responses_websocket.rs`; `codex-rs/codex-api/src/requests/headers.rs`; `codex-rs/core/src/client.rs`; `codex-rs/core/src/session/mod.rs`; `codex-rs/core/src/turn_metadata.rs`; `codex-rs/chatgpt/src/chatgpt_client.rs`; `codex-rs/chatgpt/src/get_task.rs`; `codex-rs/chatgpt/src/workspace_settings.rs`; `codex-rs/chatgpt/src/connectors.rs`; `codex-rs/connectors/src/lib.rs`; `codex-rs/login/src/auth/storage.rs`; `codex-rs/login/src/token_data.rs`; `codex-rs/login/src/auth/manager.rs`; `codex-rs/model-provider/src/auth.rs`; `codex-rs/model-provider/src/bearer_auth_provider.rs`; `codex-rs/Cargo.lock` via targeted search only.

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-14T06:23:32Z

中文总结：本文件的真实观察包括 Apache-2.0 license、reqwest 主 HTTP 路径、ChatGPT Codex base/path、UA 拼接、headers 构造、Bearer+Account-ID 鉴权、refresh endpoint/触发条件、custom CA/rustls 路径和 WebSocket beta header；合理推断包括“无显式 h2 SETTINGS/cipher/ALPN 所以 wire fingerprint 由库与 binary 决定”“header 构造顺序不等于 HTTP/2 wire 顺序”等 6 项；仍有 4 个 open questions，最高优先级是用 0.128.0 目标平台二进制抓 HTTP/2 SETTINGS/header wire 顺序和 TLS backend，避免把源码层结论误当请求指纹真值。
