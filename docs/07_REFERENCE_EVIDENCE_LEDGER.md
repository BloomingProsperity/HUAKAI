This file is agent-facing and authoritative.

# Reference Evidence Ledger

## Purpose

The ledger records what was learned from reference projects without copying protected implementation.

## License Verification Ledger

Establishing the license tier of every primary reference is a prerequisite to any other evidence row. These rows are the foundation; behavior evidence stacks on top.

| Evidence ID | Reference | Source URL | SPDX | Verified Date | Verified By | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| E-LIC-001 | Sub2API | github.com/Wei-Shaw/sub2api/blob/main/LICENSE | LGPL-3.0 | 2026-04-27 | Claude (PM) | Strong copyleft. |
| E-LIC-002 | New API | github.com/QuantumNous/new-api/blob/main/LICENSE | AGPL-3.0-or-later | 2026-04-27 | Claude (PM) | Network copyleft; service distribution triggers source disclosure. Forked from MIT one-api. |
| E-LIC-003 | All API Hub | github.com/qixing-jk/all-api-hub/blob/main/LICENSE | AGPL-3.0 (+ MIT upstream portions) | 2026-04-27 | Claude (PM) | Browser extension; client-side management UI for relay stations, not a gateway. |
| E-LIC-004 | one-api | github.com/songquanpeng/one-api/blob/main/LICENSE | MIT | 2026-04-27 | Claude (PM) | Anchor reference. Safe to read freely; New API is a derivative fork. |

## Evidence Template

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-TBD | TBD | Docs/Issue/UI behavior/Release note | TBD | TBD | TBD | No code copied. | TBD | TBD |

## Source Types

- Public documentation.
- Public issue or discussion.
- Release note.
- Public demo behavior.
- Public UI behavior.
- Public API behavior.
- Security advisory or bug report.

## Rules

- Record behavior, not implementation.
- Do not paste protected source.
- Do not copy schema, comments, UI source, or file structure.
- Link or cite public evidence when possible.
- Each parity matrix row should point to at least one evidence ID.
- Every behavior evidence row must reference the license tier of its source via the corresponding E-LIC-XXX row.
- New references added to [06_REFERENCE_PROJECTS.md](06_REFERENCE_PROJECTS.md) must first receive a license verification row here before any behavior evidence is captured.
