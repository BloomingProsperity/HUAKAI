# L1 TLS BoringSSL fork backend — Synthesis (Claude × Codex)

- UTC: 2026-05-24T08:50Z
- 输入:
  - Claude: [2026-05-24-boringssl-fork-backend-claude.md](2026-05-24-boringssl-fork-backend-claude.md) (17K,6 个 Phase,5 个 D 决策)
  - Codex: [2026-05-24-boringssl-fork-backend-codex.md](2026-05-24-boringssl-fork-backend-codex.md) (71K,5 个 D 决策,2 个固定切片)

## §0 Codex 揭示的关键事实(Claude 漏)

| # | 事实 | 影响 |
|---|---|---|
| **F-1** | **HUAKAI 已 vendor boring 5.1.0** 在 `exploratory/rust-core-gateway/vendor/boring/`(2026-05-17 R-3-A-fix-1),并 R-3-A-fix-3 加 `[patch.crates-io]` redirect。还预备 R-3-A-fix-2 `SSL_CTX_set_extension_order` 本地 patch | Claude plan §7 D-Sidecar-2 推"pin cloudflare/boring 0.4.x"是**错的**,实际应**用 HUAKAI 已 vendor 5.1.0 + 累积本地 patch** |
| **F-2** | **HUAKAI backend/go.mod 不含 google.golang.org/grpc** | Claude plan §7 D-Sidecar-1 推 gRPC bidi 是**漏看 Go 端依赖代价**,加 grpc-go 是新 runtime 依赖需 Owner 高风险确认 |

**结论**:Claude D-Sidecar-1 / D-Sidecar-2 两条**默认推荐被 codex 翻盘**;synthesis 以 codex 立场为准。

## §A 共识区(直接落地)

| 主题 | Claude | Codex | 一致 |
|---|---|---|---|
| 走 Rust 子层 + boring crate + sidecar 接 Go | Phase 1 起手 | D1/D5 框架同 | **共识** |
| wreq 仅借鉴不 vendor | Claude §3.3 行为借鉴 | D3-A Owner 已锁 | **共识** |
| Fallback 默认 fail-closed | D-Sidecar-5 (B) | D5-A (Recommended for production) | **共识 fail-closed**;Phase 1 测试期允许 audit-only |
| Phase 1 = sidecar skeleton + IPC | 1.5 周 | D4-B 2 周含 fixture | **2 周** (codex 更稳) |
| JA3 复刻 = Phase 1 内 done,JA4/H2/ECH/PQ 后续 | Claude Phase 2-5 | Codex 没单列 Phase 2-5 | Claude 更完整,合并 |
| Frozen package 不动 | 同 | 同 | 共识 |

## §B Codex 翻盘(默认采纳)

### B-1 IPC 协议 → **Unix socket + framed stream**(非 gRPC)

- **Codex D1-A (推)**:Unix-domain socket + HUAKAI 自有 framing(简单 length-prefixed frame)
- **Claude D-Sidecar-1 错**:推 gRPC bidi 因为 D-rust-1 已锁 — 但 D-rust-1 是 **control plane** (route.proto / RouteClient),不是 transport sidecar
- **真原因**:Go backend `go.mod` 没 grpc-go;加它是新 runtime 依赖触发 Owner 高风险确认,Phase 1 卡进度
- **采纳**:Unix socket framed
- **Owner 验证**:Phase 1 起手前确认 `backend/go.mod` 不加 grpc-go;如要 gRPC 用,单独依赖切片 Owner 拍

### B-2 boring fork pin → **HUAKAI 已 vendor 5.1.0 + 持续累积 patch**

- **Codex D2-A (推)**:Pin existing HUAKAI vendored boring fork (`exploratory/rust-core-gateway/vendor/boring/`)
- **Claude D-Sidecar-2 错**:推 0.4.x pin — 既漏 HUAKAI 已 vendor 5.1.0,又错版本号(应 5.1.0 不 0.4.x)
- **真原因**:HUAKAI 已在 R-3-A-fix-1/3 投入 vendor 工程,丢了重 vendor 损失;floating 上游会 fingerprint 漂移
- **采纳**:HUAKAI 已 vendor 5.1.0
- **后续路径**:Phase 4-5 ECH/PQ 需要时,fork refresh 单独切片(R-3-A-fix-4),不 floating

## §C Claude 补充(纳入)

Claude plan 多出 Phase 2-6:
- Phase 2: JA4 计算 (1 周)
- Phase 3: H2 SETTINGS frame 控制 (1.5 周)
- Phase 4: ECH (1 周)
- Phase 5: PQ X25519MLKEM768 (0.5 周)
- Phase 6: profile DB + ops dashboard (1 周)

**Codex 没单列**,但 §6 风险测试矩阵覆盖了 H2 SETTINGS / fingerprint 漂移。
**合成**:沿用 Claude Phase 2-6 编号,作为长尾交付。

## §D 锁定后的执行序

```
[Phase 1: sidecar skeleton + Unix socket IPC + JA3 复刻] (2 周,采纳 codex D4-B)
  ├── 新 crate exploratory/rust-core-gateway/crates/tls-sidecar/
  ├── 用 HUAKAI vendor boring 5.1.0 (workspace 已 patch)
  ├── Unix socket framed protocol (非 gRPC) ← 关键 B-1 翻盘
  ├── Go backend/internal/transport/mimicry/sidecar_client.go
  └── wireshark 对照 Anthropic CLI JA3 = HUAKAI sidecar 出站 JA3

[Phase 2: JA4 计算] (1 周,Claude 补)
[Phase 3: H2 SETTINGS frame 控] (1.5 周,Claude 补;关键 codex evidence:hyperium/h2@d361b75 settings.rs:213)
[Phase 4: ECH] (1 周)
[Phase 5: PQ key share] (0.5 周)
[Phase 6: profile PG + dashboard] (1 周)

[R-3-A-fix-2 平行做]
  HUAKAI 自加 SSL_CTX_set_extension_order C-level patch (vendor/boring/MODIFICATIONS.md L41 预定)
  → Phase 1 完工后立即接,作为 Phase 2 准备依赖
```

## §E 借鉴项目对照(CLAUDE.md #15)

| 维度 | cloudflare/boring (Apache-2.0+MIT @ 5.1.0 vendored 3acc9820eb71) | hyperium/h2 (MIT) | 0x676e67/wreq (BSD, 仅借鉴) | envoyproxy/ai-gateway (Apache-2.0) |
|---|---|---|---|---|
| Rust → BoringSSL FFI | **主 ref**,HUAKAI 已 vendor 5.1.0 | 不适用 | 用了 boring,但 facade 太重 | 不用 boring |
| H2 SETTINGS 控制 | `boring-sys` 暴露 BoringSSL 但 H2 在 hyper/h2 | settings.rs:213 推荐 | tests/emulate.rs:227 fingerprint 顺序 | filterapi 控制 H2 通过 envoy core |
| Unix socket IPC | 不涉及 | 不涉及 | 不涉及 | internal/filterapi/filterconfig.go:6/9/23 配置分离边界 |
| 适合 HUAKAI | **Phase 1 主参考**,vendor 已就位 | **Phase 3 H2 主参考**,行为借鉴 | Phase 1-3 fingerprint 实现思路 | Phase 6 control plane 模式参考 |

**HUAKAI 升级 delta**:
- 架构:Rust transport sidecar + Go gatewayhttp 出站 router(envoy 是单进程 control+data;HUAKAI 双 binary 双语言 split)
- 算法:逐字节 JA3+JA4+H2 SETTINGS+ECH+PQ 5 维度组合控(envoy/wreq 各自只控 subset)
- 生态:profile DB + ops dashboard 可改(Phase 6)

## §F Owner 决策清单(Surface,本次较简,主要 codex 翻盘已采纳)

| ID | 决策 | 选项 | 推荐 | 必要性 |
|---|---|---|---|---|
| BS-D1 | IPC 协议 | (A) Unix socket framed / (B) gRPC bidi (需加 grpc-go) | **A**(codex 翻盘) | **共识可锁**,无需 surface |
| BS-D2 | boring fork pin | HUAKAI vendor 5.1.0 / 0.4.x crates.io / floating | **HUAKAI vendor 5.1.0**(codex 翻盘) | **共识可锁**,无需 surface |
| BS-D3 | wreq 用法 | behavior 借鉴 / vendor / plugin later | **behavior 借鉴** | 已共识 |
| BS-D4 | Phase 1 长度 | 1 周 mock / 2 周 +fixture / 3+ 周 含 JA3 | **2 周**(codex D4-B) | 已共识 |
| BS-D5 | Fallback 语义 | fail-closed / audit-only fallback / silent fallback | **fail-closed**(production)+ **audit-only fallback**(Phase 1 测试) | 已共识 |
| BS-D6 | R-3-A-fix-2 启动时机 | Phase 1 同期 / Phase 1 后再启 / 不做 | **Phase 1 完工后立启**(为 Phase 2 准备) | Claude 建议,**Owner 待拍** |

## §G Lane + UTC

- Synthesis: Claude (claude-opus-4-7)
- UTC: 2026-05-24T08:50Z
- Inputs: Claude plan + Codex plan §1-§7
- 重大 cross-discuss 收获:codex 抓住 Claude 完全漏的 **F-1 HUAKAI 已 vendor boring 5.1.0** + **F-2 backend Go 没 grpc-go**,两条默认推荐翻盘
- 下一步:Owner 拍 BS-D6 R-3-A-fix-2 启动时机 → 进 Phase 1 实施 plan
