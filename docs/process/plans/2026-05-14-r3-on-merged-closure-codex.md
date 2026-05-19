# 2026-05-14 R-3 Transport Mimicry On Merged Closure Codex Plan

| Owner directive | “R-3 transport mimicry 改用 Rust, 建在已有的 exploratory/rust-core-gateway/merged/ 上；rquest spike 必须先做，gate 一切。” |
|---|---|
| Scope | 先完成 rquest/wreq 可行性 spike；再规划 Phase R-C/R-D/R-E。计划不写生产代码。 |
| Out of scope | 不引用 sub2api；不新建 sidecar；不改 `LICENSE`；不改 DB schema/auth core/billing/quota。 |
| Success criteria | 明确 rquest 是否够用；给出 Rust merged 数据面接指纹层、验真、主线化的执行闭环；每 phase 有范围、成功标准、风险、lane 数、时间、决策点。 |
| Time estimate | 计划与 spike：0.5 天；R-C：3-5 天；R-D：2-4 天；R-E：5-8 天。 |
| Blast radius | 主要影响 Rust 数据面 outbound transport；默认必须保留 hyper-rustls fallback，不影响 Go 控制面凭据存储。 |
| Clean-room | 只读 HUAKAI 内部文件与 rquest/wreq/boring/h2/http2 官方 crates/docs；不读取任何非 MIT 参考项目源码。 |

## 0. Spike 结论

结论：`rquest`/`wreq` 不能作为 R-3 精确指纹层的唯一实现。

实测路径：

- `cargo add rquest@5.1.0` 失败；`cargo info rquest` 在当前 crates.io index 中不可解析。
- `https://github.com/0x676e67/rquest` 重定向到 `0x676e67/wreq`；本 spike 使用 Apache-2.0 的 `wreq 5.3.0`，没有使用 LGPL/GPL 的 `wreq-util/rquest-util` preset 包。
- 临时 crate：`/tmp/huakai-r3-rquest-spike`。
- 本地 capture：Rust 程序启动 TCP listener，wreq client 连 `https://chatgpt.com:<local-port>/...`，DNS override 到 127.0.0.1，读取第一条 TLS ClientHello record。
- 环境补齐：临时下载 CMake 到 `/tmp/huakai-tools/`；用 `BINDGEN_EXTRA_CLANG_ARGS` 指向 GCC include。

实测 JA3 结构：

```text
template codex-cli:
772,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2

wreq custom capture:
772,4866-4867-4865-49196-49200-159-52393-52392-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-23-13-43-45-51,65073-29-23-24-25-256-257,0
```

字段比对：

| 字段 | 结果 | 真实差异 |
|---|---:|---|
| cipher suite 列表/顺序 | FAIL | 缺 `52394`，BoringSSL 未发出 DHE-CHACHA。 |
| TLS extension 列表/顺序 | FAIL | 缺 `22` encrypt_then_mac；wreq/BoringSSL 公开 API 不能添加任意 extension。 |
| supported groups | FAIL | 模板首项 `4588` 未复现，capture 首项为 `65073`。 |
| signature algorithms | FAIL | BoringSSL 拒绝 `rsa_pss_pss_*` 名称；降级子集后仍不匹配模板 26 项。 |
| EC point formats | FAIL | 模板 `[0,1,2]`，capture 只有 `[0]`。 |
| h2 SETTINGS/pseudo-header | API 部分可控 | wreq 的 MIT `http2` fork 暴露 order knobs；hyperium `h2` 官方 crate 未暴露 SETTINGS 顺序/pseudo-header 顺序。 |
| HTTP/1 header order/case | 可控 | `headers_order` 与 HTTP/1 preserve/title-case API 可用。 |

备选验证：

- Cloudflare `boring` crate 暴露 cipher/curve/sigalg/GREASE/extension permutation 控制，但没有“任意 TLS extension + payload + 精确顺序”的安全公开 API。
- hyperium `h2` crate 暴露常规 SETTINGS 值，不暴露 SETTINGS frame 顺序与 pseudo-header 顺序。
- wreq 依赖的 MIT `http2` fork 暴露 `settings_order` 和 pseudo-header order；如果采用，应作为显式依赖审计对象，不从 LGPL/GPL util preset 复制 profile。

R-C 技术选择：

1. 不把 wreq 作为唯一实现。
2. 建 `mimicry` 自有层：profile/schema/selection/testing 属于 HUAKAI；transport backend 先按 profile 分后端。
3. CLI 级精确 parity 需要一个能控制任意 TLS extension 的路径；可选项是 HUAKAI-owned BoringSSL patch、native OpenSSL path、或更低层 owned TLS ClientHello builder。该点是 R-C 的第一个 Owner 决策。

## 1. 现有 merged 事实

已读内部证据：

- `ProxyEngine` 当前持有 `GatewayHttpClient = hyper_rustls Client`，字段在 `proxy_engine.rs:48-65`。
- outbound 请求在 `forward_inner` 构造上游 URI 和 header 后调用 `self.client.request(...)`，插入点在 `proxy_engine.rs:282-302`。
- 当前 `build_http_client()` 使用 `hyper-rustls`、HTTP/1 + HTTP/2、webpki roots，见 `proxy_engine.rs:356-368`。
- header 白名单和 bearer token 注入在 `proxy_engine.rs:703-764`；token 来自 `PlannedAttempt.acquisition_token`，不是 Rust 本地存储。
- `PlannedAttempt` 包含 `route_plan/vendor_endpoint/auth_mode/acquisition_token`，见 `account_planner.rs:263-297`。
- `route.proto` 已有 `RoutePlan.vendor/vendor_endpoint/acquisition_token/auth_mode`，见 `proto/route.proto:40-52`。
- `READINESS.md` 记录 merged 当前模块完成和 cargo build/test/clippy/fmt PASS，见 `READINESS.md:21-45`。

## 2. Phase R-C: ProxyEngine 接指纹层

范围：

- 在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/` 下新增 `mimicry/` 模块。
- 从三份真实模板构造 `FingerprintProfile`：`codex-cli.json`、`kiro-cli.json`、`gemini-advanced.json`。
- `ProxyEngine.client` 从单一 hyper-rustls client 改为“fallback hyper-rustls + profile-aware mimicry client pool”。
- `forward_inner` 按 `planned.route_plan.vendor` 与 mode 选择 profile；未知 vendor 或未启用 mimicry 时回落 hyper-rustls。
- Rust 只控制 transport 指纹；bearer token 继续由 Go control plane 通过 route plan/acquisition token 下发。
- HTTP 层按 profile 应用：HTTP/1 header 顺序/大小写/UA；HTTP/2 SETTINGS 顺序、值、pseudo-header 顺序、HPACK 验证点。

不做：

- 不在 Rust 中新增凭据存储。
- 不改变 Go auth core。
- 不为追求指纹匹配而删除 fallback。
- 不把 LGPL/GPL util preset 包作为生产依赖。

候选文件与 LoC 估计：

| 文件 | 变更 | LoC |
|---|---|---:|
| `src/mimicry/mod.rs` | module export、feature gate | 30 |
| `src/mimicry/profile.rs` | JSON schema、校验、vendor/mode map | 220 |
| `src/mimicry/tls_profile.rs` | TLS profile 类型、JA3/JA4 normalization、GREASE policy | 180 |
| `src/mimicry/http_profile.rs` | HTTP/1 header order、HTTP/2 settings/pseudo order 类型 | 180 |
| `src/mimicry/client.rs` | client pool、backend selection、fallback adapter | 260 |
| `src/mimicry/capture.rs` | test-only ClientHello/h2 capture helpers | 240 |
| `src/proxy_engine.rs` | client field、constructor、request dispatch branch | 120 |
| `src/lib.rs` | module export | 5 |
| `Cargo.toml` | 依赖/feature，仅 Owner 确认后落地 | 10 |
| tests | profile load、selection、capture compare | 350 |

成功标准：

- `cargo test` 仍通过现有 127 测试，新增测试覆盖三 profile loader。
- 对 codex/kiro/gemini，每个 profile 都能生成明确 backend plan：`Exact`、`SampleSet`、`KnownGap` 三态之一。
- 未知 vendor 回落 hyper-rustls，行为与现在一致。
- `ProxyEngine` 中 token 注入路径不变；测试证明 client-provided authorization 不透传，route plan token 生效。
- codex profile 的 local ClientHello capture test 在“当前 backend 能力”下先明确失败字段，不能 silent pass。

风险：

- 高：精确 TLS parity 需要新增 runtime dependency 或维护 patch；需 Owner 决策。
- 中：`ProxyEngine` response body 类型可能从 `Incoming` 变为自有 body adapter，影响 stream relay。
- 中：连接池按 vendor/profile 分裂后，池容量与 idle timeout 要重新估算。
- 低：模板 JSON 来自真实抓包但不含 secret；仍要加 secret scanner 测试。

Lane 数与时间：

- 3 lanes，3-5 天。
- Lane 1：profile schema + loader + clean-room/license check。
- Lane 2：transport backend spike-to-implementation + local capture tests。
- Lane 3：ProxyEngine dispatch + fallback + existing tests repair。

决策点：

- D1：是否接受新增 `boring`/`boring2` 与 MIT `http2` fork，或要求 HUAKAI-owned TLS ClientHello builder。
- D2：codex extension `22`/EC point `[0,1,2]` 若无法用 safe public API 表达，是否允许维护 HUAKAI patch。
- D3：R-C 是否先以 `KnownGap` feature flag 合入，还是必须 exact capture PASS 才接 ProxyEngine。

## 3. Phase R-D: 端到端验真

范围：

- CI 新增本地 capture harness：
  - raw TCP ClientHello capture：JA3/JA4、cipher、extension、groups、sigalg、EC point、ALPN。
  - local TLS terminating server：读取 HTTP/2 preface、SETTINGS frame、header block order；HTTP/1 记录 raw header 顺序和大小写。
- 对模板执行 normalized compare：
  - codex/gemini stable profiles：精确字段比对。
  - kiro rustls randomized profile：使用 sample-set 策略，比对 stable prefix、允许扩展顺序在真实样本集合内波动。
- Owner 本机真实上游验真：
  - Rust gateway 经 mimicry 层访问真实上游。
  - Owner 本机 tcpdump/mitmproxy 仅由 Owner 自己账号运行。
  - 输出脱敏 capture summary 回填模板或验收报告。

成功标准：

- CI 中三 profile 均有 local capture artifact。
- JA3/JA4 比对不使用伪造数据；所有 mismatch 显示字段级 diff。
- HTTP/1 header order/case 与模板一致；HTTP/2 SETTINGS order/value/pseudo-header order 有可复核 artifact。
- Owner 本机真实上游至少各 vendor 3 次样本；codex/gemini stable hash 全匹配，kiro 落在 sample-set。

GREASE/randomized 处理：

- GREASE 值不进入 JA3 hash 比对，但进入 raw artifact。
- kiro 以 `ja3_hash_samples`/`ja4_samples` 为允许集合；新增样本必须由 Owner 本机真实抓包扩展集合，不能由代码猜。
- 随机扩展顺序测试必须记录随机种子或 raw capture，避免“偶然 PASS”。

风险：

- 高：CI 无法访问真实上游，只能 local capture；真实验收必须 Owner 本机配合。
- 中：HTTP/2 HPACK header order 需要可解码工具，不能只看应用层 HeaderMap。
- 中：JA4 实现若本地工具不成熟，先以 raw字段 + 外部复核脚本双轨。

Lane 数与时间：

- 2 lanes，2-4 天。
- Lane 1：CI local capture server + artifact writer。
- Lane 2：Owner-machine runbook + report template + sample-set policy。

决策点：

- D4：Owner 是否接受“CI local capture PASS + Owner 本机真实上游 PASS”作为 Released-spec gate。
- D5：真实上游抓包工具链选 tcpdump-only、mitmproxy、还是二者都要。

## 4. Phase R-E: Mainline Rust 数据面

范围：

- 将 `exploratory/rust-core-gateway/merged/` 移到主线位置，建议 `backend/rust-gateway/`。
- 保留当前 merged 的 listener/route_client/account_planner/proxy_engine/stream_pipeline/attempt_reporter/metrics/heartbeat/redaction 模块边界。
- Go control plane 实现 `RouteService` gRPC server，直接服务现有 `route.proto`。
- Go 网关请求路径通过 feature flag 切到 Rust 数据面：
  - `off`：现状 Go hot path。
  - `shadow`：Go 正常响应，复制请求到 Rust，不使用响应。
  - `canary`：按 tenant/model/percentage 走 Rust。
  - `on`：Rust 数据面主路径，Go 保留回退。
- Docker 多阶段：Cargo build Rust data plane；Go build control plane/gateway；运行镜像按部署拓扑拆分。
- OCAW gate：测试、license、clean-room、Owner 真实上游验真、rollback runbook 全部通过后才能推进。

成功标准：

- 主线目录 cargo build/test/clippy/fmt PASS。
- Go control plane gRPC server 能返回 `RoutePlan` 并接收 `AttemptReport`。
- shadow 模式不改变用户响应；canary 模式可按配置比例启用并可秒级回滚。
- Rust 数据面 heartbeat/metrics 能被现有 ops 观测读取。
- R-C/R-D capture gate 作为 mainline enable 前置条件，不被 feature flag 绕过。

风险：

- 高：引入 grpc-go 或 Go gRPC server 对主线依赖有影响，需要 Owner 确认。
- 高：部署与回滚路径属于生产风险，不能在计划阶段直接改部署脚本。
- 中：Rust data plane 与 Go gateway 双路径可能造成 tracing/request_id 不一致。
- 中：shadow 复制真实请求必须严格避免重复计费/重复 attempt report。

Lane 数与时间：

- 4 lanes，5-8 天。
- Lane 1：repo move + Cargo workspace/mainline path。
- Lane 2：Go control plane gRPC server。
- Lane 3：Go gateway feature flag + shadow/canary router。
- Lane 4：Docker/OCAW/release-readiness docs。

决策点：

- D6：主线目录名确认：`backend/rust-gateway/` 还是 `backend/dataplane-rust/`。
- D7：是否接受 grpc-go 作为主线依赖；若否，必须定义 HTTP/JSON shim。
- D8：shadow 是否允许发送真实 upstream 请求；默认建议不允许，只允许 mock/local capture，避免重复计费。

## 5. Phase R-C 第一可执行 Atom

Atom 名称：`R-C-A1 mimicry profile loader + capture test gate`

目标：

- 先把三份真实模板变成强类型 profile，并建立本地 capture/fail-fast 测试框架。
- 不接 `ProxyEngine`，不新增生产 transport dependency，先锁住真实数据边界。

文件范围：

- 新增 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs`
- 新增 `src/mimicry/profile.rs`
- 新增 `src/mimicry/http_profile.rs`
- 新增 `src/mimicry/tls_profile.rs`
- 新增 `tests/mimicry_profile_test.rs`
- `src/lib.rs` export `mimicry`

执行步骤：

1. 定义 `FingerprintProfile`，字段只覆盖模板中已真实存在的 TLS/http/auth metadata。
2. 用 `include_str!` 读取三份模板，反序列化并校验必需字段。
3. 加 `ProfileMatchPolicy`：`ExactStable`、`SampleSetRandomized`、`KnownGapBlocked`。
4. codex 先标记 `KnownGapBlocked`，原因写入测试断言：extension 22、sigalg、EC point formats、group 4588 当前 backend 未复现。
5. 单测断言三 profile 可加载、无 secret、vendor/mode 映射正确、known gap 不会被当成 pass。

成功标准：

- `cargo test mimicry_profile` PASS。
- 不改 `ProxyEngine` 行为。
- 不新增高风险生产依赖。
- 测试失败信息能指出 template 字段差异，不能只写 “mismatch”。

## 6. Required Checks

- R-C-A1：`cargo fmt --check`、`cargo test mimicry_profile`、`cargo test`。
- R-C full：`cargo test`、`cargo clippy -- -D warnings`、local capture tests。
- R-D：CI capture artifacts + Owner-machine true upstream report。
- R-E：Go unit/integration tests、Rust full test、Docker build smoke、OCAW gate。

## 7. Assumptions

- Owner 提供的三份 JSON 是当前唯一可用真实指纹真源。
- R-3 不允许用“浏览器 preset 接近”代替 CLI 指纹。
- 未知 vendor 回落 hyper-rustls 是产品安全阀，不算功能缩水。
- Rust 数据面不拥有凭据存储，只消费 Go control plane 下发的短期 acquisition token。

## 8. Open Risks

- `rquest` crate 名称不可安装，生产依赖若选 wreq/boring/http2 需要重新跑 dependency license audit。
- exact TLS extension parity 可能需要维护 patch；这会增加 release/安全维护成本。
- `wreq-util`/`rquest-util` 许可证不适合作为 MIT 主线生产依赖；不得引入。
- 若 Owner 本机真实上游样本更新，旧模板不得继续作为“最新真相”使用。

## 9. Owner 决策点汇总

1. D1：transport backend 选型：`boring/boring2 + MIT http2 fork`、HUAKAI-owned TLS builder、还是 native OpenSSL path。
2. D2：是否允许为 codex extension 22/EC point formats 维护 HUAKAI patch。
3. D3：R-C 是否允许 `KnownGap` feature flag 先合入，还是 exact capture PASS 才接入。
4. D4：验收 gate 是否采用 CI local capture + Owner 本机真实上游双轨。
5. D5：Owner 本机验真工具链选择。
6. D6：主线目录名。
7. D7：是否接受 grpc-go 主线依赖。
8. D8：shadow 模式是否允许真实 upstream 请求；默认不允许。

## 10. Source Coverage Proof

- 读 `tools/fingerprint-collector/templates/codex-cli.json`、`kiro-cli.json`、`gemini-advanced.json`：用于 profile 字段、JA3/JA4、header order、auth boundary。
- 读 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine.rs`：确认 client 插入点、header normalize、bearer token 注入、fallback 形态。
- 读 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`：确认 `PlannedAttempt` 字段和 token 来源。
- 读 `exploratory/rust-core-gateway/merged/proto/route.proto`：确认 Go/Rust gRPC contract 已有 route plan 字段。
- 读 `exploratory/rust-core-gateway/merged/READINESS.md`：确认 merged 当前 readiness 和测试基线。
- 读 docs.rs/crates official docs and crate source for rquest/wreq/boring/h2/http2 public APIs；未读取任何非 MIT reference project source。

中文总结：本计划基于真实 spike 与 HUAKAI 内部代码阅读，不用浏览器 preset 冒充 CLI 指纹；观察到 rquest 名称不可安装、wreq/BoringSSL 无法精确复现 codex-cli JA3 多个字段，因此 R-C 必须先做 HUAKAI 自有 profile/capture gate，再决定底层 transport backend。功能没有缩水，fallback 只作为安全路径；clean-room 风险当前低，但新增依赖和 TLS patch 需要 Owner 决策。
