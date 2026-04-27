This file is agent-facing and authoritative.

# Acceptance Test Matrix

## Purpose

Acceptance tests prove that feature parity, safety, and operational workflows are real.

## Test Template

| Test ID | Scenario | Capability | Preconditions | Steps | Expected Result | Risk Covered | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| AT-TBD | TBD | TBD | TBD | TBD | TBD | TBD | Planned |

## Required Test Groups

- Gateway routing and fallback.
- Protocol compatibility.
- Provider account lifecycle.
- Credential rotation and disablement.
- User key lifecycle.
- Quota enforcement.
- Billing and usage recording.
- Admin audit logs.
- Secret redaction.
- Observability and investigation workflow.
- Feature flags and plugin boundaries.
- UI operations workflows.

## Release Rule

No capability group may be marked release-ready without acceptance tests covering normal path, failure path, and operator recovery path.
