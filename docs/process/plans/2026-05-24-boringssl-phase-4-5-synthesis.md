# BoringSSL Phase 4-5 Synthesis (Claude x Codex)

- UTC: 2026-05-24T15:40:33Z
- 输入:
  - Claude: [2026-05-24-boringssl-phase-4-5-claude.md](2026-05-24-boringssl-phase-4-5-claude.md) (123 lines, 4 D 决策)
  - Codex: [2026-05-24-boringssl-phase-4-5-codex.md](2026-05-24-boringssl-phase-4-5-codex.md) (764 lines, 4 D 决策 + R-3-A-fix-2..5 status review)
- 输出性质: synthesis only. No implementation. No `git add`. No commit. No push.

> For implementation workers: do not execute this plan until Owner approves §F. This artifact is the CLAUDE.md #10 parallel-draft step 3 synthesis.

## §0 Codex 揭示 Claude 漏的事实

| # | 事实 | Claude plan 覆盖 | 影响 |
|---|---|---|---|
| F-1 | R-3-A-fix-2..5 的 C/Rust wrapper 状态已被逐项核过,并有 coverage gap 清单 | Claude 只把 Phase 4/5 当下一步 plan,没有复审 fix-2..5 | 执行序必须先加 Slice 0 baseline/hardening gate |
| F-2 | 当前 `profile.rs` / `boring_ctx.rs` fixture 存在 mismatch 风险 | Claude 未标 dirty fixture 风险 | Phase 4/5 前必须跑并修 `cargo test -p tls-sidecar` |
| F-3 | 当前 Boring ECH client setter 已可经 `ConnectConfiguration` 进入 per-connection path | Claude 方向一致,但证据较少 | Phase 4 不应先做 vendor C patch |
| F-4 | PQ primitive 与 C-level key-share path 已在 vendor tree / build patch路径中,但 sidecar profile 未接入 `0x11ec` | Claude 只从目标 fixture 角度锁 `4588` | Phase 5 先做 existing-fields wire proof,不要直接 R-3-A-fix-6 |
| F-5 | DNS HTTPS/SVCB resolver 会带来 dependency/cache/TTL/split-horizon/proxy policy 风险 | Claude 也建议不在 dial hot path 做 DNS | synthesis 采纳 inline first, DNS 变 Owner optional slice |

## §A 共识区(直接落地)

| 主题 | Claude | Codex | Synthesis |
|---|---|---|---|
| D 共识 1: ECH config source | profile carries explicit ECH config; refresh outside hot path | inline/hardcoded config first; DNS later | **采纳 inline profile first; DNS/SVCB 不进 Phase 4A** |
| D 共识 2: ECH failure | production fail-closed; audit-only explicit lab mode | required mode fail-closed; audit fallback explicit | **采纳 fail-closed default + audit-only named mode** |
| D 共识 3: PQ implementation path | verify existing template and wire before adding behavior | existing Boring/group-list path first; R-3-A-fix-6 only if needed | **采纳 existing-fields path first** |
| D 共识 4: PQ candidate/fallback | target `4588` plus classical X25519; no extra PQ candidates without capture | `0x11ec` wire proof; required fails closed; audit fallback explicit | **采纳 `4588`/`0x11ec` only + classical fallback audit unless required** |
| Package/file scope | avoid frozen Go package new files | Rust sidecar files only; no frozen package writes | **采纳; no new files under frozen Go packages** |
| License posture | clean-room specifier evidence only | no copied source; vendor attribution if patch | **采纳; any vendor patch updates `MODIFICATIONS.md`** |

共识 D 决策数: **4**.

## §B 冲突区(需要 synthesis 取舍)

### B-1 ECH profile field shape

- Claude shape: flat `ech_config_list_base64` plus `ech_fail_policy`.
- Codex shape: nested `ech` block with `mode`, `config_list_base64`, `public_name`, `retry_once`, `source`, and expiry guard.
- Synthesis recommendation: **nested `ech` block**. Reason: Phase 4B needs retry/stale/source policy without growing unrelated top-level fields. A temporary import alias for Claude's flat name may be allowed only for migration tests, not as the canonical config surface.

### B-2 PQ policy field timing

- Claude shape: keep target group simple now; future `pq_required` can force failure.
- Codex shape: Phase 5A uses existing fields; Phase 5B adds explicit `pq.mode` / expected key-share fields only if needed.
- Synthesis recommendation: **existing fields in 5A, explicit `pq` policy in 5B only after the wire test proves what policy cannot be represented safely**. This keeps Phase 5A small while preserving the required/audit/off feature.

冲突 D 决策数: **2**.

## §C 各方独有维度(纳入/降级/延后)

| 来源 | 独有维度 | Synthesis 处理 |
|---|---|---|
| Claude | Go-Rust production socket slice after Phase 4/5 | 纳入 execution sequence,但不与 ECH/PQ 同 commit |
| Claude | Operator-facing profile example/docs | 纳入 Phase 4A/5A docs acceptance criteria |
| Codex | R-3-A-fix-2..5 status review + coverage gaps | 纳入 Slice 0 gate |
| Codex | Dirty fixture mismatch risk | 纳入 Slice 0 blocking precondition |
| Codex | Negative setter tests for extension/cipher/EC point validation | 纳入 Slice 0 hardening |
| Codex | DNS resolver dependency/cache/split-horizon risk | 延后为 Owner optional Phase 4C |
| Codex | ECH retry/stale config policy | 纳入 Phase 4B |
| Codex | R-3-A-fix-6 conditional trigger list | 纳入 Phase 5C optional gate |

各方独有维度数: **8**.

## §D 最终采纳 D 决策

| ID | 决策 | 最终建议 | 参考对照 |
|---|---|---|---|
| BS45-SD1 | Execution gate and sequence | **Slice 0 baseline first**, then Phase 4 ECH, then Phase 5 PQ, then Go-Rust production socket. Parallel ECH/PQ only after baseline is green and file ownership is disjoint. | §E.1 |
| BS45-SD2 | ECH config source and profile schema | **Nested `ech` block + inline config first**. `source=dns_https` returns unsupported until Owner approves resolver/cache design. | §E.2 |
| BS45-SD3 | ECH failure, retry, and stale policy | **Production require mode fail-closed**. Retry once only when returned replacement configs exist. Audit fallback must be explicit and observable. | §E.3 |
| BS45-SD4 | PQ implementation path | **Existing BoringSSL + existing profile fields first**. R-3-A-fix-6 key-share wrapper only if 5A cannot produce deterministic wire proof. Self-maintained PQ crypto is rejected. | §E.4 |
| BS45-SD5 | PQ candidate and downgrade policy | **Target only `X25519MLKEM768` / `0x11ec` / `4588` plus classical X25519 fallback**. Classical negotiation is allowed and audited unless profile later declares PQ-required. | §E.5 |

## §E 借鉴对照(CLAUDE.md #15)

### §E.1 BS45-SD1 Execution Gate And Sequence

| Project | Evidence | Synthesis read |
|---|---|---|
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/test/ech.rs:47` | Upstream-style ECH behavior is tested as distinguishable branches; HUAKAI should not start ECH/PQ work on a red baseline. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/gateway_config.go:13` | Gateway behavior is expressed as explicit config surface, supporting staged control-plane/data-path rollout rather than hidden transport changes. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:172` | TLS emulation tests encode profile choices as fixtures; HUAKAI's Slice 0 must make fixtures discriminating before adding new knobs. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/utls_transport.go:18` | Special transport handling is separated into a specific path, supporting HUAKAI's sequence of sidecar proof before production socket wiring. |

### §E.2 BS45-SD2 ECH Config Source And Profile Schema

| Project | Evidence | Synthesis read |
|---|---|---|
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3775` | Client-side ECH config bytes can be supplied by the caller, so HUAKAI can load inline profile data without adding C API first. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:6` | Filter behavior is held in a separate config model, supporting the "profile/control plane produces config; sidecar consumes config" split. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:189` | ECH-related emulation is modeled as profile-level TLS behavior, supporting a nested profile block rather than an ad hoc runtime flag. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:config.example.yaml:220` | Claude-client defaults are stored in config, but the read region does not show an ECH config-list source; HUAKAI must define its own explicit ECH field. |

### §E.3 BS45-SD3 ECH Failure, Retry, And Stale Policy

| Project | Evidence | Synthesis read |
|---|---|---|
| cloudflare/boring | `cloudflare/boring@3acc9820eb71:boring/src/ssl/mod.rs:3812` | Rejection exposes replacement material and points clients toward retry/failure handling, not invisible downgrade. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:216` | Fallback behavior is represented as explicit route policy, reinforcing that ECH fallback must be a named mode. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:172` | ECH behavior appears as deliberate emulation data, not evidence for silent classical fallback. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@50d19e204fed:internal/runtime/executor/helps/utls_client.go:143` | Host-gated special transport supports explicit routing decisions; it does not justify hidden target-host TLS downgrade. |

### §E.4 BS45-SD4 PQ Implementation Path

| Project | Evidence | Synthesis read |
|---|---|---|
| cloudflare/boring | `cloudflare/boring@3acc9820eb71:boring-sys/deps/boringssl/ssl/ssl_key_share.cc:438` | The hybrid group exists in the vendor path, so HUAKAI should prove existing behavior before adding a local wrapper. |
| cloudflare/boring | `cloudflare/boring@3acc9820eb71:boring-sys/deps/boringssl/include/openssl/ssl.h:2629` | A lower-level key-share configuration path exists if deterministic ordering cannot be reached through current fields. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:23` | Decoupled config supports escalating to a narrower later slice instead of expanding the initial transport patch. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:178` | Emulation data includes the hybrid key-share candidate, supporting wire proof as the first implementation criterion. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:152` | The read config region handles Claude defaults, not PQ crypto selection; HUAKAI should not import a crypto policy from it. |

### §E.5 BS45-SD5 PQ Candidate And Downgrade Policy

| Project | Evidence | Synthesis read |
|---|---|---|
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/patches/boring-pq.patch:118` | The target codepoint maps to the hybrid group named by the Owner's Phase 5 scope. |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/patches/boring-pq.patch:451` | Hybrid and classical groups are tested together, supporting offer-PQ-with-classical-compatibility rather than PQ-only by default. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:201` | Backend behavior is explicit in route policy, supporting a future `pq_required` mode instead of treating every classical negotiation as failure. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:115` | The same hybrid group appears in an emulation curve list, supporting `4588` as the narrow target rather than adding speculative PQ groups. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:177` | Hybrid and classical key-share choices appear together, supporting audit-on-classical negotiation in non-required mode. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/utls_transport.go:50` | The read transport region focuses on connection behavior, not PQ-required policy; HUAKAI must make downgrade observability explicit. |

## §F Owner 决策清单(Surface)

| ID | Owner decision | Options | Recommendation | Required now? |
|---|---|---|---|---|
| BS45-SD1 | Execution gate and sequence | A: Slice 0 -> Phase 4 -> Phase 5 -> Go-Rust socket / B: parallel ECH+PQ after no baseline gate / C: PQ before ECH | **A** | **Yes** |
| BS45-SD2 | ECH config source/schema | A: nested `ech` inline first / B: flat `ech_config_list_base64` / C: DNS HTTPS/SVCB in sidecar now | **A**; C becomes optional Phase 4C | **Yes** |
| BS45-SD3 | ECH failure policy | A: require fail-closed + explicit audit / B: audit fallback default / C: silent classical fallback | **A** | **Yes** |
| BS45-SD4 | PQ implementation path | A: existing fields first / B: R-3-A-fix-6 wrapper now / C: self-maintained PQ crypto | **A**, with B only after failed wire proof | **Yes** |
| BS45-SD5 | PQ candidate/downgrade | A: only `4588` + classical audit fallback / B: add more PQ candidates / C: fail every classical negotiation | **A** | **Yes** |

§F Owner 决策清单 D 数: **5**.

## §G 锁定后的执行序

```
[Slice 0: Phase 1-3 baseline stabilization] (0.5-1 day)
  ├── Run cargo tests from exploratory/rust-core-gateway/merged
  ├── Fix profile/test fixture mismatch without changing product scope
  ├── Add negative setter tests for extension/cipher/EC point validation
  └── Exit only when tls-sidecar baseline is green and fixtures are discriminating

[Slice 4A: ECH inline config] (1-2 days)
  ├── Create tls-sidecar/src/ech.rs
  ├── Add nested profile ech block with inline config source
  ├── Apply ECH config via existing Boring per-connection path
  └── Wire test proves real ECH differs from GREASE-only and missing config fails in require mode

[Slice 4B: ECH retry/failure/stale policy] (1 day)
  ├── Required mode retry once only with returned replacement configs
  ├── Stale inline config fails closed in require mode
  └── Audit mode returns explicit reason and never silently downgrades

[Slice 5A: PQ existing-fields wire proof] (1 day)
  ├── Add pq constants/helpers only in tls-sidecar scope
  ├── Put 0x11ec in curves/supported_groups/raw groups fixture
  └── Wire parser proves supported_groups and key_share contain 0x11ec

[Slice 5B: PQ explicit policy, if needed] (0.5-1 day)
  ├── Add pq.mode only if 5A cannot express require/audit/off safely
  └── Required mode fails closed; audit mode records classical fallback reason

[Slice 4C/5C optional Owner gates]
  ├── 4C DNS HTTPS/SVCB resolver only after dependency/cache/TTL/proxy policy approval
  └── 5C R-3-A-fix-6 wrapper only if existing fields cannot lock deterministic key_share

[Production socket slice]
  └── Connect Go gateway to Rust sidecar only after Phase 4/5 tests pass
```

## Owner 中文摘要

这份 synthesis 合并了 Claude 与 Codex 两条 specifier plan。真实观察来自两份已 source-cited plan,本 reviewer lane 没有重读参考源码;合理推断是把两边共识合并为 5 个 Owner 决策。共识是 inline ECH、生产 fail-closed、PQ 先走现有 Boring 能力、只锁 `4588` 并审计 classical fallback;主要冲突是 ECH 字段形态和 PQ policy 字段时机。没有功能缩水,没有复制参考项目实现;clean-room 风险可控但后续如果做 R-3-A-fix-6 或 DNS resolver 必须单独过 Owner gate。需要 Owner 现在确认 §F 的 5 个 D 决策。

## Clean-room Provenance

Source files read: CLAUDE.md; docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md; docs/process/plans/2026-05-24-boringssl-phase-4-5-claude.md; docs/process/plans/2026-05-24-boringssl-phase-4-5-codex.md; specifier-cited reference evidence reviewed through those plan artifacts, not re-read in this reviewer lane: /home/codex/refs/boring/boring/src/ssl/mod.rs; /home/codex/refs/boring/boring/src/ssl/test/ech.rs; /home/codex/refs/boring/boring-sys/patches/boring-pq.patch; /home/codex/refs/envoy-ai-gateway/api/v1beta1/gateway_config.go; /home/codex/refs/envoy-ai-gateway/api/v1beta1/ai_gateway_route.go; /home/codex/refs/wreq/src/tls.rs; /home/codex/refs/wreq/tests/emulate.rs; /home/codex/refs/CLIProxyAPI/config.example.yaml; /home/codex/refs/CLIProxyAPI/internal/config/config.go; /home/codex/refs/CLIProxyAPI/internal/auth/claude/utls_transport.go; /home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/runtime/executor/helps/utls_client.go; /home/codex/refs-latest/envoy-ai-gateway-extracted/ai-gateway-main/internal/filterapi/filterconfig.go; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/ech.rs; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc; https://boringssl.googlesource.com/boringssl/+/HEAD/include/openssl/ssl.h; https://boringssl.googlesource.com/boringssl/+/main/ssl/ssl_key_share.cc
Lane: reviewer
Agent: Codex (GPT-5)
UTC timestamp: 2026-05-24T15:40:33Z
