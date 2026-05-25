# DEFERRED — sidecar raw tunnel 在 ALPN=h2 时未路由到 H2 path

- **Severity**: S2 (sidecar 未接通生产,当前 production no-impact)
- **来源 codex review**: 2026-05-25T00:02Z, Phase 3 H2 SETTINGS Round 2 P2 finding
- **Affected files**:
  - `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:80` (`connect_upstream` raw tunnel return)
- **问题描述**:
  - `connect_upstream` raw tunnel TLS handshake 后即使 ALPN 选中 `h2`,仍直接返 raw TLS 流
  - Go sidecar transport `backend/internal/transport/mimicry/sidecar_client.go:32-35` 用 `ForceAttemptHTTP2: false` HTTP/1.1 only
  - 若 ALPN 真选了 h2,Go 客户端发 HTTP/1.1 request bytes → 服务端按 H2 解 → 协议错乱
  - Phase 3 plan (`docs/process/plans/2026-05-24-h2-settings-phase3-codex.md:4-5`) 期望 Rust 端 own H2 preface + SETTINGS framing,但 raw tunnel 设计不持 H2 framing 状态
- **不 block 当前 commit 的原因**:
  - sidecar 当前**未接通生产** (`Factory.SidecarSocketPath` 默认空, gateway 走 Go uTLS path)
  - 实际生产 ALPN 协商不会跑到这条 raw tunnel 路径
  - Phase 4-5 + Go-Rust 接通切片才会真触发,届时同期修
- **应在哪个切片修**:
  - **选项 A**: 接通生产切片 (Factory.SidecarSocketPath wire),`connect_upstream` 在 ALPN=h2 时 fail 让 Go 走 fallback,或转 `connect_h2_upstream`
  - **选项 B**: Rust sidecar own 完整 H2 framing,Go 端切纯隧道转 H2 client (重大重构)
  - **选项 C**: sidecar profile 强制 ALPN=`http/1.1` only,绕开 H2 framing 复杂度 (但失 Anthropic Node 22 真 h2 指纹)
- **Tracker**: 跟 Go-Rust sidecar 接通生产切片合并
