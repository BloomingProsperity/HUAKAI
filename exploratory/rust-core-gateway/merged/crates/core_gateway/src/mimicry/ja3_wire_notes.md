# HUAKAI JA3 wire notes

- Clean-room 边界: 本记录只来自 salesforce/ja3 README、FoxIO JA4 README、IETF RFC、IANA TLS registry、docs.rs boring rustdoc。
- 禁止输入: rquest / curl_cffi / wreq / utls / chrome-impersonate / ja3er / boring crate source / BoringSSL C source。

## JA3

- JA3 输入来自 TLS ClientHello 的五段十进制值: TLS version, cipher suites, extension types, supported groups, ec point formats。
- 字段间用 `,`，字段内用 `-`；空字段保留空段。
- JA3 对上面的字符串做 MD5，输出 32 字符 lowercase hex。
- GREASE 值按 RFC 8701 形态跳过: `0x0a0a`, `0x1a1a`, ... `0xfafa`。
- HUAKAI profile 中 `tls.ja3` 是 collector 已采样的 canonical 五元组；`tls.extensions` 是 wire 侧完整列表。
- Anthropic sample: `de88744b20558d50f03a5f0ea176ee98`。

## JA4

- JA4 README 将 JA4 定义为 TLS client fingerprint，格式由多个下划线分段组成，便于局部检索。
- FoxIO README 说明 JA4 TLS Client Fingerprinting 是 BSD-3-Clause；其他 JA4+ 方法有单独许可。
- R-2-B-2 只实现 JA3 hash 与 wire layout，不实现 JA4 计算。

## TLS / ALPN

- TLS 1.2 ClientHello 布局含 `client_version`, random, session id, cipher suites, compression methods, extensions。
- TLS 1.3 仍保留 ClientHello legacy_version；真实版本集合在 supported_versions extension 内表达。
- ALPN extension type 是 16；wire format 是每个协议名前置 1 byte 长度，协议顺序即偏好顺序。

## boring public API

- `SslConnector::builder(SslMethod::tls())` 创建 client connector builder。
- `SslConnectorBuilder` deref 到 `SslContextBuilder`。
- 公开 setter:
  - `set_cipher_list(&str)` 配置 TLS 1.3 之前的 cipher list。
  - BoringSSL 不实现 OpenSSL 的 TLS 1.3 `set_ciphersuites`。
  - `set_curves_list(&str)` 配置 supported groups / curves。
  - `set_sigalgs_list(&str)` 配置 signature algorithms。
  - `set_alpn_protos(&[u8])` 使用 ALPN wire format。
  - `set_min_proto_version` / `set_max_proto_version` 配置协议上下限。
  - `set_grease_enabled` / `set_permute_extensions` 可控制 GREASE 和 extension permutation。

## IANA lookup

- Cipher suite、supported group、signature scheme 数值来自 IANA TLS Parameters。
- HUAKAI lookup table 只覆盖 profile 当前需要和常见兼容项；未知值返回显式错误，不猜默认值。
