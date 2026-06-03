# 2026-06-03 OBS002 OpenTelemetry Metrics Bridge

| Owner directive | "实现+验证。设计依据:读 /tmp/verify-obs002.log(sonnet 的 F-OBS-002 OpenTelemetry 残差设计)。按它实现。" |
| Scope | In: backend-only OpenTelemetry/Prometheus bridge, default-off env gate, expvar-to-OTel observable counters for the Sonnet-specified metrics, `/metrics` route only when enabled, focused tests, go.mod/go.sum update. Out: changing existing expvar names/semantics, changing `/debug/vars`, adding DB/schema/auth/billing/quota logic, committing. |
| Success criteria | With `HUAKAI_METRICS_PROMETHEUS=true`, `/metrics` exposes current expvar values for group-policy fail-open and billing resolver failures. With env unset, setup returns nil handler and gateway does not expose `/metrics`. Required gates: `cd backend && go build ./... && go vet ./... && go test ./internal/otelbridge/... ./cmd/gateway/... 2>&1 | tail -18`. |
| Time estimate | 60-90 minutes wall time; one Codex implementation pass plus verification. |
| Blast radius | New runtime dependency path in backend startup and router wiring. If wrong, risk is accidental metrics exposure, missing money-risk counters, or shutdown hook error handling. Default-off requirement bounds production behavior. |
| Failure modes | Env parsing too broad/narrow -> accidental exposure; mitigate by only accepting case-insensitive `true`. Expvar lookup/key drift -> discriminating tests set exact expvar keys and assert exact Prometheus output values. OTel global provider leakage between tests -> use local provider and explicit shutdown. `/debug/vars` regression -> do not change existing handler line except adjacent `/metrics` registration. |
| Decision points | Owner has already authorized new OpenTelemetry runtime dependencies in the task. No further Owner sign-off needed unless `go get` cannot resolve dependencies locally/network, the OTel API version differs materially from the Sonnet design, or implementing would require touching high-risk files (`LICENSE`, schema, auth core, billing ledger, quota enforcement, secrets, deploy scripts). |
| Pre-execution checklist | Read `CLAUDE.md` and `AGENTS.md`; read `/tmp/verify-obs002.log`; check `.coordination` locks; confirm new package is not frozen; write RED tests before production code; run dependency/license sanity check; run required gates; release coordination lock. |

## File Scope And Structure

- `backend/internal/otelbridge/provider.go`: new production file in new `internal/otelbridge` package. This package is not frozen and is scoped only to metrics provider setup.
- `backend/internal/otelbridge/expvarbridge.go`: new production file in the same non-frozen package. This file only bridges existing expvar values to OTel observable counters.
- `backend/internal/otelbridge/otelbridge_test.go`: new test file for the three requested discriminating cases. Non-production test file; does not violate the "2 production files" design.
- `backend/cmd/gateway/wiring.go`: existing file only; add metrics handler and shutdown injection.
- `backend/cmd/gateway/middleware.go`: existing file only; add `/metrics` route only when a handler exists.
- `backend/go.mod` and `backend/go.sum`: add OpenTelemetry modules authorized by Owner.

Frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto` are not changed and receive no new files.

## Clean-Room And Reference Scope

Reference projects in scope by project rule: CLIProxyAPI, sub2api, new-api. This implementation does not read or claim behavior from those projects because Owner explicitly supplied `/tmp/verify-obs002.log` as the design authority and the patch is a HUAKAI-internal observability bridge over existing HUAKAI expvar counters. No reference-project source, names, schemas, comments, or implementation structures will be copied or paraphrased.

## Execution Order

1. Inspect existing expvar declarations and gateway wiring/router lifecycle.
2. Add `internal/otelbridge` tests first and run them to observe RED.
3. Add OpenTelemetry dependencies with `go get` / `go mod tidy`.
4. Implement `Setup` and `RegisterBridge`.
5. Wire provider setup/shutdown and route registration into `cmd/gateway`.
6. Run focused tests, then full requested gate.
7. Review diff for structure, clean-room, default-off behavior, and dependency footprint.
