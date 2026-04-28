This file is agent-facing and authoritative.

# Risk Register

## Purpose

Track risks that can affect implementation method, rollout, testing, or release readiness. Risks must not be used to silently drop features.

## Risk Template

| Risk ID | Area | Risk | Impact | Mitigation | Feature Impact | Owner | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R-TBD | TBD | TBD | TBD | TBD | No deletion allowed. | TBD | Open |

## Initial Risks

| Risk ID | Area | Risk | Impact | Mitigation | Feature Impact | Owner | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R-LIC-001 | License | Two of three primary references (New API, All API Hub) are AGPL-3.0; one (Sub2API) is LGPL-3.0. AGPL is triggered by network distribution. | Copying any protected detail risks forcing the entire platform under AGPL when offered as a service. | Verified license tiers in E-LIC-001..004. Operate under Option B clean-room (two-lane separation) per [20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md](20_CLEAN_ROOM_METHODOLOGY_OPTIONS.md) until Owner picks a methodology. Prefer MIT anchor reference (one-api) for source-level study. | Use safe equivalent or independent implementation; no feature deletion. | Claude | Open |
| R-SEC-001 | Security | Admin operations can expose secrets or dangerous controls. | Credential leak or unauthorized changes. | Redaction, RBAC, audit logs, confirmations. | Gate or stage, do not delete. | Claude | Open |
| R-BILL-001 | Billing | Usage and cost accounting can drift. | Revenue loss or incorrect charges. | Acceptance tests and reconciliation views. | Preserve billing feature with stronger checks. | Codex | Open |
| R-OPS-001 | Operations | UI hides important provider/account state. | Operators cannot resolve incidents. | Scenario-driven dashboard contracts. | Improve UI parity. | Gemini | Open |
| R-REL-001 | Reliability | Failover masks provider problems. | Silent degradation or cost spikes. | Health states, alerts, fallback logs. | Add visibility and controls. | Claude | Open |

## Rule

Each high or release-blocking risk must map to mitigation, test coverage, and release gate status.
