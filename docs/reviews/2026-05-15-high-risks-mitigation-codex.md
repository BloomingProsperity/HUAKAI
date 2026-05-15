# 2026-05-15 HIGH Risks Mitigation Convergence - Codex

| Field | Value |
| --- | --- |
| Lane | reviewer + scribe |
| Scope | 收敛 `docs/10_RISK_REGISTER.md` 中 3 个 HIGH risk；只写 review/triage 文档与 risk register 注记 |
| Out of scope | 不改 R-3 plan docs；不改 feature parity matrix；不实施 mTLS；不改 transport 代码；不新增依赖；不 stage/commit/push |
| Clean-room boundary | 只读 HUAKAI 内部 `docs/**`、`exploratory/**`、技能说明与本地 cargo 输出；未阅读禁止 reference repos |
| Timestamp | 2026-05-15T12:08:02Z |

## 1. Executive Summary

三个 HIGH risk 都应保持 `Open`。当前 mitigation 方向正确，但都还不够关闭：

| Risk ID | 当前状态评估 | 已有 mitigation 是否够 | 推荐 owner | 推荐截止时间 |
| --- | --- | --- | --- | --- |
| `R-SEC-002` | Rust `RoutePlan` 已包含 `upstream_auth` material 与 `acquisition_token`，R-E 会引入 Go control plane gRPC server；跨进程/跨主机控制面 transport 若未认证，会把上游凭据暴露在 vendor 请求之前。证据：`docs/10_RISK_REGISTER.md:24`、`exploratory/rust-core-gateway/merged/proto/route.proto:40`、`:50`、`:60`、`docs/plans/2026-05-14-r3-on-merged-closure-codex.md:176`、`:182`、`:194` | 不够。风险登记写了 mTLS/UDS/local-only authenticated transport，但 R-E plan 目前只写 Go gRPC server、shadow/canary、OCAW gate，没有把 control-plane transport protection 固化为 R-E gate。 | Claude 负责 R-E 架构/计划修订；Codex 负责安全 review 与测试场景；Owner 决策 mTLS vs UDS 默认。 | 2026-05-17 前完成方案决策；必须早于 R-E Lane 2 Go control-plane gRPC server 或任何 shadow/canary。 |
| `R-TRANSPORT-001` | OpenSSL + MIT `http2` fork 路线已 feature-gated，KnownGapBlocked policy 已形成；但 dispatch gate 仍有“profile eligibility”与“production exact gate”混淆风险，A5.4 也只证明 extension 22，不证明完整 extension list/order。证据：`docs/plans/2026-05-15-r-c-lane-2-architecture-codex.md:17`、`:19`、`:151`、`docs/reviews/2026-05-15-l2-lane2-retrospective-bulk-codex-review.md:93`、`docs/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md:11`、`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:6`、`:27`、`:35` | 不够。已有 feature flag/local capture/R-D gate 是正确方向，但 production dispatch 之前还缺强制 provenance gate：完整 local exact capture + Owner real-upstream artifact + ops/audit surfacing。 | Codex 负责 transport gate review 与 small safe patch建议；Claude/实现 lane 负责 R-C/R-D atom；Owner 负责 real-upstream gate。 | 2026-05-16 前收紧 dispatch 语义或明确标为 non-production eligibility；必须早于 L2-A9/ProxyEngine production wiring。 |
| `R-LIC-003` | `wreq-util`/`rquest-util` 已被拒绝；当前 Cargo manifest/lock 中未出现 `wreq-util`/`rquest-util`，mimicry feature graph 名称扫描未命中 GPL/LGPL/AGPL 字符串。证据：`docs/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md:41`、`:54`、`:55`、`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:27`、`:35`、`:42` | 部分够，但不能关闭。`cargo deny` 本轮未完成，因为缺失 crate 下载被沙箱代理阻断；`cargo tree | grep` 只是名称扫描，不是许可证解析。 | Codex 负责 dependency/license audit gate；Owner/maintainer 决定是否提交 deny config 与 CI gate。 | 2026-05-16 前补一份可离线复跑的 deny config/CI 步骤；必须早于新增任何 runtime transport dependency 或启用 exact backend release。 |

## 2. R-SEC-002 Convergence

### 当前状态评估

`R-SEC-002` 的风险仍真实存在。Rust contract 已把 `UpstreamAuthMaterial` 放入 `RoutePlan`，同时 `RoutePlan` 仍携带 `acquisition_token`，`AttemptReportRequest` 继续回传 acquisition token。证据：`exploratory/rust-core-gateway/merged/proto/route.proto:40`、`:47`、`:50`、`:60`、`:77`、`:81`。

R-E plan 明确会让 Go control plane 提供 gRPC `RouteService`，并支持 shadow/canary/on 切流。证据：`docs/plans/2026-05-14-r3-on-merged-closure-codex.md:176`、`:182`、`:183`、`:184`、`:185`、`:186`、`:187`。这与 `R-SEC-002` 不冲突，但当前 R-E plan 没有把 mTLS/UDS/local-only authenticated transport 写成显式前置条件。

### 与 Lane beta R-3 R-E plan 是否兼容

兼容，但必须补 gate。建议把 `R-SEC-002` 作为 R-E Lane 2 的前置安全 gate，而不是等到 Docker/OCAW 最后才发现控制面明文 transport。推荐规则：

1. 单机开发/CI 默认只允许 Unix domain socket 或 loopback-only transport。
2. 跨进程/跨主机部署必须 mTLS，证书身份绑定到 Rust data plane instance 与 Go control plane service identity。
3. `RoutePlan.upstream_auth.material` 出现在任何 network transport 前，transport 必须已经通过身份认证与加密保护。
4. shadow/canary 默认不得通过未认证 TCP 发送真实请求或真实 upstream auth material。

### 后续步骤

| Step | Action | Why |
| --- | --- | --- |
| S1 | 在后续 R-E synthesized plan 中加入 control-plane transport security gate；本轮不修改 R-3 docs。 | 用户明确禁止本轮改 R-3 plan docs。 |
| S2 | Owner 选择默认：UDS-only first，或 mTLS first。 | 决定部署复杂度和跨主机能力。 |
| S3 | 为 Rust config/Go server 加测试要求：非 loopback TCP + 无 TLS 时启动失败；mTLS 双向验证失败时 fail closed。 | 防止“先接通再补安全”的回归。 |
| S4 | 在 attempt/report/debug/redaction 测试中确认 `upstream_auth.material` 不进日志、metrics、error body。 | credential disclosure 风险不仅在 wire，也在观测面。 |

推荐 owner：Claude for R-E architecture plan；Codex for reviewer/scenario tests；Owner for transport default decision。

推荐截止时间：2026-05-17 for decision；before any R-E Go gRPC control-plane implementation or shadow/canary.

## 3. R-TRANSPORT-001 Convergence

### 当前状态评估

mitigation 方向已经成形：D1 选择 native OpenSSL + HUAKAI adapter + MIT `http2` fork，D3 要求 KnownGapBlocked 只能保留 diagnostics/plumbing，生产 dispatch 要等 exact capture PASS。证据：`docs/plans/2026-05-15-r-c-lane-2-architecture-codex.md:17`、`:19`、`:21`、`:117`、`:128`、`:151`。

但风险还没收敛到可降级：

- `dispatch.rs` 注释称它是生产 dispatch gate 的最终判定，但 `AllowOpenSsl` 当前由 feature availability、EC point formats 与 extension 22 是否存在决定，没有携带完整 local capture diff 或 R-D Owner gate provenance。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:6`、`:19`、`:27`、`:30`、`:35`。
- L2-A5.4 review 已明确：extension 22 safe-equivalent 可接受，但不能代表完整 extension-list parity。证据：`docs/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md:11`、`:15`、`:17`。
- RUNBOOK 也承认当前 OpenSSL preflight 的 extension 策略不是精确相等，wire extras 会被记录但不失败。证据：`exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:483`、`:485`、`:486`、`:490`。

### 后续步骤

| Step | Action | Why |
| --- | --- | --- |
| T1 | 把当前 `AllowOpenSsl` 语义改名或文档化为 `EligibleOpenSsl` / non-production eligibility，直到它消费完整 capture/R-D artifact。 | 避免未来 ProxyEngine wiring 误接生产。 |
| T2 | L2-A9 前要求 exact local capture artifact：cipher、extensions/order、groups、sigalgs、EC point、ALPN、H2 SETTINGS/pseudo-header 都有字段级 PASS 或明确 blocking disposition。 | `R-TRANSPORT-001` 不能靠单字段 PASS 收敛。 |
| T3 | R-D Owner real-upstream artifact 必须加入 release gate，CI local capture 不能单独放行。 | RUNBOOK 已要求 R-D 不以 local capture 单独通过作为 Released gate。证据：`exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:470`、`:474`。 |
| T4 | 如果需要 HUAKAI-owned crypto/transport patch，先开 patch ledger：scope、upstream SHA、license proof、CVE rebase owner、recapture checklist。 | patch burden 是本 risk 的核心。 |

推荐 owner：Codex for transport-risk review and acceptance tests；implementation lane for R-C/R-D atoms；Owner for real-upstream artifacts and patch approval。

推荐截止时间：2026-05-16 for dispatch semantics; before L2-A9/ProxyEngine production wiring for full gate.

## 4. R-LIC-003 Convergence

### 当前状态评估

已有 mitigation 的方向是正确的：L2-A0 明确没有批准 GPL/LGPL/AGPL runtime dependency，并拒绝 `wreq-util`、`rquest-util` 进入生产 runtime。证据：`docs/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md:41`、`:54`、`:55`、`:57`。

当前 merged Rust core 的 relevant runtime/optional transport 依赖为：

- `http2` git dependency behind `mimicry-http2-fork` feature。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:12`、`:27`。
- `openssl` / `tokio-openssl` behind `mimicry-openssl` feature。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:10`、`:11`、`:35`、`:42`。
- `Cargo.lock` 中只看到 `http2` fork source，不存在 `wreq`/`rquest` utility package source。证据：`exploratory/rust-core-gateway/merged/Cargo.lock:632`、`:634`。

### 现场命令结果

运行目录：`exploratory/rust-core-gateway/merged`

隔离目标目录：`CARGO_TARGET_DIR=/tmp/cargo-target-license-audit`

`cargo-deny` 已安装：

```text
cargo-deny 0.19.6
```

执行：

```bash
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo deny check licenses
```

结果：未得到完整 license verdict。关键输出：

```text
WARN unable to find a config path, falling back to default config
ERROR failed to fetch crates
ERROR `cargo metadata` exited with an error
error: failed to download from `https://static.crates.io/crates/redox_syscall/0.5.18/download`
Caused by: [7] Could not connect to server (Failed to connect to 127.0.0.1 port 8118)
```

fallback 执行：

```bash
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --edges=normal
grep -Ein 'gpl|lgpl|agpl' /tmp/huakai-cargo-tree-license-audit.txt
```

结果：

```text
default feature graph: 404 cargo-tree lines; grep found 0 GPL/LGPL/AGPL name matches.
```

补充扫描 mimicry features：

```bash
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --features mimicry-openssl,mimicry-http2-fork --edges=normal
grep -Ein 'gpl|lgpl|agpl' /tmp/huakai-cargo-tree-license-audit-mimicry-features.txt
```

结果：

```text
mimicry feature graph: 432 cargo-tree lines; grep found 0 GPL/LGPL/AGPL name matches.
Relevant feature deps observed:
http2 v0.5.17 (https://github.com/0x676e67/http2?rev=a33b27e469434a99105f35670c9970f22112e892#a33b27e4)
openssl v0.10.79
openssl-sys v0.9.115
tokio-openssl v0.6.5
```

Interpretation: 本轮没有发现 GPL/LGPL/AGPL 命名命中，也没有发现 `wreq-util`/`rquest-util` 进入 current/mimicry feature graph。但这是弱证据：`cargo tree | grep` 只扫 crate 名称，不解析许可证；`cargo deny` 因离线/代理问题未完成，所以 `R-LIC-003` 不能降级或关闭。

### 真实可跑命令清单

以下命令均从 `exploratory/rust-core-gateway/merged` 运行，并使用隔离 target dir：

```bash
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo deny check licenses
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo deny --offline check licenses
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --edges=normal | grep -Ei 'gpl|lgpl|agpl'
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --features mimicry-openssl,mimicry-http2-fork --edges=normal | grep -Ei 'gpl|lgpl|agpl'
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --edges=normal --duplicates
CARGO_TARGET_DIR=/tmp/cargo-target-license-audit cargo tree --target=x86_64-unknown-linux-gnu --features mimicry-openssl,mimicry-http2-fork --edges=normal --duplicates
```

`cargo tree --duplicates` 本轮可运行；default graph 当前重复包包括 `axum` 0.7/0.8、`getrandom` 0.2/0.4、`hashbrown` 0.12/0.14/0.17、`indexmap` 1/2、`socket2` 0.5/0.6、`thiserror` 1/2、`tower` 0.4/0.5 等。这是 dependency hygiene 风险，不等同 GPL/LGPL 命中。

### 后续步骤

| Step | Action | Why |
| --- | --- | --- |
| L1 | 提交或生成项目级 `deny.toml`，明确允许 MIT/Apache-2.0/BSD/ISC/Unicode 等，拒绝 GPL/LGPL/AGPL/unknown/custom，CI 中跑 `cargo deny check licenses`。 | 当前没有 config，default config 不足以表达 HUAKAI MIT policy。 |
| L2 | CI 或 release gate 预热 cargo cache，或在有网络环境跑 deny 并归档输出。 | 本轮失败原因是 crates 下载被代理阻断，不是依赖许可证 clean。 |
| L3 | 对每个 optional mimicry feature 单独跑 `cargo deny` 与 `cargo tree --duplicates`。 | 默认 feature graph 不覆盖 exact transport feature。 |
| L4 | 继续禁止 `wreq-util`/`rquest-util`；如需要 preset-like 结果，只能用 HUAKAI-owned templates 或 permissive crates。 | 功能保留，但实现方法换成 Safe Equivalent。 |

推荐 owner：Codex for audit script/review；Owner/maintainer for CI placement and deny policy approval。

推荐截止时间：2026-05-16 for reproducible audit gate; before any new runtime transport dependency.

## 5. Risk Register Update Direction

本轮 risk register 只做三件事：

1. 保留所有 risk，不删除、不降级。
2. 给风险表增加 `Last triage` 列；3 个 HIGH risk 写 `2026-05-15`，其它现有 risk 暂写 `-`。
3. 在表后追加 `2026-05-15 Triage Notes`，链接本文件作为收敛证据。

## 6. Owner Summary

本次 HIGH risk 收敛没有发现可以关闭的 HIGH。`R-SEC-002` 与 R-E plan 兼容，但必须把 mTLS/UDS/local-only authenticated transport 写成 R-E Go control-plane gRPC 之前的 gate；`R-TRANSPORT-001` 已有 feature flag/capture/R-D 方向，但 dispatch gate 仍需防止把 eligibility 当 production pass；`R-LIC-003` 现场 `cargo deny` 因沙箱网络失败未完成，fallback `cargo tree` 对 default 与 mimicry feature graph 都没有 GPL/LGPL/AGPL 名称命中，但只能作为弱证据。功能没有缩水；没有读取禁止 reference repos；clean-room 风险低；安全风险仍主要集中在 control-plane secret-in-transit 与 exact transport gate provenance。需要 Owner 确认的是 R-E transport 默认选 UDS 还是 mTLS、R-D real-upstream artifact owner、以及是否批准提交 deny policy/CI gate。

Source files read: `/home/codex/HUAKAI/.agents/skills/api-gateway-risk-review/SKILL.md`; `/home/codex/HUAKAI/.agents/skills/dependency-license-auditor/SKILL.md`; `docs/10_RISK_REGISTER.md`; `docs/plans/2026-05-14-rust-contract-fix-codex.md`; `docs/plans/2026-05-14-r3-on-merged-closure-codex.md`; `docs/plans/2026-05-15-r-c-lane-2-architecture-codex.md`; `docs/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md`; `docs/reviews/2026-05-15-l2-lane2-retrospective-bulk-codex-review.md`; `docs/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md`; `exploratory/rust-core-gateway/merged/READINESS.md`; `exploratory/rust-core-gateway/merged/Cargo.toml`; `exploratory/rust-core-gateway/merged/Cargo.lock`; `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`; `exploratory/rust-core-gateway/merged/proto/route.proto`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs`; `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`; `/tmp/huakai-cargo-tree-license-audit.txt`; `/tmp/huakai-cargo-tree-license-audit-mimicry-features.txt`.

Lane: reviewer + scribe

Agent: Codex GPT-5

UTC timestamp: 2026-05-15T12:08:02Z
