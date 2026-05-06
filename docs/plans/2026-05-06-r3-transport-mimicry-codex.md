# 2026-05-06 R3 Transport Mimicry Plan - Codex Lane

| Owner directive | "You are CODEX LANE drafting the R3 transport mimicry plan independently." |
| --- | --- |
| Isolation rule | I did not read `docs/plans/2026-05-*-r3-transport-mimicry-claude.md`. |
| Codex decision | Codex cannot provide an implementation plan for impersonating Claude Code transport fingerprints to bypass Anthropic detection or ban controls. This plan preserves the project decision process by converting R3 into a Safe Equivalent / Mandatory Roadmap gate: compliance-safe upstream transport policy, operator-visible risk controls, and a hard stop before any TLS/HTTP2 fingerprint spoofing work. |
| Local context read | `docs/01_PROJECT_BRIEF.md`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/10_RISK_REGISTER.md`, `docs/12_AGENT_WORKFLOW.md`, `docs/specs/upstream-credential-management.md` references, `backend/internal/auth/auth.go`, `backend/internal/gateway/*` surface. |
| Success criteria | R3 has an Owner-reviewable plan that states what is blocked, what safe work can proceed, what tests/gates are required, and what explicit Owner/legal authorization would be needed before any transport impersonation proposal is reconsidered. |
| Blast radius | Upstream provider path, account-pool survivability, legal/compliance posture, outbound network stack, observability, and operator configuration. |
| Required Owner decision | Whether HUAKAI must continue with a compliance-safe official upstream path, or whether Owner wants to obtain written authorization from the upstream provider before any identity-matching transport work is revisited. |

## 1. Scope and Constraints

R3, as requested, targets transport-layer identity: TLS ClientHello fingerprints, HTTP/2 connection preface and SETTINGS behavior, ALPN, SNI, and lower-level connection characteristics. The explicit goal in the prompt is to make HUAKAI's Anthropic upstream requests appear to originate from the real Claude Code CLI so that pooled-account usage survives upstream detection.

Codex cannot draft executable steps, collector designs, library configurations, or validation gates whose purpose is to defeat Anthropic detection or impersonate a first-party client process. The prompt itself states the pooled access violates Anthropic ToS and that "pass = survive, fail = banned"; that makes the requested R3 implementation a detection-evasion capability, not ordinary compatibility engineering.

Safe R3 scope:

- Define an outbound transport policy boundary for provider paths.
- Restrict any existing "mimicry" terminology to already-built application-layer transforms until Owner/legal reclassifies it.
- Preserve non-Anthropic vendors on normal `net/http` transport.
- Add documentation and tests that ensure OpenAI, Vertex, Bedrock, and other non-Anthropic vendors never enter a transport-mimicry branch.
- Add observability for outbound transport mode, without collecting or replaying real Claude Code fingerprints.
- Gate any future impersonation work behind explicit written authorization from the upstream provider and a new Owner-approved plan.

Out of scope:

- Non-Anthropic vendors do not need transport mimicry.
- No Claude Code packet capture collection.
- No JA3/JA4 matching implementation.
- No HTTP/2 SETTINGS order spoofing implementation.
- No uTLS ClientHello replay or byte-level fingerprint templates.
- No plan to bypass provider bans, abuse controls, or ToS enforcement.

Disposition recommendation:

- R3 transport impersonation: `Mandatory Roadmap` only after legal/provider authorization.
- R3 safe transport policy and vendor scoping: `Safe Equivalent`, proceed now.
- R7 body transform already implemented in `backend/internal/gateway/`: leave unchanged in this plan, but require legal-risk labeling in docs/config before release.

## 2. Threat Model

Signals named by the Owner prompt:

- JA3 and JA4 client fingerprint signals.
- JA4S server-side pairing observations.
- HTTP/2 SETTINGS frame contents and ordering.
- ALPN negotiation.
- SNI value.
- TLS extension ordering.
- `signature_algorithms` list.
- GREASE presence and values.
- `key_share` groups.
- `supported_versions`.
- `ec_point_formats`.
- TLS padding behavior.
- TCP-layer signals such as initial window, MSS, connection timing, burst shape, and reuse behavior.

Codex interpretation:

- These are real categories of passive and active transport fingerprinting.
- In a legitimate compatibility project, these signals can be used to diagnose broken enterprise proxies, TLS termination, or protocol negotiation failures.
- In this R3 prompt, the same signals are explicitly framed as controls to defeat so Anthropic cannot distinguish HUAKAI from Claude Code. Codex will not provide bypass methods for those controls.

Safe threat model to document instead:

- Upstream provider may identify policy-violating pooled usage.
- Operator may lose Anthropic accounts if they route pooled traffic through unauthorized paths.
- Corporate MITM, antivirus TLS inspection, or egress proxies may alter outbound TLS and cause unexplained upstream failures.
- Mis-scoped mimicry flags may accidentally affect OpenAI, Vertex, Bedrock, or other providers.
- A custom transport stack may break streaming reliability, timeout semantics, or billing settlement.

TCP-layer treatment:

- Initial window, MSS, congestion behavior, retransmission timing, and pacing are usually dominated by OS, kernel, NIC, container runtime, NAT, proxy, and cloud egress path.
- Chasing these for impersonation would expand blast radius into deployment/network operations and is not appropriate for HUAKAI.
- Safe handling is diagnostic only: log coarse egress path metadata owned by HUAKAI, not imitate a proprietary client network stack.

## 3. Capture Plan

Requested collector design is not provided because its requested purpose is to collect real Claude Code fingerprints for replay/matching. Codex recommends not building or running that collector.

Safe diagnostic alternative:

- Build an internal egress diagnostic mode that checks HUAKAI's own outbound connectivity to configured providers.
- Inputs: provider target, egress node ID, timeout budget, optional operator-supplied proxy URL.
- Outputs: DNS resolution status, TCP connect latency, negotiated protocol family as reported by the client library, HTTP status class, timeout/error class, and whether a configured corporate proxy or TLS inspection endpoint appears to be in use.
- Redaction: no credentials, no request bodies, no raw ClientHello bytes, no upstream account identifiers in exported diagnostics.
- Storage: short retention in ops logs, tenant-scoped, RBAC-gated.

Operator runbook:

1. Confirm whether Anthropic use is through an officially authorized API path.
2. Disable any transport impersonation flags; they should not exist in release builds without a separate authorization record.
3. Run HUAKAI egress diagnostics against the provider endpoint using a non-sensitive health request.
4. If the network path is behind corporate MITM or antivirus TLS inspection, record it as an operator environment issue.
5. If TLS inspection is present, do not attempt to "correct" it by spoofing another client; either use an approved clean egress path or disable that provider route.

MITM detection, safely framed:

- Treat unexpected enterprise CA roots, proxy configuration, or inconsistent negotiated protocol as evidence that the local environment is modifying traffic.
- Use that only to explain failures and route selection, not to tune a mimicry template.

## 4. Library Selection

Do not add `utls` or uTLS forks for Anthropic impersonation in R3. Do not fork `net/http2` for SETTINGS ordering mimicry. Adding such dependencies would be high-risk under the project rules because it changes the outbound identity of provider calls and introduces a runtime dependency for evasion-sensitive behavior.

Safe v1 library posture:

- Continue using Go standard `net/http` for ordinary provider clients.
- Keep provider-specific request shaping at protocol/application layers that are documented and allowed by provider APIs.
- Put transport selection behind an interface so OpenAI, Vertex, Bedrock, and Anthropic official API calls can share standard behavior.
- Add a config validator that rejects any transport mode named or behaving like client impersonation unless a future legal/provider authorization artifact exists.

Potential safe package layout:

- `backend/internal/transportpolicy`: provider-scoped transport mode validation and factory.
- `backend/internal/providerclient`: upstream HTTP client construction, consuming `transportpolicy`.
- `backend/internal/opsdiag`: redacted egress diagnostics.

HTTP/3 / QUIC:

- No R3 implementation coverage now.
- Risk if ignored: if a provider's official API requires HTTP/3 in the future, HUAKAI may fail compatibility.
- Safe handling: treat HTTP/3 as a future official-protocol compatibility item, not an impersonation surface.

## 5. Implementation Plan

Minimum safe implementation, not transport mimicry:

1. Add an R3 design note or spec amendment marking transport impersonation blocked pending legal/provider authorization.
2. Add `transportpolicy` types:
   - `ProviderCode`
   - `TransportModeStandard`
   - `TransportModeDiagnosticsOnly`
   - explicit rejection for `TransportModeImpersonateClaudeCode` unless future authorization exists.
3. Add config validation:
   - Anthropic route may use only standard official API transport or diagnostics-only mode.
   - OpenAI / Vertex / Bedrock routes must never select Anthropic-specific transport behavior.
4. Add audit fields for selected transport mode:
   - `transport_mode`
   - `provider_code`
   - `egress_node_id`
   - `diagnostics_run_id` when applicable.
5. Add tests:
   - Anthropic impersonation mode is rejected by default.
   - Non-Anthropic providers cannot enter Anthropic-specific code paths.
   - Diagnostics mode redacts credentials and body content.
   - Standard transport remains the default for every provider.

Integration points:

- Current `backend/internal/gateway` forwarder consumes an `io.Reader`; it is not yet the component that owns upstream HTTP dialing.
- The safe integration point is the future upstream request executor / provider client that obtains the upstream response body before passing it into `gateway.StreamForwarder`.
- `backend/internal/auth.MimicryEngine` currently documents body-level transforms only. Do not expand that interface into transport behavior without a new reviewed spec.

Randomization strategy:

- No replay pool, no byte-for-byte ClientHello capture, no GREASE perturbation plan.
- Safe diagnostics may record coarse library/runtime versions for supportability, but must not optimize toward a first-party client fingerprint.

Connection reuse policy:

- Use standard Go client pooling semantics for official API calls.
- Tune only for reliability, latency, stream lifetime, and resource usage.
- Do not choose keep-alive vs per-request connections to match Claude Code identity.

## 6. High-Concurrency Considerations

Safe concerns that remain valid:

- TLS handshakes are CPU-expensive relative to reused HTTP/2 connections.
- Per-request connection churn increases latency, socket pressure, and upstream error rates.
- Connection pools should be scoped by provider, egress node, route policy, and credential/account where needed for isolation.
- Existing storm-control ideas in `gateway/singleflight.go`, token buckets, and F-AUTH-005 A07 remain relevant for refresh bursts and can inspire connection warmup guards.

Recommended safe controls:

- Reuse standard connections where provider API and account isolation allow it.
- Cap concurrent dials per `(egress_node, provider, account_or_pool)` to avoid thundering-herd handshakes.
- Use singleflight-like suppression for simultaneous warmups after deploy or egress failover.
- Expose metrics: active conns, idle conns, dial errors, TLS handshake latency, first-byte latency, stream duration, and pool wait.
- Horizontal scaling should prefer stable egress assignment per account/pool for reliability and observability, not fingerprint imitation.

## 7. Validation Plan

Do not validate "HUAKAI JA3 equals Claude Code JA3". That would validate evasion.

Safe validation:

- Unit tests for provider scoping:
  - Anthropic official transport mode accepts standard config.
  - Anthropic impersonation config fails closed.
  - OpenAI / Vertex / Bedrock do not load Anthropic transport settings.
- Integration tests with local `httptest` upstream:
  - Standard transport streams through the future provider client into `gateway.StreamForwarder`.
  - Timeouts and cancellation preserve billing/settlement invariants.
  - Diagnostics mode never sends real credentials.
- Ops tests:
  - Diagnostic output is redacted.
  - Corporate proxy / TLS inspection is reported as environment metadata only.
  - Feature flag defaults keep unsafe modes unavailable.

OCAW gate criteria:

- `PASS`: no transport impersonation implementation exists; standard transport works; provider scoping tests pass; diagnostics redaction tests pass.
- `BLOCK`: code can replay or tune Claude Code TLS/HTTP2 fingerprints; non-Anthropic routes can enter Anthropic-specific transport logic; diagnostics emit raw credentials/body/fingerprint templates.
- `OWNER DECISION REQUIRED`: any proposal to add uTLS, fork HTTP/2 for frame ordering, collect Claude Code packet captures, or compare HUAKAI outbound fingerprints to Claude Code.

## 8. Risks and Rollback

Risks:

- Legal/compliance: transport impersonation is explicitly tied to ToS-violating pooled access in the prompt.
- Security: packet capture tools may collect credentials or sensitive prompts if misused.
- Reliability: custom TLS/HTTP2 stacks can break streaming, proxies, connection reuse, and timeout behavior.
- Operability: low-level fingerprints vary by OS, cloud, container, proxy, NAT, and antivirus tooling.
- License/dependency: new transport libraries require license audit and maintenance review.
- Scope creep: "Anthropic only" logic could accidentally contaminate OpenAI / Vertex / Bedrock paths.

Rollback:

- Safe v1 uses standard `net/http`; rollback is keeping or restoring standard transport.
- Config switch: `transport.mode=standard` should be the only release-ready provider mode.
- Diagnostics can be disabled per deployment.
- Any future experimental transport module must be behind build tags and release-disabled until legal/provider authorization exists.

## 9. Sequencing

Blocking decisions:

1. Owner/legal must decide whether any transport impersonation work is allowed at all.
2. If not authorized, proceed only with Safe Equivalent work.
3. If Owner claims authorization, require a written authorization artifact and a new cross-discussed plan before any implementation details are drafted.

Minimum viable safe v1:

1. Add R3 safe-equivalent documentation.
2. Add config validation that blocks transport impersonation modes.
3. Add provider scoping tests for Anthropic vs non-Anthropic routes.
4. Add redacted egress diagnostics for operator troubleshooting.
5. Add release-gate language that forbids JA3/JA4/HTTP2 fingerprint matching as a shipping criterion.

Full safe v2:

1. Build provider-client abstraction around standard transport.
2. Add per-provider transport metrics and egress health dashboards.
3. Add connection-pool controls for concurrency and reliability.
4. Add failure-mode tests for proxies, timeouts, stream cancellation, and dial storms.
5. Revisit HTTP/3 only when needed for an official provider API compatibility requirement.

## 10. Open Questions for Owner

1. Should R3 be reclassified from "transport mimicry" to "transport policy + compliance-safe diagnostics" for the next synthesized plan?
2. Does Owner have written permission from Anthropic to route pooled account traffic through HUAKAI while impersonating Claude Code transport identity? If not, Codex recommends blocking that path.
3. Should existing R7 body-level mimicry remain in code, or should it be feature-flagged off pending legal review?
4. Where should the authoritative R3 disposition live: `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/10_RISK_REGISTER.md`, or a new `docs/specs/transport-policy.md`?
5. Who owns the safe v1 implementation: Claude for backend architecture, Codex for tests/review, or both after synthesized plan approval?
6. Should release gates explicitly fail any build containing uTLS / HTTP2 frame-order spoofing dependencies unless an authorization artifact is present?

## Codex Assumptions

- The prompt's statement that pooled access violates Anthropic ToS is accurate.
- R7 body transform code already exists under `backend/internal/gateway/`, but upstream HTTP dialing is not yet owned by that forwarder layer.
- Non-Anthropic providers are out of R3 transport scope per Owner constraint.
- This document is an independent Codex lane draft and should be diffed against Claude's independent plan by a coordinating lane before execution.

## Chinese Owner Summary

本 Codex lane 计划没有提供 Claude Code TLS/HTTP2 指纹采集、复刻、绕过检测或封禁规避的实现路线；因为 Owner prompt 明确说明该目标服务于违反 Anthropic ToS 的账号池化，并且以“检测失败即封号”为对抗目标。Codex 建议把 R3 改成 Safe Equivalent：标准官方 upstream transport、强 provider scope、防止非 Anthropic 路径被污染、红acted egress diagnostics、默认拒绝任何 transport impersonation 配置；真正的 Claude Code transport impersonation 只能在 Owner 提供上游书面授权和新的交叉讨论计划后重新进入设计。功能没有被静默删除，而是标记为高风险 Mandatory Roadmap / Safe Equivalent；clean-room 风险低，因为未读参考项目源码、未复制外部实现；安全/合规风险高，需 Owner 明确确认是否接受合规替代路线并决定 R7 body mimicry 是否继续保留或先 feature-flag off。
