# 2026-05-13 Trust Chain Feature Family — Codex Lane

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: sub2api / new-api / portkey

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| Field | Value |
| --- | --- |
| Owner directive | "我们的核心还有一个就是链路公开，无用户数据保留日志，模型校验用户能看到，商家无法做假，掺水，搞映射。日志只做系统报错，等等重要的东西，还有用户得消费等" |
| Scope | 独立起草 `F-TRUST-*` / `F-PRIV-*` / `F-AUDIT-*` feature family；不执行代码实现；不读取 Claude lane 同名计划。 |
| Success criteria | 6 个 Owner 要求都有 feature ID、L 级、Phase、HCSF 挂载点；每个 feature 有行为、schema、INV、验证、vendor 兼容、PASR cache 交互；sub2api/new-api/portkey 反例有源码行号；切片有依赖、风险、工程量；Owner 决策点明确。 |
| Time estimate | Plan 起草 1 Codex session；后续实现约 20-32 engineer-days，取决于 DB ledger 与签名设施是否已有。 |
| Blast radius | 后续若采纳，会触及 HCSF schema、日志边界、usage API、audit ledger、header contract、CLI/web verify；暂不触及 `LICENSE`、真实 secrets、auth core、billing ledger实现。 |
| Failure modes | 过度记录请求体导致隐私违背；签名链只覆盖 operator DB 状态导致用户不可验证；vendor 不返回模型证明却被展示成已验证；cache 命中绕过证明；公开 ledger 泄露低熵 prompt hash。 |
| Mitigation | 默认不持久化 body，仅存 commitment；证明等级分层；cache 命中签名独立事件；系统日志与审计账本分表；public proof 只发布不可逆 commitment，用户私下用本地请求重算。 |
| Decision points | 见 §8 Owner 决策点。 |
| Pre-execution checklist | 先补 acceptance tests；先锁 v0.4.1 schema；先定义签名 key 发布与轮换；再切 runtime；最后发布 CLI/web verify。 |

Metadata:
- Observed regions: 24
- Inferences: 12
- Open questions: 7
- Claude lane: not read; file existence observed only via `find`.
- Stub: `/tmp/codex-trust-chain.txt` written before source reading.

## 0. Thesis

HUAKAI 的新差异化不应被放进一般 observability。现有矩阵已经覆盖 operator dashboard、usage logging、protocol loss、model substitution disclosure 等基础能力；但 Owner 这里要求的是 **user-verifiable gateway**：用户能验证自己到底走了哪条链路、实际上游模型是什么、消费如何计算，同时商家/运营方不能靠后台日志或映射表单方面改口。

这意味着三条 feature family：

- `F-TRUST-*`: 面向用户的链路、模型、消费可验证证明。
- `F-PRIV-*`: 默认不保留用户请求/响应数据，系统日志只留故障定位元数据。
- `F-AUDIT-*`: 商家/运营方不能事后篡改链路、模型、消费、缓存命中事实。

现有 HCSF v0.4 已有基础锚点：顶层 envelope 包含 `RequestMeta`、`ProviderProjection`、`Accounting`、`Policy`，并且 `RequestMeta.UpstreamModel` 已存在（`backend/internal/proto/envelope.go:19`, `backend/internal/proto/request_meta.go:47`）。`Policy.DataRetention` 已经通过 `DataRetentionNode` 表达 request-store / contract / region / ZDR 等状态（`backend/internal/proto/policy.go:39`, `backend/internal/proto/capability_data_retention.go:3`）。`ProviderProjection` 已能表达 capability 投影结果和 loss（`backend/internal/proto/projection.go:20`）。缺口是：没有用户可验证签名链、没有公开 proof header/API、没有 system log 与 audit ledger 的硬拆分。

## 1. Owner 要求映射

| Owner 要求 | Feature ID | Lx 级 | Phase | HCSF 字段挂载点 |
|---|---|---:|---|---|
| 链路公开 | `F-TRUST-001` | L2 | Phase 5 | `Accounting.HopChain[]` + per-hop signature |
| 无用户数据日志 | `F-PRIV-001` | L1 | Phase 4 | `Policy.DataRetention.RequestStore=false` + log boundary |
| 模型校验用户可见 | `F-TRUST-002` | L2 | Phase 5 | `RequestMeta.UpstreamModel` + `ProviderProjection` + `X-HUAKAI-Upstream-Model` |
| 商家不能做假 | `F-AUDIT-001` | L2 | Phase 5 | signed dispatch ledger + append-only hash chain |
| 日志只系统报错 | `F-PRIV-002` | L1 | Phase 4 | `system_log` vs `audit_ledger` split; `Policy.Audit` label |
| 用户消费透明 | `F-TRUST-003` | L1 | Phase 4-5 | user-facing usage API + signed usage proof |

Level rationale follows `docs/17_FEATURE_LEVEL_MATRIX.md`: L1 is the smallest useful workflow, L2 is production-usable and safe enough for real use (`docs/17_FEATURE_LEVEL_MATRIX.md:13`). `F-PRIV-001/002` must be L1 because privacy boundaries are prerequisites, not polish. `F-TRUST-001/002` and `F-AUDIT-001` are L2 because signatures, public key rotation, and replay-proof verification are production controls.

## 2. Feature Specs

### F-TRUST-001 Public Hop Chain

Behavior:
- Every request produces a user-visible hop chain: inbound request ID, route decision, attempt order, selected pool/account pseudonym, upstream protocol family, actual upstream model when known, terminal status, cache status, and ledger settlement reference.
- The user sees this via response headers for simple cases and via `GET /v1/usage/{request_id}/proof` for full detail.
- The proof must be payload-minimized: no prompt, response body, tool input, image/audio bytes, raw upstream credential, or internal account secret.

HCSF schema impact:
- Requires v0.4.1 patch. Add `Accounting.HopChain []HopProof`.
- Proposed `HopProof` fields: `hop_index`, `request_id`, `attempt_id`, `lease_id`, `route_id`, `provider_code`, `upstream_protocol`, `upstream_model`, `cache_event`, `status_class`, `started_at`, `ended_at`, `dispatch_commitment`, `usage_commitment`, `prev_hash`, `signature_ref`.
- Do not put this in `Extensions`; INV-12 allows prefixed extensions only (`backend/internal/proto/envelope.go:48`), but this is a core product feature, not vendor-specific optional data.

INV/CMB relationship:
- Extends INV-5 because `RequestMeta.RequestID` is already mandatory (`backend/internal/proto/envelope_validate.go:26`).
- Must not violate CMB-1/2/3/4: Router plans, Pool claims, Executor signs attempt facts after Pool/Adapter/Ledger return, Ledger settles only via explicit event (`docs/specs/_invariants/cross-module-boundaries.md:24`, `docs/specs/_invariants/cross-module-boundaries.md:125`).
- Add new INV proposal: `INV-51 HopChain entries must form a contiguous hash chain; any non-cache upstream attempt must include attempt_id + lease_id + dispatch_commitment`.

Verification:
- Response headers: `X-HUAKAI-Proof-ID`, `X-HUAKAI-Hop-Count`, `X-HUAKAI-Proof-Sig`.
- CLI: `huakai verify --request-id ... --body-hash ... --pubkey huakai.jwks.json`.
- Web: user pastes request ID + optional local body hash; UI shows hops and signature status.
- Public key publishing: JWKS endpoint with key ID, creation time, edition scope, and revocation list.

Vendor compatibility:
- If vendor returns model/request identifiers, include them as `vendor_reported` facts.
- If vendor returns no reliable fingerprint, mark proof grade `dispatch_signed` instead of `vendor_attested`; do not claim stronger proof than observed.
- For native passthrough, proof wraps route and upstream dispatch facts; it must not parse opaque payload unless adapter already does.

PASR cache locality:
- Cache hit still emits a hop entry with `cache_event=hit`, `lease_id` empty, and `origin_proof_digest` if the cache artifact has an upstream proof.
- For cross-user cache, never reveal the origin user/request; only reveal a cache-artifact digest and policy label.

### F-PRIV-001 No User Data Logs

Behavior:
- Request body, response body, messages, tool payloads, image/audio/file bytes, and raw upstream response chunks are not persisted by default.
- Runtime may stream/transform data in memory, but persistent logs and audit ledger store only commitments, counts, status classes, and proof metadata.
- A debug capture mode, if ever needed, must be feature-flagged, time-limited, tenant-scoped, owner-approved, and visibly incompatible with "zero retention" badge.

HCSF schema impact:
- No new core field needed for the first cut: use existing `Policy.DataRetention.Value=request_store_false` with `RequestStore=false`.
- v0.4 already defines the five retention labels (`backend/internal/proto/capability_data_retention.go:3`) and `RequestStore` condition (`backend/internal/proto/capability_data_retention.go:43`).
- Add v0.4.1 optional `Policy.DataRetention.LogBoundary` only if implementers need a machine-readable split between `system_error_only`, `audit_commitment_only`, and `debug_capture`.

INV/CMB relationship:
- Existing INV-30 already requires explicit false when request-store is false (`backend/internal/proto/envelope_validate.go:733`).
- Existing INV-33 requires graph/policy consistency (`backend/internal/proto/envelope_validate.go:63`).
- CMB-5 already bans credentials in logs (`docs/specs/_invariants/cross-module-boundaries.md:133`); this feature extends the same boundary from secrets to user payload.
- Add new INV proposal: `INV-52 DataRetention request_store_false forbids persistent raw request/response fields in system logs, audit ledger, usage rows, and async queue payloads`.

Verification:
- Unit tests grep structured log fields and queue payloads.
- Acceptance test sends a unique canary phrase and asserts it does not appear in DB logs, system logs, async queue rows, or exported traces.
- User proof page displays retention grade and proof ID, not request text.

Vendor compatibility:
- Vendor-side retention cannot be guaranteed unless vendor/account contract supports it. Represent this honestly through `provider_contract_required` or `zdr_verified`, not blanket claims.
- If upstream has no no-store option, HUAKAI can still promise "HUAKAI does not retain user data"; vendor retention remains separate and visible.

PASR cache locality:
- Cache keys must use commitments, not raw prompts.
- Locality hints can be stored as `account_pin`, `account_recent`, or `global` style facts; no prompt text needed.
- Low-entropy prompt hash leakage is a risk; public ledger should publish salted or HMAC commitments while user-private proof can verify against the user's local body hash.

### F-TRUST-002 User-Visible Model Verification

Behavior:
- The response must disclose requested public model, resolved upstream model, substitution/mapping reason, and proof grade.
- If actual upstream model differs from requested model, user-visible headers and usage proof must show it.
- If vendor cannot attest model, UI says "HUAKAI signed dispatch target; vendor did not return independent model fingerprint."

HCSF schema impact:
- Reuse `RequestMeta.Model` and `RequestMeta.UpstreamModel` (`backend/internal/proto/request_meta.go:47`).
- Reuse `ProviderProjection.TargetVendor/TargetProtocol` (`backend/internal/proto/projection.go:38`).
- No new HCSF field required unless Owner wants a first-class `ModelProof` object; otherwise include model proof in `Accounting.HopChain`.

INV/CMB relationship:
- Extends INV-5 required request metadata.
- Integrates with INV-7: if a model substitution or lossy protocol projection changes semantics, a `ProtocolLossEntry` must not be silent (`backend/internal/proto/envelope_validate.go:28`).
- Must merge cleanly with `F-MODEL-SUBSTITUTION-001`, whose existing matrix row already requires explicit client header disclosure for substitution (`docs/03_FEATURE_PARITY_MATRIX.md:101`).

Verification:
- Headers: `X-HUAKAI-Requested-Model`, `X-HUAKAI-Upstream-Model`, `X-HUAKAI-Model-Proof-Grade`, `X-HUAKAI-Model-Proof-ID`.
- CLI verifies header values against signed proof.
- UI shows exact upstream model only if available; otherwise shows dispatch target and vendor-attestation gap.

Vendor compatibility:
- OpenAI-like fingerprints may be attached when present.
- Anthropic-like and many relay/upstream APIs may only echo model or return no fingerprint; proof grade must distinguish "returned model string" from "cryptographic vendor proof."
- Azure/Vertex deployment names may not equal public model names; proof should include both deployment alias and resolved upstream model where adapter can observe them.

PASR cache locality:
- Cache hits return the model proof of the cached artifact plus a fresh HUAKAI cache-hit signature.
- If cache artifact came from a different upstream model than the current public alias, serve only when compatibility policy allows and disclose the artifact upstream model.

### F-AUDIT-001 Merchant Cannot Falsify Dispatch

Behavior:
- Every dispatch and settlement event is signed into an append-only ledger.
- Operator/admin can add corrections but cannot mutate or delete original facts without producing a visible reversal/correction event.
- Users can verify proof offline against published keys.

HCSF schema impact:
- v0.4.1 should add `Accounting.HopChain` and `Accounting.LedgerRef`.
- Runtime DB likely needs an append-only table or event stream. This touches database schema and billing/audit boundaries, so execution requires Owner confirmation before implementation.

INV/CMB relationship:
- Extends CMB-4: Ledger remains the sole settlement path (`docs/specs/_invariants/cross-module-boundaries.md:125`).
- Existing F-OBS-001 already frames atomic Tx1/Tx2 settlement and immutable usage reconciliation (`docs/03_FEATURE_PARITY_MATRIX.md:48`).
- Add new INV proposal: `INV-53 audit ledger rows must include prev_hash, event_hash, signer_key_id, and signature; correction rows reference prior event_hash`.

Verification:
- CLI verifies Merkle/hash chain continuity and per-event signature.
- Web verifier fetches `/v1/audit/roots/{date}` and `/v1/usage/{request_id}/proof`.
- Daily root can be published to object storage, transparency page, or optional third-party timestamping service.

Vendor compatibility:
- Vendor disagreement is represented as a correction/adjustment event, not overwrite.
- If vendor later reports different usage, ledger appends adjustment pair and links it to original proof.

PASR cache locality:
- Cache insert, hit, invalidation, and eviction are ledger events.
- A cache hit cannot be billed as upstream dispatch unless a signed upstream-origin proof exists or the pricing policy explicitly bills cache hits differently.

### F-PRIV-002 System-Error-Only Logs

Behavior:
- System logs are for runtime failure diagnosis only: timestamp, request_id, attempt_id, error_class, provider class, HTTP status, invariant ID, retry decision, and redacted structured context.
- Usage/accounting/audit facts live in signed ledger and user-facing usage API, not free-form logs.
- Operator dashboards read from ledger/usage views, not raw request logs.

HCSF schema impact:
- Reuse `Policy.Audit` for label and visibility (`backend/internal/proto/policy.go:20`).
- Add `Policy.Audit.LogClass` in v0.4.1 only if validators need to distinguish `system_error`, `audit_event`, `usage_event`, `debug_capture`.

INV/CMB relationship:
- CMB-5 already forbids credential leakage; extend to payload and PII (`docs/specs/_invariants/cross-module-boundaries.md:133`).
- INV-40 already blocks visible text in hidden/provider-only thinking blocks (`backend/internal/proto/envelope_validate.go:67`); same spirit should cover log sinks.
- Add new INV proposal: `INV-54 system_log entries must not include messages, content blocks, request body, response body, raw headers outside allowlist, or credentials`.

Verification:
- Canary phrase test across system logs.
- Structured-log allowlist test.
- Operational drill: trigger upstream 500 and verify log has enough error metadata but no user data.

Vendor compatibility:
- Upstream error response bodies can contain snippets of user input. Store error class/code and bounded sanitized message only.
- For debugging vendor incidents, add "manual first" secure capture workflow with explicit Owner approval.

PASR cache locality:
- Cache errors log cache layer status and key class only, not cache key material or prompt-derived data.

### F-TRUST-003 Transparent User Spend

Behavior:
- User can query per-request consumption: request_id, requested model, upstream model/proof grade, input/output/cache/reasoning tokens, price version, rate multiplier, cache-hit pricing, balance delta, correction events, and signature.
- Response headers expose proof ID immediately; full record may appear after settlement if streaming.
- No raw prompt/response body is required for spend verification.

HCSF schema impact:
- Reuse `Accounting.Usage`, `UsageSource`, `ReasoningTokens`, and `EvidenceLabel` (`backend/internal/proto/accounting.go:5`).
- Add `Accounting.PricingSnapshotRef`, `Accounting.SettlementRef`, and `Accounting.UsageProofSig` in v0.4.1 if not already represented elsewhere.

INV/CMB relationship:
- Extends F-BILL-001 versioned pricing context (`docs/03_FEATURE_PARITY_MATRIX.md:42`).
- Extends F-OBS-001 Tx1/Tx2 settlement and client bill attribution (`docs/03_FEATURE_PARITY_MATRIX.md:48`).
- Must not bypass Ledger; usage API is read-side projection only.

Verification:
- User API: `GET /v1/usage`, `GET /v1/usage/{request_id}`, `GET /v1/usage/{request_id}/proof`.
- CLI verifies spend calculation from signed pricing snapshot + usage tuple.
- Web shows correction history instead of overwriting totals.

Vendor compatibility:
- If upstream usage is missing, record `estimated` with estimator version and later correction event when authoritative usage arrives.
- If vendor token fields differ, HCSF maps them into canonical usage with evidence labels.

PASR cache locality:
- Cache read/creation tokens and cache-hit pricing are first-class in the usage tuple.
- Cache-locality benefit can be shown without disclosing prompt; user sees "served from cache artifact digest X under policy Y".

## 3. Reference Counterexamples

### sub2api trust gap

Observed behavior:
- The regular-user usage DTO exposes request ID, requested model, account ID, tokens, costs, endpoint, timing, and related shallow objects, but the actual upstream model and mapping chain are in the admin DTO rather than the regular-user DTO (`Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/types.go:358`, `Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/types.go:424`).
- The user DTO mapper chooses the requested model for user output, while the admin mapper appends upstream-model and mapping-chain data (`Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/mappers.go:561`, `Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/mappers.go:625`).
- The forwarding path applies an operator/account-driven model redirection before upstream dispatch and records debug fields for original and mapped model (`Wei-Shaw/sub2api@18790386a76f:backend/internal/service/gateway_forward_as_chat_completions.go:62`).
- Aggregation code can choose requested/upstream/mapping dimensions for dashboard-style reporting (`Wei-Shaw/sub2api@18790386a76f:backend/internal/repository/usage_log_repo.go:3279`).

HUAKAI implication:
- sub2api is strong on operator accounting, but the user-facing surface is not a cryptographic route/model proof. HUAKAI should not merely show "usage row"; it must expose signed upstream-model and hop proof to the user.

### new-api trust gap

Observed behavior:
- The log record stores model, quota, token counts, channel, request IDs, and an extensible metadata string (`Calcium-Ion/new-api@aa56667b8f23:model/log.go:19`).
- Consume logging writes content, model, quota, token counts, channel, request IDs, IP depending on user setting, and metadata into the log database (`Calcium-Ion/new-api@aa56667b8f23:model/log.go:192`, `Calcium-Ion/new-api@aa56667b8f23:model/log.go:207`).
- Model redirection can follow an operator-configured chain and then rewrite the outgoing request model (`Calcium-Ion/new-api@aa56667b8f23:relay/helper/model_mapped.go:16`).
- Both admin and self log endpoints read from stored logs; the user endpoint filters by current user and query parameters (`Calcium-Ion/new-api@aa56667b8f23:controller/log.go:13`, `Calcium-Ion/new-api@aa56667b8f23:controller/log.go:36`).

HUAKAI implication:
- new-api is log-centric and operationally useful, but it mixes consumption records with mutable database log semantics and does not provide a signed, user-verifiable dispatch chain. HUAKAI should split system logs from audit ledger and make model redirection provable to the user.

### portkey trust gap

Observed behavior:
- The gateway log object schema includes provider options, transformed request body/headers, final request body, original response body, response object, cache status, selected option index, cache metadata, hook span, and execution time (`Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:9`).
- Runtime log object creation copies provider option data, transformed request, final request, original response body, response clone, cache status, selected option index, and timing into a log object (`Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:165`).
- Request context merges operator/provider override parameters into the effective request params (`Portkey-AI/gateway@351692fd9236:src/handlers/services/requestContext.ts:47`).
- The log middleware broadcasts a request-options bundle and response summary to connected log clients for `/v1/` traffic (`Portkey-AI/gateway@351692fd9236:src/middlewares/log/index.ts:50`).

HUAKAI implication:
- Portkey provides rich operator/developer observability, but that is not equivalent to privacy-minimized, user-verifiable proof. HUAKAI should avoid raw body logging by default and treat operator override/model rewrite as signed, user-visible facts.

## 4. Implementation Slices

| Slice | Scope | Est. engineer-days | Dependencies | Main risk | Mitigation |
| --- | --- | ---:| --- | --- | --- |
| Slice 1 | `Policy.DataRetention` enforcement + log redaction boundary | 3-5 | HCSF validators; structured logger; existing tests | Canary phrase leaks via error/body dump | log allowlist, DB grep tests, async payload grep |
| Slice 2 | `Accounting.HopChain` fields in HCSF v0.4.1 patch | 2-4 | HCSF schema freeze; INV-51 tests | schema churn before P-2 adapters settle | additive fields, fixture migration only |
| Slice 3 | `X-HUAKAI-Upstream-Model` + proof headers + dispatch signature | 4-6 | Router/Executor attempt IDs; signer service; public key endpoint | overclaiming vendor proof | proof-grade enum and vendor-attestation gap |
| Slice 4 | user-facing usage/proof API endpoint | 4-6 | Obs Reader; billing usage rows; auth subject resolver | exposing cross-tenant proof data | strict ownership checks, redacted public view |
| Slice 5 | audit ledger Merkle/hash chain | 5-8 | DB schema; append-only event writer; daily root job | billing ledger/schema high risk | Owner approval; migration plan; append-only correction model |
| Slice 6 | public verify CLI/web | 2-3 | JWKS endpoint; proof API; stable canonicalization | verifier drift from server canonicalization | golden fixtures, versioned proof schema |

Execution order:
1. Slice 1 must precede all public trust claims.
2. Slice 2 locks schema before runtime emits proof.
3. Slice 3 can ship header-only MVP after Slice 2.
4. Slice 4 makes proof queryable by user and should land before marketing any "transparent spend" claim.
5. Slice 5 is the anti-falsification core and needs Owner approval because it touches schema/audit/billing boundaries.
6. Slice 6 is the public adoption layer; not required for internal alpha, required for product differentiation.

## 5. De-dup / Merge With Existing Features

| Existing feature | Coverage today | Proposal |
| --- | --- | --- |
| `F-OBS-001` | Covers operator dashboard, Tx1/Tx2, usage/billing invariants, and immutable reconciliation direction (`docs/03_FEATURE_PARITY_MATRIX.md:48`). | Do not supersede. `F-TRUST-003` becomes user-facing proof/read API on top of F-OBS-001. |
| `F-BILL-001` | Covers versioned pricing context and historical replay safety (`docs/03_FEATURE_PARITY_MATRIX.md:42`). | Merge pricing snapshot proof into `F-TRUST-003`; no duplicate pricing engine. |
| `F-OBS-002` | Covers OpenTelemetry export (`docs/03_FEATURE_PARITY_MATRIX.md:87`). | Keep operator telemetry separate. `F-PRIV-002` constrains what telemetry may contain. |
| `F-PROTO-002` | Covers protocol translation and explicit protocol loss (`docs/03_FEATURE_PARITY_MATRIX.md:64`). | Reuse its `protocol_loss` contract for user-visible model/capability proof; do not rename. |
| `F-MODEL-SUBSTITUTION-001` | Already requires explicit substitution disclosure headers (`docs/03_FEATURE_PARITY_MATRIX.md:101`). | Merge into `F-TRUST-002` proof display; keep feature ID because substitution has broader routing semantics. |
| `F-COMPAT-001` | Warm-up interception has audit row and synthetic response replay hook (`docs/03_FEATURE_PARITY_MATRIX.md:111`). | It must emit HopChain entries when enabled, because synthetic responses are otherwise easy to confuse with upstream dispatch. |
| `F-AUTH-006` | OAuth bootstrap and client identity mimicry are operator-risk features (`docs/03_FEATURE_PARITY_MATRIX.md:112`). | Trust proof must disclose account/pool pseudonym and model proof without exposing credentials or legal-risk internals. |

New feature family rows should be added to `docs/03_FEATURE_PARITY_MATRIX.md` only after Owner approves this family. Suggested dispositions:
- `F-TRUST-001`: Implemented Better, Status Open L2 Phase 5.
- `F-TRUST-002`: Implemented Better, Status Open L2 Phase 5.
- `F-TRUST-003`: Implemented Better, Status Open L1 Phase 4 / L2 Phase 5.
- `F-PRIV-001`: Implemented Better, Status Open L1 Phase 4.
- `F-PRIV-002`: Implemented Better, Status Open L1 Phase 4.
- `F-AUDIT-001`: Implemented Better, Status Open L2 Phase 5.

## 6. Acceptance Test Direction

- `AT-PRIV-001-canary`: send unique canary in prompt; assert no DB/system-log/queue/trace persistence.
- `AT-PRIV-002-error-only`: trigger upstream 500; assert system log has request_id/error_class/status and no body.
- `AT-TRUST-001-hop-chain`: fallback request produces contiguous signed hop chain with attempt order.
- `AT-TRUST-002-model-proof`: mapped/substituted model returns requested/upstream/proof-grade headers and proof API values.
- `AT-TRUST-003-cache-hit-proof`: cache hit emits cache proof and never pretends upstream dispatch occurred.
- `AT-TRUST-004-usage-proof`: user verifies cost from signed usage tuple and pricing snapshot.
- `AT-AUDIT-001-tamper`: mutate one ledger event in test DB; verifier rejects chain.
- `AT-AUDIT-002-correction`: vendor late usage correction appends adjustment event and preserves original event.

## 7. Open Questions / Risk Notes

1. Public commitments: raw SHA-256 of prompt bodies can be dictionary-attacked for low-entropy prompts. Need Owner decision on HMAC vs client-supplied public hash vs user-private proof secret.
2. Signature scope: sign per attempt, per request, daily Merkle root, or all three?
3. Key custody: Personal Edition local file key is simple; SaaS Edition should use KMS/HSM or cloud KMS.
4. Cross-user cache: proof should avoid origin leakage; may need tenant-only cache for early release.
5. Vendor attestation: many providers do not sign model identity. HUAKAI must distinguish dispatch proof from vendor proof.
6. DB migration: audit ledger hash chain needs schema. This is high-risk under AGENTS.md and requires Owner approval before implementation.
7. Debug capture: if Owner wants operator incident capture, it conflicts with zero-retention marketing unless off by default and visibly marked.

## 8. Owner 决策点

1. 签名算法：Ed25519 JWS/JWKS（推荐）还是 P-256 / HMAC-only internal?
2. 私钥存储：Personal Edition 本地 encrypted file，还是从第一版就接 KMS?
3. public verify 是否需要第三方 timestamp / transparency log，还是 v1 只发布 HUAKAI daily root?
4. public ledger 默认开还是只对用户本人 proof 开？SaaS 租户是否可关闭？
5. Personal vs SaaS Edition 边界：Personal 是否必须公开 JWKS，SaaS 是否必须强制 Merkle daily root？
6. cache proof 策略：允许跨用户 cache proof 只显示 artifact digest，还是 Phase 4-5 先限制 per-user/per-tenant cache？
7. vendor 无模型 fingerprint 时，产品文案是否允许说“模型校验”，还是必须说“HUAKAI dispatch proof + vendor returned model when available”？
8. debug capture 是否存在？如果存在，Owner 是否接受 debug mode 与 `F-PRIV-001` 零保留 badge 互斥？

## 9. Concrete Next Plan After Owner Approval

1. Add six rows to feature parity matrix and feature level matrix.
2. Draft HCSF v0.4.1 mini-spec for `Accounting.HopChain`, proof grade enum, and audit ledger refs.
3. Write acceptance tests before implementation.
4. Implement Slice 1 as safe low-risk privacy boundary.
5. Stop for Owner confirmation before Slice 5 database/audit-ledger migration.

## 10. Source Coverage Proof

HUAKAI docs/code read:
- `docs/01_PROJECT_BRIEF.md`: product identity, dual business model, no silent feature drop.
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`: Router/Pool/Executor/Ledger responsibility split, 3-ID audit chain, current implementation state.
- `docs/03_FEATURE_PARITY_MATRIX.md`: existing F-OBS/F-BILL/F-PROTO/F-COMPAT/F-AUTH rows and disposition rules.
- `docs/17_FEATURE_LEVEL_MATRIX.md`: L1/L2/L3/L4 definitions and capability levels.
- `docs/specs/_invariants/cross-module-boundaries.md`: CMB-1..5 and 3-ID chain.
- `backend/internal/proto/envelope.go`: HCSF v0.4 top-level fields.
- `backend/internal/proto/accounting.go`: usage/accounting fields.
- `backend/internal/proto/policy.go`: audit/data-retention/redaction policy fields.
- `backend/internal/proto/protocol_loss.go`: no silent protocol loss semantics.
- `backend/internal/proto/capability_graph.go`: capability graph and data-retention family.
- `backend/internal/proto/request_meta.go`: request/upstream model fields.
- `backend/internal/proto/projection.go`: provider projection fields.
- `backend/internal/proto/capability_data_retention.go`: retention vocabulary and request-store field.
- `backend/internal/proto/envelope_validate.go`: INV-5/7/10/30/31/32/33/40/45 validation anchors.

Reference source read:
- `Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/types.go`
- `Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/mappers.go`
- `Wei-Shaw/sub2api@18790386a76f:backend/internal/service/gateway_forward_as_chat_completions.go`
- `Wei-Shaw/sub2api@18790386a76f:backend/internal/repository/usage_log_repo.go`
- `Calcium-Ion/new-api@aa56667b8f23:model/log.go`
- `Calcium-Ion/new-api@aa56667b8f23:relay/helper/model_mapped.go`
- `Calcium-Ion/new-api@aa56667b8f23:controller/log.go`
- `Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts`
- `Portkey-AI/gateway@351692fd9236:src/handlers/services/requestContext.ts`
- `Portkey-AI/gateway@351692fd9236:src/middlewares/log/index.ts`

Truth-first note:
- Observed: upstream logging/model-redirection surfaces cited above; HCSF existing fields and INV constraints cited above.
- Inferred: "trust gap" conclusions compare observed upstream behavior against Owner's user-verifiable requirement; they are product-fit inferences, not claims about hidden upstream intent.
- Open questions: 7 listed in §7.

中文 Owner 摘要：本 Codex lane 独立提出 `F-TRUST-* / F-PRIV-* / F-AUDIT-*` 六项 feature family，把 Owner 的“链路公开、无用户数据日志、模型校验用户可见、商家不能做假、日志只系统报错、用户消费透明”映射为 HCSF v0.4.1 proof/audit/privacy 方案；真实观察来自 HUAKAI HCSF/矩阵/CMB 与 sub2api/new-api/portkey 源码行号，合理推断是这些项目偏 operator-centric、HUAKAI 应升级为 user-verifiable gateway；open question 共 7 个，最高优先级是签名算法、proof commitment 隐私、audit ledger schema 是否批准。

Source files read: docs/01_PROJECT_BRIEF.md; docs/02_HUAKAI_FUSION_ARCHITECTURE.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/17_FEATURE_LEVEL_MATRIX.md; docs/specs/_invariants/cross-module-boundaries.md; backend/internal/proto/envelope.go; backend/internal/proto/accounting.go; backend/internal/proto/policy.go; backend/internal/proto/protocol_loss.go; backend/internal/proto/capability_graph.go; backend/internal/proto/request_meta.go; backend/internal/proto/projection.go; backend/internal/proto/capability_data_retention.go; backend/internal/proto/envelope_validate.go; Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/types.go; Wei-Shaw/sub2api@18790386a76f:backend/internal/handler/dto/mappers.go; Wei-Shaw/sub2api@18790386a76f:backend/internal/service/gateway_forward_as_chat_completions.go; Wei-Shaw/sub2api@18790386a76f:backend/internal/repository/usage_log_repo.go; Calcium-Ion/new-api@aa56667b8f23:model/log.go; Calcium-Ion/new-api@aa56667b8f23:relay/helper/model_mapped.go; Calcium-Ion/new-api@aa56667b8f23:controller/log.go; Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts; Portkey-AI/gateway@351692fd9236:src/handlers/services/requestContext.ts; Portkey-AI/gateway@351692fd9236:src/middlewares/log/index.ts
Lane: specifier
Agent: GPT-5 Codex / Codex lane
UTC timestamp: 2026-05-13T07:55:00Z
