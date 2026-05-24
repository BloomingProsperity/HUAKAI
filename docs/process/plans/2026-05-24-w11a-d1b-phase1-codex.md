# 2026-05-24 W11-A D-1b Phase 1 Codex Plan

> **For agentic workers:** REQUIRED SUB-SKILL: implementation must use `superpowers:subagent-driven-development` or `superpowers:executing-plans` after Claude/Codex parallel plans are reconciled. This file is an independent Codex draft only; do not execute from it until the synthesized plan exists.

**Goal:** 在 Rust core gateway 中提取 client credential，写入新的 `RouteQueryRequest.client_credential` 字段，并在 Phase 1 通过 `Manual First` 静态映射完成 mock/staging/internal-smoke 兼容，Go control plane 暂时可忽略该字段。

**Architecture:** Rust listener 在发送 route query 之前先解析 client credential；缺失或无效时 fail closed 为 public 401，且不触发 route query。`account_planner` 只接收已解析的 credential 与可选 Manual First tenant，不再从 `x-tenant-id` 派生身份；`RouteQueryRequest` 双写新 `client_credential` 和 Phase 1 旧 `tenant_id`，但只有 Manual First 开启并匹配时才写旧 tenant。redaction/debug/logging 必须只输出 credential fingerprint，不输出 raw secret。

**Tech Stack:** Rust, axum, tonic/prost, proto3, existing `serde_json` config parsing, existing mock control plane test harness, optional Owner-approved `sha2` dependency for SHA-256 fingerprint/hash matching.

---

| Owner directive | "协调 go 和解析逻辑一起做" (2026-05-24) |
|---|---|
| Source constraints | `docs/process/plans/2026-05-22-rust-hardening-plan.md` §2 D-1b, §4.5 P-1, §7-H, §7-J, §8 W11-A A1-A5；`docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md` §5 P1-1 |
| Current branch observed | `claude/rust-hardening` |
| Plan lane | Plan only. No Rust code, no proto edit, no git commit in this dispatch. |
| Clean-room posture | HUAKAI-original implementation plan only. No reference-project source read or copied for this plan. |

## Scope

In scope:

- Add `RouteQueryRequest.client_credential` write path in Rust Phase 1.
- Extract client credential at listener/request boundary before route planning.
- Ignore `x-tenant-id` in all Rust identity paths.
- Add `Manual First` static key-to-tenant map as a temporary Phase 1 backstop.
- Make `Manual First` default OFF and restrict Phase 1 to mock/staging/internal-smoke, not production billable traffic.
- Update mock control plane/tests so the new proto field and old/new dual-write behavior are observable.
- Add mutation-resistant tests for A1-A5.
- Add redaction/fingerprint support so route-query debug/log output never contains raw credential.

Out of scope:

- Go control-plane consumption/authentication of `client_credential` (Phase 2).
- Production billable traffic using Phase 1 `Manual First`.
- Trusted header mode `γ`; `x-tenant-id` remains untrusted and ignored.
- Building a Rust user/auth system.
- Any non-MIT reference-source reading or implementation copying.
- Any git commit.

## Success Criteria

A1 `client credential missing -> 401, route query NOT sent`

- Public request with no `Authorization` and no `x-api-key` returns `401` with non-secret error code such as `client_credential_required`.
- `MockControlPlane::route_queries_seen()` remains `0`.
- Mutation check: delete the pre-route credential guard or allow default credential; test must fail because status is no longer 401 or route query count becomes 1.

A2 `body.model wins over x-huakai-model header`

- Keep the existing D-1a behavior and ensure the new credential path does not regress it.
- Add or extend an e2e listener test with valid credential + conflicting `body.model` and `x-huakai-model`; recorded route query must use body model.
- Mutation check: change planner back to header-first; test must fail on `requested_model`.

A3 `credential-derived tenant wins over x-tenant-id header; header always ignored`

- With `Manual First` ON and a static credential mapping to `tenant-from-key`, request also sends `x-tenant-id: attacker-tenant`.
- Recorded route query must have `tenant_id == "tenant-from-key"` and never `attacker-tenant`.
- With `Manual First` OFF, same request must not copy `x-tenant-id` into `tenant_id`; old field should be empty or another explicit non-header sentinel chosen by the implementation plan synthesis.
- Mutation check: any fallback to `header_str(headers, "x-tenant-id")` must turn the test red.

A4 `route query log/debug contains only hash/fingerprint, never raw secret`

- `RouteQueryRequest` debug/log rendering must include only kind + fingerprint, for example `bearer:sha256:<12-16 hex chars>`, never the full credential.
- Add a test with a deliberately distinctive secret string and assert all route-query debug/log helpers exclude it while including the expected fingerprint marker.
- Mutation check: generated/default `Debug` or a direct log of `client_credential` must turn the test red.

A5 `Manual First flag OFF -> old field paths inert; ON -> dual-write old+new`

- OFF: valid client credential is extracted and written to `client_credential`; `tenant_id` is not populated from headers or static map.
- ON: same credential writes both `client_credential` and `tenant_id` from the static map.
- ON with unknown credential fails closed before route query, unless synthesized plan explicitly chooses "new-field-only pass-through" for internal smoke. Recommended default is fail closed for unknown Manual First credential because Phase 1 otherwise has no authoritative local tenant.
- Mutation check: always filling `tenant_id`, ignoring the flag, or using header fallback must turn tests red.

## Proposed Contract Shape

Owner-locked default from §4.5 P-1 should be used unless Owner explicitly reopens the decision:

```proto
message RouteQueryRequest {
  string request_id = 1;
  string tenant_id = 2;
  string requested_model = 3;
  string session_hash = 4;
  string request_protocol = 5;
  bool stream = 6;
  uint64 client_deadline_ms = 7;
  repeated PreviousAttempt previous_attempts = 8;
  repeated CapabilityHint capability_hints = 9;
  string client_credential = 10;
}
```

Recommended canonical value inside the flat string:

```text
bearer:<raw bearer token>
x-api-key:<raw api key>
```

Rationale: field number `10` is the next free field after `capability_hints = 9`, and the locked plan already chose proto3 `string` over metadata. The kind prefix keeps the single field typed enough for Go Phase 2 without adding a nested message in Phase 1.

## Proposed Rust Shapes

Credential extraction module:

```rust
pub enum ClientCredentialKind {
    Bearer,
    XApiKey,
}

pub struct ClientCredential {
    pub kind: ClientCredentialKind,
    raw_secret: String,
}

impl ClientCredential {
    pub fn from_headers(headers: &HeaderMap) -> Result<Self, ClientCredentialError>;
    pub fn as_route_proto_value(&self) -> String;
    pub fn fingerprint(&self) -> ClientCredentialFingerprint;
}
```

Manual First config model:

```rust
pub struct ManualFirstConfig {
    pub enabled: bool,
    pub key_file: Option<PathBuf>,
    pub entries: Vec<ManualFirstKeyEntry>,
}

pub struct ManualFirstKeyEntry {
    pub kind: ClientCredentialKind,
    pub secret_sha256: String,
    pub tenant_id: String,
    pub label: String,
}
```

Route query builder input:

```rust
pub struct RouteIdentity {
    pub client_credential: ClientCredential,
    pub manual_first_tenant_id: Option<String>,
}

pub fn build_route_query(
    headers: &HeaderMap,
    protocol: GatewayProtocol,
    request_id: &RequestId,
    body_signal: &BodyRouteSignal,
    identity: &RouteIdentity,
) -> RouteQueryRequest;
```

## Files To Touch

Expected Rust/proto files:

- Modify: `exploratory/rust-core-gateway/merged/proto/route.proto`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/build.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_credential.rs`
- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/manual_first.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_proto/redacting_debug.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/redaction.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`

Expected tests:

- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/route_client_test.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/listener_test.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/observability_test.rs`
- Add focused unit tests inside `client_credential.rs`, `manual_first.rs`, `account_planner.rs`, and `route_proto/redacting_debug.rs`.

Conditional file:

- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml` only if Owner approves direct `sha2` dependency for SHA-256 fingerprinting and Manual First hash matching.

## Time Estimate

- Planning/synthesis review: 0.25 codex-day.
- Implementation: 1.0-1.5 codex-day.
- Test hardening and mutation self-check: 0.5 codex-day.
- Review/fix loop: 0.25-0.5 codex-day.

Total: 1.75-2.75 codex-day for a careful Phase 1 commit.

## Blast Radius

Behavioral blast radius:

- Public listener auth boundary changes from "no client credential required" to "credential required before route query".
- Existing tests that send `/v1/messages` or `/v1/chat/completions` through the non-mock control-plane path must add a valid fake credential.
- `RouteQueryRequest` generated Rust type changes; all literals constructing it need the new field.
- `build_route_query` signature changes; all direct unit tests must pass `RouteIdentity`.
- Mock control plane stores and exposes the new field through `last_route_query`.
- Debug/log rendering changes for `RouteQueryRequest`.

Operational blast radius:

- Phase 1 must not run production billable tenant traffic.
- If `Manual First` is accidentally enabled in production, Rust would temporarily hold identity authority, violating §7-H. Mitigation is config validation that rejects `HUAKAI_MANUAL_FIRST_ENABLED=true` when `HUAKAI_RUNTIME_MODE=production` unless Owner adds a separate emergency override in a later decision.

## Failure Modes And Mitigations

Missing credential still sends route query:

- Mitigation: listener extracts credential before `account_planner.plan`; A1 e2e test asserts 401 and route query count 0.

`x-tenant-id` remains trusted through an old fallback:

- Mitigation: delete `TENANT_ID_HEADER` usage from `account_planner`; A3 tests assert conflicting header loses and OFF mode does not copy header.

Raw credential leaks through generated `Debug`:

- Mitigation: add `.skip_debug(".huakai.route.v1.RouteQueryRequest")` in `build.rs` and implement manual `Debug` in `route_proto/redacting_debug.rs`.

Raw credential leaks through ad hoc logs:

- Mitigation: add a single redaction/fingerprint helper and use that helper wherever route-query credential context is logged. Reviewer should grep for `client_credential` logging and reject direct field logging.

Manual First config puts raw secrets in environment:

- Mitigation: prefer `HUAKAI_MANUAL_FIRST_KEYS_FILE` JSON parsed with existing `serde_json`, containing hashes not raw secrets. Avoid `HUAKAI_MANUAL_FIRST_KEYS` raw secret env var.

Hash/fingerprint implementation adds a dependency without approval:

- Mitigation: Owner decision required before adding `sha2`; if not approved, execution must use a no-raw placeholder redaction and mark A4 hash specificity as blocked, not silently weaken the gate.

Ambiguous credentials (`Authorization` and `x-api-key`) are accepted inconsistently:

- Mitigation: recommended behavior is fail closed with 401 `ambiguous_client_credential`; mutation test sends both and expects no route query.

Phase 1 accidentally handles billable traffic:

- Mitigation: startup validation rejects Manual First in production by default; docs/test names explicitly say mock/staging/internal-smoke only.

Go Phase 2 reads a different canonical credential string:

- Mitigation: this plan writes a flat kind-prefixed value and calls out Go contract alignment as a decision point before execution.

## Decision Points Needing Owner Sign-Off

ClientCredential proto field shape:

- Recommendation: use locked `string client_credential = 10`.
- Owner sign-off needed only if changing to `bytes`, nested message, or `oneof`.

Field number choice:

- Recommendation: use `10`, next free after existing 1..9.
- Owner sign-off needed if reserving gaps or using another number.

Credential kinds taxonomy:

- Recommendation: support `Authorization: Bearer <token>` and `x-api-key: <key>` in Phase 1; reject both-present as ambiguous.
- Owner sign-off needed if only Bearer is allowed or if both-present should prefer one.

Manual First config shape:

- Recommendation: `HUAKAI_MANUAL_FIRST_ENABLED=false` default; `HUAKAI_MANUAL_FIRST_KEYS_FILE=<json path>` for non-production static map. JSON avoids new TOML/YAML dependency because `serde_json` is already present.
- Owner sign-off needed for env JSON, TOML/YAML file, hot reload, or production emergency override.

Log redaction algorithm:

- Recommendation: SHA-256 hex prefix, 12-16 chars, over the canonical `kind:<secret>` string. Do not log raw secret prefix.
- Owner sign-off needed because adding direct `sha2` dependency is a high-risk dependency change under AGENTS.md.
- HMAC would be stronger for cross-environment correlation control but needs key management and is too large for Phase 1 unless Owner explicitly chooses it.

Default Manual First flag value:

- Recommendation: OFF. OFF means old `tenant_id` path inert and only new `client_credential` is populated; because Go ignores it in Phase 1, this is for contract smoke only, not billable flow.
- ON is allowed only for mock/staging/internal-smoke with explicit static map and startup warning.

Unknown credential when Manual First is ON:

- Recommendation: fail closed before route query because the local map is the Phase 1 canary authority.
- Owner sign-off needed if internal smoke requires forwarding unknown credentials to Go with empty `tenant_id`.

## Pre-Execution Checklist

- Confirm current branch is `claude/rust-hardening`.
- Confirm no execution agent has read `docs/process/plans/2026-05-24-w11a-d1b-phase1-claude.md` before writing independent plan.
- Wait for synthesized plan after Claude/Codex cross-discussion.
- Run baseline from `exploratory/rust-core-gateway/merged`:

```bash
cargo build
cargo test
```

- If baseline is red, record failing tests before changing code; do not hide pre-existing failures.
- Confirm Go control-plane Phase 2 is not part of this commit.
- Confirm no production billable canary is planned for Phase 1.
- Confirm whether Owner approved `sha2` or alternative fingerprint method.

## Concrete Execution Order

1. Write failing unit tests for `client_credential.rs` extraction:
   - missing credential -> `Missing`
   - Bearer credential canonicalizes to `bearer:<secret>`
   - x-api-key canonicalizes to `x-api-key:<secret>`
   - both present -> ambiguous/fail closed
   - mutation statement: accepting missing or both-present must fail tests.

2. Add `client_credential.rs` with minimal extraction/canonicalization/fingerprint API.

3. Write failing config tests in `config.rs`:
   - default `manual_first_enabled == false`
   - `HUAKAI_MANUAL_FIRST_ENABLED=true` without key file fails
   - production + Manual First enabled fails by default
   - valid development/test key file parses.

4. Add `manual_first.rs` and wire `StartupConfig` fields:
   - parse JSON file with entries `{ kind, secret_sha256, tenant_id, label }`
   - validate tenant id non-empty and no raw secret field accepted
   - expose resolver returning `Option<String>` tenant for a credential.

5. Update `proto/route.proto` with `string client_credential = 10`.

6. Regenerate/build via cargo, then update all `RouteQueryRequest` literals to include `client_credential`.

7. Update `build.rs` to skip generated `Debug` for `RouteQueryRequest` if manual redacted Debug is implemented.

8. Update `route_proto/redacting_debug.rs` with redacted `RouteQueryRequest` Debug:
   - include request id, tenant id, requested model, protocol, stream
   - include credential fingerprint only
   - exclude raw credential.

9. Update `account_planner.rs`:
   - remove `TENANT_ID_HEADER` identity fallback
   - accept `RouteIdentity`
   - populate `client_credential`
   - populate `tenant_id` only from Manual First tenant when present, else `String::new()`
   - keep existing D-1a body model precedence.

10. Update `listener.rs`:
    - after bounded body read and `BodyRouteSignal`, extract credential before planning
    - on missing/invalid/ambiguous return 401 and do not call `account_planner.plan`
    - resolve Manual First from state/config
    - pass `RouteIdentity` into planner
    - ensure mock-upstream branch remains separate and still strips vendor credentials as already tested.

11. Update `GatewayState` wiring in `lib.rs`:
    - construct Manual First resolver from config
    - expose resolver to listener or include it in AccountPlanner, whichever keeps responsibilities cleaner.

12. Update `mock_control_plane.rs` only as needed to preserve `last_route_query` visibility after proto regeneration.

13. Add/adjust A1-A5 e2e tests:
    - Prefer `route_client_test.rs` for control-plane recorded route query assertions.
    - Prefer `listener_test.rs` for public 401/no route query behavior.
    - Prefer `observability_test.rs` or unit `redacting_debug.rs` tests for A4 no raw secret.

14. Run focused tests:

```bash
cargo test -p core_gateway client_credential
cargo test -p core_gateway manual_first
cargo test -p core_gateway build_route_query
cargo test -p core_gateway listener_
cargo test -p core_gateway route_client_
cargo test -p core_gateway redaction
```

15. Run full verification:

```bash
cargo build
cargo test
```

16. Stage the single intended commit group:

```bash
git add exploratory/rust-core-gateway/merged/proto/route.proto \
  exploratory/rust-core-gateway/merged/crates/core_gateway
```

17. Run required review before commit:

```bash
codex exec review --uncommitted --full-auto
```

18. If review is clean, commit as one commit for `D-1b + Manual First + P-1 field` with this body literal:

```text
Clean-room-attestation: original HUAKAI implementation; no copied source/comments/tests/schemas from non-permissive references.
```

## Test Matrix

| Gate | Test location | Discriminating fixture | Expected mutation failure |
|---|---|---|---|
| A1 | `listener_test.rs` | request has `x-tenant-id` but no credential | deleting credential guard makes route query count 1 |
| A2 | `route_client_test.rs` or existing `account_planner.rs` unit | body model `claude-real-from-body`, header model `cheap-header-model` | header-first planner records wrong model |
| A3 | `route_client_test.rs` | credential maps to `tenant-a`, header says `tenant-b` | any header fallback records `tenant-b` |
| A4 | `route_proto/redacting_debug.rs` + redaction unit | secret `sk-phase1-raw-secret-never-log-123456` | default/direct Debug contains raw secret |
| A5 OFF | `account_planner.rs` unit or `route_client_test.rs` | Manual First disabled + valid credential + attacker header | old `tenant_id` populated despite OFF |
| A5 ON | `route_client_test.rs` | Manual First enabled + hash-matched credential | missing dual-write or wrong tenant fails |

## Assumptions

- Phase 1 may populate raw credential in the gRPC payload because §7-H β requires the Go control plane to become identity authority in Phase 2; transport is UDS by default and HTTP is loopback-only in current config validation.
- Go control plane ignores `client_credential` in Phase 1, so OFF mode is a contract smoke path, not a functioning billable route path.
- `Manual First` is temporary and should be deleted in Phase 3 after Go derives tenant authoritatively.
- The synthesized plan may adjust exact error codes, but must preserve 401/no-route-query for missing credential.

## Risks

- Clean-room risk: low if implementation remains HUAKAI-original and no non-MIT source is read or copied.
- Security risk: medium because raw client credential crosses Rust->Go boundary; mitigated by UDS/mTLS/loopback transport and log redaction.
- Production risk: high if Manual First is used for billable production; mitigated by default OFF and production startup rejection.
- Dependency risk: medium/high if `sha2` is added; requires Owner approval and license check.
- Contract risk: medium because Go Phase 2 must parse exactly the same canonical string.

## Owner Confirmation Needed

- 是否确认继续使用 locked `string client_credential = 10`，并用 `kind:<secret>` canonical string 承载 kind。
- 是否确认 Phase 1 支持 `Bearer` 和 `x-api-key`，both-present fail closed。
- 是否确认 `Manual First` 用 JSON file config，默认 OFF，production 默认拒绝启用。
- 是否批准新增 direct `sha2` dependency；若不批准，需要指定可接受的 fingerprint/hash 方法。
- 是否确认 unknown credential + Manual First ON 默认 fail closed。

## Feature Preservation / Clean-Room / Safety Notes

- 功能没有缩水：β control-plane authoritative path、Manual First transitional backstop、A1-A5 gates 都保留。
- `x-tenant-id` 不作为 Safe Equivalent；它是明确被禁用的 untrusted input。
- 本计划不读取、不复用 non-MIT reference source，也不复制 schema/test/comment。
- Phase 1 禁止真实可计费生产流量；任何例外都必须另开 auth-core 决策。

