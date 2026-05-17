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
