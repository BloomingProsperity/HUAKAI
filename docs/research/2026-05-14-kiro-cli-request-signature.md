# 2026-05-14 kiro CLI 请求签名抓包分析

| 字段 | 值 |
|---|---|
| 任务 | R-3：记录 kiro CLI 的 TLS、HTTP/1.1、header 顺序、User-Agent 和鉴权机制 |
| 数据来源 | Owner 自己账号、自己机器上的 mitmproxy 解密流量与 TLS ClientHello 抓包 |
| 合规边界 | 协议逆向分析；不读取、复制或翻译 Kiro 非公开实现源码；真实 token 已脱敏 |
| HTTP 抓包 | `/tmp/fingerprint-data/kiro-http-capture.jsonl`，60 行事件，30 个 request + 30 个 response |
| TLS 抓包 | `/tmp/fingerprint-data/kiro-tls/clienthello-template.json`，6 个 ClientHello 样本 |
| Observed regions | 7 |
| Inferences | 3 |
| Open questions | 2 |

## 结论摘要

1. kiro 模型 API 主请求是 `POST https://q.us-east-1.amazonaws.com/`，协议是 HTTP/1.1，核心操作名是 `AmazonCodeWhispererStreamingService.GenerateAssistantResponse`。证据来自模型请求摘要和 JSONL 第 13/53 行同类请求。
2. 模型 API 使用 AWS SDK Rust 的 CodeWhisperer Streaming UA：`aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/linux lang/rust/1.92.0 md/appVersion-2.3.0 app/AmazonQ-For-CLI`。
3. 模型 API 鉴权是 `authorization: Bearer <Builder ID token>`，抓包中 token 长度约 232 字符；模板和文档只保留占位。
4. 遥测链路不同：`client-telemetry.us-east-1.amazonaws.com/metrics` 使用 `AWS4-HMAC-SHA256` SigV4，并带 `x-amz-date` 与 `x-amz-security-token`；不要把遥测 SigV4 套到模型 API。
5. TLS 层是 rustls 指纹，扩展顺序在 6 个样本间随机化，JA3 每次变化是预期反指纹行为；JA4 前两段稳定为 `t13d0910_00_5a0d15427bfb`。

## TLS ClientHello

TLS 捕获目录是 `/tmp/fingerprint-data/kiro-tls/`。

主模板样本：

- `sample_count`: 6
- `target_host`: `q.us-east-1.amazonaws.com`
- `tls_backend`: rustls
- `grease`: true
- `extension_order`: randomized

代表样本的 cipher suite 顺序是：

```text
4866,4865,4867,49196,49195,52393,49200,49199,52392,255
```

代表样本的 extension 顺序是：

```text
10,43,51,0,45,11,5,35,23,13
```

6 个 JA3 hash 分别是：

```text
ed5338278fb7f0fb5cfd4ad58a98241f
5dac1d44bcb356eec78de1ee97d7c929
8423a9e17c183e9131b51f718b805651
d156d79cbfb609e9205c3b6460f48d9f
75cba41e90322d5824606be0780f3a13
323fe048d7a4c9afa70dbdb4da66ad9d
```

这说明不能把单个 JA3 hash 当成 kiro 的稳定身份。HUAKAI 模板必须记录
`extension_order=randomized`，运行时也不能强行固定 rustls 0.23+ 的扩展顺序。

JA4 样本是：

```text
t13d0910_00_5a0d15427bfb_ac698dc7b72d
t13d0910_00_5a0d15427bfb_64a48fab2835
t13d0910_00_5a0d15427bfb_ac698dc7b72d
t13d0910_00_5a0d15427bfb_64a48fab2835
t13d0910_00_5a0d15427bfb_64a48fab2835
t13d0910_00_5a0d15427bfb_64a48fab2835
```

稳定部分是 `t13d0910_00_5a0d15427bfb`。第三段随扩展/签名等细节变化。

supported groups 是：

```text
4588,29,23,24
```

其中 `4588` 是 X25519MLKEM768 后量子 group。signature algorithms 是：

```text
1283,1027,1539,2055,2054,2053,2052,1537,1281,1025
```

## HTTP/1.1 模型 API

模型请求摘要文件 `/tmp/fingerprint-data/kiro-model-request-detail.txt` 给出主请求：

```text
POST q.us-east-1.amazonaws.com/
x-amz-target: AmazonCodeWhispererStreamingService.GenerateAssistantResponse
content-type: application/x-amz-json-1.0
```

JSONL 第 13 行和第 53 行显示同一模型 API 请求形状，HTTP version 都是
`HTTP/1.1`，method 是 `POST`，host 是 `q.us-east-1.amazonaws.com`，path 是 `/`。

固定 header 顺序是：

```text
content-type
x-amz-target
content-length
user-agent
x-amz-user-agent
x-amzn-codewhisperer-optout
authorization
amz-sdk-request
amz-sdk-invocation-id
accept
accept-encoding
host
```

模型 API 的主要 header 值：

| Header | 值 |
|---|---|
| `content-type` | `application/x-amz-json-1.0` |
| `x-amz-target` | `AmazonCodeWhispererStreamingService.GenerateAssistantResponse` |
| `user-agent` | `aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/linux lang/rust/1.92.0 md/appVersion-2.3.0 app/AmazonQ-For-CLI` |
| `x-amz-user-agent` | `aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/linux lang/rust/1.92.0 m/F app/AmazonQ-For-CLI` |
| `x-amzn-codewhisperer-optout` | `false` |
| `authorization` | `Bearer <Builder ID token>` |
| `amz-sdk-request` | `attempt=1; max=3` |
| `accept` | `*/*` |
| `accept-encoding` | `gzip` |

`amz-sdk-invocation-id` 是每次请求动态 UUID。`content-length` 随请求体变化。

同一 host 下还观察到 `ListAvailableModels` 与 `SendTelemetryEvent` 操作。它们仍使用
Bearer token 和类似 AWS SDK Rust header 栈，但 `x-amz-target` 与 UA 的 API 名不同；
模板主目标应以 `GenerateAssistantResponse` 为准。

## 鉴权机制

模型 API 鉴权：

```text
authorization: Bearer <Builder ID token>
```

抓包中 token 是 Builder ID 会话 token，长度约 232 字符。模板不得保存真实 token，
也不得把 token 与其他账号或 profile 混用。

遥测 API 鉴权：

```text
authorization: AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
x-amz-date: <timestamp>
x-amz-security-token: <session token>
```

遥测 endpoint 是 `client-telemetry.us-east-1.amazonaws.com/metrics`。它使用 SigV4，
和模型 API 的 Bearer 不同。HUAKAI mimicry 在模型 API 上只需要 Bearer；如果后续要
复刻遥测，则必须另建 telemetry template，不能把两套机制混写。

## HUAKAI Mimicry 实施建议

1. TLS 层使用 rustls 模式，保留 X25519MLKEM768、cipher 列表、supported versions、
   supported groups 和 signature algorithms。
2. 对 kiro 不要使用固定 JA3 判等。监控和回归测试应优先检查 JA4 稳定前缀、
   cipher suite 集合、supported groups 和 header 层。
3. HTTP 层必须保持 HTTP/1.1，不要强制 h2。抓包没有显示模型 API 使用 HTTP/2。
4. Header 名全部按抓包小写写出，顺序必须按 12-header 列表发送。
5. `authorization` 只放 Builder ID Bearer token；SigV4 只属于遥测链路。
6. `x-amz-target` 是模型操作名，不是普通 REST path。endpoint path 是 `/`。
7. `amz-sdk-invocation-id`、`content-length` 和 Bearer token 都是动态字段，模板只保存
   位置、名称和格式。
8. 如果后续升级 Amazon Q/Kiro CLI 版本，必须重新抓 TLS 与 HTTP 层；AWS SDK Rust
   UA 中的 SDK 版本、API 版本和 appVersion 都可能变化。

## Open Questions

1. Builder ID token refresh 的具体 endpoint 未在模型 API 主请求摘要中展开；本次模板
   不编造 refresh endpoint。
2. rustls 扩展随机化的精确版本边界未从源码确认；本次只记录 Owner 抓到的 wire 行为。

## Source Coverage Proof

- `/tmp/fingerprint-data/kiro-tls/clienthello-template.json`：cipher suites、extensions、
  supported groups、signature algorithms、JA3/JA4 代表样本。
- `/tmp/fingerprint-data/kiro-tls/ja3-hashes.txt`：6 个 JA3 hash 均不同，支撑扩展顺序随机化结论。
- `/tmp/fingerprint-data/kiro-tls/ja4-hashes.txt`：JA4 前两段稳定，第三段在两种值之间变化。
- `/tmp/fingerprint-data/kiro-tls/metadata.json`：目标 host、样本数、抓包时间。
- `/tmp/fingerprint-data/kiro-model-request-detail.txt`：模型 API 主请求、12-header 顺序、UA、x-amz-target、Bearer 摘要。
- `/tmp/fingerprint-data/kiro-http-capture.jsonl:13`：一次 `GenerateAssistantResponse` 模型请求及响应。
- `/tmp/fingerprint-data/kiro-http-capture.jsonl:53`：第二轮同形状 `GenerateAssistantResponse` 请求及响应。

Source files read: `/tmp/fingerprint-data/kiro-tls/clienthello-template.json`; `/tmp/fingerprint-data/kiro-tls/ja3-hashes.txt`; `/tmp/fingerprint-data/kiro-tls/ja4-hashes.txt`; `/tmp/fingerprint-data/kiro-tls/metadata.json`; `/tmp/fingerprint-data/kiro-model-request-detail.txt`; `/tmp/fingerprint-data/kiro-http-capture.jsonl`.

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-14T00:00:00Z

中文总结：本文件的真实观察包括 kiro TLS/rustls 指纹、JA3 随扩展顺序随机化、JA4 稳定前缀、HTTP/1.1 模型 API、12 个 header 的固定顺序、AWS SDK Rust UA、模型 API Builder ID Bearer 鉴权，以及遥测链路 SigV4 的差异；合理推断有 3 项，主要是 HUAKAI 不应固定 JA3、不应把 SigV4 用到模型 API、升级版本必须重抓；open question 有 2 个，分别是 Builder ID refresh endpoint 和 rustls 随机化的版本边界。
