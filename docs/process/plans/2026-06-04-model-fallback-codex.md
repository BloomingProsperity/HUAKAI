# 2026-06-04 model-fallback-codex

| Owner directive | "给 HUAKAI chat 通路加模型级 fallback 链...默认关闭(opt-in)...money + 热路径件...严禁读 /home/ubuntu/refs 等任何外部参考源码" |
| Scope | In: opt-in platform setting key `model_fallback_chains`, new non-frozen package `backend/internal/modelfallback`, chat executor outer model loop in existing `gatewayhttp` files, gateway wiring, focused tests. Out: DB schema, auth core, billing ledger schema, quota enforcement schema, external reference-source research, commits. |
| Success criteria | With no setting/nil settings, chat behavior stays single-model. With enabled config, pre-delivery retryable exhaustion can switch A->B up to max_depth, re-resolve registry/router per model, reserve/abort/settle exactly once per model attempt, never fallback after any client byte, and emit success headers only after fallback success. |
| Time estimate | 2-4 hours wall time: 30-45 min source read, 30 min red tests, 60-120 min implementation, 30-60 min verification/fix. |
| Blast radius | Chat completions/messages/responses hot path, billing reserve/settle/abort accounting, platform settings validation, gateway route wiring. |
| Failure modes | Claim leak if failed model is not aborted; duplicate charge if logical_request_id collides across models; response corruption if fallback starts after streaming bytes; behavior regression if disabled config does extra reserves; stale success headers on final error; invalid config accidentally enables fallback. Mitigation: derive per-model logical ids, rely on existing abort-before-retry paths, wrap response with deliveryTracker, clear fallback headers before final errors, fail closed on missing/invalid config. |
| Decision points | Owner already specified opt-in JSON and clean-room no external refs. No new runtime dependency. No DB schema. If platformsettings validation rejects JSON shape, fail closed by disabling fallback rather than blocking primary request. |
| Pre-execution checklist | Read CLAUDE.md and AGENTS.md; read chat executor and claim_gate; run coordination claim; write failing resolver tests; write failing gateway behavior tests; implement minimal package/handler/wiring changes; run targeted tests; run requested gate. |

## File Scope And Package Check

- Create `backend/internal/modelfallback/resolver.go`: new non-frozen package; owns config parsing, resolver, error-class mapping, per-model logical id derivation.
- Create `backend/internal/modelfallback/resolver_test.go`: new non-frozen package tests for chain selection, wildcard, max depth, loop skipping, mapping, and id derivation.
- Modify `backend/internal/platformsettings/types.go`: existing non-frozen file; add allowed key `model_fallback_chains`, default empty string, JSON value validation.
- Modify `backend/internal/gatewayhttp/chat_completions_handler.go`: existing frozen-package file only; extract single-model executor and add outer fallback loop.
- Modify `backend/internal/gatewayhttp/chat_completions_retry_failover_test.go`: existing frozen-package test file only; add discriminating model fallback scenarios.
- Modify `backend/cmd/gateway/routes.go`: existing file; wire `d.platformSettings` into chat deps.

No files will be added under frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Clean-Room Position

This task follows the Owner's narrower instruction for this feature: do not read `/home/ubuntu/refs` or other external reference source. All implementation decisions are derived from HUAKAI internal source and the Owner-provided self-designed spec.

## Execution Order

1. Write `internal/modelfallback` tests first and run them to observe RED.
2. Add minimal resolver/config implementation and run package tests to GREEN.
3. Add gateway behavior tests in existing `chat_completions_retry_failover_test.go` and observe RED.
4. Refactor `NewChatCompletionsHandler` into outer model loop plus reusable single-model execution.
5. Add platformsettings key and gateway wiring.
6. Run focused tests after each green step.
7. Run final gate: `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/modelfallback/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -20`.

## Risk Review Notes

- Billing exactly-once depends on the observed `ClaimGate.Reserve` behavior: aborted claims can be re-reserved for the same idempotency fingerprint, but the logical_request_id scan rejects a different fingerprint under the same logical_request_id. Because RequestedModel is part of the fingerprint, model fallback must derive a deterministic logical id per model.
- Delivery boundary is enforced by the existing `deliveryTracker`; the outer loop will pass the tracker through all writes and will not fallback once it has started.
- Invalid or unavailable settings disable fallback for that request, preserving primary behavior.
