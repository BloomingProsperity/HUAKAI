# 2026-06-04 group ratio billing

| Owner directive | "let pricing-catalog per-pool-group pricing ratio truly participate in charging so display == actual charge; implement + verify; do not git commit; clean-room: do not read /home/ubuntu/refs or external reference source." |
| Scope | In: HUAKAI-internal pricing evaluator group-ratio multiplier, pricingcatalog ratio resolver cache/fail-safe, chat and embeddings reserve/settle wiring, pricing ratio display gate for public_ratio, discriminating tests, gateway dependency injection. Out: schema changes, billing ledger/quota enforcement core changes, auth changes, LICENSE, production secrets, external reference-source research. |
| Success criteria | A configured ratio such as 0.8 changes ActualCost and reserve prediction to base * 0.8; missing ratio and resolver/store errors fail safe to 1.0; CostSnapshot records the applied group_ratio; display endpoints read the same catalog store and hide ratio when public_ratio=false; requested build/vet/test gate is run from backend. |
| Time estimate | 1-2 hours wall clock in this Codex session. |
| Blast radius | Money hot path for chat and embeddings predicted/actual cost, quota reserve predicted cost, pricing-catalog admin display, gateway wiring. |
| Failure modes | Double-multiplying group_ratio: keep multiplication centralized in pricingeval final Total path and test tiered/flat mutation. Zeroing charges on missing/error: resolver defaults to decimal 1.0 and tests missing/error. Reserve/settle mismatch: both chat paths call the same completionCost helper; embeddings already calls the same inputCost helper. Stale cache: short TTL plus fail-safe last-known-good only for valid positive ratios. Frozen package violation: modify existing gatewayhttp files only, no new files there; embeddingshttp also existing files only. |
| Decision points | None needing more Owner approval unless implementation requires schema, auth, billing ledger, quota core, LICENSE, real secrets, new runtime dependencies, destructive commands, or external reference source reads. |
| Pre-execution checklist | Read CLAUDE.md and AGENTS.md; do not read /home/ubuntu/refs; inspect HUAKAI pricingeval, pricingcatalog, pricingcataloghttp, chat, embeddings, gateway wiring; coordinate target file locks; write failing tests before production code; run requested verification gate. |

## File Scope

- Create `backend/internal/pricingcatalog/ratio_resolver.go` in non-frozen `pricingcatalog`: cached fail-safe decimal ratio resolver backed by `Store.GetRatio`.
- Create `backend/internal/pricingcatalog/ratio_resolver_test.go` in non-frozen `pricingcatalog`: missing/error/default/cache behavior.
- Modify `backend/internal/pricingeval/resolver.go` and `resolver_test.go`: add group ratio input, apply exactly once to flat and tiered final totals, snapshot the applied ratio.
- Modify `backend/internal/pricingcataloghttp/pricing_ratio_handler.go` and existing test: make `public_ratio=false` hide the ratio field in display output.
- Modify existing `backend/internal/gatewayhttp/{chat_completions_handler.go,chat_completions_pricing.go,chat_completions_pricing_test.go}` only: add optional ratio resolver dependency and apply it through pricingeval for predicted and actual costs.
- Modify existing `backend/internal/embeddingshttp/{handler.go,pricing.go,handler_test.go}` only: add optional ratio resolver dependency and apply it through pricingeval for reserve and settle.
- Modify `backend/cmd/gateway/{wiring.go,routes.go,routes_pricing.go,wiring_test.go}`: construct one pricing ratio store/resolver source and inject it into chat, embeddings, and admin display routes.

## Execution Order

1. Add pricingeval failing tests for group ratio on flat/tiered costs and zero-value default.
2. Add pricingcatalog failing tests for resolver missing/error/default/cache fail-safe behavior.
3. Add pricingcataloghttp failing tests for public_ratio=false display hiding.
4. Add gatewayhttp and embeddingshttp failing tests for ratio-adjusted reserve/settle cost.
5. Implement pricingeval group ratio final multiplier and CostSnapshot annotation.
6. Implement pricingcatalog cached fail-safe resolver.
7. Wire ratio resolver into chat, embeddings, and gateway deps using the same catalog store source used by display.
8. Implement public_ratio display hiding.
9. Run focused tests after each green step and then the requested final gate.

## Clean-Room Note

This work intentionally reads only HUAKAI-owned code and docs. The default reference-project prestudy rule is superseded here by the Owner's explicit clean-room instruction for this task: do not read `/home/ubuntu/refs` or external reference source. No reference-project behavior claims are used in this plan.
