# all-api-hub — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | All API Hub (AGPL-3.0, [github.com/qixing-jk/all-api-hub](https://github.com/qixing-jk/all-api-hub), [E-LIC-003](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |

## Inventory

### Browser Extension / Account Hub

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Relay-station site recognition | F-OPS-003 | shallow-evidence | E-AAH-001 |
| Multi-account overview | F-OPS-003 | shallow-evidence | E-AAH-001 |
| Site Account credential vault | (propose F-KEY-002) | unmined | Client UX pattern |
| Independent API credential profiles | F-EXPORT-001 | shallow-evidence | E-AAH-004 |
| Local-first browser storage | (propose F-SEC-007) | unmined | Extension-specific |

### Dashboard / Comparison

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Balance dashboard | F-OPS-003 | shallow-evidence | E-AAH-001 |
| Usage dashboard | F-OPS-003 | shallow-evidence | E-AAH-001 |
| Usage visualization | F-OPS-003 | unmined | SaaS Phase 7+ UX |
| Model catalog sync | F-OPS-003 | shallow-evidence | README evidence |
| Model grouping view | F-OPS-003 | shallow-evidence | README evidence |
| Cross-source price comparison | F-OPS-003 | shallow-evidence | E-AAH-003 |
| Token multiplier comparison | F-OPS-003 | shallow-evidence | E-AAH-003 |

### Automation / Site Ops

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Automated daily check-in | F-OPS-004 | shallow-evidence | E-AAH-002 |
| Site-specific check-in plugins | F-OPS-004 | unmined | Needs per-source inventory |
| Browser anti-bot assist | (out-of-scope) | unmined | Do not put in gateway core |
| Self-hosted station backend linkage | F-OPS-003 | unmined | Admin UX candidate |
| Channel / Model sync and redirect | F-OPS-003 | unmined | Gateway-core relevance TBD |

### Export / Validation

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| One-click downstream tool export | F-EXPORT-001 | shallow-evidence | E-AAH-004 |
| Supported tool profiles | F-EXPORT-001 | shallow-evidence | README evidence |
| In-page API availability test | F-EXPORT-001 | shallow-evidence | E-AAH-005 |
| Model / token-cap validation | F-EXPORT-001 | shallow-evidence | E-AAH-005 |
| Export confirmation pause-point | F-EXPORT-001 | unmined | HUAKAI safety improvement |

### Sync / Packaging

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| WebDAV config sync | F-SYNC-001 | shallow-evidence | E-AAH-006 |
| Encrypted backup option | F-SYNC-001 | shallow-evidence | E-AAH-006 |
| Selective sync rules | F-SYNC-001 | shallow-evidence | E-AAH-006 |
| Browser extension packaging | (out-of-scope) | shallow-evidence | Not gateway core |
| Stable / nightly channels | (out-of-scope) | unmined | Release-process pattern only |
| Client UX patterns for SaaS Edition | (propose F-UI-002) | unmined | Phase 7+ candidate |

## Coverage Summary

- shallow-evidence: 17
- unmined: 10
- deep-decomposed: 0

L1/L2-relevant unmined rows: none for gateway core. Most unmined rows are SaaS Phase 7+ client UX or out-of-scope browser-extension behavior.

## Mandated Next Dives (Priority Order)

1. Multi-account dashboard UX.
2. Credential export confirmation flow.
3. API availability test workflow.
4. Price comparison normalization.
5. WebDAV sync security model.
6. Daily check-in abuse controls.
7. Decide out-of-scope boundary for browser anti-bot assist.

## Specifier-Lane Contamination Note

All API Hub is **AGPL-3.0** (specifier-lane only). All API Hub is a browser extension, not a gateway — most rows above are SaaS Edition Phase 7+ admin UX patterns, not gateway-core features. Gateway-core L1/L2 has no blocking unmined rows here.
