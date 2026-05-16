# F-CRED-001: Credential Acquisition

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-CRED-001 |
| Specifier | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Option B, implementer-consuming draft from HUAKAI plans and prior review artifacts only |
| Supersedes | `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md` implementation outline |
| Superseded by | — |

## Sources

This Phase A draft consumes HUAKAI-owned plans, specs, and review artifacts. It does **not** reread reference-project source.

- `docs/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md` — Owner OCAW decisions S1-S9.
- `docs/plans/2026-05-15-f-cred-001-synthesis-codex.md` — RF union, AT-CRED-001-016..026, and AT-AUTH-SESSION-001.
- `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md` — AT-CRED-001-001..015 and prior acquisition boundary.
- `docs/reviews/2026-05-15-f-cred-001-preservation-codex-review.md` — prior reviewer-lane findings, read as a review artifact only.
- `docs/reviews/2026-05-15-f-cred-001-preservation-sonnet-review.md` — prior reviewer-lane findings, read as a review artifact only.
- `docs/specs/upstream-credential-management.md` — F-AUTH-005 final encrypted credential store boundary.
- `backend/internal/credentialstore/types.go` — HUAKAI-owned 15-mode handler registry shape.
- `docs/03_FEATURE_PARITY_MATRIX.md` — F-CRED-001 row.

## Capability

F-CRED-001 satisfies the local capability row: Admin/Owner can acquire upstream Provider Account credentials through guided browser, import, paste, cloud, exchange, and dedicated-mode flows, then finalize the result into F-AUTH-005 encrypted `account_credentials`.

F-CRED-001 owns **first acquisition** only. F-AUTH-005 continues to own storage encryption, runtime material validation, refresh, storm control, CAS discipline, and request-path credential use.

## Actor

- **Admin/Owner** starts, cancels, retries, previews, and finalizes acquisition flows.
- **System** records short-lived acquisition state, validates callback/import material, redacts payloads, and calls F-AUTH-005 finalization.
- **F-TRUST audit chain** records acquisition lifecycle events without token bytes.
- **Upstream Provider** receives OAuth or token-exchange calls only through Phase B adapters after Owner confirms production implementation.

## Preconditions

1. Admin identity is authenticated and authorized for the target tenant and Provider Account.
2. Target `(vendor, auth_mode)` is one of the 15 F-AUTH-005 mode cells.
3. A tenant-scoped Provider Account exists, or the create-account operation has reserved an account target for finalization.
4. Phase B has an approved `credential_acquisition_flow_sessions` migration before any production storage is added.
5. Phase A tests use mocks only and do not write real credential rows.

## Admin API Surface

The admin contract has two layers: five canonical lifecycle routes that own flow state, and six input helper routes that normalize specific operator inputs into that lifecycle. This keeps start/status/cancel/finalize semantics consistent with the actor contract while preserving every Phase A input trigger.

### Canonical lifecycle routes

| Endpoint | Lifecycle action | Purpose | Raw secret handling |
| --- | --- | --- | --- |
| `POST /v1/admin/pool-accounts/{id}/credential-acquisitions` | Create/start | Create a short-lived acquisition flow for the target Provider Account. Request body selects vendor, auth mode, flow kind, and input helper metadata. | No raw secret is required for OAuth start. Paste/import bodies may be accepted only through allowlisted helper shapes and are parsed into transient candidates. |
| `GET /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}` | Status/preview/retry guidance | Return current flow status, redacted preview metadata, validation errors, retry hints, and whether cancel/finalize is currently allowed. | Response is allowlisted redacted metadata only; no token, cookie, key, private key, code, verifier, or cloud secret is returned. |
| `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/callback` | Callback | Consume OAuth callback or callback-equivalent exchange material, validate state/idempotency, and move the flow to callback-received, validated, or failed. | Callback values are never logged raw. The endpoint consumes a flow at most once and stores only hashes, encrypted verifier material, and redacted context. |
| `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/cancel` | Cancel | Cancel an unfinalized, unexpired flow and emit a token-free cancellation audit event. | Cancel does not read or return candidate credential material. |
| `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/finalize` | Finalize | Consume a validated candidate and call the F-AUTH-005 finalizer exactly once under the flow idempotency guard. | Raw candidate bytes are handed to the F-AUTH-005 create boundary only; acquisition status and audit store redacted result metadata. |

### Input helper routes

| Helper endpoint | Input trigger | Maps to canonical lifecycle | Raw secret handling |
| --- | --- | --- | --- |
| `POST /admin/v1/credentials/paste` | Manual paste | Creates or updates a `paste`, `token_exchange`, `cloud_bootstrap`, or `manual_first` flow, then returns canonical status/preview. | Accept API key, refresh token, session token, or cloud fields in a structured request; only masked preview metadata is returned. |
| `POST /admin/v1/credentials/cli-import` | CLI import | Creates or updates a `cli_import` flow, parses uploaded/pasted content, then returns canonical status/preview. | Server never reads local workstation paths by default. Raw content is parsed once and discarded after finalizer handoff. |
| `POST /admin/v1/credentials/csv-import` | Batch import | Creates one canonical flow per accepted row or a batch parent with per-row child flows after Phase B schema approval. | Each row is parsed independently; audit payload stores row counts and failure classes, not credential values. |
| `POST /admin/v1/credentials/json-import` | Batch import | Creates one canonical flow per JSON object, array item, or JSON-lines item after Phase B schema approval. | Parser rejects token-shaped data in audit/log context and keeps raw material out of flow metadata. |
| `POST /admin/v1/credentials/oauth-init` | Browser OAuth | Calls canonical create/start for an `oauth` flow and returns authorization instructions. | No upstream tokens accepted. PKCE verifier is encrypted in the flow row. State is stored only as a hash. |
| `GET /admin/v1/credentials/oauth-callback` | Browser OAuth redirect | Translates browser redirect parameters into the canonical callback action for the target flow. | Callback parameters are never logged raw. The underlying canonical callback consumes the flow exactly once. |

All endpoints are Phase A contract only. Phase B must wire them in a new handler file and must not mutate `backend/internal/gatewayhttp/admin_credentials_handler.go` unless Owner explicitly reopens that boundary.

## Acquisition Session Schema

Future table name: `credential_acquisition_flow_sessions` (HUAKAI-owned name). Phase A documents the schema only; it does not create a migration.

TTL: 10 minutes from `created_at` to `expires_at` for any unfinalized interactive flow. Non-interactive paste/import flows also get a 10-minute finalize window so retry and preview behavior remains bounded.

| Field | Type intent | Required | Purpose |
| --- | --- | --- | --- |
| `id` | opaque UUID | yes | Public flow identifier returned to admin UI. |
| `tenant_id` | tenant id | yes | Tenant isolation and audit scope. |
| `provider_account_id` | account id | yes | Finalization target. |
| `vendor` | text enum | yes | One of `anthropic`, `openai`, `gemini`. |
| `auth_mode` | text enum | yes | One of the 15 F-AUTH-005 modes. |
| `flow_kind` | text enum | yes | `oauth`, `cli_import`, `paste`, `csv_import`, `json_import`, `cloud_bootstrap`, `token_exchange`, `setup_token`, or `manual_first`. |
| `status` | text enum | yes | `started`, `waiting_for_user`, `callback_received`, `validated`, `finalized`, `cancelled`, `expired`, or `failed`. |
| `actor_id` | admin id | yes | Admin who started the flow. |
| `actor_role` | text | yes | Role snapshot for audit. |
| `state_hash` | bytes/text | oauth only | Hash of OAuth state; raw state is never stored. |
| `nonce_hash` | bytes/text | optional | Hash for import/finalize idempotency correlation. |
| `encrypted_pkce_verifier` | encrypted bytes | oauth only | PKCE verifier encrypted with the same key-provider discipline as F-AUTH-005. |
| `client_identity_source` | text enum | oauth/token only | `public_cli_client`, `operator_config`, `per_account_override`, or `disabled_missing_config`. |
| `redirect_uri` | text | oauth only | Callback URI selected for the flow. |
| `requested_scopes` | JSON text array | optional | Redacted OAuth scope names needed for operator preview. |
| `redacted_context` | JSON object | yes | Provider account email hash, org/project/tier labels, import counts, warning flags, and error classes. |
| `long_lived_requested` | boolean | anthropic setup token only | Records explicit admin selection for long-lived token acquisition; UI warning lands in Phase C. |
| `idempotency_key_hash` | bytes/text | yes | Prevents duplicate finalize/callback effects. |
| `result_account_credential_id` | credential id | no | Set after F-AUTH-005 create succeeds. |
| `error_class` | text | failed only | Machine-readable failure reason. |
| `error_message_redacted` | text | failed only | Human-readable, token-free diagnosis. |
| `expires_at` | timestamp | yes | `created_at + 10 minutes` unless already finalized/cancelled. |
| `consumed_at` | timestamp | no | Set when callback/finalize consumes the flow. |
| `cancelled_at` | timestamp | no | Set by cancellation. |
| `created_at`, `updated_at` | timestamp | yes | Lifecycle timeline. |

Schema invariants:

1. No raw access token, refresh token, API key, cookie, private key, session token, authorization code, or cloud secret appears in unencrypted flow columns.
2. `state_hash` comparison is constant-time after hashing the incoming state.
3. `encrypted_pkce_verifier` is destroyed or made inaccessible after finalization/cancellation/expiry.
4. A finalized flow cannot be finalized again, even under callback replay or concurrent finalize.
5. `redacted_context` is allowlisted; unknown keys that look token-shaped are rejected before audit.

## OAuth Client Identity Strategy

Owner OCAW S4 selects a hybrid strategy:

1. **Per-account override wins**: advanced operators may provide client identity metadata for a single account. This is required for customer-owned OAuth app identities and for OpenAI-style advanced cases.
2. **Operator config is next**: modes without a safe public client identity, especially AI Studio OAuth-like paths, require tenant/operator configuration. If absent, the mode is disabled with `disabled_missing_config`.
3. **Vendor-published public CLI client fallback is allowed**: browser/CLI-adjacent modes may use the upstream CLI tool's publicly released client identity where legally and operationally acceptable. Phase B must fetch and verify current values from approved public sources; Phase A must not hardcode old values.
4. **No client secret in repo**: client secrets, if any, come from operator configuration or per-account override and are never committed.
5. **Audit every source**: acquisition audit includes `client_identity_source`, not the actual client identifier or secret.

## 15-Mode Acquisition Plan

| Vendor | Auth mode | Primary strategy | OAuth client source | Finalizer payload expectation | Phase A disposition |
| --- | --- | --- | --- | --- | --- |
| anthropic | `api_key` | `paste` | n/a | API key payload accepted by F-AUTH-005 handler. | Implemented in Phase B manual path. |
| anthropic | `claude_ai_oauth` | `oauth` plus optional cookie/session bootstrap input | public CLI client if verified, otherwise operator config | OAuth/session payload with org metadata preview. | Specified; production exchange waits for Phase B. |
| anthropic | `claude_code` | `cli_import` or browser flow | public CLI client if verified, otherwise operator config | Session/OAuth payload accepted by `claude_code` handler. | Specified; no server-side file reads. |
| anthropic | `bedrock` | `paste` now; `cloud_bootstrap` STS later | n/a | AWS SigV4 payload with region and expiry metadata when temporary. | Manual path included; STS bootstrap gated by Owner. |
| anthropic | `vertex_anthropic` | `json_import` service-account bootstrap | operator config for project/location defaults where needed | Upstream-passthrough or service-account payload with token endpoint controlled by HUAKAI. | Specified; implementation Phase B. |
| openai | `api_key` | `paste` | n/a | API key payload. | Implemented in Phase B manual path. |
| openai | `chatgpt_oauth` | `oauth` | public CLI client fallback, per-account override allowed | Session/OAuth payload plus account metadata and privacy action outcome. | Specified; privacy mutation non-blocking by Owner S1. |
| openai | `codex_cli_oauth` | `cli_import` | public CLI client fallback, per-account override allowed | Session/OAuth payload parsed from uploaded/pasted CLI content. | Specified; no local path reads. |
| openai | `azure` | `paste`, `cloud_bootstrap`, or token endpoint exchange | operator config/per-account override | API key or access-token passthrough payload. | Manual-first; cloud depth deferred. |
| openai | `refresh_token` | `token_exchange` via `paste` | per-account override or operator config | OAuth payload preserving refresh-token rotation rules for F-AUTH-005. | Specified; storm/refresh behavior remains F-AUTH-005. |
| gemini | `aistudio_api_key` | `paste` | n/a | API key payload. | Implemented in Phase B manual path. |
| gemini | `vertex_sa` | `json_import` service-account bootstrap | operator config for project/location defaults | Service-account or access-token passthrough payload. | Specified; ignore uploaded token endpoint. |
| gemini | `code_assist` | `oauth` | public CLI client fallback when verified | Session/OAuth payload with project metadata status. | Specified; fallback side effects bounded and audited. |
| gemini | `google_one` | `oauth` | public CLI client fallback when verified | Session/OAuth payload with tier metadata. | Specified; tier normalization retained. |
| gemini | `antigravity` | dedicated `oauth` adapter plus project/plan follow-up | public CLI/operator source as verified in Phase B | Session/OAuth payload with dedicated metadata status. | Specified; runtime hardening remains a separate R-E+1 roadmap item. |

Additional policy from OCAW:

- Anthropic long-lived setup-token acquisition is represented by `flow_kind=setup_token` and `long_lived_requested=true`. It is default off and must surface an explicit admin warning in Phase C before production enablement.
- Gemini cross-client fallback is allowed only through a compatibility matrix approved in Phase B. Each fallback attempt emits audit with source/target family labels and success flag.
- Antigravity dedicated adapter is in scope, but runtime hardening remains Phase R-E+1 roadmap work and must not be specified as Phase A behavior.

## Refresh Lock (S8)

OCAW S8 maps to the F-AUTH-005 refresh boundary, not to the acquisition finalizer. Phase A specifies the required lock behavior so F-CRED-001-acquired OAuth credentials enter the same refresh safety model after finalization.

Refresh workers for the same credential must take a transaction-scoped PostgreSQL advisory lock before performing an upstream refresh:

```sql
SELECT pg_advisory_xact_lock(hashtext('credential_refresh:' || account_credential_id::text));
```

Required behavior:

1. The lock is acquired inside the same transaction that rereads the `account_credentials` row and writes the refresh result.
2. If N refresh workers race on the same `account_credential_id`, exactly one worker performs the real upstream refresh call.
3. Other workers wait for lock release, reread the credential row, observe the newer token/version/refresh window, and reuse the current credential without a second upstream refresh call.
4. Existing F-AUTH-005 credential-version CAS remains a second-line defense for stale writers and cross-process edge cases.
5. This lane does not implement the refresh worker or advisory-lock SQL. Phase B/C must land it in the F-AUTH-005 module boundary, alongside the credential refresh scheduler and storm controller.

## Normal Path

1. Admin starts an acquisition flow through one of the admin endpoints.
2. The system validates tenant/account/mode, chooses a `ModePlan`, stores a `credential_acquisition_flow_sessions` row, and emits `credential_acquisition_started`.
3. For OAuth, the system hashes state, encrypts PKCE verifier, records 10-minute expiry, and returns redirect instructions.
4. For import/paste/batch flows, the system parses the input into normalized credential candidates and redacted preview metadata without persisting raw material in the flow row.
5. The system validates the selected `(vendor, auth_mode)` through the F-AUTH-005 `credentialstore.HandlerRegistry`.
6. The finalizer calls `credentialstore.Create` with tenant id, provider account id, vendor, auth mode, encrypted payload input, expiry metadata, and redacted source metadata.
7. If F-AUTH-005 create succeeds, the flow is marked `finalized`, the credential id is linked, and `credential_acquisition_completed` is emitted.
8. If an optional post-acquisition action is configured, such as ChatGPT privacy preference handling, the result is audited separately and does not block finalization unless a future strict tenant policy says so.

## Failure Path

### Failure: Unauthorized Admin

- Trigger: admin identity is missing, expired, or not permitted for tenant/account.
- Observable outcome: endpoint rejects before reading or parsing credential material.
- Operator-visible signal: admin auth failure audit; no acquisition event with credential context.

### Failure: Unknown Mode

- Trigger: `(vendor, auth_mode)` is not in the 15-mode F-AUTH-005 registry.
- Observable outcome: flow is rejected or marked failed before external calls.
- Operator-visible signal: redacted error class `unknown_mode`; no credential row.

### Failure: State Mismatch

- Trigger: OAuth callback state hash does not match stored hash.
- Observable outcome: callback fails, flow remains unconsumed or moves to failed depending on replay policy.
- Operator-visible signal: `credential_acquisition_failed` with reason `state_mismatch`; no code exchange; no credential row.

### Failure: Callback Replay

- Trigger: consumed/finalized OAuth flow receives another callback or finalize request.
- Observable outcome: duplicate callback cannot call finalizer again; existing result is returned only as redacted status.
- Operator-visible signal: replay audit with flow id and credential id if already finalized.

### Failure: Expired Flow

- Trigger: current time is after `expires_at`.
- Observable outcome: callback/finalize rejects and requires a new flow.
- Operator-visible signal: `credential_acquisition_failed` with reason `expired`.

### Failure: Parser Rejects Input

- Trigger: malformed JSON/CSV, unsupported shape, missing required field, or token-shaped value in audit context.
- Observable outcome: flow fails before finalizer; raw input is discarded.
- Operator-visible signal: row-level failure counts for batch imports and redacted parse errors.

### Failure: Code Exchange Or Provider Probe Fails

- Trigger: upstream token exchange, user metadata probe, project/tier probe, or cloud bootstrap fails.
- Observable outcome: no credential row unless the failure is explicitly classified as non-blocking metadata enrichment.
- Operator-visible signal: failure class, retryability, and operator action hint; no token bytes.

### Failure: Finalizer Rejects Payload

- Trigger: F-AUTH-005 mode handler rejects payload or storage returns an error.
- Observable outcome: flow stays failed or validated-but-not-finalized; raw candidate payload is not kept in flow metadata.
- Operator-visible signal: `credential_acquisition_failed` plus F-AUTH-005 error class, redacted.

### Failure: Batch Partial Failure

- Trigger: CSV/JSON import contains mixed valid and invalid rows.
- Observable outcome: valid rows may finalize independently under idempotency keys; invalid rows are reported without blocking unrelated rows.
- Operator-visible signal: batch summary with total, completed, failed, skipped duplicate counts.

## Operator Recovery

| Failure | Recovery |
| --- | --- |
| Unauthorized admin | Reauthenticate or adjust admin RBAC outside this feature. |
| Unknown mode | Select one of the 15 published modes or wait for a new mode registry change. |
| State mismatch/replay | Start a fresh OAuth flow from the admin UI. |
| Expired flow | Restart acquisition; old flow remains audit-only. |
| Parser failure | Correct file/input shape and re-import; failed raw content is not recoverable from HUAKAI. |
| Metadata enrichment failure | Use the credential if finalization succeeded, then retry metadata fill through the Phase B worker policy. |
| Antigravity project metadata transient failure | Account can be marked operator-attention or metadata-stale per Owner S5; background retry is Phase B. |
| Missing AI Studio/operator client config | Configure tenant/operator OAuth client identity, then restart flow. |
| Long-lived Anthropic setup token blocked | Enable future feature flag and acknowledge UI warning after Phase C/Owner confirmation. |

## Audit / Usage / Log Evidence

F-CRED-001 emits four F-TRUST event types:

| Event | When | Payload allowlist |
| --- | --- | --- |
| `credential_acquisition_started` | Flow row created. | tenant id, provider account id, vendor, auth mode, flow kind, client identity source, actor id, expiry, request id. |
| `credential_acquisition_completed` | F-AUTH-005 create succeeds. | tenant id, provider account id, credential id, vendor, auth mode, flow kind, redacted metadata keys present, non-blocking warning count. |
| `credential_acquisition_failed` | Flow cannot proceed or finalizer fails. | tenant id, provider account id, vendor, auth mode, flow kind, error class, retryable flag, redacted message. |
| `credential_acquisition_cancelled` | Admin cancels before finalization. | tenant id, provider account id, vendor, auth mode, flow kind, actor id, reason code. |

Audit invariants:

1. Payload allowlist is enforced before event write.
2. Token-shaped substrings are rejected or replaced with `[REDACTED]` before log/audit emission.
3. Audit payload never contains authorization code, access token, refresh token, API key, session token, cookie, private key, PKCE verifier, client secret, or cloud secret.
4. Batch import audit contains aggregate counts and stable row ordinals only.
5. Privacy action outcomes, Gemini cross-client fallback attempts, Antigravity metadata retry status, and long-lived token usage are separate event details or follow-on audit events, never raw credential fields.

## Acceptance Test Direction

Acceptance coverage is `AT-CRED-001-001..026` plus `AT-AUTH-SESSION-001` in `docs/11_ACCEPTANCE_TEST_MATRIX.md`.

Phase A mock-only Go scaffold covers:

- Flow enum and 15-mode plan coverage.
- In-memory acquisition session CRUD, TTL, cancel, expire, and consume behavior.
- OAuth state mismatch, callback replay, and exchange success/failure.
- CLI import parsing for JSON object, JSON array, JSON-lines, and single-token shapes.
- Finalizer idempotency and F-AUTH-005 handler registry validation.
- Audit payload token redaction.

Phase B production implementation must add handler, store, adapter, and integration tests after Owner confirms schema and code changes.

## Open Questions

None for Phase A. Phase B still requires Owner confirmation before:

1. Adding `credential_acquisition_flow_sessions` migration and generated query code.
2. Wiring production admin endpoints.
3. Fetching/verifying current public CLI client identity values from approved sources.
4. Enabling cloud bootstrap beyond manual-first paths.
5. Enabling Anthropic long-lived setup token mode in production UI.
6. Implementing Antigravity runtime-hardening work in the separate Phase R-E+1 track.

## Implementer Notes

- 2026-05-16 — Codex GPT-5 — Phase A creates mock-only tests under `backend/internal/credentialacq`; no production package files, schema migrations, real credential store changes, or existing admin credential handler edits.
- 2026-05-16 — Codex GPT-5 — Review-fix pass closes endpoint drift, table-name drift, S8 refresh-lock mapping, and concurrent finalize scaffold gap without reading reference-project source.

Source files read: docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/plans/2026-05-16-f-cred-001-phase-a-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-claude.md; backend/internal/credentialacq/finalizer_test.go; .agents/skills/acceptance-test-writer/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T05:47:06Z
