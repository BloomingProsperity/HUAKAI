# DEFERRED Hermes Runner SSE Disconnect Cancel

## Slice 2.5 Check (2026-05-26)

Status: still deferred. The current runner can cancel the async wrapper task, but
synchronous Hermes agent execution is run through a one-worker `ThreadPoolExecutor`;
cancelling the asyncio task does not interrupt already-running sync work and the
executor shutdown still waits for that work to return. Closing this properly needs
a runner streaming lifecycle contract for bounded non-cancelable work or
cooperative agent cancellation, which is outside the HMAC cleanup patch.

Deferred review findings:
- [S2] Runner does not cancel synchronous hermes-agent work when the SSE client disconnects — source: Codex review round 2 S2 finding; rationale: current Slice 2.2.a closes the S1 security and terminal-event correctness bugs, while sync hermes-agent cancellation needs a separate interruption contract and may affect streaming lifecycle behavior; follow-up: Slice 2.2.b/2.2.c runner streaming lifecycle hardening; Owner decision: none.

## Notes

- Scope for follow-up: propagate request disconnect/cancel state into the runner agent execution boundary and verify that no `internal_token` or upstream credential material is logged while cancellation is processed.
- Acceptance test idea: start a long-running mock sync agent, close the SSE iterator before terminal output, and assert the runner stops work or records an explicit bounded non-cancelable state without emitting `done`.
