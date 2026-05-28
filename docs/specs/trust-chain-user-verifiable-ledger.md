# Trust Chain User-Verifiable Ledger — F-TRUST-001 Spec

| 字段 | 值 |
| --- | --- |
| Feature ID | F-TRUST-001 trust chain user-verifiable ledger |
| Current slice | TRUST-B-5 docs closeout for TRUST-A + TRUST-B |
| Lane | implementer (docs only) |
| UTC | 2026-05-28T02:39:40Z |
| Implementation base | TRUST-A + TRUST-B commits 67659a6 -> 780f7d6, no backend/frontend edits in this slice |
| Owner decisions | D-1..D-9 + D-B-* from 2026-05-27 brief are adopted here |
| Observed regions | 37 source/test/doc regions listed in §14 |
| Inferences | 5, explicitly marked |
| Open questions | 2, listed in §13 |

## 1. Problem

HUAKAI 的 trust chain 目标不是普通 observability, 而是让用户能独立判断 gateway 是否按承诺路由、计费、披露模型状态。TRUST-A+B 已把 Phase 1 收敛到一个可落地闭环:

- response header 先给用户一个即时 trust status;
- non-streaming response 可带 inline provisional signature;
- settled cost receipt 追加 final detached signature;
- public well-known JWK Set 发布签名公钥和 revocation overlay;
- public verify endpoint 与 receipt verify endpoint 返回同一套 5 状态语义;
- Merkle 字段先保持 nullable forward-compatible, Phase 2 再补完整 transparency log.

## 2. Status Vocabulary

所有入口只使用 5 个用户可见状态:

| Status | Meaning | Typical source |
| --- | --- | --- |
| `verified` | 签名有效, 且 HUAKAI 已有独立事实链可证明 observed facts 与 signed facts 一致。 | Phase 2 full Merkle / future stronger reconciliation path. |
| `signed-only` | 签名有效, 但当前入口只证明 payload 未被篡改, 不证明所有 observed facts 已独立对账。 | TRUST-B-2 inline provisional; TRUST-B-3/B-4 detached receipt signature. |
| `unverified` | 系统保留功能但无法给出可验证签名, 或 key 已被 CRL overlay 标记 revoked。 | signer nil, ledger append/sign deferred, revoked key. |
| `missing` | 必要字段、header、payload、signature 或 pubkey 缺失。 | malformed client verify request. |
| `mismatch` | 签名不匹配, 或签名有效但 wire/observed facts 与 signed facts 不一致。 | tamper, wrong key, observed/wire mismatch. |

Priority rule: `mismatch` dominates `signed-only`. If `signature_valid=true` but observed/wire mismatch exists, public status MUST be `mismatch` (Owner D-B-mismatch-priority).

## 3. Verification Entrances

TRUST-A+B exposes three verification entrances.

| Entrance | Route / surface | Auth | Purpose |
| --- | --- | --- | --- |
| Detached verify | `POST /v1/trust/verify` | public, no auth | Verify a submitted canonical `trust.receipt.v1` payload plus detached signature. Body cap: 10 KiB. Rate limit: 60/min per IP. |
| Inline verify | response headers | no extra round trip | Client reads trust status and optional provisional signature directly from the gateway response. Streaming is not signed. |
| Inline-by-receipt | `GET /v1/receipts/{request_id-or-receipt_id}/verify` and submitted receipt verify path | session / receipt owner scoped | Verify stored final detached receipt, reject cross-tenant/user probes, and map unsigned/revoked/tampered receipts to the same 5 states. |

`/.well-known/huakai-pubkey.json` is the public key-discovery route, not a fourth verify endpoint. It is still part of the verification ecosystem.

## 4. Response Header Contract

The trust header set is intentionally small and stable:

| Header | Required | Meaning |
| --- | --- | --- |
| `X-Huakai-Trust-Status` | yes | One of `verified`, `signed-only`, `unverified`, `missing`, `mismatch`. |
| `X-Huakai-Trust-Signature` | conditional | Base64 Ed25519 signature for non-streaming inline provisional receipts when signer is available. |
| `X-Huakai-Trust-Pubkey-Fingerprint` | conditional | Fingerprint that selects the JWK in `.well-known`. |
| `X-Huakai-Trust-Schema` | conditional | `trust.receipt.v1` for inline signed payload semantics. |

Supporting operational headers remain separate: `X-Huakai-Upstream-Provider`, `X-Huakai-Upstream-Model`, `X-Huakai-Request-Id`, and `X-HUAKAI-Ledger-DLQ-Ref`.

Mismatch UX rule: response mismatch gets red badge + warning banner. The UI may display signature validity as detail, but the primary badge follows `X-Huakai-Trust-Status`.

## 5. Canonical Receipt V1

Canonical schema is `trust.receipt.v1`. The canonical JSON writer MUST emit fields in this exact order:

1. `schema_version`
2. `receipt_id`
3. `request_id`
4. `receipt_sequence`
5. `tenant_scope_ref`
6. `occurred_at`
7. `provider`
8. `requested_model`
9. `routed_model`
10. `upstream_model`
11. `delivered_model`
12. `cost_cents`
13. `token_counts` with `input`, `output`, `cached`
14. `price_snapshot` with `rate_table_snapshot_id`, `snapshot_version`, `currency_code`
15. `validation_state`
16. `redacted_metadata_allowlist`

Canonical rules:

- RFC3339Nano UTC timestamps only.
- JSON map keys sorted by UTF-8 byte order.
- `redacted_metadata_allowlist` only allows string, bool, and int64-like integer values.
- Non-ASCII is escaped in canonical bytes.
- Money and token values are integer-only. No float money is accepted; final settlement metadata may use micro-USD integer units where the billing ledger requires finer precision.
- Receipt identity is `request_id:seq` internally, with public display ID `receipt_<32hex>`.
- Schema forward compatibility reserves Merkle/checkpoint fields as nullable; null means "not yet part of Phase 1 proof", not "feature dropped".

## 6. Signature Timing

HUAKAI uses a dual-rail signature model:

| Rail | Timing | Storage / surface | Status |
| --- | --- | --- | --- |
| Inline provisional | During non-streaming response finalization, before final cost settlement is durable. | Response headers. | `signed-only` when signer exists; `unverified` on signer/deferred failure. |
| Final detached | After billing settlement, through `AppendSettledReceipt`. | `user_cost_receipts.signed_hash` plus `signer_fingerprint`. | `signed-only` unless a stronger observed-fact proof promotes it later. |

Streaming responses are not signed inline. They keep trust headers/trailers as `unverified` or DLQ-referenced until a final detached receipt is produced.

## 7. Key Storage, Rotation, And Revocation

Private key storage:

- Production signer key path is `HUAKAI_AUDIT_PRIVATE_KEY_PATH`.
- Empty path is allowed only for dev/test ephemeral signer behavior.
- The public distribution format is JWK Set-compatible JSON with HUAKAI extensions under `/.well-known/huakai-pubkey.json`.

Rotation:

- Target rotation cadence is 90 days.
- Registry keeps active, grace, and historical keys so old receipts remain verifiable by fingerprint.
- A rotated key may remain accepted during the grace window; a revoked key MUST make verified receipts report `unverified` with a revocation reason.

CRL overlay:

- File overlay env: `HUAKAI_TRUST_REVOKED_KEYS_FILE`.
- JSON overlay env: `HUAKAI_TRUST_REVOKED_KEYS_JSON`.
- File overlay is capped at 1 MiB and fails closed on oversize or parse error.
- Overlay format supports `fingerprint`, `revoked_at`, and `reason_class`; file env takes precedence over inline JSON env.

## 8. TOFU Client Anchor

`huakai-verify` uses TOFU for detached verification:

- first use fetches `/.well-known/huakai-pubkey.json` over HTTPS and caches the JWK Set under `~/.huakai/known_keys/<host>.json`;
- cache hit does not refetch unless `--refresh` is requested;
- caller may pin an expected fingerprint;
- fingerprint mismatch fails;
- revoked key fails even when the signature is cryptographically valid.

This is inspired by OpenSSH's user-known-hosts workflow, but HUAKAI keeps its own JSON cache format and does not copy OpenSSH file format or code.

## 9. Fail-Open Policy

Owner D-8 adopts fail-open for user responses:

- If signer is nil, key load fails, inline signing fails, or ledger append is deferred, the gateway still returns the primary response when business logic succeeded.
- The trust status MUST be `unverified`.
- HUAKAI MUST NOT fabricate a signature or promote status.
- Where a DLQ reference exists, return `X-HUAKAI-Ledger-DLQ-Ref`.
- W4 durable DLQ and operator logs own recovery: queue, monitor, alert, manual replay if automatic replay cannot close the gap.

This preserves availability without silently pretending to have a verified trust chain.

## 10. Storage Contract

TRUST-B reuses existing schema rather than adding a new table:

- `user_cost_receipts.signed_hash` stores the final detached signature bytes encoded for transport.
- `user_cost_receipts.signer_fingerprint` selects the public key.
- Storage rejects one-sided signature state: signature without fingerprint, or fingerprint without signature.
- Stored receipt verification reconstructs canonical facts and verifies the detached signature rather than trusting submitted client payloads.

Merkle/checkpoint proof remains Phase 2 Mandatory Roadmap. Phase 1 does not add a new trust table.

## 11. Reference Evidence And HUAKAI Upgrade Delta

Observed reference behaviors:

- Rekor verifies signed tree heads and consistency against prior local state: `sigstore/rekor@9bc540f214712dfa4b891cca828382855ada227a:cmd/rekor-cli/app/log_info.go:126`.
- Tessera signs checkpoints, can use additional signers for rotation, and can send checkpoints to witnesses: `transparency-dev/tessera@db8e65f3001be44ef5118e62be4a129e60760af8:append_lifecycle.go:712`, `transparency-dev/tessera@db8e65f3001be44ef5118e62be4a129e60760af8:append_lifecycle.go:802`, `transparency-dev/tessera@db8e65f3001be44ef5118e62be4a129e60760af8:internal/witness/witness.go:113`.
- Trillian exposes append-only Merkle log APIs and tile-based storage internals: `google/trillian@3d57cf1a97c81b1ad648ed44a61b9ee1018fba30:trillian_log_api.proto:28`, `google/trillian@3d57cf1a97c81b1ad648ed44a61b9ee1018fba30:storage/cache/log_tile.go:33`.
- LiteLLM, Portkey, and Envoy AI Gateway source regions read show response metadata/transformation/proxying, not a HUAKAI-style signed response receipt: `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8951`, `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/responseHandlers.ts:38`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/mcpproxy/handlers.go:785`.
- Sub2API, New API, All-API-Hub, and one-api were checked only through README/public docs in this lane because of license-clean-room constraints for Sub2API/New API/All-API-Hub; their public feature lists do not document HUAKAI-style signed response receipts: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:README.md:35`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:README.md:182`, `qixing-jk/all-api-hub@7e4d0dfef0da3b2b150fb6bb50974f1e7be527ef:README.md:64`, `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:README.md:69`.

HUAKAI delta:

- 架构升级: 3 verification entrances + 5 status vocabulary + public `.well-known` JWK Set + session-scoped receipt verify.
- 算法升级: HUAKAI canonical `trust.receipt.v1` fixed-order bytes + dual-rail signature timing + mismatch priority rule.
- 生态升级: `huakai-verify` TOFU cache + CRL overlay + final detached receipt in cost transparency flow.

These deltas are HUAKAI-owned design. They are inspired by transparency-log patterns, but they are not source-code translations from any reference project.

## 12. Acceptance Coverage

The detailed acceptance rows live in `docs/11_ACCEPTANCE_TEST_MATRIX.md`. TRUST-A+B must keep at least these guards:

- header status wiring across all 5 values;
- canonical determinism with mutation-kill tests;
- inline provisional `signed-only`;
- D-8 fail-open 200 + `unverified` + DLQ reference;
- final detached receipt stored in `user_cost_receipts.signed_hash`;
- storage rejection for one-sided signature mismatch;
- well-known JWK Set + cache control;
- `/v1/trust/verify` 5-state mapping;
- receipt verify CRL overlay;
- CLI TOFU cache and revoked-key rejection;
- mismatch priority across inline and stored receipt paths.

## 13. Open Questions

1. OpenSSH source-code citation remains a documentation gap in this lane: no local `openssh-portable` checkout was available, so §8 records the TOFU analogy without a `<repo>@sha>` OpenSSH source citation. Owner can require a follow-up source-cited pass.
2. Helicone AI Gateway exact source checkout (`Helicone/ai-gateway`) was not available locally; this spec does not use it as a load-bearing source citation. The reference ledger records this as a citation gap rather than fabricating evidence.

## 14. Source Coverage Proof

HUAKAI files read:

- `backend/internal/trust/status.go`
- `backend/internal/gatewayhttp/chat_completions_handler_headers.go`
- `backend/internal/gatewayhttp/chat_completions_handler_headers_test.go`
- `backend/internal/trustreceipt/canonical.go`
- `backend/internal/trustreceipt/canonical_test.go`
- `backend/internal/trusthttp/verify_handler.go`
- `backend/internal/trusthttp/verify_handler_test.go`
- `backend/internal/trusthttp/wellknown_handler.go`
- `backend/internal/trusthttp/wellknown_handler_test.go`
- `backend/internal/trusthttp/revocation.go`
- `backend/internal/trusthttp/revocation_test.go`
- `backend/cmd/gateway/routes.go`
- `backend/cmd/gateway/wiring_test.go`
- `backend/cmd/huakai-verify/main.go`
- `backend/cmd/huakai-verify/main_test.go`
- `backend/internal/audit/receipt_worker.go`
- `backend/internal/audit/receipt_worker_test.go`
- `backend/internal/audit/receipt_storage_test.go`
- `backend/internal/gatewayhttp/cost_receipt_handler.go`
- `backend/internal/gatewayhttp/cost_receipt_handler_test.go`
- `backend/sql/migrations/0028_user_cost_receipts.up.sql`

Reference files read:

- `/home/codex/refs/rekor/cmd/rekor-cli/app/log_info.go`
- `/home/codex/refs/rekor/pkg/signer/file.go`
- `/home/codex/refs/tessera/append_lifecycle.go`
- `/home/codex/refs/tessera/internal/witness/witness.go`
- `/home/codex/refs/trillian/trillian_log_api.proto`
- `/home/codex/refs/trillian/storage/cache/log_tile.go`
- `/home/codex/refs/litellm/litellm/router.py`
- `/home/codex/refs/portkey-gateway/src/handlers/responseHandlers.ts`
- `/home/codex/refs/envoy-ai-gateway/internal/mcpproxy/handlers.go`
- `/home/codex/refs/rust-openssl/openssl/src/x509/store.rs`
- `/home/codex/refs/sub2api/README.md`
- `/home/codex/refs/new-api/README.md`
- `/home/codex/refs/all-api-hub/README.md`
- `/home/codex/refs/one-api/README.md`

Source files read: listed above.
Lane: implementer (docs)
Agent: GPT-5 Codex
UTC timestamp: 2026-05-28T02:39:40Z

### Owner 中文摘要

本 spec 更新把 TRUST-A+B 的真实实现状态收敛成 5 状态 vocabulary、3 个验证入口、HUAKAI canonical `trust.receipt.v1`、双轨签名、`.well-known` JWK Set、TOFU、CRL overlay、fail-open+DLQ 和 Phase 2 Merkle forward-compat。真观察来自 HUAKAI 代码/测试、Rekor/Tessera/Trillian/LiteLLM/Portkey/Envoy/rust-openssl 以及受限 license 项目的 README；合理推断是 HUAKAI 三维升级对照与 README-only scoped negative；open questions 2 个，分别是 OpenSSH 源码 cite 和 Helicone AI Gateway 源码 checkout 缺口。
