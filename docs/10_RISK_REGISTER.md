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
| R-LIC-001 | License | Two of three primary references (New API, All API Hub) are AGPL-3.0; one (Sub2API) is LGPL-3.0. AGPL is triggered by network distribution. | Copying any protected detail risks forcing the entire platform under AGPL when offered as a service. | Decided in [DR-000](decisions/DR-000-clean-room-methodology.md): Option B (two-lane separation) default + Option C carve-out for billing ledger, account-pool routing, provider failover/account-health. Lane definitions in [05_CLEAN_ROOM_POLICY.md](05_CLEAN_ROOM_POLICY.md) and [12_AGENT_WORKFLOW.md](12_AGENT_WORKFLOW.md). Spec-leakage review required before specs leave the specifier lane. MIT anchor reference (one-api) is the safe source-level study target. | Use safe equivalent or independent implementation; no feature deletion. | Claude | Mitigated |
| R-SEC-001 | Security | Admin operations can expose secrets or dangerous controls. | Credential leak or unauthorized changes. | Redaction, RBAC, audit logs, confirmations. | Gate or stage, do not delete. | Claude | Open |
| R-BILL-001 | Billing | Usage and cost accounting can drift. | Revenue loss or incorrect charges. | Acceptance tests and reconciliation views. | Preserve billing feature with stronger checks. | Codex | Open |
| R-OPS-001 | Operations | UI hides important provider/account state. | Operators cannot resolve incidents. | Scenario-driven dashboard contracts. | Improve UI parity. | Gemini | Open |
| R-REL-001 | Reliability | Failover masks provider problems. | Silent degradation or cost spikes. | Health states, alerts, fallback logs. | Add visibility and controls. | Claude | Open |

## Rule

Each high or release-blocking risk must map to mitigation, test coverage, and release gate status.
