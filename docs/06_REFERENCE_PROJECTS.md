This file is agent-facing and authoritative.

# Reference Projects

References must be mined to preserve full feature parity or better without copying protected implementation.

## Primary References

| Reference | Repository | SPDX License | Risk Tier | Clean-Room Handling |
| --- | --- | --- | --- | --- |
| Sub2API | github.com/Wei-Shaw/sub2api | LGPL-3.0 | High (copyleft) | Public docs / issues / behavior only. No source reading by implementer lane. |
| New API | github.com/QuantumNous/new-api (formerly Calcium-Ion/new-api) | AGPL-3.0-or-later | Highest (network copyleft) | Public docs / issues / behavior only. No source reading by implementer lane. Treat any architectural detail as protected. |
| All API Hub | github.com/qixing-jk/all-api-hub | AGPL-3.0 (with MIT upstream portions) | Highest (network copyleft) | Public docs / issues / behavior only. Note: this is a browser-extension client for managing relay-station accounts, not a gateway server — its evidence value is mostly UI workflow and operator pain points. |

License verification dates and evidence IDs are recorded in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md).

## Anchor Reference (MIT-Safe)

| Reference | Repository | SPDX License | Role |
| --- | --- | --- | --- |
| LiteLLM | github.com/BerriAI/litellm | MIT | Anchor reference (MIT-safe, actively maintained). Reading LiteLLM source is license-compatible with this MIT project; may be cited in the parity matrix without copyleft concerns, subject to standard attribution + clean-room methodology. |
| Portkey gateway | github.com/Portkey-AI/gateway | MIT | Anchor reference (MIT-safe, actively maintained). License-compatible; citable in parity matrix subject to clean-room methodology. |

> **one-api RETIRED as anchor/forward reference 2026-05-28** (abandoned, last commit 2025-02-21; superseded by New API). Historical one-api evidence in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md) remains valid provenance; do NOT use one-api as a NEW reference or clean-room anchor.

## Additional References

Agents may consider similar high-star, actively maintained open-source AI gateway, provider routing, account hub, API key management, billing, quota, and admin operations projects. Examples to consider, each subject to license verification before use:

- LiteLLM (Python LLM proxy, MIT — verified E-LIC-005).
- Portkey-AI/gateway (TypeScript, MIT — verified E-LIC-006).
- Helicone/ai-gateway (Rust, **GPL-3.0-or-later — verified E-LIC-007**, NOT Apache-2.0 as marketing pages sometimes claim).
- Cloudflare AI Gateway (proprietary; public docs only).
- songquanpeng/one-api (Go, MIT) — RETIRED 2026-05-28 (abandoned; superseded by New API). Historical evidence only.

Each candidate must be added to this table with verified SPDX before being used as evidence.

## Reference Qualification

A reference is useful when it provides evidence about:

- Real user workflows.
- Common provider/account/routing capabilities.
- Operational failure modes.
- Security or billing risk.
- Admin dashboard expectations.
- Compatibility expectations.
- Publicly reported issues.

## Reference Limits

References are not implementation templates. Do not copy protected code, structure, schemas, comments, UI source, or distinctive implementation detail from non-MIT projects.

## License Risk Reminder

Two of the three primary references are AGPL-3.0. AGPL's copyleft is triggered by **network distribution**, not just binary distribution. This means an AI gateway that incorporates AGPL-derived implementation must itself be AGPL when offered as a network service. Strict clean-room methodology is therefore the default — see [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md) for the methodology choice the Owner must confirm.

## Recording Requirement

All mined evidence must be recorded in `docs/07_REFERENCE_EVIDENCE_LEDGER.md` before it drives a product decision. Each evidence row must cite the verified license tier of its source.
