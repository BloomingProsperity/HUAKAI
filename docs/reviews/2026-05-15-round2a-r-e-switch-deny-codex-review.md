## scope

Reviewer lane：只审 HUAKAI 当前工作区里的 Round 2-A R-E transport baseline switch 与 cargo-deny / dependency-policy 变更；未读取 sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway 等参考项目源码。

实际读取范围：
- `git diff`：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`
- 文件内容：`exploratory/rust-core-gateway/merged/deny.toml`、`docs/dependency-policy.md`、`docs/plans/2026-05-15-r-e-transport-baseline-switch-codex.md`、`docs/runbooks/r-e-transport-baseline-switch.md`、`exploratory/rust-core-gateway/merged/config/route_client_config.toml`
- 证书 fixture 列表：`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/fixtures/mtls/`
- 额外为了验证风险映射和工具行为读取：`docs/10_RISK_REGISTER.md`、`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`、`exploratory/rust-core-gateway/merged/Cargo.lock`、本地 cargo registry 的 `tonic-0.12.3` API 定义。

注意：`git status --short` 显示当前这些变更是工作区修改 / 未跟踪文件，不是 index staged 状态；本报告按 Owner 指定的工作区 diff 和文件内容评审。

## findings (HIGH/MED/LOW)

### HIGH

1. `cargo deny check` 对当前 workspace 实跑失败，R-LIC-003 / dependency-policy gate 不能算已收敛。

   证据：`deny.toml` 使用 allow-list，仅允许 MIT / Apache / BSD / ISC / Unicode-3.0 / CC0 / Zlib / MPL-2.0 及 `Apache-2.0 WITH LLVM-exception`，未列 license 会被拒绝：`exploratory/rust-core-gateway/merged/deny.toml:8`-`24`。当前 runtime 依赖 `hyper-rustls` 明确启用 `webpki-roots` feature：`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:31`，lockfile 中 `hyper-rustls` 依赖 `webpki-roots`：`exploratory/rust-core-gateway/merged/Cargo.lock:683`-`697`，`webpki-roots 1.0.7` 存在于 lockfile：`exploratory/rust-core-gateway/merged/Cargo.lock:2431`-`2438`。本机 `cargo deny check` 使用 `cargo-deny 0.19.6` 返回 exit 5，拒绝 `webpki-roots 1.0.7` 的 `CDLA-Permissive-2.0`，因为该 license 未在 allow-list 中。

   同一次 `cargo deny check` 还因 `protobuf 2.28.0` 的 RustSec advisory 失败。lockfile 显示 `prometheus 0.13.4` 依赖 `protobuf`：`exploratory/rust-core-gateway/merged/Cargo.lock:1261`-`1273`，并锁定 `protobuf 2.28.0`：`exploratory/rust-core-gateway/merged/Cargo.lock:1328`-`1332`。`deny.toml` 启用了 advisories 检查且没有 ignore：`exploratory/rust-core-gateway/merged/deny.toml:1`-`6`。

   影响：这不是网络失败，而是实质 policy failure。`docs/dependency-policy.md` 要求新增 runtime dependency 前运行 `cargo deny check`，且 license 风险不能通过功能删除处理：`docs/dependency-policy.md:3`、`docs/dependency-policy.md:13`。因此 Round 2-A 不能以 “deny gate 已通过” 状态落地。修复方向需要 Owner 二选一或制定明确迁移计划：移除 / 替换触发 `CDLA-Permissive-2.0` 的依赖路径，或显式批准该 license 加入 HUAKAI policy；同时处理或显式治理 `protobuf 2.28.0` advisory。

### MED

1. UDS 默认路径的真实 gRPC 请求链路还没有 end-to-end 覆盖。

   证据：runbook 把 personal / single-host 默认写为 UDS：`docs/runbooks/r-e-transport-baseline-switch.md:3`，config 默认也落在 `uds`：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:70`-`73`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:352`-`356`。实现已有 UDS connector：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:159`-`172`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:457`-`472`，但测试只验证 endpoint parts 从 fake socket path 构造成功，并未启动 UDS gRPC server 后执行 `route_query` / `heartbeat`：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:529`-`543`。

   影响：当前 `cargo test` 通过，说明代码可编译且 helper 行为符合现有断言；但在把默认 UDS 真正接入 startup wiring 前，还缺一条 UDS loopback integration test，防止 personal edition 默认配置在真实 route RPC 上失败。建议在 R-E wiring 前补，不需要阻塞本次 config-shape patch 本身。

2. mTLS baseline 目前是配置形态和 PEM 输入读取，不是可用 channel activation。

   证据：计划明确当前 crate 没有启用 tonic TLS，真实 `ClientTlsConfig` activation 留给 R-SEC-002：`docs/plans/2026-05-15-r-e-transport-baseline-switch-codex.md:13`。Cargo manifest 当前 `tonic` 只启用 `transport`：`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:44`；tonic 0.12.3 的 `tls` 是独立 feature：`/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/Cargo.toml:275`-`281`。实现中 mTLS 分支读取 PEM bytes 并返回 local `RouteClientTlsConfig`：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:376`-`388`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:393`-`405`；若直接用 `RouteClient::from_transport_config` 激活 mTLS channel，会 fail-fast 返回 R-SEC-002 approval error：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:173`-`178`。

   影响：这符合本 lane plan 的边界，不是 clean-room 或 license 问题；但 ship note 必须写成 “mTLS config baseline present, runtime activation deferred”，不能写成 “mTLS route channel 已可生产使用”。

### LOW

1. 当前待提交内容未 stage。

   证据：`git status --short` 显示 `config.rs`、`route_client.rs`、`.gitignore` 是工作区修改，`deny.toml`、policy、plan、runbook、config fixture 等是未跟踪文件。`git diff --cached --stat` 为空。提交前需要先 stage 正确文件集合；本报告本身按 Owner 要求写入 `docs/reviews/2026-05-15-round2a-r-e-switch-deny-codex-review.md`。

2. `.gitignore` 例外允许测试 PEM 文件入库，当前内容看起来是 placeholder，不是真证书；但提交前仍建议 reviewer 复核这些 fixture 没有真实 key material。

   证据：`.gitignore` 只对白名单路径放开 `*.pem`：`.gitignore:30`-`35`。fixture 文件头是 `HUAKAI TEST ... PLACEHOLDER`，不是标准真实 PEM 头；例如 `client-chain.pem` / `client-key.pem` / `ca.pem` 都是 placeholder。该风险低，但因为 `.gitignore` 改动不在 Owner 最初列出的 review 文件清单中，报告记录这个 scope 事实。

## clean-room verification

未发现 clean-room 违规信号。

我没有读取任何禁止的参考项目源码。对本轮变更文件执行了参考项目名扫描，未命中 sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway / one-api 等名称。实现和文档中的 transport switch、UDS connector、mTLS PEM input、cargo-deny policy 都是 HUAKAI 内部工程形态；没有出现复制上游函数名、结构名、注释、schema、测试或独特目录结构的证据。

功能缩水检查：没有删除 transport 能力。UDS 作为 personal edition 默认已落配置和 helper；mTLS 作为 SaaS baseline 已落配置和 PEM 输入读取，但 runtime activation 被安全地留给 R-SEC-002，不是删除功能。dependency policy 明确要求 preset-like 能力通过 HUAKAI 自有模板或 permissive 依赖保留：`docs/dependency-policy.md:8`-`13`。

## ship-or-block

结论：**BLOCK landing as “Round 2-A ready”**，原因是 HIGH-1：`cargo deny check` 对当前依赖图实质失败。即使 `CARGO_TARGET_DIR=/home/codex/cargo-targets/round2a-review cargo test -p core_gateway` 已通过，且 `cargo fmt --check`、`git diff --check` 已通过，也不能宣称 dependency-policy / deny gate 已就绪。

可接受的落地条件：
- `cargo deny check` 变绿；或
- Owner 明确批准以 “deny policy introduced with known existing failures” 方式提交，并把 `CDLA-Permissive-2.0` 处置、`protobuf 2.28.0` advisory 处置写入 Mandatory Roadmap / risk register，且 commit message 不声称 R-LIC-003 已关闭。

其他 sanity 结论：
- `transport_baseline` 默认是 `uds`，符合 personal edition 默认：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:70`-`73`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:352`-`356`。
- mTLS 缺少 cert/key/CA 路径会 fail-fast：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:151`-`153`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:162`-`179`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs:276`-`280`。
- tonic 0.12.3 的 signatures 匹配未来 activation 方向：`Certificate::from_pem(pem: impl AsRef<[u8]>) -> Self`，`Identity::from_pem(cert: impl AsRef<[u8]>, key: impl AsRef<[u8]>) -> Self`：`/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/src/transport/tls.rs:14`-`20`、`/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/src/transport/tls.rs:51`-`59`。当前 lane 未启用 tonic TLS feature，所以没有错误调用这些 API。
- `deny.toml` 明确 ban `wreq-util` / `rquest-util`，reason 引用 `R-LIC-003`：`exploratory/rust-core-gateway/merged/deny.toml:32`-`37`。
- 风险映射方向正确：`R-SEC-002` 要求 mainline R-E 前使用 mTLS / UDS / 等效本地认证 transport：`docs/10_RISK_REGISTER.md:24`；`R-TRANSPORT-001` 保持 exact transport gated：`docs/10_RISK_REGISTER.md:29`；`R-LIC-003` 要求拒绝 `wreq-util` / `rquest-util` 并在新增 runtime transport dependency 前做 license audit：`docs/10_RISK_REGISTER.md:30`。

## sources read

- `git diff -- exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- `git diff -- exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`
- `cat exploratory/rust-core-gateway/merged/deny.toml`
- `cat docs/dependency-policy.md`
- `cat docs/plans/2026-05-15-r-e-transport-baseline-switch-codex.md`
- `cat docs/runbooks/r-e-transport-baseline-switch.md`
- `cat exploratory/rust-core-gateway/merged/config/route_client_config.toml`
- `ls -la exploratory/rust-core-gateway/merged/crates/core_gateway/tests/fixtures/mtls/`
- `docs/10_RISK_REGISTER.md`
- `.gitignore`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/Cargo.lock`
- `/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/src/transport/tls.rs`
- `/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/Cargo.toml`
- `/home/codex/.cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tonic-0.12.3/src/transport/channel/endpoint.rs`

Commands run:
- `cargo-deny 0.19.6`
- `cargo deny check`：FAILED，exit 5；licenses FAILED (`webpki-roots` / `CDLA-Permissive-2.0`)；advisories FAILED (`protobuf 2.28.0`, RustSec advisory)；bans ok；sources ok。
- `CARGO_TARGET_DIR=/home/codex/cargo-targets/round2a-review cargo test -p core_gateway`：PASSED。
- `cargo fmt --check`：PASSED。
- `git diff --check`：PASSED。
