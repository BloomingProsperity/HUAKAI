# Sub2API — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | Sub2API ([github.com/Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api), LGPL-3.0, [E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Claude (PM-Orchestrator) |
| Inventory created | 2026-04-28 |
| Last refreshed | 2026-04-28 |

## Why This File Exists

Owner directive 2026-04-28: "整体代码和逻辑都读完" — the question of whether a reference is comprehensively mined cannot be answered without an explicit feature inventory. This file is the audit instrument: it lists every feature area in the reference, marks the current decomposition state, and gates Phase 1 → Phase 2 transition. A status of `unmined` on any L1/L2-relevant row blocks the Phase 1 exit.

## Status Legend

- `unmined` — no source-code-verified evidence yet, no decomposition file. README-derived rows in [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md) do not count for L1/L2 features.
- `shallow-evidence` — at least one `E-X-DEEP-NNN` source-verified evidence row exists in [07](../../07_REFERENCE_EVIDENCE_LEDGER.md), but no prose decomposition file under [decompositions/sub2api/](.).
- `deep-decomposed` — prose decomposition file exists under [decompositions/sub2api/](.) and is at least Status: Draft. The file path is linked in the row.

## Inventory

### Gateway / Reverse-Proxy Core

| Feature | HUAKAI matrix row | Status | File |
| --- | --- | --- | --- |
| Layered Account selection (continuation → sticky → fresh, with revalidation gates) | F-POOL-001 (L1) | deep-decomposed | [layered-account-selection.md](layered-account-selection.md) |
| Adaptive top-k randomized pooled selection | F-POOL-001 (L1) | shallow-evidence (E-S2A-DEEP-007) | TBD — fold into layered-account-selection or split |
| Sticky session affinity with TTL refresh + 8-reason break taxonomy | F-SESSION-001 (L2) | shallow-evidence (E-S2A-DEEP-009) | TBD: `sticky-session.md` |
| Per-Account concurrency slot + bounded wait, separate sticky vs fallback budgets | F-CONC-001 (L2) | shallow-evidence (E-S2A-DEEP-008) | TBD: `per-account-concurrency.md` |
| Protocol translation pipeline (full-body parse-and-rebuild) | F-PROTO-002 (L2) | deep-decomposed | [protocol-translation.md](protocol-translation.md) |
| Multi-protocol envelope normalizer (Chat/Responses/Anthropic/Gemini/Azure) | F-PROTO-002 (L2) | shallow-evidence (E-S2A-PROXY-015) | covered inside [protocol-translation.md](protocol-translation.md) |
| Vision payload normalization (URL/data-URI/base64/inline) | F-MM-001 (L3) | shallow-evidence (E-S2A-PROXY-016) | covered inside [protocol-translation.md](protocol-translation.md) |
| Thinking-mode compatibility guard (effort/budget/signature) | F-MODEL-001 (L2) | shallow-evidence (E-S2A-PROXY-017) | covered inside [protocol-translation.md](protocol-translation.md) |
| Provider Account transport pool with isolation modes | F-GW-001 / F-GW-004 (L1/L2) | shallow-evidence (E-S2A-PROXY-018) | TBD: `upstream-transport.md` |
| Bounded upstream transport (idle/per-host/total caps + active-stream protection) | F-GW-001 (L1) | shallow-evidence (E-S2A-PROXY-019) | TBD: covered inside `upstream-transport.md` |
| Transport identity profiles (TLS / proxy mimicry) | (no matrix row yet — propose F-NET-001) | shallow-evidence (E-S2A-PROXY-020) | TBD: covered inside `upstream-transport.md` |
| Protocol-aware SSE streaming forwarder | F-GW-002 (L1) | deep-decomposed | [streaming-forwarder.md](streaming-forwarder.md) |
| Billing-preserving stream drain after client disconnect | F-GW-002 (L1) | shallow-evidence (E-S2A-PROXY-022) | covered inside [streaming-forwarder.md](streaming-forwarder.md) |
| Inline Usage Record extractor (4 sources tracked) | F-GW-002 (L1) | shallow-evidence (E-S2A-PROXY-023) | covered inside [streaming-forwarder.md](streaming-forwarder.md) |
| Partial-stream terminal detection | F-GW-002 (L1) | shallow-evidence (E-S2A-PROXY-024) | covered inside [streaming-forwarder.md](streaming-forwarder.md) |
| Error normalization + operator diagnostics | (cross-cutting) | shallow-evidence (E-S2A-PROXY-025) | TBD: `error-normalization.md` |
| Typed proxy failure taxonomy | F-GW-004 (L1) | shallow-evidence (E-S2A-PROXY-026) | TBD: `typed-failure-taxonomy.md` |
| Header firewall + credential rewrite | F-SEC-005 (L1) | shallow-evidence (E-S2A-PROXY-027) | TBD: `header-firewall.md` |

### Account / Identity / Auth

| Feature | HUAKAI matrix row | Status | File |
| --- | --- | --- | --- |
| Health probe runner (worker pool, 4-state status) | F-CH-002 (L2) | shallow-evidence (E-S2A-DEEP-012, E-S2A-DEEP-001..005) | TBD: `health-probe-runner.md` |
| Unified Provider Account availability state (probe + credential + quota + runtime) | F-CH-002 (L2) | shallow-evidence (E-S2A-DEEP-013) | TBD: `availability-state.md` |
| OAuth + API Key dual-paradigm Account abstraction | (cross-cutting) | shallow-evidence (E-S2A-008 README) | unmined (source) |
| Credential rotation / decryption / refresh handling | F-AUTH-002 (L1) | unmined (source) | unmined |
| Discord / Telegram / LinuxDo OAuth Identity Sources | F-AUTH-004 (L2-L3) | shallow-evidence (E-S2A-007 README) | unmined (source) |

### Quota / Billing / Pricing

| Feature | HUAKAI matrix row | Status | File |
| --- | --- | --- | --- |
| Idempotent Billing Ledger claim gate (request/API-Key fingerprint) | F-BILL-001 (L2) — but identity-critical | shallow-evidence (E-S2A-DEEP-011) | TBD: `billing-claim-gate.md` (HIGH priority — Option C carve-out) |
| Token-level cost engine (input/output/cache/image/tier/long-context multipliers, non-negative clamp) | F-BILL-001 (L2) | shallow-evidence (E-S2A-DEEP-010) | TBD: `cost-engine.md` (HIGH priority — Option C carve-out) |
| Per-User × per-Account concurrency caps (covered above) | F-CONC-001 | shallow-evidence | (see Account section) |
| Subscription usage tracking | F-PAY-001 (L4 SaaS) | unmined | unmined |
| Voucher / redemption code system | F-BILL-002 (L3) | shallow-evidence (E-S2A-007 README) | unmined (source) |
| Recharge URL / payment integration (Alipay / WeChat Pay / Stripe / EasyPay) | F-PAY-001 (L4 SaaS) | shallow-evidence (E-S2A-003 README) | unmined (source) |
| Per-User × per-Model rate limit windows | F-SEC-004 (L2) | unmined (source) | unmined |
| API-Key-level quota and rate windows | F-KEY-001 (L1) | shallow-evidence (E-S2A-DEEP-011 mentions) | TBD: `api-key-quota.md` |
| Provider-Account-level quota | (cross-cutting) | shallow-evidence (E-S2A-DEEP-011 mentions) | TBD: covered inside `cost-engine.md` |

### Edition / Configuration / Operations

| Feature | HUAKAI matrix row | Status | File |
| --- | --- | --- | --- |
| Edition / run-mode flag (toggle SaaS-only features) | F-MODE-001 (L1 MVP) | shallow-evidence (E-S2A-005 paraphrased per CL-001a) | TBD: `edition-mode.md` |
| Configurable upstream response-header allow/block list | F-SEC-005 (L1) | shallow-evidence (E-S2A-008 README) | TBD: covered inside `header-firewall.md` |
| Operator-driven self-upgrade with rollback | F-OPS-002 (L3 Phase 8) | shallow-evidence (E-S2A-007 README) | unmined (source) |
| Iframe-embed admin extension surfaces | (cross-cutting) | shallow-evidence (E-S2A-006 README) | unmined (source) |
| Channel monitor scheduler (interval + watermark) | F-CH-002 (L2) | shallow-evidence (E-S2A-DEEP-001..005) | TBD: covered inside `health-probe-runner.md` |
| Daily rollup watermark (idempotent re-process protection) | F-OPS-001 (L2) | shallow-evidence (E-S2A-DEEP-005) | TBD: covered inside `health-probe-runner.md` |

### Web / Admin / UX

| Feature | HUAKAI matrix row | Status | File |
| --- | --- | --- | --- |
| Admin dashboard (overview / stats / Pool view) | F-OPS-003 (L3 Phase 7+) | unmined | unmined |
| User self-service top-up UI | F-PAY-001 (L4 SaaS) | unmined | unmined |
| Announcements / branding / homepage customization | F-UI-001 (L3 Phase 7+) | shallow-evidence (E-OAI-011/012 README) | unmined (source) |
| Antigravity hybrid scheduling | (niche, low priority) | shallow-evidence (E-S2A-012 README) | unmined (source) |

## Open Inventory Items (Promote To Matrix Or Discard)

These are Sub2API features that do not yet have a HUAKAI matrix row. Each must either be added to [03](../../03_FEATURE_PARITY_MATRIX.md) or explicitly noted as out-of-scope for HUAKAI:

- Transport identity / TLS mimicry profiles (E-S2A-PROXY-020) — propose F-NET-001 (Plugin / SaaS-only) or out-of-scope.
- Iframe-embed admin extension surfaces — propose F-UI-002 or fold into F-UI-001.
- Antigravity hybrid scheduling — out-of-scope for HUAKAI L1-L9 (vendor-specific).
- Subscription tracking — fold into F-PAY-001.

## Coverage Summary (2026-04-28)

- **Deep-decomposed**: 3 features (layered-account-selection, streaming-forwarder, protocol-translation).
- **Shallow-evidence**: 18 features (have at least one E-X-DEEP-NNN row but no prose file).
- **Unmined**: 11 features (README only or nothing). 5 of these are L1/L2-relevant.

**Phase 1 exit blockers from this inventory** (per [22 §Phase Exit Gate](../../22_DEEP_MINING_MANDATE.md)): every L1/L2 row in the table above must reach `deep-decomposed` status. Currently 9 L1/L2 rows are still `shallow-evidence` (need a prose file) and 5 L1/L2 rows are `unmined` (need source-code dive AND a prose file).

## Mandated Next Dives (Priority Order)

1. **Billing claim gate + cost engine** (Option C carve-out, high-trust core): `billing-claim-gate.md`, `cost-engine.md`.
2. **Health probe + availability state** (Account fitness foundation): `health-probe-runner.md`, `availability-state.md`.
3. **Sticky session + per-Account concurrency** (relay-station identity): `sticky-session.md`, `per-account-concurrency.md`.
4. **Edition / run-mode flag** (Personal vs SaaS plumbing): `edition-mode.md`.
5. **Header firewall + typed failure taxonomy** (security + retry classifier): `header-firewall.md`, `typed-failure-taxonomy.md`.
6. **Upstream transport** (transport pool + bounded budgets): `upstream-transport.md`.
7. **API-Key quota / rate windows** (close the User-side billing loop): `api-key-quota.md`.
8. **Credential rotation / decryption / refresh** (auth fitness for Phase 2): unmined source, requires source dive first.
