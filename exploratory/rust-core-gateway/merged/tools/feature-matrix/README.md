# P1-6 feature-matrix CI 验证脚本

## 目的

`core_gateway` 默认 build 不含 mimicry feature。但 W11-F L1/L2 守门 + Boring TLS
profile dispatch + HTTP2 fork 的字节级 SETTINGS 顺序测试全部都在对应 feature 编入时
才会被编译/执行。如果 CI 只跑 default build, 这些守门代码可能在 mimicry feature 上线
时才发现已经被破坏 — production canary 见到 KnownGap 拒绝构造 connector, 网关启动
panic。

本脚本 `verify.sh` 强制在所有以下 feature 组合下都跑 `cargo test -p core_gateway`,
任一失败即整体 fail-fast, 让 CI 立刻红。

## 当前覆盖的 feature 组合

| 标签 | feature flag | 覆盖意图 |
|---|---|---|
| `default` | (无 feature) | 默认 build 路径; 不含 mimicry, 退到 hyper-rustls 普通 HTTPS。 |
| `mimicry-boring` | `--features mimicry-boring` | 生产 Boring TLS 字节级指纹路径; canary fail-fast 等同样在此 build 启用。 |
| `mimicry-openssl` | `--features mimicry-openssl` | OpenSSL exact adapter 路径; openssl_adapter 测试集只在此 feature 编入时存在。 |
| `mimicry-http2-fork` | `--features mimicry-http2-fork` | L2-A6 HTTP2 SETTINGS/pseudo-header 字节级顺序路径; 仅在此 feature 编入时启用。 |

## 用法

```bash
cd exploratory/rust-core-gateway/merged
bash tools/feature-matrix/verify.sh         # 跑全部 4 个组合
bash tools/feature-matrix/verify.sh quick   # PR 预审快速绿: 仅 default + boring
```

退出码 0 = 全绿; 非 0 = 至少一个 feature 组合 fail。CI 应将此脚本作为 release gate
强制项, 阻止有任一 feature 失败的 PR 合到主分支。

## 与 r-d-smoke 的关系

`tools/r-d-smoke/run.sh` 验证 mock vendor 端点的端到端 smoke (不含 feature 维度);
本脚本 `tools/feature-matrix/verify.sh` 专门验证 feature flag 组合维度。两者互补,
都在 release gate 列表里。

## 自验证 (CLAUDE.md #14)

`core_gateway` lib 测试 `feature_matrix_script_lists_all_required_feature_combinations`
(`src/lib.rs`) 解析本脚本文本, 强制其中必须含全部 4 个 cargo invocation。
mutation 删任一 feature 组合 -> 测试红, CI 漏覆盖 feature 立刻被发现。

## 不破坏

- 不改 `route.proto` / 不新增 dep / 不改主链路 / 不改 Go control plane。
- 默认 build 路径不受影响 (verify.sh 第一条仍是 default build)。
