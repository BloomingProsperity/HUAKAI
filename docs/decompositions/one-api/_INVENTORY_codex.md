# one-api — Feature Inventory (Codex parallel take)

| Field | Value |
| --- | --- |
| Reference | one-api (MIT, [github.com/songquanpeng/one-api](https://github.com/songquanpeng/one-api), [E-LIC-004](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |
| Companion file | [_INVENTORY.md](_INVENTORY.md) (Claude's parallel take — same reference, different specifier session per Owner directive "你做、他做、然后讨论") |

## Inventory

### Gateway / Routing

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| OpenAI-compatible gateway core | F-GW-001 | shallow-evidence | E-OAI-001, E-OAI-DEEP-010 |
| Broad Provider catalog | F-GW-001 | shallow-evidence | README evidence only |
| Channel load balancing | F-GW-001 | shallow-evidence | Needs routing decomposition |
| Priority-bucket Channel selection | F-GW-001 | shallow-evidence | E-OAI-DEEP-010 |
| Forced Channel override path | F-GW-001 | shallow-evidence | E-OAI-DEEP-011 |
| Failed-Channel retry exclusion | F-GW-004 | shallow-evidence | E-OAI-DEEP-003 |
| Retry class filter | F-GW-004 | shallow-evidence | E-OAI-DEEP-012 |
| Model alias / mapping rewrite | F-PROTO-002 | unmined | README-observed; no one-api row |
| External gateway bridge | (propose F-NET-002) | unmined | Gateway-core relevance TBD |

### Streaming / Usage / Billing

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Streaming response relay | F-GW-002 | shallow-evidence | E-OAI-002, E-OAI-DEEP-014 |
| Non-stream Usage Record path | F-GW-002 | shallow-evidence | Needs paired decomposition |
| Stream usage inference fallback | F-GW-002 | shallow-evidence | E-OAI-DEEP-014 |
| Pre-call Quota reservation | F-BILL-001 | shallow-evidence | E-OAI-DEEP-015 |
| Estimate-then-reconcile accounting | F-BILL-001 | shallow-evidence | E-OAI-DEEP-007 |
| Non-atomic quota mutation gap | F-BILL-001 | shallow-evidence | E-OAI-DEEP-008 |
| API Key cache invalidation | F-KEY-001 | shallow-evidence | E-OAI-DEEP-009 |
| API Key expiration / usage cap | F-KEY-001 | shallow-evidence | E-OAI-005 |
| Voucher batch export | F-BILL-002 | shallow-evidence | E-OAI-007 |
| Invite reward credits | (propose F-GROWTH-001) | unmined | SaaS growth feature |

### Account / Auth / Security

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Email registration and reset | F-AUTH-001 | shallow-evidence | E-OAI-003 |
| OAuth Identity Sources | F-AUTH-001 / F-AUTH-004 | shallow-evidence | E-OAI-003 |
| Persistent session secret | F-AUTH-002 | shallow-evidence | E-OAI-004 |
| Privileged management API | F-SEC-002 | shallow-evidence | E-OAI-015 |
| First-run bootstrap credential | F-SEC-002 | shallow-evidence | Must be improved locally |
| CAPTCHA gate | F-SEC-001 | shallow-evidence | E-OAI-014 |
| Per-IP rate limiting | F-SEC-001 | shallow-evidence | E-OAI-014 |

### Channel / Ops / UI

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Channel CRUD and bulk create | F-CH-001 | shallow-evidence | E-OAI-008 |
| Channel model allow-list | F-CH-001 | shallow-evidence | E-OAI-008 |
| Channel health probe | F-CH-002 | shallow-evidence | E-OAI-009, E-OAI-DEEP-016 |
| Balance / reachability polling | F-CH-002 | shallow-evidence | Needs deep edge cases |
| Success-rate auto-disable | F-OBS-001 / F-CH-002 | shallow-evidence | E-OAI-013 |
| Quota detail dashboard | F-OBS-001 | shallow-evidence | E-OAI-013 |
| Branding and custom pages | F-UI-001 | shallow-evidence | E-OAI-011 |
| Announcements / recharge / initial balance | F-UI-001 | shallow-evidence | E-OAI-012 |
| Theme switching | F-UI-001 | unmined | UI parity candidate |
| Multi-node deployment | (propose F-DEPLOY-003) | unmined | L2 ops relevance |
| Single-binary / Docker deployment | F-DEPLOY-001 | unmined | Packaging parity |
| Alert push integration | (propose F-OPS-005) | unmined | Ops plugin candidate |

## Coverage Summary

- shallow-evidence: 30
- unmined: 7
- deep-decomposed: 0

L1/L2-relevant unmined rows: Model alias / mapping rewrite; Multi-node deployment; Single-binary / Docker deployment.

## Mandated Next Dives (Priority Order)

1. Channel selection and retry loop.
2. Streaming Usage Record settlement.
3. Atomic Quota reservation and reconciliation gaps.
4. Channel health probe and auto-disable.
5. API Key cache invalidation.
6. Management API and bootstrap credential hardening.
7. Model alias / mapping rewrite.

## Convergence with Claude's parallel take

Both inventories agree on:
- Same gateway-core rows (priority bucket / random / forced override / retry exclusion).
- Same billing-gap flagging (E-OAI-DEEP-008 non-atomic deduct; E-OAI-DEEP-015 batched-write unsafety).
- Same channel-test auto-disable / auto-re-enable observation.

Codex's take adds:
- Invite reward credits, alert push integration, model-alias rewrite as candidate F-* rows Claude's take did not propose.
- Voucher batch export framing.

Claude's take adds (vs Codex):
- Configuration model row (env vars / config file / secrets) — Codex's take does not name this separately.
- Auth middleware chain row — Codex's take does not call out the chain order.

Both takes will be retained side-by-side until one is reviewed; the union of features informs HUAKAI's mandated dive list.
