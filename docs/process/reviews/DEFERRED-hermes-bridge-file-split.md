# Deferred Review Finding: Hermes bridge file split

Status: Deferred S2

Source: Slice 2.2.b Gateway Round 2 review S2.

Rationale: `backend/internal/hermeschat/bridge.go` is a 600+ line single file that currently mixes multiple responsibilities. This does not invalidate the Slice 2.2.b S1 correctness fix because the current change is narrowly scoped to pre-insert validation, but it raises maintainability risk for future Hermes bridge changes.

Responsibilities currently mixed in `bridge.go`:

- Request preparation and validation before runner dispatch.
- SSE parsing and event filtering.
- Conversation/message persistence.
- Audit savepoint handling and DLQ fallback.
- Response header rewrite and hop-by-hop header filtering.

Suggested split:

- `bridge_request.go` for request decoding, validation, conversation selection/creation, and internal token injection.
- `bridge_sse.go` for SSE parsing, event handling, and stream state.
- `bridge_persist.go` for assistant message persistence and conversation touch logic.
- `bridge_audit.go` for message audit savepoint handling, warning, and DLQ fallback.

Deferred review findings:
- [S2] Split oversized Hermes bridge file by responsibility — source: Codex review round 2 S2 finding; rationale: file size and responsibility mix are real maintainability risks, but no current Slice 2.2.b release behavior depends on the split; follow-up: Slice 2.4 or a later Hermes hygiene slice; Owner decision: pending for Slice 2.4 or later hygiene scheduling.
