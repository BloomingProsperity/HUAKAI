# 2026-06-02 Sidecar Contract Harden Codex Plan

| Owner directive | "HUAKAI Phase2 S2 — Go<->Rust sidecar 契约健壮化 + 可降级。IMPLEMENTER。只动 Go backend/internal/transport(非冻结)+ config + wiring + cmd/gateway。中文注释。自主:实现→判别测试→build/test→self-review(<=2轮)→push origin HEAD:work/sidecar-contract-harden。不碰 landing。" |
| Scope | In: `backend/internal/transport`, `backend/internal/config`, `backend/cmd/gateway` wiring/tests, this plan doc. Out: frozen packages, auth core, billing ledger, quota enforcement, DB schema, `LICENSE`, landing. |
| Success criteria | Sidecar socket configured + fallback flag off fails closed with stable transport error class. Fallback flag on returns Go native mimicry transport and emits observable fallback metric state. Probe validates sidecar liveness and requested profile availability. Mandatory profile attempts never degrade. Requested build/test/self-review/push commands complete or failures are reported honestly. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus up to two review/fix rounds. |
| Blast radius | Transport selection for mimicry modes using `HUAKAI_TRANSPORT_SIDECAR_SOCKET`; config parsing for new boolean env; gateway wiring of transport factory. |
| Failure modes | Silent downgrade could hide loss of fingerprint fidelity: mitigate with fail-closed default, explicit flag, metric counter, and tests. Probe could accept sidecar alive but missing profile: mitigate by probing the real profile ID. Mandatory mode could regress to Go native: mitigate with a discriminating test. Config parse could accept typo: use existing strict `envBool`. |
| Decision points | None during this run unless a high-risk file becomes necessary. The old 2026-05-21 direction said default degrade; this task explicitly changes production default to fail-closed, so no extra Owner gate is needed. |
| Pre-execution checklist | Read requested files and actual sidecar client path; confirm package scope is not frozen; write failing tests first; verify red; implement minimal code; run targeted tests; run `go build ./...`; run requested test packages; run self-review; push requested branch. |

## Concrete execution order

1. Add failing transport tests for sidecar unavailable with fallback off, fallback on, profile probe failure class, and mandatory profile no-degrade.
2. Add config and gateway wiring tests for `HUAKAI_TRANSPORT_SIDECAR_FALLBACK`.
3. Run targeted tests and confirm they fail for the intended missing behavior.
4. Implement stable sidecar transport error class, explicit fallback flag, fallback metric counter, profile-aware probe, and mandatory-mode no-degrade behavior.
5. Wire config field into `buildTransportFactory`.
6. Run targeted tests, then `go build ./...` and `go test ./internal/transport/... ./cmd/gateway/...` from `backend/`.
7. Stage intended files, run `codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh </dev/null`, fix S0/S1 findings with at most one follow-up review round.
8. Push `origin HEAD:work/sidecar-contract-harden`.

## Assumptions

- `backend/internal/transport/mimicry/sidecar_client.go` is the actual file corresponding to the requested `backend/internal/transport/sidecar_client.go`.
- `backend/cmd/gateway` is the actual gateway command package path.
- "metric/audit" can be satisfied in this small slice by a transport-level fallback metric counter plus structured warning log; durable audit wiring is outside the allowed scope and would touch broader ops/audit surfaces.
