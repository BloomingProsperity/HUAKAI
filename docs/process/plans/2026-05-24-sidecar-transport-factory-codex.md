# 2026-05-24 Sidecar Transport Factory

| Owner directive | "[OWNER AUTHORIZED 2026-05-24T10:45Z workspace-write — 第三发 AI review #4 修复:Sidecar transport factory 未接]" |
| Scope | In: wire `HUAKAI_TRANSPORT_SIDECAR_SOCKET` / config injection into the Go transport factory, route mimicry modes through the sidecar when configured, fail closed on missing or unresponsive socket, add discriminating tests. Out: starting the Rust sidecar binary, changing provider routing policy, changing frozen packages, adding dependencies, git add/commit/push. |
| Success criteria | `socket_path=""` keeps existing uTLS compatibility; configured missing socket fails from `Factory.For` instead of falling back to uTLS; configured unresponsive socket fails on a bounded timeout; gateway wiring passes `cfg.TransportSidecarSocket`; `go test ./internal/transport/...`, `go build ./...`, and `echo DONE` run successfully. |
| Time estimate | 30-60 minutes wall clock; one Codex executor session. |
| Blast radius | Transport selection for mimicry modes and gateway boot config. Standard mode, diagnostics mode, provider policy, auth, billing, quota, schema, and frozen packages stay untouched. |
| Failure modes | A sidecar branch could mask the legacy uTLS fallback when no socket is configured; tests cover empty socket. A bad socket could silently fall back to uTLS; tests cover missing socket. A connected but nonresponsive sidecar could hang; tests cover timeout with a reduced test timeout and implementation uses a 5s production default. |
| Decision points | None for Owner in this work unit: Owner already selected production fail-closed with optional audit-only fallback deferred to test-mode only. No reference-project behavior comparison is included because this is HUAKAI-internal wiring and no reference-project claim is made. |
| Pre-execution checklist | 1. Confirm worktree state and do not revert unrelated changes. 2. Read `Factory.For`, sidecar client protocol, runtime config, and gateway wiring. 3. Add failing tests before production code. 4. Implement minimal factory/config/wiring changes. 5. Run targeted tests, then requested full checks. |

Concrete execution order:

1. Add factory tests for empty socket compatibility, missing socket fail-closed, and unresponsive sidecar timeout.
2. Run the new tests to confirm they fail against the current factory.
3. Add `TransportSidecarSocket` to runtime config loaded from `HUAKAI_TRANSPORT_SIDECAR_SOCKET`.
4. Add `SidecarSocketPath` and bounded probe support to `transport.Factory`.
5. Route configured mimicry modes through `mimicry.NewSidecarRoundTripperForMode` after sidecar readiness succeeds.
6. Wire `cfg.TransportSidecarSocket` in `backend/cmd/gateway/wiring.go`.
7. Run `go test ./internal/transport/...`, `go build ./...`, and `echo DONE`.
