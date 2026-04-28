# one-api — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | one-api ([github.com/songquanpeng/one-api](https://github.com/songquanpeng/one-api), MIT, [E-LIC-004](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Claude (PM-Orchestrator) |
| Inventory created | 2026-04-28 |
| Last refreshed | 2026-04-28 |
| Top-level dirs (verified via api.github.com contents) | `.github` `bin` `common` `controller` `middleware` `model` `monitor` `relay` `router` `web` |

## Why This File Exists

Owner directive 2026-04-28: "整体代码和逻辑都读完". one-api is HUAKAI's MIT safe-anchor reference (DR-000): we may read its source freely. This inventory is the audit instrument that proves comprehensive coverage of one-api's relevant feature surface before Phase 1 → Phase 2 transition.

## Status Legend

`unmined` / `shallow-evidence` (E-X-DEEP row exists in [07](../../07_REFERENCE_EVIDENCE_LEDGER.md)) / `deep-decomposed` (prose file under [decompositions/one-api/](.)).

## Inventory

### Gateway / Relay (`relay/`, `controller/relay.go`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Channel resolution by User-Group + Model | F-GW-001 (L1) | shallow-evidence (E-OAI-DEEP-001, E-OAI-DEEP-010) | priority-bucket selection with random within bucket |
| Forced-specific-Channel override path | F-GW-001 (L1) | shallow-evidence (E-OAI-DEEP-011) | bypasses normal selection + retry |
| Retry on rate-limit + 5xx + non-success | F-GW-004 (L1) | shallow-evidence (E-OAI-DEEP-002, E-OAI-DEEP-012) | NO exponential backoff observed |
| Failed-Channel exclusion within retry pool | F-GW-004 (L1) | shallow-evidence (E-OAI-DEEP-003) | exclusion is per-request, not global |
| Auto-disable Channel on permanent-error pattern | F-CH-002 (L2) | shallow-evidence (E-OAI-DEEP-006) | risk: needs alert + operator-confirm-resume |
| Streaming forwarder (line-based SSE) | F-GW-002 (L1) | shallow-evidence (E-OAI-DEEP-014) | usage-inference fallback when upstream silent |
| Cancellation propagation via context | F-GW-002 (L1) | shallow-evidence (E-OAI-DEEP-014) | mention only; depth needed |
| Quota reservation timing | F-KEY-001 (L1) | **gap** flagged (E-OAI-DEEP-004) | one-api does NOT reserve before upstream call |
| Duplicate-billing prevention | F-BILL-001 (L2) | **gap** flagged (E-OAI-DEEP-005, E-OAI-DEEP-013) | reservation/refund only; no idempotent claim gate |
| Quota formula (prompt + reserve + max-output × ratios) | F-BILL-001 (L2) | shallow-evidence (E-OAI-DEEP-015) | batched DB write option = unsafe for money |

### Channel Management (`model/channel.go`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Channel CRUD + bulk create | F-CH-001 (L1) | shallow-evidence (E-OAI-008 README) | unmined source-side |
| Per-Channel model allow-list | F-CH-001 (L1) | shallow-evidence (E-OAI-008 README) | unmined source-side |
| Channel test (manual + scheduled, global non-overlap guard) | F-CH-002 (L2) | shallow-evidence (E-OAI-DEEP-016) | auth/quota/balance/permission/slow-response → disable |
| Channel auto-re-enable on success after disable | F-CH-002 (L2) | shallow-evidence (E-OAI-DEEP-016) | configurable; needs flap dampening |
| Channel priority + weighted random | F-GW-001 (L1) | shallow-evidence (E-OAI-DEEP-010) | priority bucket then within-bucket random |

### API Key / User / Quota (`model/token.go`, `model/user.go`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Two-stage quota: pre-deduct estimate + post-deduct reconcile | F-KEY-001 (L1) | shallow-evidence (E-OAI-DEEP-007) | matches Sub2API claim-gate idea |
| **Non-atomic** validation-then-deduct | F-KEY-001 (L1) | **gap** flagged (E-OAI-DEEP-008) | concurrent overspend possible — HUAKAI must lock |
| In-memory API Key cache | F-KEY-001 (L1) | shallow-evidence (E-OAI-DEEP-009) | cross-instance invalidation concern |
| API Key status transitions (active / exhausted / expired / disabled) | F-KEY-001 (L1) | unmined (source) | needs per-status row in HUAKAI lifecycle |
| User balance + low-quota notification | F-BILL-001 (L2) | shallow-evidence (E-OAI-DEEP-007) | async notification trigger |
| User Group × Channel Group differential pricing | F-GROUP-001 (L2) | shallow-evidence (E-OAI-010 README) | unmined source-side |

### Routing (`router/`, `controller/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| OpenAI-compatible endpoint (`/v1/chat/completions` etc) | F-GW-001 (L1) | unmined (source) | full route table needs enumeration |
| Provider-specific endpoint variants (Anthropic / Gemini / Azure) | F-PROTO-002 (L2) | unmined (source) | how upstream-protocol selection happens |
| Admin API (CRUD for Channels / Users / etc) | F-OPS-001 (L2) | unmined (source) | full admin surface needs enumeration |

### Authentication / Middleware (`middleware/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| API Key authentication middleware | F-AUTH-001 (L1) | unmined (source) | how Authorization header is parsed and resolved |
| Admin token gating | F-SEC-002 (L1) | shallow-evidence (E-OAI-015 README) | privileged management API |
| Per-IP rate limit | F-SEC-001 (L1) | shallow-evidence (E-OAI-014 README) | unmined source |
| CORS / Cloudflare Turnstile gating | F-SEC-001 (L1) | shallow-evidence (E-OAI-014 README) | unmined source |

### Admin / Web (`web/`, `controller/admin*.go`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Admin React frontend | F-OPS-003 (L3) | unmined | one-api ships React; HUAKAI uses React per DR-004 |
| Branding / homepage / about customization | F-UI-001 (L3) | shallow-evidence (E-OAI-011 README) | unmined source |
| Announcements + recharge link + new-user initial balance | F-UI-001 (L3) | shallow-evidence (E-OAI-012 README) | unmined source |
| Quota detail dashboard + per-Channel success rate | F-OBS-001 (L2) | shallow-evidence (E-OAI-013 README) | unmined source |

### Configuration / Monitor (`common/`, `monitor/`, `bin/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Operator-supplied SESSION_SECRET requirement | F-AUTH-002 (L1) | shallow-evidence (E-OAI-004 README) | unmined source |
| Multi-instance session sharing | F-AUTH-002 (L1) | shallow-evidence (E-OAI-004 README) | requires shared MySQL |
| Built-in monitoring (Prometheus or similar) | F-OBS-002 (L2) | unmined (source) | what gets emitted |
| Single-binary deployment + Docker image | F-DEPLOY-001 (L3) | unmined (source) | influences HUAKAI Phase 3 skeleton |

## Coverage Summary (2026-04-28)

- **Deep-decomposed**: 0 features (no prose file yet for one-api).
- **Shallow-evidence**: 18 features (E-X-DEEP rows present, no prose).
- **Unmined**: 14 features (no source dive yet).

**Phase 1 exit blockers from this inventory**: 13 L1/L2 rows are in `shallow-evidence` or `unmined` state and need at least a prose decomposition file.

## Mandated Next Dives (Priority Order)

1. **Streaming forwarder edge cases** — beyond E-OAI-DEEP-014; partial-stream errors after partial flush, response-too-large handling. → [`one-api/streaming-forwarder.md`](.)
2. **Quota reservation race-fix evidence** — confirm the non-atomic gap (E-OAI-DEEP-008) by reading `model/token.go` further; identify exact race window. → [`one-api/quota-reservation.md`](.)
3. **Channel test runner** — auto-disable / auto-re-enable mechanics + global non-overlap guard. → [`one-api/channel-health-runner.md`](.)
4. **Admin API surface** — enumerate endpoints; identify what HUAKAI must replicate. → [`one-api/admin-api-surface.md`](.)
5. **Auth middleware chain** — how `Authorization: Bearer <key>` resolves to a User+Quota; cache-coherence on key invalidation. → [`one-api/auth-middleware.md`](.)
6. **Routing endpoint enumeration** — full route table + provider-protocol selection. → [`one-api/route-table.md`](.)
7. **Configuration model** — env vars / config file / secrets handling for Phase 3 skeleton inputs. → [`one-api/configuration-model.md`](.)
