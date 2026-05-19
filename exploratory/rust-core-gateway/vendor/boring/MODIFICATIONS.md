# boring vendor 修改记录

vendored 自: cloudflare/boring crates.io package 5.1.0, VCS commit 3acc9820eb7117f0b36078bf119c81c5ea337e6a
vendored 时: 2026-05-17 by HUAKAI codex R-3-A-fix-1

## R-3-A-fix-1: vendor only

本轮只把 boring 5.1.0 的 `boring/` 与 `boring-sys/` source tree 复制进 HUAKAI
workspace，并补充 HUAKAI 自写的 `NOTICE`、`MODIFICATIONS.md` 和 surface survey。

本轮没有修改 `boring/` 或 `boring-sys/` 上游源码。

Source lock note:
- `/home/codex/refs/boring` 当前 HEAD 是 `3921f35aa406c4cbff6efca9688f1fc9ead2508f`，
  且没有本地 tag refs。
- 该 clone 的 `boring-sys/deps/boringssl` 是未初始化 submodule 空目录。
- 为满足本 wave “完整 vendored BoringSSL C source” 要求，本轮最终使用本机
  crates.io cache 中的 `boring-5.1.0` 和 `boring-sys-5.1.0` package tree；
  package 内 `.cargo_vcs_info.json` 记录的 VCS commit 是
  `3acc9820eb7117f0b36078bf119c81c5ea337e6a`。

License note:
- `boring/` declares Apache-2.0 and keeps its upstream `boring/LICENSE`.
- `boring-sys/` declares MIT and keeps its upstream `boring-sys/LICENSE-MIT`.
- `boring-sys/deps/boringssl/` keeps the BoringSSL license file shipped by the
  `boring-sys` 5.1.0 package.
- vendor 根目录的 `LICENSE` 是从 upstream `boring/LICENSE` 复制的 Apache-2.0 文本。

## R-3-A-fix-2: planned patch surface

预定下一轮再落最小 patch，目标是把 ClientHello extension ordering 暴露为
HUAKAI 可调用的窄 API。候选范围:

- `boring/src/ssl/mod.rs`: 在 `SslContextBuilder` 上增加公开方法，例如
  `set_extension_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`。
- `boring-sys/src/lib.rs`: 如果 bindgen 没有暴露所需 BoringSSL 符号，则补 FFI
  声明或通过极小 C shim 承接。
- `boring-sys/deps/boringssl/include/openssl/ssl.h`: 如果 BoringSSL vendored C
  层已经提供对应入口，则只绑定现有入口；如果没有，R-3-A-fix-2 需要 Owner
  重新确认 C 层 patch 方案。

R-3-A-fix-2 实施后必须继续追加本文件，记录实际文件、接口、原因和 Apache-2.0
attribution 状态。

## R-3-A-fix-2: 加 SSL_CTX_set_extension_order public API

本轮在 vendored BoringSSL C 层和 Rust wrapper 上增加一个 HUAKAI 本地窄 API，
用于控制 ClientHello 内部 extension table 的写入顺序。输入是 IANA extension
type id 列表；C 层会校验未知 type 和重复 type，并转换为 `kExtensions[]`
内部 index。未列出的内部 extension 会按 BoringSSL 默认表顺序追加，避免漏发必需
extension。GREASE、padding、PSK 保留 BoringSSL 原有特殊处理位置。

### boring-sys/deps/boringssl/

- `ssl/internal.h:2130`: 声明 HUAKAI 本地转换 helper
  `ssl_huakai_extension_order_from_types`，供 public setter 调用。
- `ssl/internal.h:3334`: 在 `SSL_CONFIG` 增加 `explicit_extension_order`
  per-connection copy，ClientHello writer 从这里读取实际顺序。
- `ssl/internal.h:3960`: 在 `SSL_CTX` 增加 `explicit_extension_order`，
  保存 context-level 配置。
- `include/openssl/ssl.h:5238`: 增加 `OPENSSL_EXPORT
  SSL_CTX_set_extension_order` 声明。该 symbol 是 HUAKAI local API，不是
  upstream BoringSSL API。
- `ssl/ssl_lib.cc:537`: `SSL_new` 复制 `SSL_CTX` 上的显式 extension 顺序到
  `SSL_CONFIG`，保持与现有 `permute_extensions` config copy 路径一致。
- `ssl/ssl_lib.cc:3102`: 实现 `SSL_CTX_set_extension_order`，处理空输入清除
  custom order、非空输入校验 null pointer 后转交 helper。
- `ssl/extensions.cc:3743`: 显式顺序存在时跳过随机 permutation 生成。
- `ssl/extensions.cc:3779`: 实现 type id 到 `kExtensions[]` index 的校验和
  补齐逻辑；GREASE/padding/PSK 跳过并保留特殊路径，未知 extension 返回
  `SSL_R_UNEXPECTED_EXTENSION`，重复 extension 返回 `SSL_R_DUPLICATE_EXTENSION`。
- `ssl/extensions.cc:3855`: ClientHelloInner extension 写入循环优先读取
  `explicit_extension_order`。
- `ssl/extensions.cc:3966`: 普通 ClientHello extension 写入循环优先读取
  `explicit_extension_order`。

### boring-sys/src/

- `src/lib.rs`: 无需手写 FFI；`build/main.rs` 通过 bindgen 从 public header
  生成 `SSL_CTX_set_extension_order` binding。
- `Cargo.lock:138`: 将既有 `libc` lock 调整到本机已缓存的 `0.2.186`，用于当前
  离线 `cargo check`；不新增依赖。

### boring/src/ssl/

- `mod.rs:1976`: 在 `SslContextBuilder` 增加
  `set_extension_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`，
  直接调用 bindgen 生成的 `ffi::SSL_CTX_set_extension_order`。

### Apache-2.0 §4 attribution

modification 改自 boring 5.1.0 + BoringSSL package tree，package VCS commit
`3acc9820eb7117f0b36078bf119c81c5ea337e6a`。新加 API
`SSL_CTX_set_extension_order` 不与 upstream 已有 API 冲突。patch 总 diff 当前低于
200 行。HUAKAI 维护本地 fork，不强求 upstream merge。

attribution: 修改人 HUAKAI codex executor lane (R-3-A-fix-2), 2026-05-17 UTC。

## R-3-A-fix-3: workspace 接入 vendored boring

- workspace `Cargo.toml` 将 `boring` 改为 vendored path dependency。
- `[patch.crates-io]` redirect `boring-sys` 到 vendored path，避免 `boring`
  内部版本依赖回落到 crates.io 未 patch 版本。
- `[patch.crates-io]` 同时 redirect `boring` 到 vendored path，避免 crates.io
  `tokio-boring` 的 transitive dependency 混入第二份 registry `boring`。
- `Cargo.lock` 同步为本地 path `boring` / `boring-sys`，不再记录 registry source
  和 checksum。
- `cargo check`、`cargo build` 和 `cargo test -p core_gateway --features
  mimicry-boring --lib` 已通过；测试结果为 105 passed / 3 ignored。

attribution: 修改人 HUAKAI codex executor lane (R-3-A-fix-3), 2026-05-17 UTC。

## R-3-A-fix-2-deeper: strict extension order + extension 22

R-3-A-fix-4 发现上一轮排序 API 仍会补齐未列出的 `kExtensions[]`，导致 Kiro 多发
65281；Codex/Gemini 的 extension 22 是 RFC 7366 `encrypt_then_mac`，不是 EMS(23)。

### boring-sys/deps/boringssl/

- `include/openssl/tls1.h:71`: 增加 `TLSEXT_TYPE_encrypt_then_mac`(22) 常量。
- `ssl/extensions.cc:828`: 增加 `ext_etm_add_clienthello`，仅在 HUAKAI strict
  order 下写入 RFC 7366 空扩展，默认 BoringSSL 路径不发 22。
- `ssl/extensions.cc:3568`: 将 22 加入 `kExtensions[]`，供 setter 校验和排序。
- `ssl/extensions.cc:3768` / `3804` / `3880` / `3994`: strict mode 下跳过
  permutation、只保留显式列出的内部扩展、ClientHello 写入不再补齐 65281。
- `ssl/internal.h:3399` / `4034` 和 `ssl/ssl_lib.cc:531` / `3114`: 增加并复制
  `has_explicit_order_strict_mode`，setter 非空输入启用 strict，空输入清除。
- `patches/boring-pq.patch`: 同步 hunk context，使既有 PQ patch 仍可叠加。

### Apache-2.0 §4 attribution
modification: HUAKAI codex executor lane (R-3-A-fix-2-deeper), 2026-05-17 UTC。
未新增非 boring 依赖，未修改 HUAKAI 主仓 LICENSE。

## R-3-A-fix-3-deeper: TLS 1.3 cipher order

本轮确认 extension order 已匹配 profile；Kiro 只差 TLS1.3 cipher suite 顺序，
Codex/Gemini 仍差 TLS1.2 cipher、group、EC point format，继续保留 diagnostic
failure，不伪 PASS。

- `include/openssl/ssl.h` / `ssl_lib.cc`: 增加 HUAKAI 本地
  `SSL_CTX_set_tls13_cipher_order`，校验 TLS1.3 AEAD cipher id 并拒绝重复值。
- `internal.h` / `handshake_client.cc`: 保存并复制显式 TLS1.3 cipher order；
  ClientHello 写入优先使用 profile 顺序，未设置时保留 BoringSSL 原逻辑。
- `boring/src/ssl/mod.rs`: 增加 Rust wrapper
  `set_tls13_cipher_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`。

modification: HUAKAI codex executor lane (R-3-A-fix-3-deeper), 2026-05-18 UTC。
未新增非 boring 依赖，未修改 HUAKAI 主仓 LICENSE。

## R-3-A-fix-4-deeper: ClientHello raw profile fields

本轮诊断确认 Codex/Gemini extension order 与 supported_versions 已匹配，剩余
JA3 mismatch 来自 TLS1.2 cipher advertisement、supported_groups 和
ec_point_formats。新增 HUAKAI 本地 `SSL_CTX_set_client_hello_profile`，只控制
ClientHello wire advertisement；未设置时保留 BoringSSL 默认路径。

- `include/openssl/ssl.h` / `ssl_lib.cc`: 增加 profile setter，复制 raw cipher
  order、raw supported groups、EC point formats。
- `internal.h` / `handshake_client.cc` / `extensions.cc`: 保存并写出显式 raw
  cipher order 与 EC point formats；renegotiation/fallback SCSV 不从 profile
  raw cipher list 写入。
- `boring/src/ssl/mod.rs`: 增加 Rust wrapper
  `set_client_hello_profile(&mut self, ciphers, groups, ec_points)`。
- `boring-sys/build/main.rs`: 追加本地修改文件的 `rerun-if-changed`，避免旧
  test-profile build output 未感知 vendored header/source 变更。

modification: HUAKAI codex executor lane (R-3-A-fix-4-deeper), 2026-05-18 UTC。
未新增非 boring 依赖，未修改 HUAKAI 主仓 LICENSE。

## R-3-A-fix-5-deeper: profile setter hardening
`ssl_lib.cc`: staged commit、cipher 去重、EC 0/1/2 校验；`extensions.cc`: strict profile 已带 GREASE group 时跳过默认 GREASE；`ssl.h` / `ssl.errordata`: 本地 reason。
modification: HUAKAI codex executor lane (R-3-A-fix-5-deeper), 2026-05-18 UTC。
未新增非 boring 依赖，未修改 HUAKAI 主仓 LICENSE。
