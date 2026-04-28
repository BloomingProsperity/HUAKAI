# new-api — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | New API ([github.com/QuantumNous/new-api](https://github.com/QuantumNous/new-api), AGPL-3.0-or-later, [E-LIC-002](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Claude (PM-Orchestrator) |
| Inventory created | 2026-04-28 |
| Last refreshed | 2026-04-28 |
| Top-level dirs (verified) | **REDACTED 2026-04-28** per Codex review CRITICAL #2: AGPL distinctive file structure must NOT appear in public artifacts (CL-002). The verification was done; the resulting top-level directory enumeration is retained in specifier-private session notes only. The inventory below groups features by **behavior**, not by source path. |

## Why This File Exists

Owner directive 2026-04-28: "整体代码和逻辑都读完". New API is **AGPL-3.0** — specifier-lane may read source per [DR-000](../../decisions/DR-000-clean-room-methodology.md), but this session and any session reading New API source enters specifier-only contamination state. New API extends one-api with cache-aware billing, native protocol translation, and reasoning-effort handling — all features HUAKAI explicitly wants. This inventory is the audit instrument for the New-API-derived L1/L2 surface.

## Status Legend

`unmined` / `shallow-evidence` / `deep-decomposed`. New-API-specific evidence carries `E-NAI-*` prefix; one-api-shared behaviors are referenced only.

## Inventory

### Gateway / Relay (behavior extends one-api gateway core)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Channel selection (inherited from one-api) | F-GW-001 (L1) | see one-api inventory | shared base |
| Cross-format protocol translation: OpenAI ⇄ Claude / Gemini → OpenAI | F-PROTO-002 (L2) | shallow-evidence (E-NAI-003 README) | **unmined source** — high priority |
| Cache-aware request handling (prompt-cache vs fresh) | F-BILL-003 (L3 Phase 6+) | shallow-evidence (E-NAI-001 README) | **unmined source** — pricing dependency |
| Reasoning-effort parameter pass-through (high/med/low + thinking-token budget) | F-MODEL-001 (L2) | shallow-evidence (E-NAI-004 README) | **unmined source** |
| Realtime API support (OpenAI Realtime + Azure variants) | F-RT-001 (L3 Phase 9+) | unmined (README only — no E-NAI ledger row yet) | **unmined source**; first-pass README evidence row pending in [docs/07](../../07_REFERENCE_EVIDENCE_LEDGER.md) |
| Rerank model dedicated interface | F-MODEL-002 (L3 Phase 9+) | shallow-evidence (E-NAI-005 README) | **unmined source** |
| Channel weighted-random + auto-retry | F-GW-004 (L1) | shallow-evidence (E-NAI-007 README + E-OAI-DEEP shared) | needs source confirmation of weighting algorithm |

### Channel / Account (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Channel CRUD + bulk + model allow-list (inherited) | F-CH-001 (L1) | see one-api inventory | shared base |
| Channel health probe + auto-disable (inherited) | F-CH-002 (L2) | see one-api inventory | shared base |
| Per-User × per-Model rate limit | F-SEC-004 (L2) | shallow-evidence (E-NAI-006 README) | **unmined source** — extends one-api per-IP limit |

### Pricing / Billing (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Differential pricing (cached vs fresh tokens) | F-BILL-003 (L3) | shallow-evidence (E-NAI-001 README) | **unmined source** — multi-provider price tables |
| Reasoning-token separate accounting | F-MODEL-001 (L2) | shallow-evidence (E-NAI-004 README) | **unmined source** |
| Usage Record token-level granularity | F-BILL-001 (L2) | unmined (source) | how token counts flow from upstream events |
| User Group × Channel Group differential pricing (inherited) | F-GROUP-001 (L2) | see one-api inventory | shared base |

### Identity / Auth (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| OAuth providers: GitHub / WeChat / email (inherited) | F-AUTH-001 (L1) | see one-api inventory | shared base |
| OAuth providers: Discord / Telegram / LinuxDo | F-AUTH-004 (L2 Plugin) | shallow-evidence (E-NAI-007 README) | **unmined source** — community-platform extensions |
| Session persistence (inherited) | F-AUTH-002 (L1) | see one-api inventory | shared base |

### Operator / Admin (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| External-tool quota introspection API | F-OPS-001 (L2) | shallow-evidence (E-NAI-008 README) | **unmined source** — what endpoints, what auth scope |
| Admin dashboard with stats / console | F-OPS-003 (L3 Phase 7+) | shallow-evidence (E-NAI-003 README) | unmined source — dashboard UI patterns |
| Branding / homepage / announcements (inherited) | F-UI-001 (L3 Phase 7+) | see one-api inventory | shared base |

### Internationalization (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Multi-language UI (5+ languages: zh-CN / zh-TW / en / fr / ja) | F-I18N-001 (L3 Phase 7+) | shallow-evidence (E-NAI-002 README) | **unmined source** — translation file format, glossary discipline |

### Desktop App (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Electron desktop application | (out-of-scope for HUAKAI core) | unmined | New API ships an Electron desktop client; HUAKAI focuses on web admin. Out-of-scope unless Owner promotes. |

### Configuration / Constants (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Application setting model + persistence | F-CONFIG-001 (L2) | unmined (source) | how settings flow at runtime |
| Constants (price multipliers, default ratios, etc.) | F-BILL-001 (L2) | unmined (source) | **must NOT copy specific values** (CL-001a) |
| Logger configuration | F-OBS-002 (L2) | unmined (source) | log format, level, sinks |

### Routing / Middleware (behavior surface)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Endpoint behavior surface (extends one-api) | F-GW-001 (L1) | unmined (source) | new-api adds Realtime, Rerank, multi-format endpoints |
| Middleware chain (auth + rate + audit) | F-AUTH-001 + F-SEC-001 + F-OPS-001 (L1/L2) | unmined (source) | enumerate the chain order |

## Coverage Summary (2026-04-28)

- **Deep-decomposed**: 0 features (no prose file yet for new-api).
- **Shallow-evidence**: 8 New-API-specific features (E-NAI-* rows present, no source dive).
- **Unmined**: 13 features (only README; high concentration in protocol translation, cache-aware billing, multi-language UI).

**Phase 1 exit blockers from this inventory**: 4 L1/L2 features are critical and still `unmined`:
- F-PROTO-002 cross-format protocol translation
- F-MODEL-001 reasoning-effort parameter
- F-SEC-004 per-User × per-Model rate limit
- F-OPS-001 external-tool introspection API

## Mandated Next Dives (Priority Order)

1. **Cross-format protocol translation** (F-PROTO-002 L2) — read `relay/` and `pkg/` for the OpenAI ⇄ Claude / Gemini → OpenAI translation matrix; capture loss-list per pair. → [`new-api/protocol-translation.md`](.)
2. **Cache-aware billing** (F-BILL-003 L3 but pricing-correctness-critical) — read `model/` and `service/` for cache-hit detection + differential pricing. → [`new-api/cache-aware-billing.md`](.)
3. **Reasoning-effort parameter handling** (F-MODEL-001 L2) — read `relay/` for thinking-token budget application + truncation handling. → [`new-api/reasoning-effort.md`](.)
4. **Per-User × per-Model rate limit** (F-SEC-004 L2) — read `middleware/` for rate-limit policy resolution. → [`new-api/per-user-model-rate-limit.md`](.)
5. **External-tool introspection API** (F-OPS-001 L2) — enumerate operator-facing endpoints for third-party tools. → [`new-api/operator-introspection-api.md`](.)
6. **Realtime API surface** (F-RT-001 L3 Phase 9+) — WebSocket lifecycle, partial-stream usage, cancellation. → [`new-api/realtime-api.md`](.)
7. **Community-platform OAuth extensions** (F-AUTH-004 L2 Plugin) — Discord / Telegram / LinuxDo callback patterns; account linking and recovery flows. → [`new-api/community-oauth.md`](.)

## Specifier-Lane Contamination Note

This inventory was authored by Claude after reading new-api's public README only. Source-level dives for the rows above must be performed in a fresh specifier-lane session (per [DR-000 Option B / Option C](../../decisions/DR-000-clean-room-methodology.md)) with attention to CL-001..010 leakage rules; cache-aware-billing and protocol-translation are Option C carve-out areas (see [22 §Owner Sharpening](../../22_DEEP_MINING_MANDATE.md)).
