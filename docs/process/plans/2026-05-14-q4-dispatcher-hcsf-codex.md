# 2026-05-14 Q4 Dispatcher HCSF

| Owner directive | "HUAKAI 反代主链路 Q4 — dispatcher 真正吃 *HCSF envelope + 上游 round-trip 回 HCSF。" |
| Scope | In: add `DispatchHCSF`, wire non-streaming chat completions behind `HUAKAI_DISPATCH_HCSF=1`, add dispatcher HCSF tests, write required `/tmp` progress/final markers. Out: raw `DispatchInput` behavior, streaming path, database/auth/billing/quota/deployment changes, external dependencies. |
| Success criteria | Env switch off keeps old raw-body path. Env switch on sends HCSF through dispatcher, preserves request envelope fields, fills buffered response and upstream-reported model metadata, fails loudly on upstream 5xx and missing adapter. Targeted tests and vet pass where runnable. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus focused regression. |
| Blast radius | Gateway request dispatch and OpenAI-compatible non-streaming handler. Failure could break chat completions proxying only when the env switch is enabled; default fallback path mitigates rollout risk. |
| Failure modes | Adapter interface mismatch: use local interface assertion and fallback to existing request builder. Envelope copy loses fields: add round-trip preservation test. Handler emits wrong client response: test existing handler path. Upstream error hidden: add 5xx fail-loud test. |
| Decision points | No Owner sign-off needed per directive "不要问 Owner"; stop only if implementation requires high-risk files, new dependency, schema/auth/billing/quota changes, or destructive command. |
| Pre-execution checklist | Read relevant dispatcher/handler/adapters/tests; identify HCSF types and adapter interfaces; implement new file under 250 LoC; update handler guarded by env var; add 5+ tests under 200 LoC; run targeted tests and `go vet` as feasible; append progress markers after each file; write final marker. |
| Concrete execution order | 1. Inspect existing dispatcher, HCSF model, provider adapter, client adapter, and handler tests. 2. Implement `internal/gateway/upstream_dispatcher_hcsf.go`. 3. Wire `internal/gatewayhttp/chat_completions_handler.go` env-gated non-streaming branch. 4. Add focused dispatcher HCSF tests. 5. Run formatter, targeted tests, vet. 6. Record `/tmp/codex-q4-dispatcher-hcsf-final.txt` and final Chinese summary. |

Note: Parallel Claude/Codex draft reconciliation is not available inside this single Codex turn. The Owner explicitly requested no additional Owner question, so this plan is recorded before execution and work proceeds with low/medium-risk constraints.
