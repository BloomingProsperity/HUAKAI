# Fingerprint Template Schema

本文档定义 `tools/fingerprint-collector/templates/*.json` 的提交格式。
模板目标不是描述一个抽象客户端，而是保存可复刻的一组真实请求指纹。
R-3 以后，一个可上线模板必须覆盖 TLS、HTTP 和鉴权三层。

## 顶层元数据

`_comment` 是给人工阅读的说明。JSON 不支持真正注释，所以不要写
`//` 或 `/* */`。

`_field_sources` 是字段来源索引。常用值：

- `capture`：来自本项目 collector、pcap、mitmproxy 或 Owner 自有流量抓包。
- `source-analysis`：来自已记录的源码分析文档。
- `manual`：人工补充的协议事实，必须在同文件或研究文档说明原因。

`mode_name` 是模板逻辑名，例如 `openai_codex_cli` 或 `kiro_cli`。

`collected_at` 是用于模板的主样本时间，UTC RFC3339 字符串。

`target_host` 是实际业务 endpoint host，不一定等于 TLS 捕获过滤 host。

`capture_target_host` 可选，用来记录 collector 当时过滤到的 host。

`sample_count` 是参与归纳的 TLS ClientHello 样本数。

## TLS 层字段

`tls_backend` 描述真实客户端 TLS 栈。例：

- `native-tls/openssl`
- `rustls`

`grease` 表示是否观察到 GREASE/反指纹随机化行为。`false` 也要显式写出。

`extension_order` 描述扩展顺序稳定性：

- `stable`：样本之间扩展顺序一致。
- `randomized`：样本之间扩展顺序会变化，通常不要用单个 JA3 hash 当唯一身份。
- `unknown`：尚未抓到足够样本。

`ja3` 保存 JA3 input string，不保存 hash。Go loader 用它解析 TLS version、
cipher、extension 和 supported group。

`ja3_hash` 保存 JA3 MD5 hash，便于人工核对。

`ja3_hash_samples` 可选，保存多样本 hash。随机化客户端应保留该字段。

`ja4` 保存代表样本的 JA4 字符串。

`ja4_stable_prefix` 可选，用于 JA4 后段会变但前段稳定的客户端。

`ja4_samples` 可选，保存所有样本的 JA4。

`cipher_suites` 是 ClientHello cipher suite 有序列表。若真实 ClientHello
包含 SCSV 值，也应保留。

`extensions` 是 ClientHello extension type 有序列表。随机化客户端记录代表样本，
同时用 `extension_order=randomized` 标明不可固定。

`supported_versions` 是 TLS supported_versions 扩展中的协议版本值。

`curves` 是 loader 当前使用的字段名，语义等同于 TLS supported_groups。

`supported_groups` 是面向人工和后续 schema 的别名，应与 `curves` 保持一致。

`sig_algos` 是 loader 当前使用的签名算法字段名。

`signature_algorithms` 是面向人工和后续 schema 的别名，应与 `sig_algos` 保持一致。

`alpn_protocols` 是 TLS ALPN 列表。被动抓包无法解密时可以为空数组，但必须在
`h2_settings.limitation_note` 说明限制。

`ec_point_formats` 是 EC point format 列表。collector 原始输出若是 base64，
提交模板应转换成数字数组。

`key_share_groups` 是 key_share 扩展的 group 顺序。后量子 group 必须保留。

`psk_modes` 是 PSK key exchange modes。

`padding_len` 是 padding extension 长度。无 padding 时写 `0`。

`early_data_enabled` 表示是否观察到 TLS early data。

`h2_settings` 保存 HTTP/2 SETTINGS 捕获情况：

- `available`：是否有真实 SETTINGS 数据。
- `settings`：可选，真实 SETTINGS 列表。
- `limitation_note`：未捕获时必须说明原因。

`h2_settings_frame` 保存可复核的初始 SETTINGS frame wire 信息：

- `available`：是否有真实 frame 数据。
- `raw_order`：按 wire 顺序记录 setting identifier 数字。
- `values`：setting identifier 到 32-bit value 的映射。
- `source` / `limitation_note`：记录抓包来源或不可用原因。

`h2_pseudo_header_order` 保存 request pseudo-header wire 顺序：

- `available`：是否有真实 HEADERS/HPACK 数据。
- `order`：例如 `:method`、`:authority`、`:scheme`、`:path`。
- `source` / `limitation_note`：记录抓包来源或不可用原因。

## HTTP 层字段

`http_layer.protocol` 是模型 API 的业务 HTTP 协议，例如 `http1.1`、
`h2` 或 `h2_or_http1.1_reqwest_default`。

`http_layer.endpoint` 是模型 API 完整 endpoint。

`http_layer.method` 是 HTTP method。

`http_layer.user_agent` 是 User-Agent 模板或真实固定值。动态片段使用尖括号占位。

`http_layer.header_order` 是请求 header 顺序，并保留真实大小写。若只能从源码得到
构造顺序，必须在 `source_note` 中写明不是 wire 顺序。

`http_layer.auth_mechanism` 是业务请求使用的鉴权摘要。

`http_layer.refresh_endpoint` 是 token refresh endpoint。没有观察到时写空字符串，
不要编造 URL。

`http_layer.x_amz_target`、`content_type`、`x_amz_user_agent` 等字段是具体客户端
的可选协议字段，可按需要扩展；loader 会忽略未知字段。

## Auth 层字段

`auth_layer.mechanism` 是鉴权类别，例如 `bearer_chatgpt` 或 `bearer_builder_id`。

`auth_layer.authorization_header` 必须脱敏，格式用 `<access_token>` 或
`<builder_id_token>` 占位。

`auth_layer.account_header` 可选，记录账号绑定 header。

`auth_layer.conditional_headers` 可选，记录只在特定账号或模式下发送的 header。

`auth_layer.refresh_endpoint` 可选，但如果 HTTP 层已有 refresh endpoint，应保持一致。

`auth_layer.telemetry_mechanism` 可选，用于记录模型 API 之外的遥测鉴权差异。

`auth_layer.token_source` 说明 token 来源，不得包含真实 secret。

## Stub 与 Real

stub 模板表示还没有真实 TLS 指纹。当前 loader 判定规则是：

`ja3` 为空就是 stub。

real 模板必须至少有非空 `ja3`、`ja4`、`cipher_suites`、`extensions`、
`supported_versions`、`curves` 和 `sig_algos`。

`gemini-advanced.json` 在 Owner 后续抓包前仍是 stub。不要为了通过测试而填假值。

真实模板可以存在 open question，例如 HTTP/2 SETTINGS 未抓到，但必须把限制写在
`h2_settings` 或研究文档里。

## Clean-room 要求

模板只记录行为、字段值和协议事实，不复制参考项目源码。

来源是非 MIT 参考项目时，只能写行为摘要和 citation，不能复制函数名、结构体字段、
注释、schema 或独特实现顺序。

本次 codex 模板使用 Apache-2.0 源码分析文档和 Owner TLS 抓包。

本次 kiro 模板使用 Owner 自己账号的 mitmproxy 解密流量和 TLS 抓包。

真实 token、账号 ID、profile ARN、主机名、IP、MAC 不得进入模板。

## 兼容性

Go loader 对未知 JSON 字段保持忽略，因此模板可以先携带更多人工字段。

旧 stub 不含 `http_layer`、`auth_layer`、`tls_backend` 或 `grease` 时不能报错。

新增字段进入运行时代码前，必须补充 loader 测试，确认 real 与 stub 判定不回退。
