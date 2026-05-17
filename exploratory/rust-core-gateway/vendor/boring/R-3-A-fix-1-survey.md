# R-3-A-fix-1 boring extension ordering surface survey

日期: 2026-05-17
lane: HUAKAI codex implementer, vendor + survey only

## 结论

本轮只调研，不 patch。当前 boring 5.1.0 的 Rust API 已暴露
`set_permute_extensions(bool)`，但没有 caller-specified extension order API。
BoringSSL C header 也只看到 `SSL_CTX_set_permute_extensions` /
`SSL_set_permute_extensions`，没有 `SSL_CTX_set_extension_order` 或同类公开入口。

因此 R-3-A-fix-2 如果要按 HUAKAI profile 的 extension list 做 byte-level 控制，
大概率需要改 BoringSSL C 层，再由 boring-sys bindgen 暴露给 Rust。

## Vendor lock note

- `/home/codex/refs/boring` 当前 HEAD: `3921f35aa406c4cbff6efca9688f1fc9ead2508f`。
- 该 clone 没有本地 tag refs，且 `boring-sys/deps/boringssl` 是未初始化 submodule 空目录。
- 为满足“完整 vendored BoringSSL C source”，本 vendor tree 使用本机 crates.io cache:
  - `boring-5.1.0/.cargo_vcs_info.json`
  - `boring-sys-5.1.0/.cargo_vcs_info.json`
- crates.io package 记录的 VCS commit:
  `3acc9820eb7117f0b36078bf119c81c5ea337e6a`。

## Rust wrapper surface

- `boring/src/ssl/connector.rs:131` 定义 `SslConnectorBuilder`。
- `boring/src/ssl/connector.rs:142` 到 `connector.rs:153` 让
  `SslConnectorBuilder` deref 到 `SslContextBuilder`，所以后续只要在
  `SslContextBuilder` 增加方法，HUAKAI 的 connector builder path 就能直接调用。
- `boring/src/ssl/mod.rs:1964` 到 `mod.rs:1967` 已有 context-level
  `set_permute_extensions(bool)`，它只是转调 FFI。
- `boring/src/ssl/mod.rs:3070` 到 `mod.rs:3073` 已有 connection-level
  `set_permute_extensions(bool)`。
- `boring/src/ssl/ext.rs` 不存在；本轮 `rg --files boring/src/ssl` 只看到
  `connector.rs`、`mod.rs`、`ech.rs`、callbacks/test 等文件。

## BoringSSL public header surface

- `boring-sys/deps/boringssl/include/openssl/ssl.h:5230` 到 `ssl.h:5236`
  只暴露“是否随机排列 ClientHello extensions”的两个入口。
- 针对 `extension_order`、`set_extension_order`、`SSL_CTX_set_extension_order`
  的 grep 没有找到公开 header 符号。

## BoringSSL implementation surface

- `boring-sys/deps/boringssl/ssl/handshake_client.cc:218` 到
  `handshake_client.cc:231` 是客户端 ClientHello 生成路径：先写无 extension 的
  ClientHello body，再调用 `ssl_add_clienthello_tlsext(...)` 追加 extensions。
- `boring-sys/deps/boringssl/ssl/extensions.cc:429` 到 `extensions.cc:466`
  定义内部 extension handler 结构。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3526` 到 `extensions.cc:3527`
  开始定义 `kExtensions[]`。默认顺序就是这个内部表的顺序。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3732` 到 `extensions.cc:3739`
  由 `kNumExtensions` 和 sent/received bitset 绑定 extension table 尺寸。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3741` 到 `extensions.cc:3762`
  在 `permute_extensions` 开启时生成随机 permutation；没有外部指定顺序输入。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3928` 到 `extensions.cc:3934`
  写普通 ClientHello extension 时选择默认 table 顺序或随机 permutation 顺序。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3820` 到 `extensions.cc:3827`
  是 ClientHelloInner 的同类循环。
- `boring-sys/deps/boringssl/ssl/extensions.cc:3958` 到 `extensions.cc:4000`
  会在普通 ClientHello 中追加 padding extension。
- `boring-sys/deps/boringssl/ssl/extensions.cc:4005` 到 `extensions.cc:4008`
  强制 PSK extension 在最后。
- `boring-sys/deps/boringssl/ssl/ssl_lib.cc:3096` 到 `ssl_lib.cc:3104`
  实现现有 permute 开关，只写 bool state。

## boring-sys binding surface

- `boring-sys/src/lib.rs:24` 到 `lib.rs:38` include bindgen 生成的
  `bindings.rs` 并 `pub use generated::*`。
- `boring-sys/build/main.rs:721` 到 `main.rs:748` 通过 bindgen 读取 BoringSSL
  include path 生成 bindings；本轮没有看到针对 `SSL_CTX_set_*` 的 allowlist。
- 结论: 如果 R-3-A-fix-2 在 public header 增加新 C symbol，通常不需要手写大块
  Rust FFI；bindgen 会生成声明，Rust wrapper 只需要调用 `ffi::...`。

## R-3-A-fix-2 patch 建议

最小可行 surface:

1. 在 BoringSSL C 层增加一个 context-level order setter，例如
   `SSL_CTX_set_extension_order(ctx, types, len)`，输入为 extension type id 列表。
2. 在 `SSL_CTX` / shared config 里保存“显式 extension index order”。复制到 `SSL`
   config 的路径需跟现有 `permute_extensions` 一致。
3. setter 内把 type id 转成 `kExtensions[]` index；未知 type、重复 type 应返回失败，
   避免静默生成错误 wire image。
4. `ssl_add_clienthello_tlsext` 与 ClientHelloInner path 优先使用显式 order；未列出的
   internal extension 建议按 `kExtensions[]` 原顺序追加，防止漏掉 BoringSSL 必需 extension。
5. Rust 层在 `SslContextBuilder` 上加窄方法:
   `set_extension_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`。

边界提醒:

- GREASE extension 当前在主循环前后特殊插入，不在 `kExtensions[]`。
- padding extension 和 PSK extension 也在主循环之后特殊处理，PSK 被强制最后。
- 如果 HUAKAI 的 byte-level target 需要控制 GREASE / padding / PSK 的相对位置，
  单纯重排 `kExtensions[]` 不够，R-3-A-fix-2 需要升级为更深的 C 层 writer patch。
- ≤200 行 diff 的现实目标是先支持 “内部 extension table 顺序可指定，特殊 extension
  保持 BoringSSL 规则”，再用 CodexCli/Kiro/GeminiAdvanced wire tests 判定是否足够。

## License finding

- `boring/` package license: Apache-2.0。
- `boring-sys/` package license: MIT。
- `boring-sys/deps/boringssl/LICENSE` 随 package 完整 vendor，BoringSSL C subtree
  按该 license file 保留。
- 风险等级: LOW。主要风险是 Apache-2.0 / MIT notice 和后续本地 patch ledger 维护；
  当前 `NOTICE` 与 `MODIFICATIONS.md` 已记录来源、原因和本轮 zero modification 状态。
