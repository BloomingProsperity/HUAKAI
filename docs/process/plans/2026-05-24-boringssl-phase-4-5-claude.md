# 2026-05-24 BoringSSL Phase 4 ECH + Phase 5 PQ - Claude Lane Plan

| Field | Value |
| --- | --- |
| Owner directive | "CONTEXT: 起草 BoringSSL Phase 4 ECH + Phase 5 PQ X25519MLKEM768 的 Claude 视角 plan。这是 CLAUDE.md #10 parallel-draft 第二条腿。" |
| Lane | Claude-view independent plan draft, authored by codex-cli as the second parallel-draft leg |
| Counterpart draft | `docs/process/plans/2026-05-24-boringssl-phase-4-5-codex.md` at commit `08e228c`; deliberately not read in this lane |
| Scope in | Phase 4 ECH profile/config/fail-closed plan; Phase 5 X25519MLKEM768 verification plan; Go-Rust production connection order |
| Scope out | Database schema, production secrets, billing/quota/auth core, deployment scripts, git add/commit/push |
| Time estimate | Phase 4: 1 week; Phase 5: 0.5 week; Go-Rust production socket slice after both |
| Blast radius | TLS mimicry sidecar behavior, profile parsing, wire-level ClientHello compatibility, fail-closed availability behavior |
| Clean-room mode | Specifier lane; reference source used only as behavior evidence and paraphrased |

## §A 共识

1. HUAKAI already vendors BoringSSL through the Rust sidecar workspace, so Phase 4/5 should use the existing vendored path instead of floating to a new upstream revision. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/Cargo.toml:11` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/Cargo.toml:12`.
2. Phase 4 ECH comes before Phase 5 PQ because ECH is the anti-blocking workstream; PQ is compatibility hardening once the ECH path is deterministic.
3. Phase 4 should add real ECH config-list support to the sidecar profile layer, then prove it on the wire. The current sidecar already maps profile data into Boring connector settings and only enables ECH GREASE when the extension is present; it does not yet carry a real config list. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:10` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:63`.
4. Phase 5 should first verify the existing template and Boring wire output before adding behavior. HUAKAI's captured Anthropic Node/OpenSSL fixture already includes group `4588` in both supported groups and key-share groups. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json:253` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json:349`.
5. The production connection order should stay: Phase 4 ECH, then Phase 5 PQ, then Go-Rust socket wiring through `Factory.SidecarSocketPath`. The Go factory already selects sidecar transport when a socket path exists and fails closed when the sidecar is unavailable. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/factory.go:29`, `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/factory.go:110`, and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/factory.go:161`.

## §B 冲突

This lane did not read the Codex Phase 4-5 draft, so conflicts below are Claude-side predictions for synthesis, not a diff against the Codex file.

| ID | Expected conflict | Claude position |
| --- | --- | --- |
| D-P45-1 | ECH config source: static embedded config vs in-process DNS HTTPS/SVCB lookup vs external refresh job | Sidecar consumes `profile.toml` `ech_config_list_base64`; config acquisition is outside the sidecar hot path. A refresh job may update the profile later, but Phase 4 should not make every dial depend on live DNS. |
| D-P45-2 | ECH negotiation failure: fail-closed vs audit-only fallback | Fail-closed for production mimicry. Audit-only can exist only as a deliberately named lab mode, not as a silent fallback. |
| D-P45-3 | PQ candidate set: only `X25519MLKEM768` group `4588` vs multiple PQ candidates | Phase 5 locks to `4588` plus classical X25519 fallback in the existing Anthropic template. Do not add extra PQ candidates unless a fresh official-client capture proves them. |
| D-P45-4 | PQ non-support downgrade: allow classical negotiation vs explicit alert/fail | Default allow classical negotiation while recording downgrade evidence. Fail only if a future profile explicitly marks PQ-required. |

## §C Claude 补充

1. Profile schema: add optional `ech_config_list_base64` to `TlsProfile` and `ProfileToml`, with strict decoding during profile load. The parser already denies unknown fields and carries explicit TLS vectors, so ECH belongs in the existing profile boundary rather than an ad hoc runtime flag. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:43` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:174`.
2. Failure policy: add explicit profile policy vocabulary, recommended `ech_fail_policy = "fail_closed"` when `ech_config_list_base64` is present. This keeps the policy observable in review and avoids hiding fallback behavior in transport code.
3. Rust sidecar ECH application: after connector configuration, pass decoded ECH bytes into the Boring client configuration before handshake. The current code already applies curves, ClientHello profile, ALPN, signature algorithms, extension order, and ECH GREASE in one place, making it the narrowest implementation point. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:10`.
4. Test quality bar: every Phase 4/5 test must be discriminating. For ECH, a bad or missing config must make the wire/fail-closed test fail. For PQ, removing `4588` from either supported groups or key shares must make the test fail. Existing profile tests already assert `4588` in profile data, but Phase 5 needs a wire-level assertion as well. Local evidence: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:378` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:392`.
5. Package/file discipline: do not add files to frozen Go packages. Go-side changes should be limited to existing sidecar wiring/tests unless Owner approves a new coherent package. Local fail-closed behavior is already in existing files: `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/mimicry/sidecar_client.go:42` and `HUAKAI@c85ffba44ef429eee784ddbdbf5c8fd9aa76385e:backend/internal/transport/factory.go:211`.

## §D 执行序

| Step | Phase | Work | Success criteria |
| --- | --- | --- | --- |
| 0 | Preflight | Confirm no edits require DB schema, auth core, quota, billing, deployment, secrets, or new files in frozen Go packages | Scope remains low/medium risk; any high-risk need is returned to Owner before execution |
| 1 | Phase 4 | Extend sidecar profile schema with `ech_config_list_base64` and explicit failure policy | Valid base64 config loads; invalid base64 fails profile load; unknown policy fails profile load |
| 2 | Phase 4 | Apply decoded ECH config list through Boring client configuration in `tls-sidecar` | A profile with config emits real ECH behavior; profile without config preserves current GREASE-only behavior |
| 3 | Phase 4 | Add wire test for ClientHello ECH and a fail-closed rejection/missing-config path | Test proves ECH is not only GREASE; rejection does not silently fall back to Go uTLS or standard transport |
| 4 | Phase 4 | Add operator-facing profile example/docs for static config-list input and refresh-job ownership | Operators can rotate ECH config by changing profile input without sidecar hot-path DNS |
| 5 | Phase 5 | Verify existing Anthropic template and Boring sidecar wire include `4588` in supported groups and key share groups | Cargo test fails if `4588` is removed from either vector |
| 6 | Phase 5 | Compare generated sidecar wire against the captured Anthropic Node/OpenSSL fixture | Captured fixture and sidecar output agree on `4588` presence and classical X25519 fallback position |
| 7 | Production wire slice | Connect Go gateway to Rust sidecar via `Factory.SidecarSocketPath` after Phase 4/5 pass | Sidecar-enabled mode uses Boring path; sidecar unavailable remains fail-closed with observable reason |

## §E 借鉴对照

### D-P45-1 ECH Config Source

Decision: sidecar consumes `ech_config_list_base64` from profile TOML; DNS/SVCB lookup and remote refresh are external producer concerns, not per-dial sidecar behavior. 对照项目数: 4.

| Project | Evidence | Summary |
| --- | --- | --- |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3775` | Boring exposes a client-side ECH config-list input and retry-config accessors, so HUAKAI can pass explicit bytes from profile state. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/gateway_config.go:13` | Envoy keeps gateway behavior in an external processor configuration object, supporting HUAKAI's "control plane writes config; data path consumes config" split. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:189` | wreq treats ECH GREASE as an explicit TLS profile setting; this supports putting ECH-related behavior in fingerprint/profile configuration. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:config.example.yaml:220` | CLIProxyAPI stores Claude client-profile defaults in config, but no equivalent ECH config-list source was observed in the read regions. |

### D-P45-2 ECH Failure Policy

Decision: production ECH is fail-closed; audit-only fallback is permitted only as an explicit lab-mode flag outside normal routing. 对照项目数: 4.

| Project | Evidence | Summary |
| --- | --- | --- |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/test/ech.rs:47` | Boring test coverage distinguishes ECH rejection from successful negotiation and exposes retry material; HUAKAI should turn that branch into an explicit failure policy. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:216` | Envoy documents fallback through explicit backend policy, reinforcing that fallback should be configured, not implicit in TLS failure handling. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:172` | wreq can express ECH GREASE as part of a deliberate emulation profile; it does not provide evidence for silently downgrading a required ECH config. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/utls_transport.go:18` | CLIProxyAPI uses a uTLS transport for Anthropic compatibility, but the read region has no equivalent ECH rejection policy; HUAKAI should not infer permissive fallback from it. |

### D-P45-3 PQ Candidate Set

Decision: Phase 5 targets only `X25519MLKEM768` group `4588` plus the existing classical fallback; no extra PQ groups without new official-client capture evidence. 对照项目数: 4.

| Project | Evidence | Summary |
| --- | --- | --- |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/patches/boring-pq.patch:118` | Boring maps group code `0x11ec` to the hybrid X25519/ML-KEM group, matching HUAKAI's target `4588`. |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/patches/boring-pq.patch:359` | Boring's default group list includes hybrid PQ before classical X25519; HUAKAI should verify its template preserves this observable order for Anthropic mimicry. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:115` | wreq's emulation test includes the same hybrid group name in the curve list, supporting `4588` as the relevant emulation target. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:178` | wreq's key-share test data also includes the hybrid key-share candidate before classical alternatives. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/gateway_config.go:59` | No equivalent TLS group-selection issue is present in the read API config region; Envoy remains a control-plane separation reference, not a PQ candidate source. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:152` | The read CLIProxyAPI region configures Claude header/device defaults, not PQ group candidates; it provides no basis for adding more PQ groups. |

### D-P45-4 PQ Downgrade Policy

Decision: offer `4588`; if the server negotiates classical X25519, allow the request but record downgrade evidence unless the profile later declares PQ-required. 对照项目数: 4.

| Project | Evidence | Summary |
| --- | --- | --- |
| cloudflare/boring | `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/patches/boring-pq.patch:451` | Boring's default-curve test keeps hybrid and classical groups together, supporting graceful negotiation rather than requiring PQ-only behavior. |
| 0x676e67/wreq | `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:177` | wreq's key-share vector includes hybrid and classical choices, which supports HUAKAI offering PQ while retaining classical compatibility. |
| envoy-ai-gateway | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:201` | Envoy route rules make backend choice explicit; by analogy, HUAKAI should make PQ-required behavior explicit instead of treating every classical negotiation as a failure. |
| router-for-me/CLIProxyAPI | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/claude/utls_transport.go:50` | CLIProxyAPI focuses on connection reuse and host-level transport behavior, with no observed PQ-required policy; HUAKAI should define its own downgrade observability. |

## §F Owner 决策清单

| Decision | Recommendation | Owner decision needed |
| --- | --- | --- |
| D-P45-1 ECH config source | Profile TOML carries `ech_config_list_base64`; external refresh job may update the profile, but sidecar does not do live DNS/SVCB fetch per dial in Phase 4 | Confirm static-profile consumer first, refresh job as later producer |
| D-P45-2 ECH failure policy | Fail-closed for production when ECH config exists and negotiation/retry fails; lab audit-only requires an explicit non-default flag | Confirm fail-closed as product default |
| D-P45-3 PQ candidate set | Only `X25519MLKEM768` / `4588` plus existing classical X25519 in Phase 5 | Confirm no additional PQ candidates without fresh capture evidence |
| D-P45-4 PQ downgrade policy | Classical negotiation is allowed and audited when PQ was offered; fail only under future `pq_required` profile mode | Confirm audit-on-downgrade rather than upstream alert/fail by default |

## §G Lane + UTC

- Lane: `specifier`
- Agent: `codex-cli` drafting the Claude-view plan artifact
- UTC: `2026-05-24T1520Z`
- Codex counterpart plan: path and commit were provided by Owner; file contents were not read in this lane.
- Next synthesis step: compare this draft against the Codex draft and produce agreements, conflicts, gaps, then an authoritative merged plan after Owner approval.

## Clean-room Provenance
Source files read: docs/process/plans/2026-05-24-boringssl-fork-backend-claude.md; docs/process/plans/2026-05-24-auth-expired-schema-gate-claude.md; CLAUDE.md; /home/codex/refs/boring/boring/src/ssl/mod.rs; /home/codex/refs/boring/boring/src/ssl/test/ech.rs; /home/codex/refs/boring/boring-sys/patches/boring-pq.patch; /home/codex/refs/envoy-ai-gateway/api/v1beta1/gateway_config.go; /home/codex/refs/envoy-ai-gateway/api/v1beta1/ai_gateway_route.go; /home/codex/refs/wreq/src/tls.rs; /home/codex/refs/wreq/tests/emulate.rs; /home/codex/refs/CLIProxyAPI/config.example.yaml; /home/codex/refs/CLIProxyAPI/internal/config/config.go; /home/codex/refs/CLIProxyAPI/internal/auth/claude/utls_transport.go; backend/internal/transport/factory.go; backend/internal/transport/mimicry/sidecar_client.go; backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs; exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md
Lane: specifier
Agent: codex-cli
UTC: 2026-05-24T1520Z
