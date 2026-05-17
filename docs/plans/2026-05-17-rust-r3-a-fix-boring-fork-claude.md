# 2026-05-17 Wave R-3-A-fix: HUAKAI boring fork + extension ordering patch — Claude

| 字段 | 内容 |
|---|---|
| Owner directive | 2026-05-17 R-3-A wire FAIL 后选 B "换下面的 boring 库, HUAKAI 自己 fork 改" |
| 前置 | R-3-A 部分闭环 ([298f21b](../../../tree/298f21b)). 3 vendor wire byte-level FAIL (boring 5.1 默认 Chrome-like ext 顺序 ≠ CodexCli/KiroCli/GeminiAdvanced 真采样) |
| 闭环目标 | HUAKAI vendor boring 5.1 + patch (新公开 API `SSL_CTX_set_extension_order` 或等价机制) → 4 vendor 全部 byte-level JA3 wire match PASS |
| Clean-room 边界变更 | **本 wave 允许读 boring crate source** (Owner 显式授权 fork). 不允许读 rquest / curl_cffi / wreq / utls source. |
| 派工 | Claude 写 plan (反代敏感); codex 写中性 vendored patch + MODIFICATIONS.md |
| 估时 | 3-5 天 codex (5 sub-phase) |

---

## 1. Vendoring 策略 (per CLAUDE.md #12)

boring license: Apache-2.0. 与 HUAKAI MIT 兼容. 可 vendor.

**位置**: `exploratory/rust-core-gateway/vendor/boring/` (HUAKAI rust workspace 内, gated 隔离)

**结构**:
```
exploratory/rust-core-gateway/vendor/boring/
├── LICENSE          (copy from upstream, Apache-2.0)
├── NOTICE           (HUAKAI 新写: source repo + commit SHA + 借了哪些, per CLAUDE.md #12)
├── MODIFICATIONS.md (HUAKAI 新写: 每个 diff 解释 + Apache-2.0 §4 attribution)
├── boring/          (上游 boring 5.1 crate source)
└── boring-sys/      (上游 boring-sys 5.1 source, 含 BoringSSL vendored C)
```

源 ref: ~/refs/boring (已 clone, HEAD 3921f35a). 仅 5.1.0 release tag 子树 vendor.

## 2. 5 Sub-phase 拆分

### R-3-A-fix-1 (0.5d): Vendor 拷贝 + 调研

- 拷 ~/refs/boring/{boring, boring-sys} v5.1.0 tree → exploratory/rust-core-gateway/vendor/boring/{boring, boring-sys}
- 拷 LICENSE; 写 NOTICE (source repo + SHA + 借哪些); 写 MODIFICATIONS.md (空 + schema)
- 调研: 找 boring/src 里 ClientHello extension 顺序处理代码 (估在 boring/src/ssl/connector.rs 或 boring/src/ssl/mod.rs)
- 不 patch, 只 survey + 记录修改候选 surface

### R-3-A-fix-2 (1-1.5d): 最小 patch — 新 SSL_CTX_set_extension_order API

scope:
- 在 boring crate `SslContextBuilder` 加新公开 method `set_extension_order(&mut self, types: &[u16]) -> Result`
- 内部调 boring-sys 已有 SSL_CTX_set_custom_ext or 类似底层接口, 让 ClientHello extension 顺序按 types[] 排
- 如 BoringSSL C 层不支持, 在 boring-sys 加 C wrapper (boring-sys/src/bindings.rs + 自家 C shim 文件)
- patch 总量 ≤ 200 行 (HUAKAI 不无限扩 patch surface)
- 写 MODIFICATIONS.md: file:line diff + reason

### R-3-A-fix-3 (0.5d): Cargo.toml replace

- workspace Cargo.toml [workspace.dependencies] `boring = { path = "vendor/boring/boring" }` (取代 `boring = "5.1"`)
- crate-level Cargo.toml 不变 (workspace dep 自动 resolve)
- 同样 tokio-boring (如有版本依 boring 5.1 的, 一起 vendor 或 path override)
- cargo build verify

### R-3-A-fix-4 (1d): client_hello_builder.rs 用新 API

scope:
- mimicry/client_hello_builder.rs `build_boring_connector`:
  ```rust
  builder.set_extension_order(&profile.tls.extensions)
      .map_err(BoringMimicryError::from_boring)?;
  ```
- 删 old set_permute_extensions(false) (新 API 替代)
- 3 vendor wire test un-ignore

### R-3-A-fix-5 (0.5d): 验证 + 4 vendor wire all PASS

- cargo test --features mimicry-boring --lib: 4 vendor wire byte-level test 全 PASS
  - Anthropic (regression check, 应仍 PASS)
  - CodexCli, KiroCli, GeminiAdvanced (un-ignored, 应 PASS)
- cargo test --workspace --features mimicry-boring 不 regress
- commit + push

## 3. Risks

| 编号 | 类型 | 严重度 | 描述 | Mitigation |
|---|---|---|---|---|
| R-MAINT-001 | maint | MED (扩) | HUAKAI vendor boring 需跟 upstream 5.x/6.x 同步, security patch 自己 backport | 单文件 patch 小; MODIFICATIONS.md 记 commit SHA; upstream 升级时 rebase patch |
| R-LIC-005 | license (新) | LOW | Apache-2.0 §4 attribution: NOTICE + MODIFICATIONS.md 必须包 + 维护 | sub R-3-A-fix-1 同步生成, 不漏 |
| R-MIMICRY-004 | algorithm (新) | LOW | 新 API set_extension_order 可能跟 boring upstream 命名风格不同, upstream merge 可能性低 | HUAKAI 不强求 upstream merge; 长期维护本地 fork |

## 4. 不动

- frontend / Go backend / LICENSE / 计费 / control plane tonic+rustls
- Anthropic profile (R-2-B 闭环, fork 后仍 PASS regression)
- R-2-B/R-3-A 已写代码 (boring_wire.rs / backend_resolver.rs / mimicry/audit_notes 不动)
- rquest / curl_cffi / wreq / utls source (本 wave 仅 boring 解锁, 不读其它)

---

Plan: Claude Opus 4.7 直写, 反代敏感 spec
UTC: 2026-05-17T~12:30:00Z
