This file is agent-facing and authoritative.

# Feature Parity Matrix

## Purpose

This matrix tracks reference-derived features and ensures full feature parity or better. It is a living control document, not a wishlist.

## Valid Dispositions

| Disposition | Meaning |
| --- | --- |
| `Implemented` | The feature exists with equivalent behavior. |
| `Implemented Better` | The feature exists with materially stronger behavior, safety, UX, or operability. |
| `Merged Equivalent` | Multiple reference features are covered by one broader capability. |
| `Safe Equivalent` | The same user outcome is delivered with a safer clean-room design. |
| `Plugin` | The feature is supported through a plugin boundary. |
| `Feature Flag` | The feature exists but is gated for rollout, risk, or deployment policy. |
| `Mandatory Roadmap` | The feature is not implemented yet but is required before parity closure. |

Invalid dispositions: `Dropped`, `Ignored`, `Not Needed`, `Too Risky`, `License Risk`, `Out of Scope`.

## Matrix Template

| Feature ID | Reference | Evidence ID | User Outcome | Risk | Disposition | Local Capability | Test ID | Owner | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| TBD | TBD | TBD | TBD | TBD | Mandatory Roadmap | TBD | TBD | TBD | Open |

## Review Rules

- Every reference feature must appear here.
- Similar features may be merged only when the merged capability fully covers every user outcome.
- A safer equivalent must document the behavior preserved and the risk reduced.
- Mandatory roadmap items block parity claims until implemented, pluginized, or feature-flagged.
