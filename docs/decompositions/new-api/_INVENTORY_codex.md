# new-api — Feature Inventory (Codex parallel take)

| Field | Value |
| --- | --- |
| Reference | New API (AGPL-3.0, [github.com/QuantumNous/new-api](https://github.com/QuantumNous/new-api), [E-LIC-002](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |
| Companion file | [_INVENTORY.md](_INVENTORY.md) (Claude's parallel take) |

## Inventory

### Core Platform / UI

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| One API data compatibility | (propose F-MIGRATE-001) | unmined | Migration candidate |
| Modern admin / user UI | F-UI-001 | unmined | UI surface not decomposed |
| Native multi-language UI | F-I18N-001 | shallow-evidence | E-NAI-002 |
| Data dashboard | F-OBS-001 | unmined | Needs dashboard inventory |
| Permission management | F-RBAC-001 / F-KEY-001 | unmined | Includes token grouping and Model restrictions |

### Billing / Payment

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Online recharge | F-PAY-001 | unmined | Payment plugin candidate |
| Pay-per-use pricing | F-BILL-001 | unmined | Needs clean-room billing dive |
| Cached-prompt billing | F-BILL-003 | shallow-evidence | E-NAI-001 |
| Flexible billing policy | F-BILL-001 | unmined | Needs pricing-rule inventory |
| API Key quota query | F-OPS-001 | shallow-evidence | E-NAI-008 |

### Auth / Identity

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Discord Identity Source | F-AUTH-004 | shallow-evidence | E-NAI-007 |
| LinuxDo Identity Source | F-AUTH-004 | shallow-evidence | E-NAI-007 |
| Telegram Identity Source | F-AUTH-004 | shallow-evidence | E-NAI-007 |
| OIDC Identity Source | F-AUTH-003 | unmined | Enterprise auth candidate |
| Multi-instance session secret | F-AUTH-002 | unmined | Deployment requirement |

### Protocol / Model Surfaces

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| OpenAI-compatible chat | F-GW-001 | shallow-evidence | README evidence |
| OpenAI Responses surface | F-PROTO-002 | shallow-evidence | README evidence |
| Claude Messages surface | F-PROTO-002 | shallow-evidence | E-NAI-003 |
| Gemini native surface | F-PROTO-002 | shallow-evidence | E-NAI-003 |
| OpenAI-compatible to Claude translation | F-PROTO-002 | shallow-evidence | E-NAI-003 |
| OpenAI-compatible to Gemini translation | F-PROTO-002 | shallow-evidence | E-NAI-003 |
| Gemini to OpenAI-compatible translation | F-PROTO-002 | shallow-evidence | E-NAI-003 |
| Responses translation | F-PROTO-002 | unmined | In-development claim |
| Thinking-to-content compatibility | F-MODEL-001 | shallow-evidence | E-NAI-004 |
| Reasoning-effort policy | F-MODEL-001 | shallow-evidence | E-NAI-004 |
| Dedicated rerank surface | F-MODEL-002 | shallow-evidence | E-NAI-005 |

### Modalities / Routing / Ops

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Image interface | F-MM-001 | unmined | Phase 9+ |
| Audio in/out interface | F-MM-001 | unmined | Phase 9+ |
| Video / music generator bridges | (propose F-MM-002) | unmined | Likely plugin |
| Midjourney-style image bridge | F-MM-001 | unmined | Plugin candidate |
| Embedding interface | (propose F-EMBED-001) | unmined | Needed for semantic cache |
| Weighted Channel random | F-GW-001 | unmined | Needs clean-room source dive |
| Automatic retry | F-GW-004 | unmined | Needs retry taxonomy |
| User-level Model rate limit | F-SEC-004 | shallow-evidence | E-NAI-006 |
| Request / stream size limits | (propose F-SEC-007) | unmined | Safety-relevant |
| Runtime profiling surface | (propose F-OBS-004) | unmined | Ops candidate |
| Docker / panel deployment | F-DEPLOY-001 | unmined | Packaging parity |

## Coverage Summary

- shallow-evidence: 17
- unmined: 21
- deep-decomposed: 0

L1/L2-relevant unmined rows: Permission management; pay-per-use pricing; flexible billing policy; OIDC Identity Source; multi-instance session secret; Responses translation; weighted Channel random; automatic retry; request / stream size limits.

## Mandated Next Dives (Priority Order)

1. Weighted Channel selection.
2. Protocol translation matrix and lossy cases.
3. Reasoning-effort and thinking-token handling.
4. Cached-prompt billing.
5. User-level Model rate limiting.
6. Payment and recharge boundary.
7. Request / stream size safety limits.

## Convergence with Claude's parallel take

Both takes agree on the same critical L1/L2-blocking unmined surfaces (cross-format protocol translation, cache-aware billing, reasoning-effort, per-User × per-Model rate limit).

Codex's take goes broader — proposes additional candidate F-* rows Claude's take did not name (F-MIGRATE-001 data compatibility from one-api; F-MM-002 video/music bridges; F-EMBED-001 embedding surface; F-SEC-007 request size limits; F-OBS-004 runtime profiling). These are queued for matrix-row addition.

Claude's take is more cautious about scope (does not invent rows). Codex's wider net catches surfaces Claude's missed; Claude's tighter net catches HUAKAI implementation-priority ordering Codex's missed.
