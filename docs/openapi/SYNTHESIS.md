# OpenAPI Synthesis — 3 Independent Drafts → 1 Unified Contract

| Field | Value |
| --- | --- |
| Status | Synthesized; pending Codex final review CL-001..011 |
| Author | Claude (PM-Orchestrator), synthesizing Claude + Codex(gateway) + Codex(admin) drafts |
| Date | 2026-04-29 |
| Inputs | [claude-draft.yaml](claude-draft.yaml), [codex-gateway-draft.yaml](codex-gateway-draft.yaml), [codex-admin-draft.yaml](codex-admin-draft.yaml) |
| Output | [openapi.yaml](openapi.yaml) (unified contract for Phase 2.2) |
| Becomes | After CL-001..011 review APPROVE, openapi.yaml is the locked Phase 2.2 contract; Phase 3 skeleton consumes it via codegen. |

## 1. Convergence (All 3 Drafts Agree)

1. OpenAPI 3.1.0.
2. Bearer token via Authorization header (HUAKAI API Key).
3. 3 gateway endpoints: `/v1/chat/completions`, `/v1/responses`, `/v1/messages`.
4. Streaming via `stream=true` flag → `text/event-stream` 200 response; non-streaming → `application/json`.
5. `Idempotency-Key` request header (per F-OBS-001).
6. `Idempotent-Stream-Replay` request header (per F-GW-002 §5.5).
7. Tenant-scoped admin API; `pool_group_id` and `provider_account_id` parameters everywhere.
8. Money fields rendered as string (numeric(20,8) precision-safe across JSON).
9. Per-endpoint `x-huakai-spec-source` extension citing Released spec.
10. CL-011 attribution per schema component.

## 2. Real Divergences and Resolutions

### D1 — JSON convention: snake_case vs camelCase

- **Claude**: snake_case (`pool_group_id`, `provider_account_id`).
- **Codex admin**: camelCase (`poolGroupId`, `providerAccountId`).

**Resolution**: **snake_case** for HUAKAI. Reasons:
- OpenAI public API uses snake_case (HUAKAI is OpenAI-compatible at gateway).
- Anthropic public API uses snake_case.
- Database column names are snake_case (per `docs/schema/*.sql`).
- One convention end-to-end avoids translation layer.

### D2 — Nullable syntax: `nullable: true` vs `type: [string, 'null']`

- **Claude**: `nullable: true`.
- **Codex**: `type: [string, 'null']`.

**Resolution**: **`type: [string, 'null']`** (Codex wins). Reason: OpenAPI 3.1 deprecated `nullable: true` in favor of JSON Schema 3.1 union types. Codex is technically correct.

### D3 — `additionalProperties` for gateway request bodies: strict vs permissive

- **Claude**: strict (only documented fields).
- **Codex gateway**: permissive (`additionalProperties: true`) for public-client compat.

**Resolution**: **permissive on gateway requests** (Codex), **strict on admin requests** (Claude). Reasons:
- Gateway: HUAKAI imitates OpenAI/Anthropic public APIs, which add fields without notice. Permissive avoids breaking on new client SDK fields.
- Admin: HUAKAI defines this surface; we control the shape end-to-end.

### D4 — Pagination shape

- **Claude**: `next_cursor` (top-level, opaque).
- **Codex admin**: `page` object with `cursor`, `next_cursor`, `has_more` etc.

**Resolution**: **Codex's structured `page` object**. Reasons:
- Richer client UX (knows whether more pages exist before fetching).
- Future-proof for cursor-validity TTL.
- Cost: 1 extra field. Acceptable.

### D5 — `protocol_loss` location: body, header, or both?

- **Claude**: body via UsageRecord JSON only.
- **Codex gateway**: open question.

**Resolution**: **both**. Reasons:
- Header `X-HUAKAI-Protocol-Loss: feature1,feature2` for client-developer convenience (don't have to fetch Usage Record to see lossy translation happened).
- Full structured array on Usage Record (per F-PROTO-002 H4) for operator queries + audit.
- Header is comma-separated feature names only; Usage Record has full {feature, direction, verdict, note}.

### D6 — `Retry-After` coverage

- **Claude**: on 429 + 503.
- **Codex gateway**: open question (also on 402?).

**Resolution**: **only on 429 + 503**. Reasons:
- 402 = quota/balance exhausted, recovery requires customer top-up — not a timed retry.
- 429 = rate-limited, has a real reset time.
- 503 = pool exhausted, has a real expected-recovery time.

### D7 — SSE event typed schemas

- **All 3 drafts**: open question / string frame.

**Resolution**: **defer to Phase 2.3**. Reasons:
- OpenAPI 3.1 doesn't model SSE event types as first-class.
- Each client protocol (Chat / Responses / Anthropic) has different event-type sets.
- Documenting them as code-generated types would explode the YAML.
- For Phase 2.2: keep as string frame + reference F-PROTO-002 spec for event-type catalog.

### D8 — Idempotency-Key echoed on cached response

- **Claude**: opens question.
- **Codex**: not addressed.

**Resolution**: **yes, echo on cache hit** via `X-HUAKAI-Idempotency-Hit: true` response header. Operator clarity + client retry-loop can short-circuit.

### D9 — Admin auth same Bearer as gateway

- **All 3 drafts**: same Bearer scheme.

**Resolution**: **same Bearer scheme**, but admin endpoints additionally check actor role via `x-huakai-required-role` extension (`platform_admin` vs `tenant_operator` per F-POOL-001 Q1).

## 3. Where Codex Sharpens Claude

- **C1**: OpenAPI 3.1 nullable syntax (D2).
- **C2**: Structured `page` pagination object (D4).
- **C3**: JSON Schema 3.1 `additionalProperties: true` for public-client compat (D3).
- **C4**: Schema-level `x-huakai-spec-source` arrays (multiple specs per schema), where Claude used single string. Adopt arrays.
- **C5**: Cleaner error envelope shape: `error.code` (machine-readable enum) + `error.message` (human) + `error.request_id` (correlation). Claude had less structure.
- **C6**: ProtocolLoss with `verdict` enum {LOSSY, UNSUPPORTED} — matches F-PROTO-002 spec exactly.

## 4. Where Claude Sharpens Codex

- **L1**: Tag taxonomy (gateway / admin-pools / admin-accounts / admin-usage / admin-billing / admin-audit / admin-dlq) — Codex didn't tag consistently.
- **L2**: Per-endpoint security explicitness — Codex assumed inherited; Claude explicit.
- **L3**: Retry-After on 429/503 — D6 resolution comes from Claude's prior decision + matrix.
- **L4**: Custom error code list on ProviderAccountUpdate — covers F-RATE-001 §1.6 operator-configurable temp-unsched rules.
- **L5**: `clear-rate-limit` cascade endpoint — Codex admin missed this; Claude has it.

## 5. Owner-Facing Open Questions Resolved

(carrying from Claude draft + Codex gateway open questions)

| Q | Resolution |
|---|---|
| Q-C1: mid-stream failover per-Pool? | YES — `PoolGroup.allow_mid_stream_failover` boolean (default false), overridable per Route. |
| Q-C2: pagination cursor opaque vs structured? | OPAQUE base64 — server-side TTL-bound; structured `page` envelope (D4). |
| Q-C3: admin auth same Bearer? | SAME (D9). |
| Q-C4: usage block per-protocol? | SHARED HCSF-aligned shape (input_tokens, output_tokens, cache_creation, cache_read). |
| Q-C5: echo Idempotency-Key on cache hit? | YES via `X-HUAKAI-Idempotency-Hit: true` (D8). |
| Q-G1 (Codex): Retry-After on 402? | NO (D6). |
| Q-G2 (Codex): protocol_loss header vs body? | BOTH (D5). |
| Q-G3 (Codex): SSE typed schemas? | DEFER to Phase 2.3 (D7). |
| Q-G4 (Codex): gateway request schema strictness? | PERMISSIVE (additionalProperties: true) on gateway; STRICT on admin (D3). |

## 6. Synthesis Output

The unified contract lives at [openapi.yaml](openapi.yaml). It applies all 9 D-resolutions + adopts every C-sharpening + keeps every L-sharpening + closes all 9 open questions.

Codex final review CL-001..011 follows.
